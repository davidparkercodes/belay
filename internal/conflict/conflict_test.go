package conflict

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
)

func openTestIndex(t *testing.T) *index.Index {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx
}

func indexEvent(t *testing.T, idx *index.Index, id, filePath string, op schema.Operation, sessionID string, ts time.Time) *schema.Event {
	t.Helper()
	ev := &schema.Event{
		EventID:       id,
		TimestampNano: ts.UnixNano(),
		FilePath:      filePath,
		Op:            op,
		ContentHash:   "hash-" + id,
		SessionID:     sessionID,
		Attribution:   schema.AttrPID,
		AttributionConfidence: 0.9,
	}
	if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}
	return ev
}

// ─── Severity ───────────────────────────────────────────────────────────────

func TestSeverity_String(t *testing.T) {
	tests := []struct {
		s    Severity
		want string
	}{
		{SeverityLow, "LOW"},
		{SeverityMedium, "MEDIUM"},
		{SeverityHigh, "HIGH"},
		{SeverityCritical, "CRITICAL"},
		{Severity(99), "UNKNOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.s.String(); got != tt.want {
				t.Errorf("Severity(%d).String() = %q, want %q", tt.s, got, tt.want)
			}
		})
	}
}

// ─── NewDetector ────────────────────────────────────────────────────────────

func TestNewDetector_DefaultWindow(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 0)
	if d.windowSize != 60*time.Second {
		t.Errorf("default windowSize = %v, want 60s", d.windowSize)
	}
}

func TestNewDetector_CustomWindow(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 30*time.Second)
	if d.windowSize != 30*time.Second {
		t.Errorf("windowSize = %v, want 30s", d.windowSize)
	}
}

// ─── Two sessions modifying same file = conflict ────────────────────────────

func TestDetectSince_TwoSessionsSameFile(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)
	base := time.Now().Add(-5 * time.Minute)

	// Two different sessions modify the same file within the window
	indexEvent(t, idx, "ev1", "src/main.go", schema.OpModify, "sess-A", base)
	indexEvent(t, idx, "ev2", "src/main.go", schema.OpModify, "sess-B", base.Add(10*time.Second))

	conflicts, err := d.DetectSince(base.Add(-time.Second))
	if err != nil {
		t.Fatalf("DetectSince: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}

	c := conflicts[0]
	if c.FilePath != "src/main.go" {
		t.Errorf("FilePath = %q, want %q", c.FilePath, "src/main.go")
	}
	if len(c.Sessions) != 2 {
		t.Errorf("expected 2 sessions in conflict, got %d", len(c.Sessions))
	}
	if len(c.Events) < 2 {
		t.Errorf("expected at least 2 events in conflict, got %d", len(c.Events))
	}
	if c.ID == "" {
		t.Error("conflict ID should not be empty")
	}
	if c.Resolved {
		t.Error("new conflict should not be resolved")
	}
}

// ─── Two sessions modifying different files = no conflict ───────────────────

func TestDetectSince_DifferentFiles(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)
	base := time.Now().Add(-5 * time.Minute)

	indexEvent(t, idx, "ev1", "src/main.go", schema.OpModify, "sess-A", base)
	indexEvent(t, idx, "ev2", "src/util.go", schema.OpModify, "sess-B", base.Add(5*time.Second))

	conflicts, err := d.DetectSince(base.Add(-time.Second))
	if err != nil {
		t.Fatalf("DetectSince: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts for different files, got %d", len(conflicts))
	}
}

// ─── Single session modifying a file = no conflict ──────────────────────────

func TestDetectSince_SingleSession(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)
	base := time.Now().Add(-5 * time.Minute)

	indexEvent(t, idx, "ev1", "src/main.go", schema.OpModify, "sess-A", base)
	indexEvent(t, idx, "ev2", "src/main.go", schema.OpModify, "sess-A", base.Add(5*time.Second))
	indexEvent(t, idx, "ev3", "src/main.go", schema.OpModify, "sess-A", base.Add(10*time.Second))

	conflicts, err := d.DetectSince(base.Add(-time.Second))
	if err != nil {
		t.Fatalf("DetectSince: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts for single session, got %d", len(conflicts))
	}
}

// ─── Events outside window = no conflict ────────────────────────────────────

func TestDetectSince_OutsideWindow(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 10*time.Second) // tight 10s window
	base := time.Now().Add(-5 * time.Minute)

	indexEvent(t, idx, "ev1", "src/main.go", schema.OpModify, "sess-A", base)
	indexEvent(t, idx, "ev2", "src/main.go", schema.OpModify, "sess-B", base.Add(30*time.Second))

	conflicts, err := d.DetectSince(base.Add(-time.Second))
	if err != nil {
		t.Fatalf("DetectSince: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts (outside window), got %d", len(conflicts))
	}
}

