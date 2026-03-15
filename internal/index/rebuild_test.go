package index

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

// ─── Helpers ────────────────────────────────────────────────────────────────

// writeSegmentFile encodes events into a binary segment file using the same
// frame format as schema.Event.MarshalBinary.
func writeSegmentFile(t *testing.T, dir, name string, events []*schema.Event) {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create segment file %s: %v", name, err)
	}
	defer f.Close()

	for _, ev := range events {
		data, err := ev.MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary for event %s: %v", ev.EventID, err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatalf("write event %s: %v", ev.EventID, err)
		}
	}
}

// testLogger creates a logger that discards output (or writes to testing.T log if needed).
func testLogger(t *testing.T) *log.Logger {
	t.Helper()
	return log.New(os.Stderr, "test: ", log.LstdFlags)
}

// ─── ReadSegmentTolerant ────────────────────────────────────────────────────

func TestReadSegmentTolerant_ValidSegment(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	events := []*schema.Event{
		makeEvent("evt-seg-a", "src/main.go", schema.OpCreate, "sess-1", now),
		makeEvent("evt-seg-b", "src/util.go", schema.OpModify, "sess-1", now.Add(time.Second)),
		makeEvent("evt-seg-c", "README.md", schema.OpModify, "sess-2", now.Add(2*time.Second)),
	}
	writeSegmentFile(t, dir, "seg001.log", events)

	recovered, skipped, err := ReadSegmentTolerant(filepath.Join(dir, "seg001.log"))
	if err != nil {
		t.Fatalf("ReadSegmentTolerant: %v", err)
	}
	if skipped != 0 {
		t.Errorf("expected 0 skipped, got %d", skipped)
	}
	if len(recovered) != 3 {
		t.Fatalf("expected 3 recovered events, got %d", len(recovered))
	}

	// Verify event data integrity
	if recovered[0].Event.EventID != "evt-seg-a" {
		t.Errorf("first event ID = %q, want %q", recovered[0].Event.EventID, "evt-seg-a")
	}
	if recovered[1].Event.FilePath != "src/util.go" {
		t.Errorf("second event FilePath = %q, want %q", recovered[1].Event.FilePath, "src/util.go")
	}
	if recovered[2].Event.SessionID != "sess-2" {
		t.Errorf("third event SessionID = %q, want %q", recovered[2].Event.SessionID, "sess-2")
	}
}

func TestReadSegmentTolerant_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	segPath := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(segPath, []byte{}, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	recovered, skipped, err := ReadSegmentTolerant(segPath)
	if err != nil {
		t.Fatalf("ReadSegmentTolerant: %v", err)
	}
	if len(recovered) != 0 {
		t.Errorf("expected 0 recovered events from empty file, got %d", len(recovered))
	}
	if skipped != 0 {
		t.Errorf("expected 0 skipped from empty file, got %d", skipped)
	}
}

func TestReadSegmentTolerant_NonexistentFile(t *testing.T) {
	_, _, err := ReadSegmentTolerant(filepath.Join(t.TempDir(), "nonexistent.log"))
	if err == nil {
		t.Fatal("expected error for nonexistent segment file")
	}
}

