package schema

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// --- UUIDv7 Tests ---

func TestNewEventID_Format(t *testing.T) {
	id := NewEventID()

	// Should be in standard UUID format: 8-4-4-4-12
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Fatalf("expected 5 dash-separated parts, got %d: %s", len(parts), id)
	}

	expectedLens := []int{8, 4, 4, 4, 12}
	for i, part := range parts {
		if len(part) != expectedLens[i] {
			t.Errorf("part %d: expected length %d, got %d (%s)", i, expectedLens[i], len(part), part)
		}
	}
}

func TestNewEventID_Version7Bits(t *testing.T) {
	id := NewEventID()
	// The 3rd group should start with '7' (version 7)
	parts := strings.Split(id, "-")
	if parts[2][0] != '7' {
		t.Errorf("expected version nibble '7', got '%c' in UUID %s", parts[2][0], id)
	}
}

func TestNewEventID_VariantBits(t *testing.T) {
	id := NewEventID()
	// The 4th group's first character should be 8, 9, a, or b (variant 10xx)
	parts := strings.Split(id, "-")
	firstChar := parts[3][0]
	if firstChar != '8' && firstChar != '9' && firstChar != 'a' && firstChar != 'b' {
		t.Errorf("expected variant char in [8,9,a,b], got '%c' in UUID %s", firstChar, id)
	}
}

func TestNewEventID_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := NewEventID()
		if seen[id] {
			t.Fatalf("duplicate UUID generated: %s", id)
		}
		seen[id] = true
	}
}

func TestNewEventID_TimeSortable(t *testing.T) {
	id1 := NewEventID()
	time.Sleep(2 * time.Millisecond)
	id2 := NewEventID()

	// UUIDv7 encodes millisecond timestamp in the first 48 bits,
	// so later timestamps should produce lexicographically greater UUIDs
	if id1 >= id2 {
		t.Errorf("expected id1 < id2 (time-sortable), got id1=%s id2=%s", id1, id2)
	}
}

// --- Operation Tests ---

