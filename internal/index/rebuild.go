package index

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

// RebuildResult holds statistics from a completed index rebuild.
type RebuildResult struct {
	EventsIndexed     int
	SessionsRebuilt   int
	CorruptedSkipped  int
	VerifiedCount     int64
	Elapsed           time.Duration
}

// Rebuild recreates the SQLite index from scratch by replaying all events from
// the event log segment files. It backs up the existing index (unless it doesn't
// exist), removes WAL/SHM files, creates a fresh index, reads all segments using
// a tolerant reader that skips corrupted frames, batch-inserts events, and
// reconstructs session records from session meta-events.
func Rebuild(indexPath string, eventsDir string, logger *log.Logger) (*RebuildResult, error) {
	start := time.Now()

	// Back up existing index if present
	if _, err := os.Stat(indexPath); err == nil {
		backupPath := indexPath + ".bak." + time.Now().Format("20060102-150405")
		logger.Printf("backing up existing index to %s", backupPath)
		if err := os.Rename(indexPath, backupPath); err != nil {
			return nil, fmt.Errorf("backup index: %w", err)
		}
	}

	// Remove WAL and SHM files (leftover from corrupted state)
	os.Remove(indexPath + "-wal")
	os.Remove(indexPath + "-shm")
	os.Remove(indexPath) // in case rename didn't remove it

	// Create fresh index
	logger.Printf("creating fresh index at %s", indexPath)
	idx, err := Open(indexPath)
	if err != nil {
		return nil, fmt.Errorf("create index: %w", err)
	}
	defer idx.Close()

	// List segment files
	segments, err := listSegmentFiles(eventsDir)
	if err != nil {
		return nil, fmt.Errorf("list segments: %w", err)
	}

	logger.Printf("found %d segment file(s) in %s", len(segments), eventsDir)

	var totalEvents int
	var totalSkipped int
	var totalSessions int
	sessions := make(map[string]*schema.Session)
	sessionFiles := make(map[string]map[string]bool)

	const batchSize = 1000

	for _, segFile := range segments {
		segPath := filepath.Join(eventsDir, segFile)
		recovered, skipped, err := ReadSegmentTolerant(segPath)
		if err != nil {
			logger.Printf("error reading segment %s: %v (skipping)", segFile, err)
			continue
		}

		logger.Printf("segment %s: recovered %d events, skipped %d corrupted frames", segFile, len(recovered), skipped)
		totalSkipped += skipped

		// Process in batches
		batch := make([]struct {
			Event         *schema.Event
			SegmentFile   string
			SegmentOffset int64
		}, 0, batchSize)

		for _, re := range recovered {
			TrackSession(sessions, sessionFiles, re.Event)

			batch = append(batch, struct {
				Event         *schema.Event
				SegmentFile   string
				SegmentOffset int64
			}{
				Event:         re.Event,
				SegmentFile:   segFile,
				SegmentOffset: re.Offset,
			})

			if len(batch) >= batchSize {
				if err := idx.IndexEventBatch(batch); err != nil {
					return nil, fmt.Errorf("index batch: %w", err)
				}
				totalEvents += len(batch)
				batch = batch[:0]
			}
		}

		// Flush remaining
		if len(batch) > 0 {
			if err := idx.IndexEventBatch(batch); err != nil {
				return nil, fmt.Errorf("index batch: %w", err)
			}
			totalEvents += len(batch)
		}
	}

	// Upsert all reconstructed sessions
	logger.Printf("rebuilding session records...")
	for _, s := range sessions {
		if err := idx.UpsertSession(s); err != nil {
			logger.Printf("warning: failed to upsert session %s: %v", s.SessionID, err)
			continue
		}
		totalSessions++
	}

	// Verify count
	verifiedCount, err := idx.CountEvents()
	if err != nil {
		logger.Printf("warning: could not verify event count: %v", err)
	}

	elapsed := time.Since(start)

	return &RebuildResult{
		EventsIndexed:    totalEvents,
		SessionsRebuilt:  totalSessions,
		CorruptedSkipped: totalSkipped,
		VerifiedCount:    verifiedCount,
		Elapsed:          elapsed,
	}, nil
}

// RecoveredEvent pairs a decoded event with its byte offset in the segment.
type RecoveredEvent struct {
	Event  *schema.Event
	Offset int64
}

