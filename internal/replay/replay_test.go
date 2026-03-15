package replay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"
)

// testFixture holds a temp index, object store, and helpers for setting up test data.
type testFixture struct {
	t        *testing.T
	idx      *index.Index
	objStore *store.Store
	tmpDir   string
	eventSeq int
	timeBase int64
}

func newFixture(t *testing.T) *testFixture {
	t.Helper()
	tmpDir := t.TempDir()

	dbPath := filepath.Join(tmpDir, "index.db")
	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { idx.Close() })

	objDir := filepath.Join(tmpDir, "objects")
	objStore, err := store.NewStore(objDir, false)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { objStore.Close() })

	return &testFixture{
		t:        t,
		idx:      idx,
		objStore: objStore,
		tmpDir:   tmpDir,
		timeBase: 1700000000000000000, // a fixed nanosecond timestamp
	}
}

// putContent stores content in the object store and returns its hash.
func (f *testFixture) putContent(content string) string {
	f.t.Helper()
	hash, _, err := f.objStore.Put([]byte(content))
	if err != nil {
		f.t.Fatalf("put content: %v", err)
	}
	return hash
}

// addEvent inserts an event into the index with auto-incrementing timestamp.
func (f *testFixture) addEvent(sessionID, filePath string, op schema.Operation, contentHash, previousHash string, contentSize int64) {
	f.t.Helper()
	f.eventSeq++
	ev := &schema.Event{
		EventID:       fmt.Sprintf("evt-%04d", f.eventSeq),
		TimestampNano: f.timeBase + int64(f.eventSeq)*1000000, // 1ms increments
		FilePath:      filePath,
		Op:            op,
		ContentHash:   contentHash,
		PreviousHash:  previousHash,
		ContentSize:   contentSize,
		SessionID:     sessionID,
	}
	if err := f.idx.IndexEvent(ev, "test.log", 0); err != nil {
		f.t.Fatalf("index event: %v", err)
	}
}

// addEventWithTime inserts an event with a specific timestamp.
func (f *testFixture) addEventWithTime(sessionID, filePath string, op schema.Operation, contentHash, previousHash string, contentSize int64, timestampNano int64) {
	f.t.Helper()
	f.eventSeq++
	ev := &schema.Event{
		EventID:       fmt.Sprintf("evt-%04d", f.eventSeq),
		TimestampNano: timestampNano,
		FilePath:      filePath,
		Op:            op,
		ContentHash:   contentHash,
		PreviousHash:  previousHash,
		ContentSize:   contentSize,
		SessionID:     sessionID,
	}
	if err := f.idx.IndexEvent(ev, "test.log", 0); err != nil {
		f.t.Fatalf("index event: %v", err)
	}
}

// addRenameEvent inserts a rename event with old_path set.
func (f *testFixture) addRenameEvent(sessionID, filePath, oldPath string, contentHash string, contentSize int64, timestampNano int64) {
	f.t.Helper()
	f.eventSeq++
	ev := &schema.Event{
		EventID:       fmt.Sprintf("evt-%04d", f.eventSeq),
		TimestampNano: timestampNano,
		FilePath:      filePath,
		Op:            schema.OpRename,
		ContentHash:   contentHash,
		ContentSize:   contentSize,
		OldPath:       oldPath,
		SessionID:     sessionID,
	}
	if err := f.idx.IndexEvent(ev, "test.log", 0); err != nil {
		f.t.Fatalf("index event: %v", err)
	}
}

// --- ReplaySession Tests ---

func TestReplaySession_EmptySession(t *testing.T) {
	f := newFixture(t)
	_, err := ReplaySession(f.idx, f.objStore, "nonexistent-session")
	if err == nil {
		t.Fatal("expected error for empty session, got nil")
	}
	if !strings.Contains(err.Error(), "no events found") {
		t.Fatalf("expected 'no events found' error, got: %v", err)
	}
}

func TestReplaySession_OnlyCreates(t *testing.T) {
	f := newFixture(t)
	session := "sess-create-only"
	hashA := f.putContent("file-a content")
	hashB := f.putContent("file-b content")

	f.addEvent(session, "src/a.go", schema.OpCreate, hashA, "", 14)
	f.addEvent(session, "src/b.go", schema.OpCreate, hashB, "", 14)

	result, err := ReplaySession(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}

	if result.SessionID != session {
		t.Errorf("SessionID = %q, want %q", result.SessionID, session)
	}
	if len(result.Files) != 2 {
		t.Fatalf("Files count = %d, want 2", len(result.Files))
	}
	for _, path := range []string{"src/a.go", "src/b.go"} {
		fc, ok := result.Files[path]
		if !ok {
			t.Errorf("missing file change for %s", path)
			continue
		}
		if fc.Operation != "create" {
			t.Errorf("%s operation = %q, want 'create'", path, fc.Operation)
		}
		if fc.EventCount != 1 {
			t.Errorf("%s EventCount = %d, want 1", path, fc.EventCount)
		}
	}
}

func TestReplaySession_OnlyModifies(t *testing.T) {
	f := newFixture(t)
	session := "sess-modify-only"
	hashOld := f.putContent("old content")
	hashNew := f.putContent("new content")

	f.addEvent(session, "src/main.go", schema.OpModify, hashNew, hashOld, 11)

	result, err := ReplaySession(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}

	if len(result.Files) != 1 {
		t.Fatalf("Files count = %d, want 1", len(result.Files))
	}
	fc := result.Files["src/main.go"]
	if fc.Operation != "modify" {
		t.Errorf("operation = %q, want 'modify'", fc.Operation)
	}
	if fc.ContentHash != hashNew {
		t.Errorf("ContentHash = %q, want %q", fc.ContentHash, hashNew)
	}
	if fc.OldHash != hashOld {
		t.Errorf("OldHash = %q, want %q", fc.OldHash, hashOld)
	}
}