func TestOperation_String(t *testing.T) {
	tests := []struct {
		op   Operation
		want string
	}{
		{OpCreate, "CREATE"},
		{OpModify, "MODIFY"},
		{OpDelete, "DELETE"},
		{OpRename, "RENAME"},
		{Operation(0), "UNKNOWN"},
		{Operation(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		got := tt.op.String()
		if got != tt.want {
			t.Errorf("Operation(%d).String() = %q, want %q", tt.op, got, tt.want)
		}
	}
}

func TestParseOperation(t *testing.T) {
	tests := []struct {
		input   string
		want    Operation
		wantErr bool
	}{
		{"CREATE", OpCreate, false},
		{"create", OpCreate, false},
		{"MODIFY", OpModify, false},
		{"modify", OpModify, false},
		{"DELETE", OpDelete, false},
		{"delete", OpDelete, false},
		{"RENAME", OpRename, false},
		{"rename", OpRename, false},
		{"INVALID", 0, true},
		{"", 0, true},
	}

	for _, tt := range tests {
		got, err := ParseOperation(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseOperation(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseOperation(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestOperation_JSONRoundtrip(t *testing.T) {
	ops := []Operation{OpCreate, OpModify, OpDelete, OpRename}

	for _, op := range ops {
		data, err := json.Marshal(op)
		if err != nil {
			t.Fatalf("marshal Operation %v: %v", op, err)
		}

		var got Operation
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal Operation from %s: %v", data, err)
		}

		if got != op {
			t.Errorf("JSON roundtrip: got %v, want %v", got, op)
		}
	}
}

// --- AttributionMethod Tests ---

func TestAttributionMethod_String(t *testing.T) {
	tests := []struct {
		attr AttributionMethod
		want string
	}{
		{AttrNone, "none"},
		{AttrPID, "pid"},
		{AttrTemporal, "temporal"},
		{AttrHeuristic, "heuristic"},
		{AttrManual, "manual"},
		{AttrHook, "hook"},
		{AttributionMethod(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.attr.String()
		if got != tt.want {
			t.Errorf("AttributionMethod(%d).String() = %q, want %q", tt.attr, got, tt.want)
		}
	}
}

func TestParseAttributionMethod(t *testing.T) {
	tests := []struct {
		input string
		want  AttributionMethod
	}{
		{"pid", AttrPID},
		{"temporal", AttrTemporal},
		{"heuristic", AttrHeuristic},
		{"manual", AttrManual},
		{"hook", AttrHook},
		{"none", AttrNone},
		{"", AttrNone},
		{"invalid", AttrNone},
	}

	for _, tt := range tests {
		got := ParseAttributionMethod(tt.input)
		if got != tt.want {
			t.Errorf("ParseAttributionMethod(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- Event Tests ---

func TestEvent_TimestampRoundtrip(t *testing.T) {
	e := &Event{}
	now := time.Now()
	e.SetTimestamp(now)

	got := e.Timestamp()
	if !got.Equal(now) {
		t.Errorf("Timestamp roundtrip: got %v, want %v", got, now)
	}
}

func TestEvent_MarshalBinary_Roundtrip(t *testing.T) {
	original := &Event{
		EventID:               NewEventID(),
		Version:               SchemaVersion,
		TimestampNano:         time.Now().UnixNano(),
		FilePath:              "src/main.go",
		Op:                    OpModify,
		ContentHash:           "abc123def456",
		PreviousHash:          "000111222333",
		ContentSize:           4096,
		SessionID:             "session-001",
		Attribution:           AttrHook,
		AttributionConfidence: 1.0,
		Metadata:              map[string]string{"tool": "claude"},
	}

	data, err := original.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	reader := bytes.NewReader(data)
	got, bytesRead, err := UnmarshalBinaryFrame(reader)
	if err != nil {
		t.Fatalf("UnmarshalBinaryFrame: %v", err)
	}

	if bytesRead != len(data) {
		t.Errorf("bytesRead = %d, want %d", bytesRead, len(data))
	}

	if got.EventID != original.EventID {
		t.Errorf("EventID: got %q, want %q", got.EventID, original.EventID)
	}
	if got.TimestampNano != original.TimestampNano {
		t.Errorf("TimestampNano: got %d, want %d", got.TimestampNano, original.TimestampNano)
	}
	if got.FilePath != original.FilePath {
		t.Errorf("FilePath: got %q, want %q", got.FilePath, original.FilePath)
	}
	if got.Op != original.Op {
		t.Errorf("Op: got %v, want %v", got.Op, original.Op)
	}
	if got.ContentHash != original.ContentHash {
		t.Errorf("ContentHash: got %q, want %q", got.ContentHash, original.ContentHash)
	}
	if got.PreviousHash != original.PreviousHash {
		t.Errorf("PreviousHash: got %q, want %q", got.PreviousHash, original.PreviousHash)
	}
	if got.ContentSize != original.ContentSize {
		t.Errorf("ContentSize: got %d, want %d", got.ContentSize, original.ContentSize)
	}
	if got.SessionID != original.SessionID {
		t.Errorf("SessionID: got %q, want %q", got.SessionID, original.SessionID)
	}
	if got.Attribution != original.Attribution {
		t.Errorf("Attribution: got %v, want %v", got.Attribution, original.Attribution)
	}
	if got.AttributionConfidence != original.AttributionConfidence {
		t.Errorf("AttributionConfidence: got %f, want %f", got.AttributionConfidence, original.AttributionConfidence)
	}
	if got.Metadata["tool"] != "claude" {
		t.Errorf("Metadata[tool]: got %q, want %q", got.Metadata["tool"], "claude")
	}
}

func TestEvent_MarshalBinary_MultipleEvents(t *testing.T) {
	events := []*Event{
		{EventID: NewEventID(), TimestampNano: time.Now().UnixNano(), FilePath: "a.go", Op: OpCreate, ContentHash: "aaa"},
		{EventID: NewEventID(), TimestampNano: time.Now().UnixNano(), FilePath: "b.go", Op: OpModify, ContentHash: "bbb"},
		{EventID: NewEventID(), TimestampNano: time.Now().UnixNano(), FilePath: "c.go", Op: OpDelete},
	}

	var buf bytes.Buffer
	for _, e := range events {
		data, err := e.MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary: %v", err)
		}
		buf.Write(data)
	}

	reader := bytes.NewReader(buf.Bytes())
	for i, original := range events {
		got, _, err := UnmarshalBinaryFrame(reader)
		if err != nil {
			t.Fatalf("UnmarshalBinaryFrame event %d: %v", i, err)
		}
		if got.EventID != original.EventID {
			t.Errorf("event %d: EventID got %q, want %q", i, got.EventID, original.EventID)
		}
		if got.FilePath != original.FilePath {
			t.Errorf("event %d: FilePath got %q, want %q", i, got.FilePath, original.FilePath)
		}
		if got.Op != original.Op {
			t.Errorf("event %d: Op got %v, want %v", i, got.Op, original.Op)
		}
	}
}

func TestEvent_MarshalBinary_MinimalEvent(t *testing.T) {
	// A zero-value Operation serializes as "UNKNOWN" which cannot be unmarshaled,
	// so the minimal valid event needs at least a valid Op.
	e := &Event{Op: OpCreate}
	data, err := e.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary minimal event: %v", err)
	}

	reader := bytes.NewReader(data)
	got, _, err := UnmarshalBinaryFrame(reader)
	if err != nil {
		t.Fatalf("UnmarshalBinaryFrame minimal event: %v", err)
	}

	if got.FilePath != "" {
		t.Errorf("expected empty FilePath, got %q", got.FilePath)
	}
	if got.Op != OpCreate {
		t.Errorf("expected OpCreate, got %v", got.Op)
	}
	if got.ContentHash != "" {
		t.Errorf("expected empty ContentHash, got %q", got.ContentHash)
	}
}

func TestEvent_MarshalBinary_ZeroOpFailsUnmarshal(t *testing.T) {
	// Verify that a zero-value Operation cannot roundtrip (documents the behavior)
	e := &Event{} // Op is 0 which maps to "UNKNOWN"
	data, err := e.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	reader := bytes.NewReader(data)
	_, _, err = UnmarshalBinaryFrame(reader)
	if err == nil {
		t.Fatal("expected error unmarshaling event with zero-value Operation, got nil")
	}
}

func TestEvent_MarshalBinary_WithRename(t *testing.T) {
	e := &Event{
		EventID:       NewEventID(),
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "new/path.go",
		OldPath:       "old/path.go",
		Op:            OpRename,
	}

	data, err := e.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	reader := bytes.NewReader(data)
	got, _, err := UnmarshalBinaryFrame(reader)
	if err != nil {
		t.Fatalf("UnmarshalBinaryFrame: %v", err)
	}

	if got.OldPath != "old/path.go" {
		t.Errorf("OldPath: got %q, want %q", got.OldPath, "old/path.go")
	}
	if got.FilePath != "new/path.go" {
		t.Errorf("FilePath: got %q, want %q", got.FilePath, "new/path.go")
	}
}

func TestUnmarshalBinaryFrame_CorruptedChecksum(t *testing.T) {
	e := &Event{EventID: NewEventID(), FilePath: "test.go", Op: OpCreate}
	data, err := e.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	// Corrupt the checksum (last 4 bytes)
	data[len(data)-1] ^= 0xff

	reader := bytes.NewReader(data)
	_, _, err = UnmarshalBinaryFrame(reader)
	if err == nil {
		t.Fatal("expected error for corrupted checksum, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("expected checksum mismatch error, got: %v", err)
	}
}

func TestUnmarshalBinaryFrame_TruncatedData(t *testing.T) {
	e := &Event{EventID: NewEventID(), FilePath: "test.go", Op: OpCreate}
	data, err := e.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	// Truncate the data
	reader := bytes.NewReader(data[:len(data)/2])
	_, _, err = UnmarshalBinaryFrame(reader)
	if err == nil {
		t.Fatal("expected error for truncated data, got nil")
	}
}

func TestUnmarshalBinaryFrame_EmptyReader(t *testing.T) {
	reader := bytes.NewReader(nil)
	_, _, err := UnmarshalBinaryFrame(reader)
	if err == nil {
		t.Fatal("expected error for empty reader, got nil")
	}
}

func TestContentHashForBytes(t *testing.T) {
	data := []byte("hello world")
	hash := ContentHashForBytes(data)

	// SHA-256 of "hello world" is well-known
	expected := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	if hash != expected {
		t.Errorf("ContentHashForBytes(\"hello world\") = %q, want %q", hash, expected)
	}
}

func TestContentHashForBytes_Empty(t *testing.T) {
	hash := ContentHashForBytes([]byte{})
	// SHA-256 of empty bytes
	expected := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if hash != expected {
		t.Errorf("ContentHashForBytes(empty) = %q, want %q", hash, expected)
	}
}

func TestContentHashForBytes_Deterministic(t *testing.T) {
	data := []byte("some content")
	h1 := ContentHashForBytes(data)
	h2 := ContentHashForBytes(data)
	if h1 != h2 {
		t.Errorf("same content produced different hashes: %q vs %q", h1, h2)
	}
}

func TestContentHashForBytes_DifferentContent(t *testing.T) {
	h1 := ContentHashForBytes([]byte("aaa"))
	h2 := ContentHashForBytes([]byte("bbb"))
	if h1 == h2 {
		t.Error("different content produced same hash")
	}
}

// --- Event ToJSON Tests ---

func TestEvent_ToJSON(t *testing.T) {
	now := time.Now()
	e := &Event{
		EventID:               "test-id",
		TimestampNano:         now.UnixNano(),
		FilePath:              "src/main.go",
		Op:                    OpModify,
		ContentHash:           "hash123",
		ContentSize:           100,
		SessionID:             "sess-1",
		Attribution:           AttrHook,
		AttributionConfidence: 0.95,
		Metadata:              map[string]string{"key": "val"},
	}

	j := e.ToJSON()
	if j.EventID != "test-id" {
		t.Errorf("EventID: got %q, want %q", j.EventID, "test-id")
	}
	if j.Operation != "MODIFY" {
		t.Errorf("Operation: got %q, want %q", j.Operation, "MODIFY")
	}
	if j.AttributionMethod != "hook" {
		t.Errorf("AttributionMethod: got %q, want %q", j.AttributionMethod, "hook")
	}
	if j.TimestampNano != now.UnixNano() {
		t.Errorf("TimestampNano: got %d, want %d", j.TimestampNano, now.UnixNano())
	}
	if j.Timestamp == "" {
		t.Error("Timestamp should not be empty")
	}
}

// --- Session Tests ---

func TestSessionStatus_String(t *testing.T) {
	tests := []struct {
		status SessionStatus
		want   string
	}{
		{SessionActive, "active"},
		{SessionEnded, "ended"},
		{SessionCrashed, "crashed"},
		{SessionStatus(0), "unknown"},
		{SessionStatus(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.status.String()
		if got != tt.want {
			t.Errorf("SessionStatus(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestSession_Duration_WithEndTime(t *testing.T) {
	start := time.Now().Add(-10 * time.Minute)
	end := time.Now()

	s := &Session{
		StartedAt: start,
		EndedAt:   end,
	}

	dur := s.Duration()
	expected := end.Sub(start)
	if dur != expected {
		t.Errorf("Duration: got %v, want %v", dur, expected)
	}
}

func TestSession_Duration_ActiveSession(t *testing.T) {
	start := time.Now().Add(-5 * time.Minute)

	s := &Session{
		StartedAt: start,
		// EndedAt is zero value
	}

	dur := s.Duration()
	// Should be approximately 5 minutes (since EndedAt is zero, uses time.Since)
	if dur < 4*time.Minute || dur > 6*time.Minute {
		t.Errorf("Duration for active session: got %v, expected ~5m", dur)
	}
}

func TestSession_ToJSON(t *testing.T) {
	start := time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 6, 10, 30, 0, 0, time.UTC)

	s := &Session{
		SessionID:    "sess-abc",
		ToolName:     "claude-code",
		PID:          12345,
		Status:       SessionEnded,
		StartedAt:    start,
		EndedAt:      end,
		Label:        "my session",
		FilesChanged: 5,
		EventCount:   10,
		Metadata:     map[string]string{"branch": "main"},
	}

	j := s.ToJSON()
	if j.SessionID != "sess-abc" {
		t.Errorf("SessionID: got %q, want %q", j.SessionID, "sess-abc")
	}
	if j.ToolName != "claude-code" {
		t.Errorf("ToolName: got %q, want %q", j.ToolName, "claude-code")
	}
	if j.PID != 12345 {
		t.Errorf("PID: got %d, want %d", j.PID, 12345)
	}
	if j.Status != "ended" {
		t.Errorf("Status: got %q, want %q", j.Status, "ended")
	}
	if j.Duration != "30m0s" {
		t.Errorf("Duration: got %q, want %q", j.Duration, "30m0s")
	}
	if j.Label != "my session" {
		t.Errorf("Label: got %q, want %q", j.Label, "my session")
	}
	if j.FilesChanged != 5 {
		t.Errorf("FilesChanged: got %d, want %d", j.FilesChanged, 5)
	}
	if j.EventCount != 10 {
		t.Errorf("EventCount: got %d, want %d", j.EventCount, 10)
	}
	if j.EndedAt == "" {
		t.Error("EndedAt should not be empty for ended session")
	}
}

func TestSession_ToJSON_ActiveSession(t *testing.T) {
	s := &Session{
		SessionID: "sess-active",
		Status:    SessionActive,
		StartedAt: time.Now(),
	}

	j := s.ToJSON()
	if j.EndedAt != "" {
		t.Errorf("EndedAt should be empty for active session, got %q", j.EndedAt)
	}
}

func TestSession_FieldDefaults(t *testing.T) {
	s := &Session{}

	if s.SessionID != "" {
		t.Error("SessionID should default to empty")
	}
	if s.ToolName != "" {
		t.Error("ToolName should default to empty")
	}
	if s.PID != 0 {
		t.Error("PID should default to 0")
	}
	if s.FilesChanged != 0 {
		t.Error("FilesChanged should default to 0")
	}
	if s.EventCount != 0 {
		t.Error("EventCount should default to 0")
	}
	if s.Status != SessionStatus(0) {
		t.Error("Status should default to 0")
	}
	if s.Metadata != nil {
		t.Error("Metadata should default to nil")
	}
}