// ReadSegmentTolerant reads a segment file, recovering valid events and skipping corrupted frames.
// It uses two strategies:
//  1. Normal sequential frame parsing (fast path for valid frames)
//  2. On checksum failure, attempt JSON-tolerant parsing (trim trailing garbage bytes)
//  3. If that fails, scan forward byte-by-byte to find the next valid frame
//
// Returns recovered events, count of skipped/corrupted frames, and any fatal error.
func ReadSegmentTolerant(segPath string) ([]RecoveredEvent, int, error) {
	data, err := os.ReadFile(segPath)
	if err != nil {
		return nil, 0, fmt.Errorf("read segment: %w", err)
	}

	var results []RecoveredEvent
	skipped := 0
	offset := 0

	for offset < len(data) {
		// Need at least 10 bytes for a minimal frame (4 len + 2 ver + 0 json + 4 crc)
		if offset+10 > len(data) {
			break
		}

		frameLen := int(binary.BigEndian.Uint32(data[offset : offset+4]))

		// Sanity check frame length
		if frameLen < 10 || frameLen > 100*1024*1024 || offset+frameLen > len(data) {
			skipped++
			offset++
			continue
		}

		remaining := data[offset+4 : offset+frameLen]
		version := binary.BigEndian.Uint16(remaining[0:2])

		if version != schema.SchemaVersion {
			skipped++
			offset++
			continue
		}

		dataEnd := len(remaining) - 4
		storedChecksum := binary.BigEndian.Uint32(remaining[dataEnd:])
		computedChecksum := crc32.ChecksumIEEE(remaining[:dataEnd])

		jsonPayload := remaining[2:dataEnd]

		if storedChecksum == computedChecksum {
			// Fast path: valid checksum
			event, err := unmarshalEventJSON(jsonPayload)
			if err == nil {
				results = append(results, RecoveredEvent{Event: event, Offset: int64(offset)})
				offset += frameLen
				continue
			}
		}

		// Checksum mismatch or JSON parse failure -- try tolerant parsing.
		// Common corruption: trailing garbage byte(s) appended to JSON payload.
		event := tryTolerantParse(jsonPayload)
		if event != nil {
			results = append(results, RecoveredEvent{Event: event, Offset: int64(offset)})
			offset += frameLen
			skipped++ // still count as a corrupted frame even though we recovered it
			continue
		}

		// Unrecoverable frame -- skip one byte and try again
		skipped++
		offset++
	}

	return results, skipped, nil
}

// unmarshalEventJSON parses a JSON payload into a schema.Event.
func unmarshalEventJSON(jsonData []byte) (*schema.Event, error) {
	var event schema.Event
	if err := json.Unmarshal(jsonData, &event); err != nil {
		return nil, err
	}
	return &event, nil
}

// tryTolerantParse attempts to parse a JSON payload that may have trailing garbage bytes.
// It finds the last valid '}' and tries to unmarshal progressively shorter slices.
func tryTolerantParse(jsonPayload []byte) *schema.Event {
	// Find the last '}' which should be the end of the JSON object
	for i := len(jsonPayload) - 1; i >= 0; i-- {
		if jsonPayload[i] == '}' {
			event, err := unmarshalEventJSON(jsonPayload[:i+1])
			if err == nil {
				return event
			}
		}
	}
	return nil
}

// TrackSession reconstructs session records from event data.
// Session meta-events (file_path=".belay/sessions") carry start/end metadata.
// Regular events with session_id contribute to event counts and file tracking.
func TrackSession(sessions map[string]*schema.Session, sessionFiles map[string]map[string]bool, event *schema.Event) {
	if event.SessionID == "" {
		return
	}

	sid := event.SessionID

	// Check for session meta-events
	if event.FilePath == ".belay/sessions" && event.Metadata != nil {
		eventType := event.Metadata["event_type"]

		switch eventType {
		case "session_start":
			s := &schema.Session{
				SessionID: sid,
				ToolName:  event.Metadata["tool_name"],
				Status:    schema.SessionActive,
				StartedAt: event.Timestamp(),
			}
			if pidStr, ok := event.Metadata["pid"]; ok {
				if pid, err := strconv.Atoi(pidStr); err == nil {
					s.PID = pid
				}
			}
			sessions[sid] = s
			return

		case "session_end":
			if s, ok := sessions[sid]; ok {
				s.EndedAt = event.Timestamp()
				switch event.Metadata["status"] {
				case "ended":
					s.Status = schema.SessionEnded
				case "crashed":
					s.Status = schema.SessionCrashed
				}
			}
			return
		}
	}

	// Regular event with a session_id -- count it
	s, ok := sessions[sid]
	if !ok {
		// Session started before our log window or via hook -- create stub
		toolName := "unknown"
		if event.Metadata != nil {
			if tn, ok := event.Metadata["tool_name"]; ok {
				toolName = tn
			}
		}
		s = &schema.Session{
			SessionID: sid,
			ToolName:  toolName,
			Status:    schema.SessionEnded, // Assume ended since we're replaying history
			StartedAt: event.Timestamp(),
		}
		sessions[sid] = s
		sessionFiles[sid] = make(map[string]bool)
	}

	s.EventCount++
	files, ok := sessionFiles[sid]
	if !ok {
		files = make(map[string]bool)
		sessionFiles[sid] = files
	}
	if !files[event.FilePath] {
		files[event.FilePath] = true
		s.FilesChanged++
	}
}

// listSegmentFiles returns sorted segment filenames from the events directory.
func listSegmentFiles(eventsDir string) ([]string, error) {
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read events directory: %w", err)
	}

	var segments []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".log") {
			segments = append(segments, entry.Name())
		}
	}
	return segments, nil
}