func TestReplaySession_OnlyDeletes(t *testing.T) {
	f := newFixture(t)
	session := "sess-delete-only"
	hashOld := f.putContent("file to delete")

	f.addEvent(session, "src/removed.go", schema.OpDelete, "", hashOld, 0)

	result, err := ReplaySession(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}

	if len(result.Files) != 1 {
		t.Fatalf("Files count = %d, want 1", len(result.Files))
	}
	fc := result.Files["src/removed.go"]
	if fc.Operation != "delete" {
		t.Errorf("operation = %q, want 'delete'", fc.Operation)
	}
}

func TestReplaySession_MixedOperations(t *testing.T) {
	f := newFixture(t)
	session := "sess-mixed"
	hashA := f.putContent("created file")
	hashBOld := f.putContent("old B")
	hashBNew := f.putContent("new B")
	hashC := f.putContent("file to delete")

	f.addEvent(session, "new.go", schema.OpCreate, hashA, "", 12)
	f.addEvent(session, "existing.go", schema.OpModify, hashBNew, hashBOld, 5)
	f.addEvent(session, "old.go", schema.OpDelete, "", hashC, 0)

	result, err := ReplaySession(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}

	if len(result.Files) != 3 {
		t.Fatalf("Files count = %d, want 3", len(result.Files))
	}

	tests := map[string]string{
		"new.go":      "create",
		"existing.go": "modify",
		"old.go":      "delete",
	}
	for path, wantOp := range tests {
		fc, ok := result.Files[path]
		if !ok {
			t.Errorf("missing file change for %s", path)
			continue
		}
		if fc.Operation != wantOp {
			t.Errorf("%s operation = %q, want %q", path, fc.Operation, wantOp)
		}
	}
}

func TestReplaySession_CreateThenDelete_Cancels(t *testing.T) {
	f := newFixture(t)
	session := "sess-cancel"
	hash := f.putContent("temporary file")

	f.addEvent(session, "temp.go", schema.OpCreate, hash, "", 14)
	f.addEvent(session, "temp.go", schema.OpDelete, "", hash, 0)

	result, err := ReplaySession(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}

	// Create followed by delete of the same file should cancel out
	if _, ok := result.Files["temp.go"]; ok {
		t.Error("expected create+delete to cancel out, but file change still present")
	}
}

func TestReplaySession_ModifyToSameContent_Cancels(t *testing.T) {
	f := newFixture(t)
	session := "sess-noop-modify"
	hash := f.putContent("same content")

	// Modify with no actual content change (firstHash == lastHash)
	f.addEvent(session, "unchanged.go", schema.OpModify, hash, hash, 12)

	result, err := ReplaySession(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}

	// No net change, should be empty
	if _, ok := result.Files["unchanged.go"]; ok {
		t.Error("expected no-op modify to be excluded, but file change still present")
	}
}

func TestReplaySession_MultipleModifiesSameFile(t *testing.T) {
	f := newFixture(t)
	session := "sess-multi-modify"
	hash1 := f.putContent("version 1")
	hash2 := f.putContent("version 2")
	hash3 := f.putContent("version 3")

	f.addEvent(session, "iterative.go", schema.OpModify, hash2, hash1, 9)
	f.addEvent(session, "iterative.go", schema.OpModify, hash3, hash2, 9)

	result, err := ReplaySession(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}

	fc := result.Files["iterative.go"]
	if fc == nil {
		t.Fatal("expected file change for iterative.go")
	}
	if fc.Operation != "modify" {
		t.Errorf("operation = %q, want 'modify'", fc.Operation)
	}
	if fc.OldHash != hash1 {
		t.Errorf("OldHash = %q, want %q (first version)", fc.OldHash, hash1)
	}
	if fc.ContentHash != hash3 {
		t.Errorf("ContentHash = %q, want %q (last version)", fc.ContentHash, hash3)
	}
	if fc.EventCount != 2 {
		t.Errorf("EventCount = %d, want 2", fc.EventCount)
	}
}

func TestReplaySession_ModifyThenDelete_NetDelete(t *testing.T) {
	f := newFixture(t)
	session := "sess-modify-delete"
	hashOld := f.putContent("original")
	hashNew := f.putContent("modified")

	f.addEvent(session, "doomed.go", schema.OpModify, hashNew, hashOld, 8)
	f.addEvent(session, "doomed.go", schema.OpDelete, "", hashNew, 0)

	result, err := ReplaySession(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}

	fc := result.Files["doomed.go"]
	if fc == nil {
		t.Fatal("expected file change for doomed.go")
	}
	if fc.Operation != "delete" {
		t.Errorf("operation = %q, want 'delete'", fc.Operation)
	}
}

func TestReplaySession_EventsReturned(t *testing.T) {
	f := newFixture(t)
	session := "sess-events"
	hash := f.putContent("content")

	f.addEvent(session, "a.go", schema.OpCreate, hash, "", 7)
	f.addEvent(session, "b.go", schema.OpCreate, hash, "", 7)

	result, err := ReplaySession(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}

	if len(result.Events) != 2 {
		t.Errorf("Events count = %d, want 2", len(result.Events))
	}
}

func TestReplaySession_IsolatesSession(t *testing.T) {
	f := newFixture(t)
	hashA := f.putContent("session A file")
	hashB := f.putContent("session B file")

	f.addEvent("sess-A", "fileA.go", schema.OpCreate, hashA, "", 14)
	f.addEvent("sess-B", "fileB.go", schema.OpCreate, hashB, "", 14)

	result, err := ReplaySession(f.idx, f.objStore, "sess-A")
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}

	if len(result.Files) != 1 {
		t.Fatalf("Files count = %d, want 1 (only sess-A)", len(result.Files))
	}
	if _, ok := result.Files["fileA.go"]; !ok {
		t.Error("expected fileA.go from sess-A")
	}
	if _, ok := result.Files["fileB.go"]; ok {
		t.Error("unexpected fileB.go from sess-B")
	}
}