// ─── Events without session ID are not conflicts ────────────────────────────

func TestDetectSince_NoSessionID(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)
	base := time.Now().Add(-5 * time.Minute)

	indexEvent(t, idx, "ev1", "src/main.go", schema.OpModify, "", base)
	indexEvent(t, idx, "ev2", "src/main.go", schema.OpModify, "", base.Add(5*time.Second))

	conflicts, err := d.DetectSince(base.Add(-time.Second))
	if err != nil {
		t.Fatalf("DetectSince: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts (no session IDs), got %d", len(conflicts))
	}
}

// ─── Multiple conflicts ─────────────────────────────────────────────────────

func TestDetectSince_MultipleConflicts(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)
	base := time.Now().Add(-5 * time.Minute)

	// Conflict on file A
	indexEvent(t, idx, "ev1", "fileA.go", schema.OpModify, "sess-1", base)
	indexEvent(t, idx, "ev2", "fileA.go", schema.OpModify, "sess-2", base.Add(5*time.Second))

	// Conflict on file B
	indexEvent(t, idx, "ev3", "fileB.go", schema.OpCreate, "sess-3", base.Add(10*time.Second))
	indexEvent(t, idx, "ev4", "fileB.go", schema.OpModify, "sess-4", base.Add(15*time.Second))

	conflicts, err := d.DetectSince(base.Add(-time.Second))
	if err != nil {
		t.Fatalf("DetectSince: %v", err)
	}
	if len(conflicts) != 2 {
		t.Errorf("expected 2 conflicts, got %d", len(conflicts))
	}
}

// ─── Severity: Critical (< 5s gap) ─────────────────────────────────────────

func TestSeverity_Critical(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)
	base := time.Now().Add(-5 * time.Minute)

	// Two sessions within 2 seconds
	indexEvent(t, idx, "ev1", "file.go", schema.OpModify, "sess-A", base)
	indexEvent(t, idx, "ev2", "file.go", schema.OpModify, "sess-B", base.Add(2*time.Second))

	conflicts, err := d.DetectSince(base.Add(-time.Second))
	if err != nil {
		t.Fatalf("DetectSince: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Severity != SeverityCritical {
		t.Errorf("severity = %v, want CRITICAL (gap < 5s)", conflicts[0].Severity)
	}
}

// ─── Severity: High (delete + write) ────────────────────────────────────────

func TestSeverity_High_DeleteAndWrite(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)
	base := time.Now().Add(-5 * time.Minute)

	// One session deletes, another creates — gap > 5s but < 30s for HIGH
	indexEvent(t, idx, "ev1", "file.go", schema.OpDelete, "sess-A", base)
	indexEvent(t, idx, "ev2", "file.go", schema.OpCreate, "sess-B", base.Add(15*time.Second))

	conflicts, err := d.DetectSince(base.Add(-time.Second))
	if err != nil {
		t.Fatalf("DetectSince: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Severity != SeverityHigh {
		t.Errorf("severity = %v, want HIGH (delete + create, gap > 5s)", conflicts[0].Severity)
	}
}

// ─── Severity: Medium (gap < 30s, no delete) ───────────────────────────────

func TestSeverity_Medium(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)
	base := time.Now().Add(-5 * time.Minute)

	// Two sessions within 20 seconds, no delete ops
	indexEvent(t, idx, "ev1", "file.go", schema.OpModify, "sess-A", base)
	indexEvent(t, idx, "ev2", "file.go", schema.OpModify, "sess-B", base.Add(20*time.Second))

	conflicts, err := d.DetectSince(base.Add(-time.Second))
	if err != nil {
		t.Fatalf("DetectSince: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Severity != SeverityMedium {
		t.Errorf("severity = %v, want MEDIUM (gap 20s, modify-only)", conflicts[0].Severity)
	}
}

// ─── Severity: Low (gap > 30s, no delete) ───────────────────────────────────

func TestSeverity_Low(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)
	base := time.Now().Add(-5 * time.Minute)

	// Two sessions 45 seconds apart, no delete
	indexEvent(t, idx, "ev1", "file.go", schema.OpModify, "sess-A", base)
	indexEvent(t, idx, "ev2", "file.go", schema.OpModify, "sess-B", base.Add(45*time.Second))

	conflicts, err := d.DetectSince(base.Add(-time.Second))
	if err != nil {
		t.Fatalf("DetectSince: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Severity != SeverityLow {
		t.Errorf("severity = %v, want LOW (gap 45s, modify-only)", conflicts[0].Severity)
	}
}

