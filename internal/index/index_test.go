package index

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

func openTestIndex(t *testing.T) *Index {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	idx, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx
}

func makeEvent(id, filePath string, op schema.Operation, sessionID string, ts time.Time) *schema.Event {
	return &schema.Event{
		EventID:       id,
		TimestampNano: ts.UnixNano(),
		FilePath:      filePath,
		Op:            op,
		ContentHash:   "abc123",
		PreviousHash:  "def456",
		ContentSize:   100,
		SessionID:     sessionID,
		Attribution:   schema.AttrPID,
		AttributionConfidence: 0.95,
	}
}

// ─── Open / Close ───────────────────────────────────────────────────────────

func TestOpen_CreatesDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "new.db")
	idx, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	// Should be usable immediately
	count, err := idx.CountEvents()
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 events, got %d", count)
	}
}

func TestOpen_InvalidPath(t *testing.T) {
	_, err := Open("/nonexistent/dir/that/surely/does/not/exist/db.sqlite")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

// ─── IndexEvent + QueryEvents ───────────────────────────────────────────────

func TestIndexEvent_AndQueryBack(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()
	ev := makeEvent("evt-1", "src/main.go", schema.OpCreate, "sess-1", now)
	ev.Metadata = map[string]string{"tool": "claude"}

	if err := idx.IndexEvent(ev, "seg001.log", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}

	events, err := idx.QueryEvents(&Query{})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	got := events[0]
	if got.EventID != "evt-1" {
		t.Errorf("EventID = %q, want %q", got.EventID, "evt-1")
	}
	if got.FilePath != "src/main.go" {
		t.Errorf("FilePath = %q, want %q", got.FilePath, "src/main.go")
	}
	if got.Op != schema.OpCreate {
		t.Errorf("Op = %v, want CREATE", got.Op)
	}
	if got.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "sess-1")
	}
	if got.ContentHash != "abc123" {
		t.Errorf("ContentHash = %q, want %q", got.ContentHash, "abc123")
	}
	if got.Attribution != schema.AttrPID {
		t.Errorf("Attribution = %v, want AttrPID", got.Attribution)
	}
	if got.Metadata == nil || got.Metadata["tool"] != "claude" {
		t.Errorf("Metadata = %v, want tool=claude", got.Metadata)
	}
}

func TestQueryEvents_EmptyIndex(t *testing.T) {
	idx := openTestIndex(t)

	events, err := idx.QueryEvents(&Query{})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

// ─── Time Range Queries ─────────────────────────────────────────────────────

func TestQueryEvents_TimeRange(t *testing.T) {
	idx := openTestIndex(t)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		ev := makeEvent("evt-time-"+string(rune('a'+i)), "file.go", schema.OpModify, "", ts)
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	tests := []struct {
		name  string
		since int64
		until int64
		want  int
	}{
		{"all", 0, 0, 5},
		{"since minute 2", base.Add(2 * time.Minute).UnixNano(), 0, 3},
		{"until minute 2", 0, base.Add(2 * time.Minute).UnixNano(), 3},
		{"range minute 1-3", base.Add(1 * time.Minute).UnixNano(), base.Add(3 * time.Minute).UnixNano(), 3},
		{"future since", base.Add(10 * time.Minute).UnixNano(), 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := idx.QueryEvents(&Query{
				Since: tt.since,
				Until: tt.until,
			})
			if err != nil {
				t.Fatalf("QueryEvents: %v", err)
			}
			if len(events) != tt.want {
				t.Errorf("got %d events, want %d", len(events), tt.want)
			}
		})
	}
}

// ─── File Path Queries ──────────────────────────────────────────────────────

func TestQueryEvents_FilePathExact(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	files := []string{"src/main.go", "src/util.go", "README.md"}
	for i, f := range files {
		ev := makeEvent("evt-fp-"+f, f, schema.OpModify, "", now.Add(time.Duration(i)*time.Second))
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	events, err := idx.QueryEvents(&Query{FilePaths: []string{"src/main.go"}})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].FilePath != "src/main.go" {
		t.Errorf("FilePath = %q, want %q", events[0].FilePath, "src/main.go")
	}
}