// --- computeNetOperation Tests ---

func TestComputeNetOperation(t *testing.T) {
	tests := []struct {
		name      string
		firstOp   schema.Operation
		lastOp    schema.Operation
		firstHash string
		lastHash  string
		want      string
	}{
		{
			name:    "create then delete cancels",
			firstOp: schema.OpCreate, lastOp: schema.OpDelete,
			firstHash: "", lastHash: "",
			want: "",
		},
		{
			name:    "modify then delete is delete",
			firstOp: schema.OpModify, lastOp: schema.OpDelete,
			firstHash: "abc", lastHash: "",
			want: "delete",
		},
		{
			name:    "delete then delete is delete",
			firstOp: schema.OpDelete, lastOp: schema.OpDelete,
			firstHash: "abc", lastHash: "",
			want: "delete",
		},
		{
			name:    "create then modify is create",
			firstOp: schema.OpCreate, lastOp: schema.OpModify,
			firstHash: "", lastHash: "xyz",
			want: "create",
		},
		{
			name:    "create only is create",
			firstOp: schema.OpCreate, lastOp: schema.OpCreate,
			firstHash: "", lastHash: "xyz",
			want: "create",
		},
		{
			name:    "modify with different hashes is modify",
			firstOp: schema.OpModify, lastOp: schema.OpModify,
			firstHash: "abc", lastHash: "xyz",
			want: "modify",
		},
		{
			name:    "modify same hash cancels",
			firstOp: schema.OpModify, lastOp: schema.OpModify,
			firstHash: "abc", lastHash: "abc",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeNetOperation(tt.firstOp, tt.lastOp, tt.firstHash, tt.lastHash)
			if got != tt.want {
				t.Errorf("computeNetOperation() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- SnapshotAt Tests ---

func TestSnapshotAt_Empty(t *testing.T) {
	f := newFixture(t)
	snap, err := SnapshotAt(f.idx, f.timeBase+999999999)
	if err != nil {
		t.Fatalf("SnapshotAt: %v", err)
	}
	if len(snap.Files) != 0 {
		t.Errorf("Files count = %d, want 0", len(snap.Files))
	}
}

func TestSnapshotAt_CreatesOnly(t *testing.T) {
	f := newFixture(t)
	hashA := f.putContent("file A")
	hashB := f.putContent("file B")

	ts1 := f.timeBase + 1000000
	ts2 := f.timeBase + 2000000

	f.addEventWithTime("s1", "a.go", schema.OpCreate, hashA, "", 6, ts1)
	f.addEventWithTime("s1", "b.go", schema.OpCreate, hashB, "", 6, ts2)

	snap, err := SnapshotAt(f.idx, ts2)
	if err != nil {
		t.Fatalf("SnapshotAt: %v", err)
	}

	if len(snap.Files) != 2 {
		t.Fatalf("Files count = %d, want 2", len(snap.Files))
	}
	if snap.Files["a.go"].ContentHash != hashA {
		t.Errorf("a.go hash = %q, want %q", snap.Files["a.go"].ContentHash, hashA)
	}
	if snap.Files["b.go"].ContentHash != hashB {
		t.Errorf("b.go hash = %q, want %q", snap.Files["b.go"].ContentHash, hashB)
	}
}

func TestSnapshotAt_CreateThenDelete(t *testing.T) {
	f := newFixture(t)
	hash := f.putContent("ephemeral")

	ts1 := f.timeBase + 1000000
	ts2 := f.timeBase + 2000000

	f.addEventWithTime("s1", "temp.go", schema.OpCreate, hash, "", 9, ts1)
	f.addEventWithTime("s1", "temp.go", schema.OpDelete, "", hash, 0, ts2)

	snap, err := SnapshotAt(f.idx, ts2)
	if err != nil {
		t.Fatalf("SnapshotAt: %v", err)
	}

	if _, ok := snap.Files["temp.go"]; ok {
		t.Error("expected deleted file to be absent from snapshot")
	}
}

func TestSnapshotAt_PartialTimestamp(t *testing.T) {
	f := newFixture(t)
	hashA := f.putContent("file A")
	hashB := f.putContent("file B")

	ts1 := f.timeBase + 1000000
	ts2 := f.timeBase + 3000000

	f.addEventWithTime("s1", "a.go", schema.OpCreate, hashA, "", 6, ts1)
	f.addEventWithTime("s1", "b.go", schema.OpCreate, hashB, "", 6, ts2)

	// Snapshot at a time between the two events
	snap, err := SnapshotAt(f.idx, ts1+500000)
	if err != nil {
		t.Fatalf("SnapshotAt: %v", err)
	}

	if len(snap.Files) != 1 {
		t.Fatalf("Files count = %d, want 1 (only a.go)", len(snap.Files))
	}
	if _, ok := snap.Files["a.go"]; !ok {
		t.Error("expected a.go in snapshot")
	}
}

func TestSnapshotAt_ModifyUpdatesState(t *testing.T) {
	f := newFixture(t)
	hashV1 := f.putContent("version 1")
	hashV2 := f.putContent("version 2")

	ts1 := f.timeBase + 1000000
	ts2 := f.timeBase + 2000000

	f.addEventWithTime("s1", "data.go", schema.OpCreate, hashV1, "", 9, ts1)
	f.addEventWithTime("s1", "data.go", schema.OpModify, hashV2, hashV1, 9, ts2)

	snap, err := SnapshotAt(f.idx, ts2)
	if err != nil {
		t.Fatalf("SnapshotAt: %v", err)
	}

	fs := snap.Files["data.go"]
	if fs == nil {
		t.Fatal("expected data.go in snapshot")
	}
	if fs.ContentHash != hashV2 {
		t.Errorf("ContentHash = %q, want %q (latest version)", fs.ContentHash, hashV2)
	}
}

func TestSnapshotAt_RenameRemovesOldPath(t *testing.T) {
	f := newFixture(t)
	hash := f.putContent("renamed file content")

	ts1 := f.timeBase + 1000000
	ts2 := f.timeBase + 2000000

	f.addEventWithTime("s1", "old_name.go", schema.OpCreate, hash, "", 20, ts1)
	f.addRenameEvent("s1", "new_name.go", "old_name.go", hash, 20, ts2)

	snap, err := SnapshotAt(f.idx, ts2)
	if err != nil {
		t.Fatalf("SnapshotAt: %v", err)
	}

	if _, ok := snap.Files["old_name.go"]; ok {
		t.Error("expected old_name.go to be removed after rename")
	}
	if _, ok := snap.Files["new_name.go"]; !ok {
		t.Error("expected new_name.go to be present after rename")
	}
}

func TestSnapshotAt_TimestampIsStored(t *testing.T) {
	f := newFixture(t)
	ts := f.timeBase + 42
	snap, err := SnapshotAt(f.idx, ts)
	if err != nil {
		t.Fatalf("SnapshotAt: %v", err)
	}
	if snap.Timestamp != ts {
		t.Errorf("Timestamp = %d, want %d", snap.Timestamp, ts)
	}
}

// --- GeneratePatch Tests ---

func TestGeneratePatch_CreateFile(t *testing.T) {
	f := newFixture(t)
	session := "sess-patch-create"
	hash := f.putContent("line1\nline2\n")

	f.addEvent(session, "new.txt", schema.OpCreate, hash, "", 12)

	patch, err := GeneratePatch(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("GeneratePatch: %v", err)
	}

	if !strings.Contains(patch, "--- a/new.txt") {
		t.Error("patch missing old file header")
	}
	if !strings.Contains(patch, "+++ b/new.txt") {
		t.Error("patch missing new file header")
	}
	if !strings.Contains(patch, "+line1") {
		t.Error("patch missing added line1")
	}
	if !strings.Contains(patch, "+line2") {
		t.Error("patch missing added line2")
	}
	// Created file should have no '-' lines (no deletions)
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			t.Errorf("unexpected deletion line in create patch: %q", line)
		}
	}
}

func TestGeneratePatch_DeleteFile(t *testing.T) {
	f := newFixture(t)
	session := "sess-patch-delete"
	hash := f.putContent("goodbye\n")

	f.addEvent(session, "removed.txt", schema.OpDelete, "", hash, 0)

	patch, err := GeneratePatch(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("GeneratePatch: %v", err)
	}

	if !strings.Contains(patch, "--- a/removed.txt") {
		t.Error("patch missing old file header")
	}
	if !strings.Contains(patch, "-goodbye") {
		t.Error("patch missing deleted line")
	}
	// Deleted file should have no '+' lines (no additions)
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			t.Errorf("unexpected addition line in delete patch: %q", line)
		}
	}
}

func TestGeneratePatch_ModifyFile(t *testing.T) {
	f := newFixture(t)
	session := "sess-patch-modify"
	hashOld := f.putContent("hello\nworld\n")
	hashNew := f.putContent("hello\nearth\n")

	f.addEvent(session, "greeting.txt", schema.OpModify, hashNew, hashOld, 12)

	patch, err := GeneratePatch(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("GeneratePatch: %v", err)
	}

	if !strings.Contains(patch, "--- a/greeting.txt") {
		t.Error("patch missing old file header")
	}
	if !strings.Contains(patch, "+++ b/greeting.txt") {
		t.Error("patch missing new file header")
	}
	if !strings.Contains(patch, "-world") {
		t.Error("patch missing deleted line 'world'")
	}
	if !strings.Contains(patch, "+earth") {
		t.Error("patch missing added line 'earth'")
	}
	if !strings.Contains(patch, " hello") {
		t.Error("patch missing context line 'hello'")
	}
}

func TestGeneratePatch_MultiplePaths_Sorted(t *testing.T) {
	f := newFixture(t)
	session := "sess-patch-sorted"
	hashZ := f.putContent("z content\n")
	hashA := f.putContent("a content\n")

	// Insert in reverse alphabetical order
	f.addEvent(session, "z.txt", schema.OpCreate, hashZ, "", 10)
	f.addEvent(session, "a.txt", schema.OpCreate, hashA, "", 10)

	patch, err := GeneratePatch(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("GeneratePatch: %v", err)
	}

	idxA := strings.Index(patch, "--- a/a.txt")
	idxZ := strings.Index(patch, "--- a/z.txt")
	if idxA < 0 || idxZ < 0 {
		t.Fatal("patch missing expected file headers")
	}
	if idxA > idxZ {
		t.Error("expected paths in sorted order (a.txt before z.txt)")
	}
}

func TestGeneratePatch_EmptyPatch(t *testing.T) {
	f := newFixture(t)
	session := "sess-patch-empty"
	hash := f.putContent("temp content")

	// Create then delete cancels out
	f.addEvent(session, "vanish.go", schema.OpCreate, hash, "", 12)
	f.addEvent(session, "vanish.go", schema.OpDelete, "", hash, 0)

	patch, err := GeneratePatch(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("GeneratePatch: %v", err)
	}

	if patch != "" {
		t.Errorf("expected empty patch, got: %q", patch)
	}
}

func TestGeneratePatch_MissingContent_Error(t *testing.T) {
	f := newFixture(t)
	session := "sess-patch-missing"

	// Add event with a hash that is not in the store
	f.addEvent(session, "ghost.go", schema.OpCreate, "nonexistent-hash-1234567890abcdef", "", 10)

	_, err := GeneratePatch(f.idx, f.objStore, session)
	if err == nil {
		t.Fatal("expected error for missing content, got nil")
	}
	if !strings.Contains(err.Error(), "get content") {
		t.Errorf("expected 'get content' error, got: %v", err)
	}
}

func TestGeneratePatch_MissingOldContent_DeleteError(t *testing.T) {
	f := newFixture(t)
	session := "sess-patch-missing-old"

	// Delete event with old hash not in store
	f.addEvent(session, "ghost.go", schema.OpDelete, "", "nonexistent-hash-abcdef1234567890", 0)

	_, err := GeneratePatch(f.idx, f.objStore, session)
	if err == nil {
		t.Fatal("expected error for missing old content, got nil")
	}
	if !strings.Contains(err.Error(), "get old content") {
		t.Errorf("expected 'get old content' error, got: %v", err)
	}
}

func TestGeneratePatch_MissingOldContent_ModifyError(t *testing.T) {
	f := newFixture(t)
	session := "sess-patch-missing-modify-old"

	hashNew := f.putContent("new data")
	f.addEvent(session, "half.go", schema.OpModify, hashNew, "nonexistent-hash-0000000000000000", 8)

	_, err := GeneratePatch(f.idx, f.objStore, session)
	if err == nil {
		t.Fatal("expected error for missing old content in modify, got nil")
	}
	if !strings.Contains(err.Error(), "get old content") {
		t.Errorf("expected 'get old content' error, got: %v", err)
	}
}

func TestGeneratePatch_MissingNewContent_ModifyError(t *testing.T) {
	f := newFixture(t)
	session := "sess-patch-missing-modify-new"

	hashOld := f.putContent("old data")
	f.addEvent(session, "half.go", schema.OpModify, "nonexistent-hash-1111111111111111", hashOld, 8)

	_, err := GeneratePatch(f.idx, f.objStore, session)
	if err == nil {
		t.Fatal("expected error for missing new content in modify, got nil")
	}
	if !strings.Contains(err.Error(), "get new content") {
		t.Errorf("expected 'get new content' error, got: %v", err)
	}
}

// --- UnifiedDiff Tests ---

func TestUnifiedDiff_EmptyToContent(t *testing.T) {
	diff := unifiedDiff("a/new.txt", "b/new.txt", "", "hello\nworld\n")
	if !strings.Contains(diff, "+hello") {
		t.Error("diff missing +hello")
	}
	if !strings.Contains(diff, "+world") {
		t.Error("diff missing +world")
	}
}

func TestUnifiedDiff_ContentToEmpty(t *testing.T) {
	diff := unifiedDiff("a/old.txt", "b/old.txt", "goodbye\n", "")
	if !strings.Contains(diff, "-goodbye") {
		t.Error("diff missing -goodbye")
	}
}

func TestUnifiedDiff_IdenticalContent(t *testing.T) {
	diff := unifiedDiff("a/same.txt", "b/same.txt", "same\n", "same\n")
	// When content is identical but non-empty, computeEdits returns editEqual
	// entries (not zero edits), so the headers are written but groupHunks
	// returns nil (no actual changes). The result is headers-only.
	if strings.Contains(diff, "@@") {
		t.Errorf("identical content should produce no hunk headers, got: %q", diff)
	}
	// Verify no +/- diff lines (only headers allowed)
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			t.Errorf("unexpected addition line in identical-content diff: %q", line)
		}
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			t.Errorf("unexpected deletion line in identical-content diff: %q", line)
		}
	}
}