// ─── DetectForFile ──────────────────────────────────────────────────────────

func TestDetectForFile(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)
	base := time.Now().Add(-5 * time.Minute)

	// Conflicts on two different files
	indexEvent(t, idx, "ev1", "fileA.go", schema.OpModify, "sess-1", base)
	indexEvent(t, idx, "ev2", "fileA.go", schema.OpModify, "sess-2", base.Add(5*time.Second))
	indexEvent(t, idx, "ev3", "fileB.go", schema.OpModify, "sess-3", base.Add(10*time.Second))
	indexEvent(t, idx, "ev4", "fileB.go", schema.OpModify, "sess-4", base.Add(15*time.Second))

	// Only detect for fileA
	conflicts, err := d.DetectForFile("fileA.go", base.Add(-time.Second))
	if err != nil {
		t.Fatalf("DetectForFile: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict for fileA.go, got %d", len(conflicts))
	}
	if conflicts[0].FilePath != "fileA.go" {
		t.Errorf("FilePath = %q, want %q", conflicts[0].FilePath, "fileA.go")
	}
}

// ─── DetectRealtime ─────────────────────────────────────────────────────────

func TestDetectRealtime_Conflict(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)
	base := time.Now().Add(-30 * time.Second)

	// Pre-existing event from sess-A
	indexEvent(t, idx, "ev-prior", "file.go", schema.OpModify, "sess-A", base)

	// New event from sess-B arrives
	newEvent := &schema.Event{
		EventID:       "ev-new",
		TimestampNano: base.Add(10 * time.Second).UnixNano(),
		FilePath:      "file.go",
		Op:            schema.OpModify,
		SessionID:     "sess-B",
	}

	c, err := d.DetectRealtime(newEvent)
	if err != nil {
		t.Fatalf("DetectRealtime: %v", err)
	}
	if c == nil {
		t.Fatal("expected a conflict, got nil")
	}
	if c.FilePath != "file.go" {
		t.Errorf("FilePath = %q, want %q", c.FilePath, "file.go")
	}
	if len(c.Sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(c.Sessions))
	}
}

func TestDetectRealtime_NoConflict_SameSession(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)
	base := time.Now().Add(-30 * time.Second)

	indexEvent(t, idx, "ev-prior", "file.go", schema.OpModify, "sess-A", base)

	newEvent := &schema.Event{
		EventID:       "ev-new",
		TimestampNano: base.Add(10 * time.Second).UnixNano(),
		FilePath:      "file.go",
		Op:            schema.OpModify,
		SessionID:     "sess-A", // same session
	}

	c, err := d.DetectRealtime(newEvent)
	if err != nil {
		t.Fatalf("DetectRealtime: %v", err)
	}
	if c != nil {
		t.Error("expected no conflict for same session, got one")
	}
}

func TestDetectRealtime_NoSessionID(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)

	newEvent := &schema.Event{
		EventID:       "ev-no-sess",
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "file.go",
		Op:            schema.OpModify,
		SessionID:     "", // no session
	}

	c, err := d.DetectRealtime(newEvent)
	if err != nil {
		t.Fatalf("DetectRealtime: %v", err)
	}
	if c != nil {
		t.Error("expected nil for event without session ID")
	}
}

func TestDetectRealtime_NoConflict_NoRecentEvents(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)

	newEvent := &schema.Event{
		EventID:       "ev-solo",
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "file.go",
		Op:            schema.OpModify,
		SessionID:     "sess-A",
	}

	c, err := d.DetectRealtime(newEvent)
	if err != nil {
		t.Fatalf("DetectRealtime: %v", err)
	}
	if c != nil {
		t.Error("expected nil when there are no recent events")
	}
}

// ─── Conflict Window field ──────────────────────────────────────────────────

