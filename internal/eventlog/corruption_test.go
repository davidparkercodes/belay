package eventlog

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

// ─── Corruption: Truncated Event Data ───────────────────────────────────────

func TestCorruption_TruncatedSegmentFile(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, 10*1024*1024)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	// Write several valid events
	for i := 0; i < 5; i++ {
		ev := makeTestEvent("file.go", schema.OpModify)
		if err := w.Append(ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	segFile := w.CurrentSegment()
	w.Close()

	// Truncate the segment file to cut off the last event(s)
	segPath := filepath.Join(dir, segFile)
	data, err := os.ReadFile(segPath)
	if err != nil {
		t.Fatalf("read segment: %v", err)
	}
	if len(data) < 20 {
		t.Fatalf("segment too small: %d bytes", len(data))
	}

	// Cut off the last 20 bytes (partial frame)
	truncated := data[:len(data)-20]
	if err := os.WriteFile(segPath, truncated, 0644); err != nil {
		t.Fatalf("write truncated: %v", err)
	}

	// Reader should read what it can without panicking
	r, err := NewReader(dir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	events, err := r.ReadSegment(segFile)
	if err != nil {
		t.Fatalf("ReadSegment returned error: %v", err)
	}

	// Should have recovered at least some events (fewer than 5)
	if len(events) >= 5 {
		t.Errorf("expected fewer than 5 events from truncated segment, got %d", len(events))
	}
	t.Logf("recovered %d out of 5 events from truncated segment", len(events))
}

// ─── Corruption: Invalid Frame Length ───────────────────────────────────────

func TestCorruption_InvalidFrameLength(t *testing.T) {
	dir := t.TempDir()

	// Create a segment file with an absurdly large frame length prefix
	segName := time.Now().Format("20060102-150405") + ".log"
	segPath := filepath.Join(dir, segName)

	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], 0xFFFFFFFF) // ~4GB frame
	if err := os.WriteFile(segPath, buf[:], 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	r, err := NewReader(dir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	// ReadSegment should handle the invalid length without panicking
	events, err := r.ReadSegment(segName)
	if err != nil {
		t.Logf("ReadSegment error (expected): %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events from invalid segment, got %d", len(events))
	}
}

// ─── Corruption: Bad Checksum ───────────────────────────────────────────────

func TestCorruption_BadChecksum(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, 10*1024*1024)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	ev := makeTestEvent("file.go", schema.OpCreate)
	if err := w.Append(ev); err != nil {
		t.Fatalf("Append: %v", err)
	}
	segFile := w.CurrentSegment()
	w.Close()

	// Read the segment and corrupt the checksum (last 4 bytes of the frame)
	segPath := filepath.Join(dir, segFile)
	data, err := os.ReadFile(segPath)
	if err != nil {
		t.Fatalf("read segment: %v", err)
	}

	// Flip bits in the last 4 bytes (checksum)
	if len(data) >= 4 {
		data[len(data)-1] ^= 0xFF
		data[len(data)-2] ^= 0xFF
	}
	if err := os.WriteFile(segPath, data, 0644); err != nil {
		t.Fatalf("write corrupted: %v", err)
	}

	r, err := NewReader(dir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	// Should detect checksum mismatch and skip the corrupt frame
	events, err := r.ReadSegment(segFile)
	if err != nil {
		t.Logf("ReadSegment error (expected): %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events from checksum-corrupted segment, got %d", len(events))
	}
}

// ─── Corruption: Invalid JSON in Frame ──────────────────────────────────────

func TestCorruption_InvalidJSONInFrame(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, 10*1024*1024)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	ev := makeTestEvent("file.go", schema.OpCreate)
	if err := w.Append(ev); err != nil {
		t.Fatalf("Append: %v", err)
	}
	segFile := w.CurrentSegment()
	w.Close()

	// Read segment and corrupt the JSON data portion (bytes 6..len-4)
	segPath := filepath.Join(dir, segFile)
	data, err := os.ReadFile(segPath)
	if err != nil {
		t.Fatalf("read segment: %v", err)
	}

	// Overwrite the JSON portion with garbage (but keep length and version intact)
	// Frame format: [length:4][version:2][json_data][checksum:4]
	if len(data) > 10 {
		for i := 6; i < len(data)-4; i++ {
			data[i] = 'X' // Replace JSON with garbage
		}
		// Recalculate checksum would make the frame "valid" but JSON still broken
		// Instead, leave the checksum wrong — the reader should fail on either checksum or JSON
	}
	if err := os.WriteFile(segPath, data, 0644); err != nil {
		t.Fatalf("write corrupted: %v", err)
	}

	r, err := NewReader(dir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	events, err := r.ReadSegment(segFile)
	if err != nil {
		t.Logf("ReadSegment error (expected): %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events from corrupted JSON segment, got %d", len(events))
	}
}

// ─── Corruption: Deleted Segment During Iteration ───────────────────────────

func TestCorruption_DeletedSegmentDuringReadAll(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, 100) // Small segments to force multiple
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	for i := 0; i < 20; i++ {
		ev := makeTestEvent("file.go", schema.OpModify)
		if err := w.Append(ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	w.Close()

	r, err := NewReader(dir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	// Get segment list first
	segments, err := r.Segments()
	if err != nil {
		t.Fatalf("Segments: %v", err)
	}
	if len(segments) < 2 {
		t.Fatalf("expected at least 2 segments, got %d", len(segments))
	}

	// Delete the first segment
	segPath := filepath.Join(dir, segments[0])
	if err := os.Remove(segPath); err != nil {
		t.Fatalf("remove segment: %v", err)
	}

	// ReadAll should skip the missing segment and continue with others
	events, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll should not return error for missing segment: %v", err)
	}

	// Should have recovered some events from the remaining segments
	t.Logf("recovered %d events after deleting first segment", len(events))
	if len(events) == 0 {
		t.Error("expected at least some events from remaining segments")
	}
}

// ─── Corruption: Zero-Byte Segment File ─────────────────────────────────────

func TestCorruption_ZeroByteSegmentFile(t *testing.T) {
	dir := t.TempDir()

	// Create an empty segment file
	segName := time.Now().Format("20060102-150405") + ".log"
	segPath := filepath.Join(dir, segName)
	if err := os.WriteFile(segPath, []byte{}, 0644); err != nil {
		t.Fatalf("write empty: %v", err)
	}

	r, err := NewReader(dir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	events, err := r.ReadSegment(segName)
	if err != nil {
		t.Logf("ReadSegment zero-byte: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events from empty segment, got %d", len(events))
	}

	// ReadAll should handle it too
	allEvents, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll with empty segment: %v", err)
	}
	if len(allEvents) != 0 {
		t.Errorf("expected 0 events from ReadAll, got %d", len(allEvents))
	}
}

// ─── Corruption: Segment With Only Partial Length Prefix ─────────────────────

func TestCorruption_PartialLengthPrefix(t *testing.T) {
	dir := t.TempDir()

	// Write only 2 bytes (incomplete 4-byte length prefix)
	segName := time.Now().Format("20060102-150405") + ".log"
	segPath := filepath.Join(dir, segName)
	if err := os.WriteFile(segPath, []byte{0x00, 0x01}, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	r, err := NewReader(dir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	events, err := r.ReadSegment(segName)
	if err != nil {
		t.Logf("ReadSegment partial prefix: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

// ─── Corruption: Valid Events Followed by Garbage ───────────────────────────

func TestCorruption_ValidEventsThenGarbage(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, 10*1024*1024)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	// Write 3 valid events
	for i := 0; i < 3; i++ {
		ev := makeTestEvent("file.go", schema.OpModify)
		if err := w.Append(ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	segFile := w.CurrentSegment()
	w.Close()

	// Append garbage bytes to the end of the segment
	segPath := filepath.Join(dir, segFile)
	f, err := os.OpenFile(segPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	garbage := []byte("GARBAGE DATA THAT IS NOT A VALID FRAME!!!")
	if _, err := f.Write(garbage); err != nil {
		f.Close()
		t.Fatalf("write garbage: %v", err)
	}
	f.Close()

	r, err := NewReader(dir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	// Should recover the 3 valid events and stop at the garbage
	events, err := r.ReadSegment(segFile)
	if err != nil {
		t.Logf("ReadSegment error: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("expected 3 valid events before garbage, got %d", len(events))
	}
}

// ─── Corruption: Frame With Zero Length ─────────────────────────────────────

func TestCorruption_FrameWithZeroLength(t *testing.T) {
	dir := t.TempDir()

	// Create segment with a zero-length frame prefix
	segName := time.Now().Format("20060102-150405") + ".log"
	segPath := filepath.Join(dir, segName)
	zeroLen := make([]byte, 4) // all zeros = frame length 0
	if err := os.WriteFile(segPath, zeroLen, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	r, err := NewReader(dir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	events, err := r.ReadSegment(segName)
	if err != nil {
		t.Logf("ReadSegment zero-length frame: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

// ─── Corruption: ReadFrom With Corrupt Offset ───────────────────────────────

func TestCorruption_ReadFromBeyondEnd(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, 10*1024*1024)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	ev := makeTestEvent("file.go", schema.OpCreate)
	if err := w.Append(ev); err != nil {
		t.Fatalf("Append: %v", err)
	}
	segFile := w.CurrentSegment()
	w.Close()

	r, err := NewReader(dir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	// Read from an offset past the end of the file
	events, err := r.ReadFrom(segFile, 999999)
	if err != nil {
		t.Logf("ReadFrom past end: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events from offset past end, got %d", len(events))
	}
}

// ─── Corruption: ReadSegmentWithOffsets on Corrupt Data ─────────────────────

func TestCorruption_ReadSegmentWithOffsets_CorruptData(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, 10*1024*1024)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	// Write 2 valid events, then corrupt the file
	for i := 0; i < 2; i++ {
		ev := makeTestEvent("file.go", schema.OpModify)
		if err := w.Append(ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	segFile := w.CurrentSegment()
	w.Close()

	// Corrupt by appending garbage
	segPath := filepath.Join(dir, segFile)
	f, err := os.OpenFile(segPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, _ = f.Write([]byte("CORRUPT"))
	f.Close()

	r, err := NewReader(dir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	results, err := r.ReadSegmentWithOffsets(segFile)
	if err != nil {
		t.Logf("ReadSegmentWithOffsets error: %v", err)
	}

	// Should have recovered the 2 valid events
	if len(results) != 2 {
		t.Errorf("expected 2 events with offsets, got %d", len(results))
	}

	// Verify offsets are non-negative and increasing
	for i, res := range results {
		if res.Offset < 0 {
			t.Errorf("event %d: negative offset %d", i, res.Offset)
		}
		if res.Size <= 0 {
			t.Errorf("event %d: non-positive size %d", i, res.Size)
		}
		if i > 0 && res.Offset <= results[i-1].Offset {
			t.Errorf("event %d: offset %d not greater than previous %d", i, res.Offset, results[i-1].Offset)
		}
	}
}

// ─── Corruption: NonExistent Events Directory ───────────────────────────────

func TestCorruption_NonExistentEventsDir(t *testing.T) {
	_, err := NewReader("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for nonexistent events directory")
	}
	t.Logf("NewReader error (expected): %v", err)
}

// ─── Helper ─────────────────────────────────────────────────────────────────

func makeTestEvent(filePath string, op schema.Operation) *schema.Event {
	return &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      filePath,
		Op:            op,
		ContentHash:   schema.ContentHashForBytes([]byte(filePath)),
		ContentSize:   int64(len(filePath)),
	}
}