func TestUnifiedDiff_BothEmpty(t *testing.T) {
	diff := unifiedDiff("a/empty.txt", "b/empty.txt", "", "")
	if diff != "" {
		t.Errorf("expected empty diff for empty-to-empty, got: %q", diff)
	}
}

func TestUnifiedDiff_Header(t *testing.T) {
	diff := unifiedDiff("a/file.go", "b/file.go", "old\n", "new\n")
	lines := strings.Split(diff, "\n")
	if len(lines) < 2 {
		t.Fatal("diff too short to contain headers")
	}
	if lines[0] != "--- a/file.go" {
		t.Errorf("first line = %q, want '--- a/file.go'", lines[0])
	}
	if lines[1] != "+++ b/file.go" {
		t.Errorf("second line = %q, want '+++ b/file.go'", lines[1])
	}
}

func TestUnifiedDiff_HunkHeader(t *testing.T) {
	diff := unifiedDiff("a/x", "b/x", "a\n", "b\n")
	if !strings.Contains(diff, "@@") {
		t.Error("diff missing hunk header (@@)")
	}
}

func TestUnifiedDiff_MultipleChanges(t *testing.T) {
	// Old: lines 1-10, new: change line 2 and line 9
	var oldLines, newLines []string
	for i := 1; i <= 10; i++ {
		oldLines = append(oldLines, fmt.Sprintf("line%d", i))
		newLines = append(newLines, fmt.Sprintf("line%d", i))
	}
	newLines[1] = "CHANGED2"
	newLines[8] = "CHANGED9"

	oldText := strings.Join(oldLines, "\n") + "\n"
	newText := strings.Join(newLines, "\n") + "\n"

	diff := unifiedDiff("a/multi.txt", "b/multi.txt", oldText, newText)

	if !strings.Contains(diff, "-line2") {
		t.Error("diff missing -line2")
	}
	if !strings.Contains(diff, "+CHANGED2") {
		t.Error("diff missing +CHANGED2")
	}
	if !strings.Contains(diff, "-line9") {
		t.Error("diff missing -line9")
	}
	if !strings.Contains(diff, "+CHANGED9") {
		t.Error("diff missing +CHANGED9")
	}
}