func TestConflict_WindowDuration(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)
	base := time.Now().Add(-5 * time.Minute)

	indexEvent(t, idx, "ev1", "file.go", schema.OpModify, "sess-A", base)
	indexEvent(t, idx, "ev2", "file.go", schema.OpModify, "sess-B", base.Add(25*time.Second))

	conflicts, err := d.DetectSince(base.Add(-time.Second))
	if err != nil {
		t.Fatalf("DetectSince: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}

	// Window should be ~25 seconds
	if conflicts[0].Window < 24*time.Second || conflicts[0].Window > 26*time.Second {
		t.Errorf("Window = %v, expected ~25s", conflicts[0].Window)
	}
}

// ─── Conflict ID determinism ────────────────────────────────────────────────

func TestConflict_IDDeterminism(t *testing.T) {
	// generateID should produce consistent IDs for the same inputs
	events := []*schema.Event{
		{EventID: "ev-1"},
		{EventID: "ev-2"},
	}

	id1 := generateID("file.go", events)
	id2 := generateID("file.go", events)
	if id1 != id2 {
		t.Errorf("generateID not deterministic: %q != %q", id1, id2)
	}

	// Different file path = different ID
	id3 := generateID("other.go", events)
	if id1 == id3 {
		t.Error("different file paths should produce different IDs")
	}

	// Different events = different ID
	events2 := []*schema.Event{
		{EventID: "ev-1"},
		{EventID: "ev-3"},
	}
	id4 := generateID("file.go", events2)
	if id1 == id4 {
		t.Error("different events should produce different IDs")
	}

	// ID should have the "cf-" prefix
	if len(id1) < 3 || id1[:3] != "cf-" {
		t.Errorf("conflict ID should start with 'cf-', got %q", id1)
	}
}

// ─── uniqueSessions ─────────────────────────────────────────────────────────

func TestUniqueSessions(t *testing.T) {
	events := []*schema.Event{
		{SessionID: "sess-B"},
		{SessionID: "sess-A"},
		{SessionID: "sess-B"},
		{SessionID: ""},
		{SessionID: "sess-C"},
	}

	sessions := uniqueSessions(events)

	// Should be sorted and deduplicated, empty excluded
	if len(sessions) != 3 {
		t.Fatalf("expected 3 unique sessions, got %d: %v", len(sessions), sessions)
	}
	expected := []string{"sess-A", "sess-B", "sess-C"}
	for i, s := range expected {
		if sessions[i] != s {
			t.Errorf("sessions[%d] = %q, want %q", i, sessions[i], s)
		}
	}
}

// ─── groupByFile ────────────────────────────────────────────────────────────

func TestGroupByFile(t *testing.T) {
	events := []*schema.Event{
		{FilePath: "a.go"},
		{FilePath: "b.go"},
		{FilePath: "a.go"},
	}

	groups := groupByFile(events)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if len(groups["a.go"]) != 2 {
		t.Errorf("expected 2 events for a.go, got %d", len(groups["a.go"]))
	}
	if len(groups["b.go"]) != 1 {
		t.Errorf("expected 1 event for b.go, got %d", len(groups["b.go"]))
	}
}

// ─── Empty index ────────────────────────────────────────────────────────────

func TestDetectSince_EmptyIndex(t *testing.T) {
	idx := openTestIndex(t)
	d := NewDetector(idx, 60*time.Second)

	conflicts, err := d.DetectSince(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("DetectSince: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts on empty index, got %d", len(conflicts))
	}
}

// ─── classifySeverity ───────────────────────────────────────────────────────

func TestClassifySeverity_SingleEvent(t *testing.T) {
	events := []*schema.Event{{}}
	if s := classifySeverity(events); s != SeverityLow {
		t.Errorf("single event severity = %v, want LOW", s)
	}
}

func TestClassifySeverity_Table(t *testing.T) {
	base := time.Now()

	tests := []struct {
		name   string
		events []*schema.Event
		want   Severity
	}{
		{
			name: "critical_2s_gap",
			events: []*schema.Event{
				{TimestampNano: base.UnixNano(), SessionID: "s1", Op: schema.OpModify},
				{TimestampNano: base.Add(2 * time.Second).UnixNano(), SessionID: "s2", Op: schema.OpModify},
			},
			want: SeverityCritical,
		},
		{
			name: "high_delete_and_create",
			events: []*schema.Event{
				{TimestampNano: base.UnixNano(), SessionID: "s1", Op: schema.OpDelete},
				{TimestampNano: base.Add(15 * time.Second).UnixNano(), SessionID: "s2", Op: schema.OpCreate},
			},
			want: SeverityHigh,
		},
		{
			name: "medium_20s_gap",
			events: []*schema.Event{
				{TimestampNano: base.UnixNano(), SessionID: "s1", Op: schema.OpModify},
				{TimestampNano: base.Add(20 * time.Second).UnixNano(), SessionID: "s2", Op: schema.OpModify},
			},
			want: SeverityMedium,
		},
		{
			name: "low_45s_gap",
			events: []*schema.Event{
				{TimestampNano: base.UnixNano(), SessionID: "s1", Op: schema.OpModify},
				{TimestampNano: base.Add(45 * time.Second).UnixNano(), SessionID: "s2", Op: schema.OpModify},
			},
			want: SeverityLow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifySeverity(tt.events)
			if got != tt.want {
				t.Errorf("classifySeverity = %v, want %v", got, tt.want)
			}
		})
	}
}