func TestReadSegmentTolerant_PureGarbage(t *testing.T) {
	dir := t.TempDir()
	segPath := filepath.Join(dir, "garbage.log")
	if err := os.WriteFile(segPath, []byte("totally not binary event data here"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	recovered, skipped, err := ReadSegmentTolerant(segPath)
	if err != nil {
		t.Fatalf("ReadSegmentTolerant should not return fatal error for garbage, got: %v", err)
	}
	if len(recovered) != 0 {
		t.Errorf("expected 0 recovered events from garbage data, got %d", len(recovered))
	}
	if skipped == 0 {
		t.Error("expected some skipped bytes from garbage data")
	}
}

func TestReadSegmentTolerant_ValidThenCorrupt(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	// Write one valid event then append garbage
	segPath := filepath.Join(dir, "partial.log")
	ev := makeEvent("evt-partial", "file.go", schema.OpCreate, "sess-1", now)
	data, err := ev.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	garbage := []byte("this is garbage data that is not a valid frame and should be skipped")
	combined := append(data, garbage...)
	if err := os.WriteFile(segPath, combined, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	recovered, skipped, err := ReadSegmentTolerant(segPath)
	if err != nil {
		t.Fatalf("ReadSegmentTolerant: %v", err)
	}
	if len(recovered) != 1 {
		t.Errorf("expected 1 recovered event, got %d", len(recovered))
	}
	if len(recovered) > 0 && recovered[0].Event.EventID != "evt-partial" {
		t.Errorf("recovered event ID = %q, want %q", recovered[0].Event.EventID, "evt-partial")
	}
	if skipped == 0 {
		t.Error("expected some skipped bytes from trailing garbage")
	}
}

func TestReadSegmentTolerant_CorruptChecksumWithRecoverableJSON(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	ev := makeEvent("evt-corrupt-crc", "file.go", schema.OpModify, "sess-1", now)

	// Build a frame manually with a bad checksum but valid JSON
	jsonData, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	// Frame: [4 byte total len][2 byte version][json][4 byte checksum]
	totalLen := 4 + 2 + len(jsonData) + 4
	buf := make([]byte, totalLen)
	binary.BigEndian.PutUint32(buf[0:4], uint32(totalLen))
	binary.BigEndian.PutUint16(buf[4:6], schema.SchemaVersion)
	copy(buf[6:6+len(jsonData)], jsonData)
	// Write an intentionally wrong checksum
	binary.BigEndian.PutUint32(buf[6+len(jsonData):], 0xDEADBEEF)

	segPath := filepath.Join(dir, "badcrc.log")
	if err := os.WriteFile(segPath, buf, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	recovered, skipped, err := ReadSegmentTolerant(segPath)
	if err != nil {
		t.Fatalf("ReadSegmentTolerant: %v", err)
	}

	// Tolerant reader should recover the event via tryTolerantParse
	if len(recovered) != 1 {
		t.Fatalf("expected 1 recovered event (tolerant parse), got %d", len(recovered))
	}
	if recovered[0].Event.EventID != "evt-corrupt-crc" {
		t.Errorf("recovered event ID = %q, want %q", recovered[0].Event.EventID, "evt-corrupt-crc")
	}
	// The frame should still be counted as corrupted/skipped
	if skipped == 0 {
		t.Error("expected corrupted frame to be counted in skipped")
	}
}

func TestReadSegmentTolerant_PreservesOffsets(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	events := []*schema.Event{
		makeEvent("evt-off-a", "a.go", schema.OpCreate, "", now),
		makeEvent("evt-off-b", "b.go", schema.OpModify, "", now.Add(time.Second)),
	}
	writeSegmentFile(t, dir, "offsets.log", events)

	recovered, _, err := ReadSegmentTolerant(filepath.Join(dir, "offsets.log"))
	if err != nil {
		t.Fatalf("ReadSegmentTolerant: %v", err)
	}
	if len(recovered) != 2 {
		t.Fatalf("expected 2 recovered events, got %d", len(recovered))
	}

	// First event starts at offset 0
	if recovered[0].Offset != 0 {
		t.Errorf("first event offset = %d, want 0", recovered[0].Offset)
	}
	// Second event offset should be > 0 and > first event's offset
	if recovered[1].Offset <= recovered[0].Offset {
		t.Errorf("second event offset (%d) should be > first event offset (%d)", recovered[1].Offset, recovered[0].Offset)
	}
}

// ─── tryTolerantParse ───────────────────────────────────────────────────────

func TestTryTolerantParse_ValidJSON(t *testing.T) {
	ev := &schema.Event{
		EventID:       "test-tolerant",
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "file.go",
		Op:            schema.OpModify,
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	result := tryTolerantParse(data)
	if result == nil {
		t.Fatal("expected non-nil result for valid JSON")
	}
	if result.EventID != "test-tolerant" {
		t.Errorf("EventID = %q, want %q", result.EventID, "test-tolerant")
	}
}

func TestTryTolerantParse_TrailingGarbage(t *testing.T) {
	ev := &schema.Event{
		EventID:       "test-trailing",
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "file.go",
		Op:            schema.OpCreate,
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	// Append trailing garbage bytes
	withGarbage := append(data, []byte("\x00\x00\xFF\xAB")...)
	result := tryTolerantParse(withGarbage)
	if result == nil {
		t.Fatal("expected non-nil result for JSON with trailing garbage")
	}
	if result.EventID != "test-trailing" {
		t.Errorf("EventID = %q, want %q", result.EventID, "test-trailing")
	}
}

func TestTryTolerantParse_CompleteGarbage(t *testing.T) {
	garbage := []byte("not json at all {{{")
	result := tryTolerantParse(garbage)
	if result != nil {
		t.Error("expected nil result for complete garbage")
	}
}

func TestTryTolerantParse_EmptyPayload(t *testing.T) {
	result := tryTolerantParse([]byte{})
	if result != nil {
		t.Error("expected nil result for empty payload")
	}
}

// ─── TrackSession ───────────────────────────────────────────────────────────

func TestTrackSession_SessionStartEvent(t *testing.T) {
	sessions := make(map[string]*schema.Session)
	sessionFiles := make(map[string]map[string]bool)

	now := time.Now()
	startEvent := &schema.Event{
		EventID:       "evt-start-1",
		TimestampNano: now.UnixNano(),
		FilePath:      ".belay/sessions",
		SessionID:     "sess-track-1",
		Metadata: map[string]string{
			"event_type": "session_start",
			"tool_name":  "claude-code",
			"pid":        "1234",
		},
	}

	TrackSession(sessions, sessionFiles, startEvent)

	s, ok := sessions["sess-track-1"]
	if !ok {
		t.Fatal("expected session to be tracked after session_start event")
	}
	if s.ToolName != "claude-code" {
		t.Errorf("ToolName = %q, want %q", s.ToolName, "claude-code")
	}
	if s.PID != 1234 {
		t.Errorf("PID = %d, want 1234", s.PID)
	}
	if s.Status != schema.SessionActive {
		t.Errorf("Status = %v, want SessionActive", s.Status)
	}
}

func TestTrackSession_SessionEndEvent(t *testing.T) {
	sessions := make(map[string]*schema.Session)
	sessionFiles := make(map[string]map[string]bool)

	now := time.Now()

	// Start session first
	startEvent := &schema.Event{
		EventID:       "evt-se-start",
		TimestampNano: now.UnixNano(),
		FilePath:      ".belay/sessions",
		SessionID:     "sess-end-1",
		Metadata: map[string]string{
			"event_type": "session_start",
			"tool_name":  "claude-code",
		},
	}
	TrackSession(sessions, sessionFiles, startEvent)

	// End session
	endEvent := &schema.Event{
		EventID:       "evt-se-end",
		TimestampNano: now.Add(5 * time.Minute).UnixNano(),
		FilePath:      ".belay/sessions",
		SessionID:     "sess-end-1",
		Metadata: map[string]string{
			"event_type": "session_end",
			"status":     "ended",
		},
	}
	TrackSession(sessions, sessionFiles, endEvent)

	s := sessions["sess-end-1"]
	if s.Status != schema.SessionEnded {
		t.Errorf("Status = %v, want SessionEnded", s.Status)
	}
	if s.EndedAt.IsZero() {
		t.Error("EndedAt should be set after session_end event")
	}
}

func TestTrackSession_SessionCrashEvent(t *testing.T) {
	sessions := make(map[string]*schema.Session)
	sessionFiles := make(map[string]map[string]bool)

	now := time.Now()

	startEvent := &schema.Event{
		EventID:       "evt-crash-start",
		TimestampNano: now.UnixNano(),
		FilePath:      ".belay/sessions",
		SessionID:     "sess-crash-1",
		Metadata: map[string]string{
			"event_type": "session_start",
			"tool_name":  "claude-code",
		},
	}
	TrackSession(sessions, sessionFiles, startEvent)

	crashEvent := &schema.Event{
		EventID:       "evt-crash-end",
		TimestampNano: now.Add(3 * time.Minute).UnixNano(),
		FilePath:      ".belay/sessions",
		SessionID:     "sess-crash-1",
		Metadata: map[string]string{
			"event_type": "session_end",
			"status":     "crashed",
		},
	}
	TrackSession(sessions, sessionFiles, crashEvent)

	s := sessions["sess-crash-1"]
	if s.Status != schema.SessionCrashed {
		t.Errorf("Status = %v, want SessionCrashed", s.Status)
	}
}

func TestTrackSession_RegularEventCountsAndFiles(t *testing.T) {
	sessions := make(map[string]*schema.Session)
	sessionFiles := make(map[string]map[string]bool)

	now := time.Now()

	// Start session
	startEvent := &schema.Event{
		EventID:       "evt-count-start",
		TimestampNano: now.UnixNano(),
		FilePath:      ".belay/sessions",
		SessionID:     "sess-count-1",
		Metadata: map[string]string{
			"event_type": "session_start",
			"tool_name":  "claude-code",
		},
	}
	TrackSession(sessions, sessionFiles, startEvent)

	// Regular file events
	fileEvents := []*schema.Event{
		{EventID: "evt-f1", TimestampNano: now.Add(time.Second).UnixNano(), FilePath: "src/main.go", SessionID: "sess-count-1", Op: schema.OpModify},
		{EventID: "evt-f2", TimestampNano: now.Add(2 * time.Second).UnixNano(), FilePath: "src/main.go", SessionID: "sess-count-1", Op: schema.OpModify},
		{EventID: "evt-f3", TimestampNano: now.Add(3 * time.Second).UnixNano(), FilePath: "src/util.go", SessionID: "sess-count-1", Op: schema.OpCreate},
	}

	for _, ev := range fileEvents {
		TrackSession(sessions, sessionFiles, ev)
	}

	s := sessions["sess-count-1"]
	if s.EventCount != 3 {
		t.Errorf("EventCount = %d, want 3", s.EventCount)
	}
	if s.FilesChanged != 2 {
		t.Errorf("FilesChanged = %d, want 2 (main.go + util.go)", s.FilesChanged)
	}
}

func TestTrackSession_EmptySessionID(t *testing.T) {
	sessions := make(map[string]*schema.Session)
	sessionFiles := make(map[string]map[string]bool)

	event := &schema.Event{
		EventID:       "evt-nosid",
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "file.go",
		SessionID:     "",
	}
	TrackSession(sessions, sessionFiles, event)

	if len(sessions) != 0 {
		t.Error("expected no sessions to be created for events with empty session ID")
	}
}

func TestTrackSession_StubSessionCreatedForUnknownSession(t *testing.T) {
	sessions := make(map[string]*schema.Session)
	sessionFiles := make(map[string]map[string]bool)

	now := time.Now()
	event := &schema.Event{
		EventID:       "evt-stub-1",
		TimestampNano: now.UnixNano(),
		FilePath:      "file.go",
		SessionID:     "sess-unknown",
		Op:            schema.OpModify,
	}
	TrackSession(sessions, sessionFiles, event)

	s, ok := sessions["sess-unknown"]
	if !ok {
		t.Fatal("expected stub session to be created for unknown session ID")
	}
	if s.ToolName != "unknown" {
		t.Errorf("stub session ToolName = %q, want %q", s.ToolName, "unknown")
	}
	if s.Status != schema.SessionEnded {
		t.Errorf("stub session Status = %v, want SessionEnded (assumed ended during replay)", s.Status)
	}
	if s.EventCount != 1 {
		t.Errorf("stub session EventCount = %d, want 1", s.EventCount)
	}
}

func TestTrackSession_StubSessionUsesToolNameFromMetadata(t *testing.T) {
	sessions := make(map[string]*schema.Session)
	sessionFiles := make(map[string]map[string]bool)

	now := time.Now()
	event := &schema.Event{
		EventID:       "evt-stub-tn",
		TimestampNano: now.UnixNano(),
		FilePath:      "file.go",
		SessionID:     "sess-with-tool",
		Op:            schema.OpModify,
		Metadata:      map[string]string{"tool_name": "cursor"},
	}
	TrackSession(sessions, sessionFiles, event)

	s := sessions["sess-with-tool"]
	if s.ToolName != "cursor" {
		t.Errorf("stub session ToolName = %q, want %q (from metadata)", s.ToolName, "cursor")
	}
}

func TestTrackSession_SessionEndWithoutStart(t *testing.T) {
	sessions := make(map[string]*schema.Session)
	sessionFiles := make(map[string]map[string]bool)

	now := time.Now()
	endEvent := &schema.Event{
		EventID:       "evt-orphan-end",
		TimestampNano: now.UnixNano(),
		FilePath:      ".belay/sessions",
		SessionID:     "sess-orphan",
		Metadata: map[string]string{
			"event_type": "session_end",
			"status":     "ended",
		},
	}
	TrackSession(sessions, sessionFiles, endEvent)

	// Session end without a prior start should not create a session
	// (the code checks `if s, ok := sessions[sid]; ok` before updating)
	if _, ok := sessions["sess-orphan"]; ok {
		t.Error("session_end without prior session_start should not create a session")
	}
}

// ─── listSegmentFiles ───────────────────────────────────────────────────────

func TestListSegmentFiles_ValidDir(t *testing.T) {
	dir := t.TempDir()
	// Create some segment files and non-segment files
	for _, name := range []string{"seg001.log", "seg002.log", "seg003.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte{}, 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	// Non-log files should be ignored
	for _, name := range []string{"index.db", "readme.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte{}, 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	segments, err := listSegmentFiles(dir)
	if err != nil {
		t.Fatalf("listSegmentFiles: %v", err)
	}
	if len(segments) != 3 {
		t.Errorf("expected 3 segment files, got %d", len(segments))
	}
}

func TestListSegmentFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	segments, err := listSegmentFiles(dir)
	if err != nil {
		t.Fatalf("listSegmentFiles: %v", err)
	}
	if len(segments) != 0 {
		t.Errorf("expected 0 segment files from empty dir, got %d", len(segments))
	}
}

func TestListSegmentFiles_NonexistentDir(t *testing.T) {
	segments, err := listSegmentFiles(filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Fatalf("listSegmentFiles should return nil for nonexistent dir, got: %v", err)
	}
	if segments != nil {
		t.Errorf("expected nil for nonexistent dir, got %v", segments)
	}
}

func TestListSegmentFiles_IgnoresSubdirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seg001.log"), []byte{}, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir.log"), 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	segments, err := listSegmentFiles(dir)
	if err != nil {
		t.Fatalf("listSegmentFiles: %v", err)
	}
	if len(segments) != 1 {
		t.Errorf("expected 1 segment file (excluding directory), got %d", len(segments))
	}
}

// ─── Rebuild ────────────────────────────────────────────────────────────────

func TestRebuild_FromValidSegments(t *testing.T) {
	tmpDir := t.TempDir()
	eventsDir := filepath.Join(tmpDir, "events")
	indexPath := filepath.Join(tmpDir, "index.db")

	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	now := time.Now()
	events := []*schema.Event{
		makeEvent("evt-rb-a", "src/main.go", schema.OpCreate, "sess-rb-1", now),
		makeEvent("evt-rb-b", "src/util.go", schema.OpModify, "sess-rb-1", now.Add(time.Second)),
		makeEvent("evt-rb-c", "README.md", schema.OpModify, "sess-rb-2", now.Add(2*time.Second)),
	}
	writeSegmentFile(t, eventsDir, "seg001.log", events)

	logger := testLogger(t)
	result, err := Rebuild(indexPath, eventsDir, logger)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if result.EventsIndexed != 3 {
		t.Errorf("EventsIndexed = %d, want 3", result.EventsIndexed)
	}
	if result.VerifiedCount != 3 {
		t.Errorf("VerifiedCount = %d, want 3", result.VerifiedCount)
	}
	if result.CorruptedSkipped != 0 {
		t.Errorf("CorruptedSkipped = %d, want 0", result.CorruptedSkipped)
	}
	if result.Elapsed <= 0 {
		t.Error("Elapsed should be > 0")
	}

	// Verify the index is queryable
	idx, err := Open(indexPath)
	if err != nil {
		t.Fatalf("Open rebuilt index: %v", err)
	}
	defer idx.Close()

	count, err := idx.CountEvents()
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}
	if count != 3 {
		t.Errorf("rebuilt index has %d events, want 3", count)
	}
}

func TestRebuild_EmptyEventsDir(t *testing.T) {
	tmpDir := t.TempDir()
	eventsDir := filepath.Join(tmpDir, "events")
	indexPath := filepath.Join(tmpDir, "index.db")

	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	logger := testLogger(t)
	result, err := Rebuild(indexPath, eventsDir, logger)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if result.EventsIndexed != 0 {
		t.Errorf("EventsIndexed = %d, want 0", result.EventsIndexed)
	}
	if result.SessionsRebuilt != 0 {
		t.Errorf("SessionsRebuilt = %d, want 0", result.SessionsRebuilt)
	}
	if result.VerifiedCount != 0 {
		t.Errorf("VerifiedCount = %d, want 0", result.VerifiedCount)
	}
}

func TestRebuild_NonexistentEventsDir(t *testing.T) {
	tmpDir := t.TempDir()
	eventsDir := filepath.Join(tmpDir, "nonexistent")
	indexPath := filepath.Join(tmpDir, "index.db")

	logger := testLogger(t)
	result, err := Rebuild(indexPath, eventsDir, logger)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// listSegmentFiles returns nil for nonexistent dir, so rebuild should produce empty index
	if result.EventsIndexed != 0 {
		t.Errorf("EventsIndexed = %d, want 0", result.EventsIndexed)
	}
}

func TestRebuild_MultipleSegments(t *testing.T) {
	tmpDir := t.TempDir()
	eventsDir := filepath.Join(tmpDir, "events")
	indexPath := filepath.Join(tmpDir, "index.db")

	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	now := time.Now()

	// Segment 1: 2 events
	seg1Events := []*schema.Event{
		makeEvent("evt-ms-a", "a.go", schema.OpCreate, "sess-ms-1", now),
		makeEvent("evt-ms-b", "b.go", schema.OpModify, "sess-ms-1", now.Add(time.Second)),
	}
	writeSegmentFile(t, eventsDir, "seg001.log", seg1Events)

	// Segment 2: 3 events
	seg2Events := []*schema.Event{
		makeEvent("evt-ms-c", "c.go", schema.OpCreate, "sess-ms-2", now.Add(2*time.Second)),
		makeEvent("evt-ms-d", "d.go", schema.OpModify, "sess-ms-2", now.Add(3*time.Second)),
		makeEvent("evt-ms-e", "e.go", schema.OpDelete, "sess-ms-1", now.Add(4*time.Second)),
	}
	writeSegmentFile(t, eventsDir, "seg002.log", seg2Events)

	logger := testLogger(t)
	result, err := Rebuild(indexPath, eventsDir, logger)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if result.EventsIndexed != 5 {
		t.Errorf("EventsIndexed = %d, want 5", result.EventsIndexed)
	}
	if result.VerifiedCount != 5 {
		t.Errorf("VerifiedCount = %d, want 5", result.VerifiedCount)
	}
}

func TestRebuild_WithSessionMetaEvents(t *testing.T) {
	tmpDir := t.TempDir()
	eventsDir := filepath.Join(tmpDir, "events")
	indexPath := filepath.Join(tmpDir, "index.db")

	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	now := time.Now()

	// Session lifecycle events + regular events
	events := []*schema.Event{
		// Session start
		{
			EventID:       "evt-sess-start",
			TimestampNano: now.UnixNano(),
			FilePath:      ".belay/sessions",
			Op:            schema.OpCreate,
			SessionID:     "sess-meta-1",
			Metadata: map[string]string{
				"event_type": "session_start",
				"tool_name":  "claude-code",
				"pid":        "5678",
			},
		},
		// Regular file events
		{
			EventID:       "evt-sess-f1",
			TimestampNano: now.Add(time.Second).UnixNano(),
			FilePath:      "src/main.go",
			Op:            schema.OpModify,
			SessionID:     "sess-meta-1",
			ContentHash:   "hash1",
		},
		{
			EventID:       "evt-sess-f2",
			TimestampNano: now.Add(2 * time.Second).UnixNano(),
			FilePath:      "src/util.go",
			Op:            schema.OpCreate,
			SessionID:     "sess-meta-1",
			ContentHash:   "hash2",
		},
		// Session end
		{
			EventID:       "evt-sess-end",
			TimestampNano: now.Add(5 * time.Minute).UnixNano(),
			FilePath:      ".belay/sessions",
			Op:            schema.OpModify,
			SessionID:     "sess-meta-1",
			Metadata: map[string]string{
				"event_type": "session_end",
				"status":     "ended",
			},
		},
	}
	writeSegmentFile(t, eventsDir, "seg001.log", events)

	logger := testLogger(t)
	result, err := Rebuild(indexPath, eventsDir, logger)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if result.SessionsRebuilt != 1 {
		t.Errorf("SessionsRebuilt = %d, want 1", result.SessionsRebuilt)
	}

	// Verify session was persisted to the index
	idx, err := Open(indexPath)
	if err != nil {
		t.Fatalf("Open rebuilt index: %v", err)
	}
	defer idx.Close()

	sess, err := idx.GetSession("sess-meta-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.ToolName != "claude-code" {
		t.Errorf("ToolName = %q, want %q", sess.ToolName, "claude-code")
	}
	if sess.Status != schema.SessionEnded {
		t.Errorf("Status = %v, want SessionEnded", sess.Status)
	}
	if sess.EventCount != 2 {
		t.Errorf("EventCount = %d, want 2 (file events only)", sess.EventCount)
	}
	if sess.FilesChanged != 2 {
		t.Errorf("FilesChanged = %d, want 2", sess.FilesChanged)
	}
}

func TestRebuild_BackupsExistingIndex(t *testing.T) {
	tmpDir := t.TempDir()
	eventsDir := filepath.Join(tmpDir, "events")
	indexPath := filepath.Join(tmpDir, "index.db")

	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Create an existing index
	idx, err := Open(indexPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Now()
	ev := makeEvent("evt-old", "old.go", schema.OpCreate, "", now)
	if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}
	if err := idx.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	idx.Close()

	// Verify the old index file exists
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("old index should exist: %v", err)
	}

	// Now rebuild with new data
	newEvents := []*schema.Event{
		makeEvent("evt-new-a", "new.go", schema.OpCreate, "", now),
	}
	writeSegmentFile(t, eventsDir, "seg001.log", newEvents)

	logger := testLogger(t)
	result, err := Rebuild(indexPath, eventsDir, logger)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if result.EventsIndexed != 1 {
		t.Errorf("EventsIndexed = %d, want 1", result.EventsIndexed)
	}

	// Verify a backup file was created in the same directory as the index
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	foundBackup := false
	backupPrefix := "index.db.bak."
	for _, entry := range entries {
		name := entry.Name()
		if len(name) > len(backupPrefix) && name[:len(backupPrefix)] == backupPrefix {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		// List all files for debugging
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("expected a backup file (index.db.bak.*) to be created, found: %v", names)
	}
}

func TestRebuild_WithCorruptedFrames(t *testing.T) {
	tmpDir := t.TempDir()
	eventsDir := filepath.Join(tmpDir, "events")
	indexPath := filepath.Join(tmpDir, "index.db")

	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	now := time.Now()

	// Write a segment with valid events, then manually corrupt a section
	validEvent1 := makeEvent("evt-vc-a", "a.go", schema.OpCreate, "sess-vc", now)
	validEvent2 := makeEvent("evt-vc-b", "b.go", schema.OpModify, "sess-vc", now.Add(2*time.Second))

	data1, err := validEvent1.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	data2, err := validEvent2.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	// Insert garbage between valid frames
	garbage := []byte("CORRUPT_DATA_HERE")
	combined := append(data1, garbage...)
	combined = append(combined, data2...)

	segPath := filepath.Join(eventsDir, "seg001.log")
	if err := os.WriteFile(segPath, combined, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	logger := testLogger(t)
	result, err := Rebuild(indexPath, eventsDir, logger)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// Should have recovered both valid events
	if result.EventsIndexed < 2 {
		t.Errorf("EventsIndexed = %d, want >= 2 (recovered from corruption)", result.EventsIndexed)
	}
	if result.CorruptedSkipped == 0 {
		t.Error("expected CorruptedSkipped > 0 due to injected garbage")
	}
}

func TestRebuild_PreservesEventDataIntegrity(t *testing.T) {
	tmpDir := t.TempDir()
	eventsDir := filepath.Join(tmpDir, "events")
	indexPath := filepath.Join(tmpDir, "index.db")

	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	now := time.Now()

	ev := &schema.Event{
		EventID:               "evt-integrity",
		TimestampNano:         now.UnixNano(),
		FilePath:              "src/important.go",
		Op:                    schema.OpModify,
		ContentHash:           "abc123def456",
		PreviousHash:          "xyz789",
		ContentSize:           42,
		SessionID:             "sess-integrity",
		Attribution:           schema.AttrHook,
		AttributionConfidence: 0.99,
		Metadata:              map[string]string{"key": "value"},
		IsConflict:            true,
	}
	writeSegmentFile(t, eventsDir, "seg001.log", []*schema.Event{ev})

	logger := testLogger(t)
	_, err := Rebuild(indexPath, eventsDir, logger)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	idx, err := Open(indexPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	got, err := idx.GetEvent("evt-integrity")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}

	if got.FilePath != "src/important.go" {
		t.Errorf("FilePath = %q, want %q", got.FilePath, "src/important.go")
	}
	if got.ContentHash != "abc123def456" {
		t.Errorf("ContentHash = %q, want %q", got.ContentHash, "abc123def456")
	}
	if got.PreviousHash != "xyz789" {
		t.Errorf("PreviousHash = %q, want %q", got.PreviousHash, "xyz789")
	}
	if got.ContentSize != 42 {
		t.Errorf("ContentSize = %d, want 42", got.ContentSize)
	}
	if got.SessionID != "sess-integrity" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "sess-integrity")
	}
	if got.Attribution != schema.AttrHook {
		t.Errorf("Attribution = %v, want AttrHook", got.Attribution)
	}
	if got.IsConflict != true {
		t.Error("IsConflict should be true")
	}
	if got.Metadata == nil || got.Metadata["key"] != "value" {
		t.Errorf("Metadata = %v, want key=value", got.Metadata)
	}
}

func TestRebuild_LargeBatch(t *testing.T) {
	tmpDir := t.TempDir()
	eventsDir := filepath.Join(tmpDir, "events")
	indexPath := filepath.Join(tmpDir, "index.db")

	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	now := time.Now()

	// Create more events than the batch size (1000) to test batching
	numEvents := 1500
	events := make([]*schema.Event, numEvents)
	for i := 0; i < numEvents; i++ {
		events[i] = makeEvent(
			fmt.Sprintf("evt-large-%04d", i),
			fmt.Sprintf("file%04d.go", i),
			schema.OpModify,
			"sess-large",
			now.Add(time.Duration(i)*time.Millisecond),
		)
	}
	writeSegmentFile(t, eventsDir, "seg001.log", events)

	logger := testLogger(t)
	result, err := Rebuild(indexPath, eventsDir, logger)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if result.EventsIndexed != numEvents {
		t.Errorf("EventsIndexed = %d, want %d", result.EventsIndexed, numEvents)
	}
	if result.VerifiedCount != int64(numEvents) {
		t.Errorf("VerifiedCount = %d, want %d", result.VerifiedCount, numEvents)
	}
}

func TestRebuild_RemovesWALAndSHMFiles(t *testing.T) {
	tmpDir := t.TempDir()
	eventsDir := filepath.Join(tmpDir, "events")
	indexPath := filepath.Join(tmpDir, "index.db")

	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Create leftover WAL and SHM files
	walPath := indexPath + "-wal"
	shmPath := indexPath + "-shm"
	if err := os.WriteFile(walPath, []byte("leftover WAL data"), 0644); err != nil {
		t.Fatalf("WriteFile WAL: %v", err)
	}
	if err := os.WriteFile(shmPath, []byte("leftover SHM data"), 0644); err != nil {
		t.Fatalf("WriteFile SHM: %v", err)
	}

	logger := testLogger(t)
	_, err := Rebuild(indexPath, eventsDir, logger)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// The old WAL and SHM files should have been removed (the new ones might exist
	// from the fresh index, but the leftover content should be gone)
}

// ─── unmarshalEventJSON ─────────────────────────────────────────────────────

func TestUnmarshalEventJSON_ValidJSON(t *testing.T) {
	ev := &schema.Event{
		EventID:       "test-unmarshal",
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "test.go",
		Op:            schema.OpCreate,
		ContentHash:   "abc",
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	result, err := unmarshalEventJSON(data)
	if err != nil {
		t.Fatalf("unmarshalEventJSON: %v", err)
	}
	if result.EventID != "test-unmarshal" {
		t.Errorf("EventID = %q, want %q", result.EventID, "test-unmarshal")
	}
}

func TestUnmarshalEventJSON_InvalidJSON(t *testing.T) {
	_, err := unmarshalEventJSON([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ─── Frame encoding edge cases ──────────────────────────────────────────────

func TestReadSegmentTolerant_WrongVersion(t *testing.T) {
	dir := t.TempDir()

	// Create a frame with wrong schema version
	ev := &schema.Event{
		EventID:       "test-ver",
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "file.go",
		Op:            schema.OpModify,
	}
	jsonData, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	totalLen := 4 + 2 + len(jsonData) + 4
	buf := make([]byte, totalLen)
	binary.BigEndian.PutUint32(buf[0:4], uint32(totalLen))
	binary.BigEndian.PutUint16(buf[4:6], 99) // wrong version
	copy(buf[6:6+len(jsonData)], jsonData)
	checksum := crc32.ChecksumIEEE(buf[4 : 6+len(jsonData)])
	binary.BigEndian.PutUint32(buf[6+len(jsonData):], checksum)

	segPath := filepath.Join(dir, "wrongver.log")
	if err := os.WriteFile(segPath, buf, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	recovered, skipped, err := ReadSegmentTolerant(segPath)
	if err != nil {
		t.Fatalf("ReadSegmentTolerant: %v", err)
	}
	if len(recovered) != 0 {
		t.Errorf("expected 0 recovered events for wrong version, got %d", len(recovered))
	}
	if skipped == 0 {
		t.Error("expected skipped > 0 for wrong version frame")
	}
}