// --- splitLines Tests ---

func TestSplitLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"single line with newline", "hello\n", []string{"hello"}},
		{"single line no newline", "hello", []string{"hello"}},
		{"multiple lines", "a\nb\nc\n", []string{"a", "b", "c"}},
		{"no trailing newline", "a\nb\nc", []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitLines(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("splitLines(%q) = %v (len %d), want %v (len %d)", tt.input, got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitLines(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// --- ApplySession Tests ---

func TestApplySession_DryRun(t *testing.T) {
	f := newFixture(t)
	session := "sess-apply-dry"
	hash := f.putContent("apply content")

	f.addEvent(session, "src/new.go", schema.OpCreate, hash, "", 13)

	targetDir := filepath.Join(f.tmpDir, "target")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("create target: %v", err)
	}

	affected, err := ApplySession(f.idx, f.objStore, session, targetDir, true)
	if err != nil {
		t.Fatalf("ApplySession (dry run): %v", err)
	}

	if len(affected) != 1 {
		t.Fatalf("affected count = %d, want 1", len(affected))
	}

	// File should NOT exist in dry run
	fullPath := filepath.Join(targetDir, "src/new.go")
	if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
		t.Error("expected file to not exist in dry run")
	}
}

func TestApplySession_CreateFiles(t *testing.T) {
	f := newFixture(t)
	session := "sess-apply-create"
	hash := f.putContent("created content")

	f.addEvent(session, "src/new.go", schema.OpCreate, hash, "", 15)

	targetDir := filepath.Join(f.tmpDir, "target")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("create target: %v", err)
	}

	affected, err := ApplySession(f.idx, f.objStore, session, targetDir, false)
	if err != nil {
		t.Fatalf("ApplySession: %v", err)
	}

	if len(affected) != 1 {
		t.Fatalf("affected count = %d, want 1", len(affected))
	}

	fullPath := filepath.Join(targetDir, "src/new.go")
	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("read created file: %v", err)
	}
	if string(data) != "created content" {
		t.Errorf("file content = %q, want 'created content'", string(data))
	}
}