func TestQueryEvents_FilePathGlob(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	files := []string{"src/main.go", "src/util.go", "README.md", "pkg/lib.go"}
	for i, f := range files {
		ev := makeEvent("evt-glob-"+f, f, schema.OpModify, "", now.Add(time.Duration(i)*time.Second))
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	// Glob with wildcard: src/*.go should match src/main.go and src/util.go
	// The query uses LIKE, so * becomes % — "src/*.go" becomes "src/%.go"
	events, err := idx.QueryEvents(&Query{FilePaths: []string{"src/*.go"}})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events for src/*.go, got %d", len(events))
	}

	// *.md should match README.md
	events, err = idx.QueryEvents(&Query{FilePaths: []string{"*.md"}})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event for *.md, got %d", len(events))
	}
}

func TestQueryEvents_FilePathSingleCharWildcard(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	files := []string{"a1.go", "a2.go", "b1.go"}
	for i, f := range files {
		ev := makeEvent("evt-sc-"+f, f, schema.OpModify, "", now.Add(time.Duration(i)*time.Second))
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	// ? becomes _ in LIKE, so "a?.go" matches a1.go and a2.go
	events, err := idx.QueryEvents(&Query{FilePaths: []string{"a?.go"}})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events for a?.go, got %d", len(events))
	}
}

// ─── Session ID Queries ─────────────────────────────────────────────────────

func TestQueryEvents_BySessionID(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	sessions := []string{"sess-alpha", "sess-beta", "sess-alpha"}
	for i, s := range sessions {
		ev := makeEvent("evt-sess-"+string(rune('a'+i)), "file.go", schema.OpModify, s, now.Add(time.Duration(i)*time.Second))
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	events, err := idx.QueryEvents(&Query{Sessions: []string{"sess-alpha"}})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events for sess-alpha, got %d", len(events))
	}

	// Multiple sessions
	events, err = idx.QueryEvents(&Query{Sessions: []string{"sess-alpha", "sess-beta"}})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("expected 3 events for both sessions, got %d", len(events))
	}
}

// ─── Operation Filter ───────────────────────────────────────────────────────

func TestQueryEvents_ByOperation(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	ops := []schema.Operation{schema.OpCreate, schema.OpModify, schema.OpDelete, schema.OpModify}
	for i, op := range ops {
		ev := makeEvent("evt-op-"+string(rune('a'+i)), "file.go", op, "", now.Add(time.Duration(i)*time.Second))
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	events, err := idx.QueryEvents(&Query{Operations: []string{"MODIFY"}})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 MODIFY events, got %d", len(events))
	}
}

// ─── Limit and Offset ───────────────────────────────────────────────────────

func TestQueryEvents_Limit(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	for i := 0; i < 10; i++ {
		ev := makeEvent("evt-lim-"+string(rune('a'+i)), "file.go", schema.OpModify, "", now.Add(time.Duration(i)*time.Second))
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	events, err := idx.QueryEvents(&Query{Limit: 3})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("expected 3 events with limit, got %d", len(events))
	}
}

func TestQueryEvents_Offset(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	for i := 0; i < 5; i++ {
		ev := makeEvent("evt-off-"+string(rune('a'+i)), "file.go", schema.OpModify, "", now.Add(time.Duration(i)*time.Second))
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	events, err := idx.QueryEvents(&Query{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events with offset, got %d", len(events))
	}
	// Verify we skipped the first 2 (ascending order by default)
	if events[0].EventID != "evt-off-c" {
		t.Errorf("first event should be evt-off-c, got %q", events[0].EventID)
	}
}

// ─── OrderDesc ──────────────────────────────────────────────────────────────

func TestQueryEvents_OrderDesc(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	for i := 0; i < 3; i++ {
		ev := makeEvent("evt-ord-"+string(rune('a'+i)), "file.go", schema.OpModify, "", now.Add(time.Duration(i)*time.Second))
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	events, err := idx.QueryEvents(&Query{OrderDesc: true})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	// Descending: latest first
	if events[0].EventID != "evt-ord-c" {
		t.Errorf("first event in desc order should be evt-ord-c, got %q", events[0].EventID)
	}
	if events[2].EventID != "evt-ord-a" {
		t.Errorf("last event in desc order should be evt-ord-a, got %q", events[2].EventID)
	}
}

// ─── CountEvents ────────────────────────────────────────────────────────────

func TestCountEvents(t *testing.T) {
	idx := openTestIndex(t)

	count, err := idx.CountEvents()
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	now := time.Now()
	for i := 0; i < 5; i++ {
		ev := makeEvent("evt-cnt-"+string(rune('a'+i)), "file.go", schema.OpModify, "", now.Add(time.Duration(i)*time.Second))
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	count, err = idx.CountEvents()
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5, got %d", count)
	}
}

// ─── GetEvent ───────────────────────────────────────────────────────────────

func TestGetEvent(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()
	ev := makeEvent("evt-get-1", "src/main.go", schema.OpCreate, "sess-1", now)
	if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}

	got, err := idx.GetEvent("evt-get-1")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if got.EventID != "evt-get-1" {
		t.Errorf("EventID = %q, want %q", got.EventID, "evt-get-1")
	}
}

func TestGetEvent_NotFound(t *testing.T) {
	idx := openTestIndex(t)
	_, err := idx.GetEvent("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent event")
	}
}

// ─── FileHistory ────────────────────────────────────────────────────────────

func TestFileHistory(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	// Events on two different files
	for i := 0; i < 4; i++ {
		fp := "target.go"
		if i%2 == 0 {
			fp = "other.go"
		}
		ev := makeEvent("evt-fh-"+string(rune('a'+i)), fp, schema.OpModify, "", now.Add(time.Duration(i)*time.Second))
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	events, err := idx.FileHistory("target.go", 10)
	if err != nil {
		t.Fatalf("FileHistory: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events for target.go, got %d", len(events))
	}
	// FileHistory returns DESC order
	if len(events) >= 2 && events[0].TimestampNano < events[1].TimestampNano {
		t.Error("FileHistory should return events in descending order")
	}
}

func TestFileHistory_DefaultLimit(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	ev := makeEvent("evt-fhd-1", "file.go", schema.OpModify, "", now)
	if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}

	// limit <= 0 defaults to 100
	events, err := idx.FileHistory("file.go", 0)
	if err != nil {
		t.Fatalf("FileHistory: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}
}

// ─── LatestEvent ────────────────────────────────────────────────────────────

func TestLatestEvent(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	for i := 0; i < 3; i++ {
		ev := makeEvent("evt-lat-"+string(rune('a'+i)), "file.go", schema.OpModify, "", now.Add(time.Duration(i)*time.Minute))
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	latest, err := idx.LatestEvent("file.go")
	if err != nil {
		t.Fatalf("LatestEvent: %v", err)
	}
	if latest.EventID != "evt-lat-c" {
		t.Errorf("expected latest to be evt-lat-c, got %q", latest.EventID)
	}
}

func TestLatestEvent_NotFound(t *testing.T) {
	idx := openTestIndex(t)
	_, err := idx.LatestEvent("nonexistent.go")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// ─── IndexEventBatch ────────────────────────────────────────────────────────

func TestIndexEventBatch(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	batch := []struct {
		Event         *schema.Event
		SegmentFile   string
		SegmentOffset int64
	}{
		{makeEvent("evt-batch-a", "a.go", schema.OpCreate, "s1", now), "seg.log", 0},
		{makeEvent("evt-batch-b", "b.go", schema.OpModify, "s1", now.Add(time.Second)), "seg.log", 100},
		{makeEvent("evt-batch-c", "c.go", schema.OpDelete, "s2", now.Add(2*time.Second)), "seg.log", 200},
	}

	if err := idx.IndexEventBatch(batch); err != nil {
		t.Fatalf("IndexEventBatch: %v", err)
	}

	count, err := idx.CountEvents()
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 events after batch, got %d", count)
	}
}

// ─── Sessions ───────────────────────────────────────────────────────────────

func TestUpsertSession_AndGet(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	sess := &schema.Session{
		SessionID:        "sess-upsert-1",
		ToolName:         "claude-code",
		PID:              1234,
		WorkingDirectory: "/home/user/project",
		Status:           schema.SessionActive,
		StartedAt:        now,
		Label:            "test session",
		Metadata:         map[string]string{"source": "test"},
		FilesChanged:     5,
		EventCount:       10,
	}

	if err := idx.UpsertSession(sess); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	got, err := idx.GetSession("sess-upsert-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.SessionID != "sess-upsert-1" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "sess-upsert-1")
	}
	if got.ToolName != "claude-code" {
		t.Errorf("ToolName = %q, want %q", got.ToolName, "claude-code")
	}
	if got.PID != 1234 {
		t.Errorf("PID = %d, want %d", got.PID, 1234)
	}
	if got.Status != schema.SessionActive {
		t.Errorf("Status = %v, want SessionActive", got.Status)
	}
	if got.Label != "test session" {
		t.Errorf("Label = %q, want %q", got.Label, "test session")
	}
	if got.FilesChanged != 5 {
		t.Errorf("FilesChanged = %d, want 5", got.FilesChanged)
	}
	if got.EventCount != 10 {
		t.Errorf("EventCount = %d, want 10", got.EventCount)
	}
	if got.Metadata == nil || got.Metadata["source"] != "test" {
		t.Errorf("Metadata = %v, want source=test", got.Metadata)
	}
}

func TestUpsertSession_Update(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	sess := &schema.Session{
		SessionID: "sess-upd-1",
		ToolName:  "claude-code",
		Status:    schema.SessionActive,
		StartedAt: now,
	}
	if err := idx.UpsertSession(sess); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	// Update it
	sess.Status = schema.SessionEnded
	sess.EndedAt = now.Add(10 * time.Minute)
	sess.EventCount = 42
	if err := idx.UpsertSession(sess); err != nil {
		t.Fatalf("UpsertSession (update): %v", err)
	}

	got, err := idx.GetSession("sess-upd-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Status != schema.SessionEnded {
		t.Errorf("Status = %v, want SessionEnded", got.Status)
	}
	if got.EventCount != 42 {
		t.Errorf("EventCount = %d, want 42", got.EventCount)
	}
}

func TestGetSession_NotFound(t *testing.T) {
	idx := openTestIndex(t)
	_, err := idx.GetSession("nonexistent-session")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

// ─── ListSessions ───────────────────────────────────────────────────────────

func TestListSessions(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	sessions := []*schema.Session{
		{SessionID: "s-active-1", ToolName: "claude", Status: schema.SessionActive, StartedAt: now, EventCount: 5},
		{SessionID: "s-active-2", ToolName: "claude", Status: schema.SessionActive, StartedAt: now.Add(-time.Hour), EventCount: 0},
		{SessionID: "s-ended-1", ToolName: "cursor", Status: schema.SessionEnded, StartedAt: now.Add(-2 * time.Hour), EndedAt: now, EventCount: 10},
	}
	for _, s := range sessions {
		if err := idx.UpsertSession(s); err != nil {
			t.Fatalf("UpsertSession: %v", err)
		}
	}

	// All sessions
	all, err := idx.ListSessions(false, 0, 0)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(all))
	}

	// Active only
	active, err := idx.ListSessions(true, 0, 0)
	if err != nil {
		t.Fatalf("ListSessions (active): %v", err)
	}
	if len(active) != 2 {
		t.Errorf("expected 2 active sessions, got %d", len(active))
	}

	// Min events
	withEvents, err := idx.ListSessions(false, 5, 0)
	if err != nil {
		t.Fatalf("ListSessions (minEvents): %v", err)
	}
	if len(withEvents) != 2 {
		t.Errorf("expected 2 sessions with >= 5 events, got %d", len(withEvents))
	}

	// Active + min events
	activeWithEvents, err := idx.ListSessions(true, 5, 0)
	if err != nil {
		t.Fatalf("ListSessions (active + minEvents): %v", err)
	}
	if len(activeWithEvents) != 1 {
		t.Errorf("expected 1 active session with >= 5 events, got %d", len(activeWithEvents))
	}
}

func TestListSessions_OrderedByStartedAtDesc(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	for i := 0; i < 3; i++ {
		s := &schema.Session{
			SessionID: "s-order-" + string(rune('a'+i)),
			ToolName:  "test",
			Status:    schema.SessionActive,
			StartedAt: now.Add(time.Duration(i) * time.Hour),
		}
		if err := idx.UpsertSession(s); err != nil {
			t.Fatalf("UpsertSession: %v", err)
		}
	}

	sessions, err := idx.ListSessions(false, 0, 0)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}
	// Descending order: c, b, a
	if sessions[0].SessionID != "s-order-c" {
		t.Errorf("first session should be s-order-c, got %q", sessions[0].SessionID)
	}
}

// ─── UpdateSessionLabel ─────────────────────────────────────────────────────

func TestUpdateSessionLabel(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	s := &schema.Session{
		SessionID: "s-label-1",
		ToolName:  "test",
		Status:    schema.SessionActive,
		StartedAt: now,
	}
	if err := idx.UpsertSession(s); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	if err := idx.UpdateSessionLabel("s-label-1", "my cool session"); err != nil {
		t.Fatalf("UpdateSessionLabel: %v", err)
	}

	got, err := idx.GetSession("s-label-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Label != "my cool session" {
		t.Errorf("Label = %q, want %q", got.Label, "my cool session")
	}
}

func TestUpdateSessionLabel_NotFound(t *testing.T) {
	idx := openTestIndex(t)
	err := idx.UpdateSessionLabel("nonexistent", "label")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

// ─── ActiveContentHashes ────────────────────────────────────────────────────

func TestActiveContentHashes(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	ev1 := makeEvent("evt-hash-1", "a.go", schema.OpCreate, "", now)
	ev1.ContentHash = "hash-aaa"
	ev1.PreviousHash = ""
	ev2 := makeEvent("evt-hash-2", "b.go", schema.OpModify, "", now.Add(time.Second))
	ev2.ContentHash = "hash-bbb"
	ev2.PreviousHash = "hash-ccc"

	if err := idx.IndexEvent(ev1, "seg.log", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}
	if err := idx.IndexEvent(ev2, "seg.log", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}

	hashes, err := idx.ActiveContentHashes()
	if err != nil {
		t.Fatalf("ActiveContentHashes: %v", err)
	}

	// Should contain hash-aaa, hash-bbb, hash-ccc (not empty strings)
	for _, h := range []string{"hash-aaa", "hash-bbb", "hash-ccc"} {
		if !hashes[h] {
			t.Errorf("expected hash %q in active hashes", h)
		}
	}
}

func TestActiveContentHashes_Empty(t *testing.T) {
	idx := openTestIndex(t)
	hashes, err := idx.ActiveContentHashes()
	if err != nil {
		t.Fatalf("ActiveContentHashes: %v", err)
	}
	if len(hashes) != 0 {
		t.Errorf("expected 0 hashes, got %d", len(hashes))
	}
}

// ─── Special Characters ─────────────────────────────────────────────────────

func TestSpecialCharacters_InFilePaths(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	tests := []struct {
		name string
		path string
	}{
		{"spaces", "path with spaces/file name.go"},
		{"unicode", "src/archivo_espanol.go"},
		{"unicode_cjk", "docs/readme_chinese.md"},
		{"dots", "some.deeply.nested.file.go"},
		{"hyphens", "my-package/my-file.go"},
		{"underscores", "my_package/my_file.go"},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := makeEvent("evt-special-"+string(rune('a'+i)), tt.path, schema.OpCreate, "", now.Add(time.Duration(i)*time.Second))
			if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
				t.Fatalf("IndexEvent: %v", err)
			}

			events, err := idx.QueryEvents(&Query{FilePaths: []string{tt.path}})
			if err != nil {
				t.Fatalf("QueryEvents: %v", err)
			}
			if len(events) != 1 {
				t.Errorf("expected 1 event for path %q, got %d", tt.path, len(events))
			}
			if len(events) > 0 && events[0].FilePath != tt.path {
				t.Errorf("FilePath = %q, want %q", events[0].FilePath, tt.path)
			}
		})
	}
}

// ─── Combined Filters ───────────────────────────────────────────────────────

func TestQueryEvents_CombinedFilters(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	events := []*schema.Event{
		makeEvent("evt-cf-a", "src/main.go", schema.OpCreate, "sess-1", now),
		makeEvent("evt-cf-b", "src/main.go", schema.OpModify, "sess-2", now.Add(time.Second)),
		makeEvent("evt-cf-c", "src/util.go", schema.OpModify, "sess-1", now.Add(2*time.Second)),
		makeEvent("evt-cf-d", "src/main.go", schema.OpModify, "sess-1", now.Add(3*time.Second)),
	}
	for _, ev := range events {
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	// Session + file path
	result, err := idx.QueryEvents(&Query{
		Sessions:  []string{"sess-1"},
		FilePaths: []string{"src/main.go"},
	})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 events (sess-1 + main.go), got %d", len(result))
	}

	// Session + operation
	result, err = idx.QueryEvents(&Query{
		Sessions:   []string{"sess-1"},
		Operations: []string{"CREATE"},
	})
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 CREATE event for sess-1, got %d", len(result))
	}
}

// ─── IsConflict flag ────────────────────────────────────────────────────────

func TestIsConflictFlag(t *testing.T) {
	idx := openTestIndex(t)
	now := time.Now()

	ev := makeEvent("evt-conflict-1", "file.go", schema.OpModify, "s1", now)
	ev.IsConflict = true
	if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}

	got, err := idx.GetEvent("evt-conflict-1")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if !got.IsConflict {
		t.Error("expected IsConflict = true")
	}
}