func TestApplySession_ModifyFiles(t *testing.T) {
	f := newFixture(t)
	session := "sess-apply-modify"
	hashOld := f.putContent("old content")
	hashNew := f.putContent("new content")

	f.addEvent(session, "existing.go", schema.OpModify, hashNew, hashOld, 11)

	targetDir := filepath.Join(f.tmpDir, "target")
	existingPath := filepath.Join(targetDir, "existing.go")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := os.WriteFile(existingPath, []byte("old content"), 0644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	_, err := ApplySession(f.idx, f.objStore, session, targetDir, false)
	if err != nil {
		t.Fatalf("ApplySession: %v", err)
	}

	data, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatalf("read modified file: %v", err)
	}
	if string(data) != "new content" {
		t.Errorf("file content = %q, want 'new content'", string(data))
	}
}

func TestApplySession_DeleteFiles(t *testing.T) {
	f := newFixture(t)
	session := "sess-apply-delete"
	hashOld := f.putContent("delete me")

	f.addEvent(session, "gone.go", schema.OpDelete, "", hashOld, 0)

	targetDir := filepath.Join(f.tmpDir, "target")
	targetFile := filepath.Join(targetDir, "gone.go")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := os.WriteFile(targetFile, []byte("delete me"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := ApplySession(f.idx, f.objStore, session, targetDir, false)
	if err != nil {
		t.Fatalf("ApplySession: %v", err)
	}

	if _, err := os.Stat(targetFile); !os.IsNotExist(err) {
		t.Error("expected file to be deleted")
	}
}

func TestApplySession_DeleteNonexistent_NoError(t *testing.T) {
	f := newFixture(t)
	session := "sess-apply-delete-gone"
	hashOld := f.putContent("already gone")

	f.addEvent(session, "phantom.go", schema.OpDelete, "", hashOld, 0)

	targetDir := filepath.Join(f.tmpDir, "target")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("create target: %v", err)
	}

	// Should not error on deleting a file that doesn't exist
	_, err := ApplySession(f.idx, f.objStore, session, targetDir, false)
	if err != nil {
		t.Fatalf("ApplySession should not fail for nonexistent delete: %v", err)
	}
}

func TestApplySession_CreateNestedDirs(t *testing.T) {
	f := newFixture(t)
	session := "sess-apply-nested"
	hash := f.putContent("deep file")

	f.addEvent(session, "a/b/c/deep.go", schema.OpCreate, hash, "", 9)

	targetDir := filepath.Join(f.tmpDir, "target")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("create target: %v", err)
	}

	_, err := ApplySession(f.idx, f.objStore, session, targetDir, false)
	if err != nil {
		t.Fatalf("ApplySession: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(targetDir, "a/b/c/deep.go"))
	if err != nil {
		t.Fatalf("read deep file: %v", err)
	}
	if string(data) != "deep file" {
		t.Errorf("content = %q, want 'deep file'", string(data))
	}
}

func TestApplySession_DeleteCleansEmptyParents(t *testing.T) {
	f := newFixture(t)
	session := "sess-apply-clean"
	hashOld := f.putContent("clean me")

	f.addEvent(session, "a/b/c/target.go", schema.OpDelete, "", hashOld, 0)

	targetDir := filepath.Join(f.tmpDir, "target")
	deepDir := filepath.Join(targetDir, "a/b/c")
	if err := os.MkdirAll(deepDir, 0755); err != nil {
		t.Fatalf("create deep dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deepDir, "target.go"), []byte("clean me"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := ApplySession(f.idx, f.objStore, session, targetDir, false)
	if err != nil {
		t.Fatalf("ApplySession: %v", err)
	}

	// The empty parent directories should be cleaned up
	if _, err := os.Stat(deepDir); !os.IsNotExist(err) {
		t.Error("expected empty parent directory c/ to be removed")
	}
}

func TestApplySession_MissingContent_Error(t *testing.T) {
	f := newFixture(t)
	session := "sess-apply-missing"

	f.addEvent(session, "ghost.go", schema.OpCreate, "nonexistent-hash-aaaaaaaaaaaaaaaa", "", 10)

	targetDir := filepath.Join(f.tmpDir, "target")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("create target: %v", err)
	}

	_, err := ApplySession(f.idx, f.objStore, session, targetDir, false)
	if err == nil {
		t.Fatal("expected error for missing content, got nil")
	}
	if !strings.Contains(err.Error(), "get content") {
		t.Errorf("expected 'get content' error, got: %v", err)
	}
}

func TestApplySession_AffectedPathsSorted(t *testing.T) {
	f := newFixture(t)
	session := "sess-apply-sort"
	hashZ := f.putContent("z")
	hashA := f.putContent("a")

	f.addEvent(session, "z.go", schema.OpCreate, hashZ, "", 1)
	f.addEvent(session, "a.go", schema.OpCreate, hashA, "", 1)

	targetDir := filepath.Join(f.tmpDir, "target")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("create target: %v", err)
	}

	affected, err := ApplySession(f.idx, f.objStore, session, targetDir, true)
	if err != nil {
		t.Fatalf("ApplySession: %v", err)
	}

	if len(affected) != 2 {
		t.Fatalf("affected count = %d, want 2", len(affected))
	}
	if affected[0] != "a.go" || affected[1] != "z.go" {
		t.Errorf("affected = %v, want [a.go z.go] (sorted)", affected)
	}
}

// --- Snapshot Diff Between Two Points ---

func TestSnapshotAt_DiffBetweenTimePoints(t *testing.T) {
	f := newFixture(t)
	hashV1 := f.putContent("version 1")
	hashV2 := f.putContent("version 2")
	hashNew := f.putContent("brand new")

	ts1 := f.timeBase + 1000000
	ts2 := f.timeBase + 2000000
	ts3 := f.timeBase + 3000000

	f.addEventWithTime("s1", "file.go", schema.OpCreate, hashV1, "", 9, ts1)
	f.addEventWithTime("s1", "file.go", schema.OpModify, hashV2, hashV1, 9, ts2)
	f.addEventWithTime("s1", "extra.go", schema.OpCreate, hashNew, "", 9, ts3)

	// Snapshot at t1: only file.go v1
	snap1, err := SnapshotAt(f.idx, ts1)
	if err != nil {
		t.Fatalf("SnapshotAt(t1): %v", err)
	}
	if len(snap1.Files) != 1 {
		t.Fatalf("snap1 files = %d, want 1", len(snap1.Files))
	}
	if snap1.Files["file.go"].ContentHash != hashV1 {
		t.Error("snap1: file.go should be v1")
	}

	// Snapshot at t3: file.go v2 + extra.go
	snap2, err := SnapshotAt(f.idx, ts3)
	if err != nil {
		t.Fatalf("SnapshotAt(t3): %v", err)
	}
	if len(snap2.Files) != 2 {
		t.Fatalf("snap2 files = %d, want 2", len(snap2.Files))
	}
	if snap2.Files["file.go"].ContentHash != hashV2 {
		t.Error("snap2: file.go should be v2")
	}
	if _, ok := snap2.Files["extra.go"]; !ok {
		t.Error("snap2: expected extra.go")
	}

	// Verify the diff between the two snapshots
	// New files in snap2 not in snap1
	for path := range snap2.Files {
		if _, existed := snap1.Files[path]; !existed {
			// extra.go is new
			if path != "extra.go" {
				t.Errorf("unexpected new file: %s", path)
			}
		}
	}
	// Modified files (same path, different hash)
	if snap1.Files["file.go"].ContentHash == snap2.Files["file.go"].ContentHash {
		t.Error("file.go should have different hashes between snapshots")
	}
}

// --- FileState fields ---

func TestSnapshotAt_FileStateFields(t *testing.T) {
	f := newFixture(t)
	hash := f.putContent("content with size")

	ts := f.timeBase + 1000000
	f.addEventWithTime("s1", "sized.go", schema.OpCreate, hash, "", 18, ts)

	snap, err := SnapshotAt(f.idx, ts)
	if err != nil {
		t.Fatalf("SnapshotAt: %v", err)
	}

	fs := snap.Files["sized.go"]
	if fs == nil {
		t.Fatal("expected sized.go in snapshot")
	}
	if fs.Path != "sized.go" {
		t.Errorf("Path = %q, want 'sized.go'", fs.Path)
	}
	if fs.ContentHash != hash {
		t.Errorf("ContentHash = %q, want %q", fs.ContentHash, hash)
	}
	if fs.Size != 18 {
		t.Errorf("Size = %d, want 18", fs.Size)
	}
	if fs.LastEvent == "" {
		t.Error("LastEvent should be set")
	}
}

// --- Edge Cases ---

func TestReplaySession_NonexistentSession(t *testing.T) {
	f := newFixture(t)
	_, err := ReplaySession(f.idx, f.objStore, "does-not-exist")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestGeneratePatch_NonexistentSession(t *testing.T) {
	f := newFixture(t)
	_, err := GeneratePatch(f.idx, f.objStore, "does-not-exist")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestApplySession_NonexistentSession(t *testing.T) {
	f := newFixture(t)
	_, err := ApplySession(f.idx, f.objStore, "does-not-exist", f.tmpDir, false)
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestGeneratePatch_UnifiedDiffFormat(t *testing.T) {
	f := newFixture(t)
	session := "sess-format"
	hashOld := f.putContent("line1\nline2\nline3\n")
	hashNew := f.putContent("line1\nmodified\nline3\n")

	f.addEvent(session, "format.txt", schema.OpModify, hashNew, hashOld, 20)

	patch, err := GeneratePatch(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("GeneratePatch: %v", err)
	}

	// Verify unified diff structure
	lines := strings.Split(strings.TrimRight(patch, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("patch too short, got %d lines", len(lines))
	}

	// Line 0: --- header
	if !strings.HasPrefix(lines[0], "--- ") {
		t.Errorf("line 0 should start with '--- ', got: %q", lines[0])
	}
	// Line 1: +++ header
	if !strings.HasPrefix(lines[1], "+++ ") {
		t.Errorf("line 1 should start with '+++ ', got: %q", lines[1])
	}
	// Line 2: @@ hunk header
	if !strings.HasPrefix(lines[2], "@@ ") {
		t.Errorf("line 2 should start with '@@ ', got: %q", lines[2])
	}

	// Every remaining line should start with ' ', '+', or '-'
	for i := 3; i < len(lines); i++ {
		if lines[i] == "" {
			continue
		}
		prefix := lines[i][0]
		if prefix != ' ' && prefix != '+' && prefix != '-' {
			t.Errorf("line %d has invalid prefix %q: %q", i, string(prefix), lines[i])
		}
	}
}

// --- Large File Diff Guard Tests ---

func TestUnifiedDiff_SmallFiles_ProduceCorrectDiff(t *testing.T) {
	// Normal small files should still produce a correct unified diff
	oldText := "line1\nline2\nline3\n"
	newText := "line1\nchanged\nline3\n"
	diff := unifiedDiff("a/small.txt", "b/small.txt", oldText, newText)

	if !strings.Contains(diff, "--- a/small.txt") {
		t.Error("diff missing old header")
	}
	if !strings.Contains(diff, "+++ b/small.txt") {
		t.Error("diff missing new header")
	}
	if !strings.Contains(diff, "-line2") {
		t.Error("diff missing deleted line")
	}
	if !strings.Contains(diff, "+changed") {
		t.Error("diff missing inserted line")
	}
	if strings.Contains(diff, "Large file") {
		t.Error("small file should not trigger large-file guard")
	}
}

func TestUnifiedDiff_LargeFile_ReturnsSummary(t *testing.T) {
	// Build a file with 15000 lines to trigger the large-file guard (limit: 10000)
	var lines []string
	for i := 0; i < 15000; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	largeText := strings.Join(lines, "\n") + "\n"
	smallText := "just one line\n"

	// Old file is large
	diff := unifiedDiff("a/big.txt", "b/big.txt", largeText, smallText)
	if !strings.Contains(diff, "Large file - diff skipped") {
		t.Error("expected large-file summary when old file exceeds limit")
	}
	if !strings.Contains(diff, "old: 15000 lines") {
		t.Errorf("expected 'old: 15000 lines' in summary, got: %s", diff)
	}
	if !strings.Contains(diff, "limit: 10000") {
		t.Errorf("expected 'limit: 10000' in summary, got: %s", diff)
	}

	// New file is large
	diff2 := unifiedDiff("a/big2.txt", "b/big2.txt", smallText, largeText)
	if !strings.Contains(diff2, "Large file - diff skipped") {
		t.Error("expected large-file summary when new file exceeds limit")
	}
	if !strings.Contains(diff2, "new: 15000 lines") {
		t.Errorf("expected 'new: 15000 lines' in summary, got: %s", diff2)
	}
}

func TestComputeEdits_SafetyNet_ExtremeCase(t *testing.T) {
	// Create inputs that would exceed maxLCSCells (100M) if fully computed.
	// We use 10001 x 10001 = 100,020,001 > 100,000,000 to trigger the fallback.
	n := 10001
	oldLines := make([]string, n)
	newLines := make([]string, n)
	for i := 0; i < n; i++ {
		oldLines[i] = fmt.Sprintf("old-%d", i)
		newLines[i] = fmt.Sprintf("new-%d", i)
	}

	edits := computeEdits(oldLines, newLines)

	// The safety net should return all-delete + all-insert
	deleteCount := 0
	insertCount := 0
	for _, e := range edits {
		switch e.op {
		case editDelete:
			deleteCount++
		case editInsert:
			insertCount++
		case editEqual:
			t.Error("safety net fallback should not produce editEqual ops")
		}
	}

	if deleteCount != n {
		t.Errorf("expected %d deletes, got %d", n, deleteCount)
	}
	if insertCount != n {
		t.Errorf("expected %d inserts, got %d", n, insertCount)
	}
}

func TestComputeEdits_BelowSafetyNet_UsesLCS(t *testing.T) {
	// Small inputs should use the real LCS algorithm and produce editEqual ops
	oldLines := []string{"a", "b", "c"}
	newLines := []string{"a", "x", "c"}

	edits := computeEdits(oldLines, newLines)

	hasEqual := false
	for _, e := range edits {
		if e.op == editEqual {
			hasEqual = true
			break
		}
	}
	if !hasEqual {
		t.Error("small inputs should use LCS and produce editEqual ops for matching lines")
	}
}

// --- Stat Output (derived from SessionResult) ---

func TestReplaySession_StatOutput(t *testing.T) {
	f := newFixture(t)
	session := "sess-stat"
	hashA := f.putContent("new A")
	hashBOld := f.putContent("old B")
	hashBNew := f.putContent("new B")
	hashC := f.putContent("deleted C")

	f.addEvent(session, "created.go", schema.OpCreate, hashA, "", 5)
	f.addEvent(session, "modified.go", schema.OpModify, hashBNew, hashBOld, 5)
	f.addEvent(session, "deleted.go", schema.OpDelete, "", hashC, 0)

	result, err := ReplaySession(f.idx, f.objStore, session)
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}

	// Build stat counts
	creates, modifies, deletes := 0, 0, 0
	for _, fc := range result.Files {
		switch fc.Operation {
		case "create":
			creates++
		case "modify":
			modifies++
		case "delete":
			deletes++
		}
	}

	if creates != 1 {
		t.Errorf("creates = %d, want 1", creates)
	}
	if modifies != 1 {
		t.Errorf("modifies = %d, want 1", modifies)
	}
	if deletes != 1 {
		t.Errorf("deletes = %d, want 1", deletes)
	}
	if len(result.Files) != 3 {
		t.Errorf("total files = %d, want 3", len(result.Files))
	}
}
