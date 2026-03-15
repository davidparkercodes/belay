package git

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"
)

// ---------------------------------------------------------------------------
// Test fixture (matches the pattern used in replay_test.go)
// ---------------------------------------------------------------------------

type testFixture struct {
	t          *testing.T
	idx        *index.Index
	objStore   *store.Store
	tmpDir     string
	eventSeq   int
	timeBase   int64
	projectDir string // a real git repo
	belayDir  string
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

	// Create a real git repo for tests that need one.
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = projectDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	// Configure git user for commits.
	for _, args := range [][]string{
		{"config", "user.email", "test@belay.dev"},
		{"config", "user.name", "Belay Test"},
	} {
		cmd = exec.Command("git", args...)
		cmd.Dir = projectDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
		}
	}
	// Create an initial commit so HEAD exists.
	readmePath := filepath.Join(projectDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# test\n"), 0644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = projectDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s: %v", out, err)
	}
	cmd = exec.Command("git", "commit", "-m", "initial commit")
	cmd.Dir = projectDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s: %v", out, err)
	}

	belayDir := filepath.Join(tmpDir, ".belay")
	if err := os.MkdirAll(belayDir, 0755); err != nil {
		t.Fatalf("create belay dir: %v", err)
	}

	return &testFixture{
		t:          t,
		idx:        idx,
		objStore:   objStore,
		tmpDir:     tmpDir,
		timeBase:   1700000000000000000,
		projectDir: projectDir,
		belayDir:  belayDir,
	}
}

func (f *testFixture) putContent(content string) string {
	f.t.Helper()
	hash, _, err := f.objStore.Put([]byte(content))
	if err != nil {
		f.t.Fatalf("put content: %v", err)
	}
	return hash
}

func (f *testFixture) addEvent(sessionID, filePath string, op schema.Operation, contentHash, previousHash string) {
	f.t.Helper()
	f.eventSeq++
	ev := &schema.Event{
		EventID:       fmt.Sprintf("evt-%04d", f.eventSeq),
		TimestampNano: f.timeBase + int64(f.eventSeq)*1000000,
		FilePath:      filePath,
		Op:            op,
		ContentHash:   contentHash,
		PreviousHash:  previousHash,
		ContentSize:   int64(len(contentHash)),
		SessionID:     sessionID,
	}
	if err := f.idx.IndexEvent(ev, "test.log", 0); err != nil {
		f.t.Fatalf("index event: %v", err)
	}
}

// ---------------------------------------------------------------------------
// IsGitRepo
// ---------------------------------------------------------------------------

func TestIsGitRepo_RealRepo(t *testing.T) {
	f := newFixture(t)
	if !IsGitRepo(f.projectDir) {
		t.Error("IsGitRepo returned false for a real git repo")
	}
}

func TestIsGitRepo_NonGitDir(t *testing.T) {
	dir := t.TempDir()
	if IsGitRepo(dir) {
		t.Error("IsGitRepo returned true for a non-git directory")
	}
}

func TestIsGitRepo_NonExistentDir(t *testing.T) {
	if IsGitRepo("/tmp/belay-nonexistent-dir-xyz") {
		t.Error("IsGitRepo returned true for a non-existent directory")
	}
}

// ---------------------------------------------------------------------------
// EnsureBelayIgnored
// ---------------------------------------------------------------------------

func TestEnsureBelayIgnored_CreatesGitignore(t *testing.T) {
	dir := t.TempDir()

	if err := EnsureBelayIgnored(dir); err != nil {
		t.Fatalf("EnsureBelayIgnored: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(content), ".belay/") {
		t.Errorf(".gitignore missing .belay/, got: %q", content)
	}
}

func TestEnsureBelayIgnored_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("node_modules/\n"), 0644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	if err := EnsureBelayIgnored(dir); err != nil {
		t.Fatalf("EnsureBelayIgnored: %v", err)
	}

	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(content), "node_modules/") {
		t.Error("original content was lost")
	}
	if !strings.Contains(string(content), ".belay/") {
		t.Error(".belay/ not appended")
	}
}

func TestEnsureBelayIgnored_AppendsNewlineIfMissing(t *testing.T) {
	dir := t.TempDir()
	gitignorePath := filepath.Join(dir, ".gitignore")
	// No trailing newline
	if err := os.WriteFile(gitignorePath, []byte("*.log"), 0644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	if err := EnsureBelayIgnored(dir); err != nil {
		t.Fatalf("EnsureBelayIgnored: %v", err)
	}

	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	// Should have newline between existing content and .belay/
	if !strings.Contains(string(content), "*.log\n.belay/\n") {
		t.Errorf("expected newline-separated entries, got: %q", content)
	}
}

func TestEnsureBelayIgnored_SkipsIfAlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	gitignorePath := filepath.Join(dir, ".gitignore")
	original := "node_modules/\n.belay/\n"
	if err := os.WriteFile(gitignorePath, []byte(original), 0644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	if err := EnsureBelayIgnored(dir); err != nil {
		t.Fatalf("EnsureBelayIgnored: %v", err)
	}

	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if string(content) != original {
		t.Errorf("content changed when .belay/ already present: %q vs %q", content, original)
	}
}

func TestEnsureBelayIgnored_SkipsIfWithoutSlash(t *testing.T) {
	dir := t.TempDir()
	gitignorePath := filepath.Join(dir, ".gitignore")
	original := ".belay\n"
	if err := os.WriteFile(gitignorePath, []byte(original), 0644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	if err := EnsureBelayIgnored(dir); err != nil {
		t.Fatalf("EnsureBelayIgnored: %v", err)
	}

	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	// ".belay" (without slash) is also recognized, so no change expected
	if string(content) != original {
		t.Errorf("content changed when .belay already present: %q vs %q", content, original)
	}
}

// ---------------------------------------------------------------------------
// computeNetChanges — table-driven tests
// ---------------------------------------------------------------------------

func TestComputeNetChanges(t *testing.T) {
	tests := []struct {
		name    string
		events  []*schema.Event
		want    []fileChange
		wantLen int
	}{
		{
			name: "single create",
			events: []*schema.Event{
				{FilePath: "a.go", Op: schema.OpCreate, ContentHash: "h1"},
			},
			want: []fileChange{
				{filePath: "a.go", op: schema.OpCreate, contentHash: "h1"},
			},
		},
		{
			name: "single modify",
			events: []*schema.Event{
				{FilePath: "a.go", Op: schema.OpModify, ContentHash: "h2", PreviousHash: "h1"},
			},
			want: []fileChange{
				{filePath: "a.go", op: schema.OpModify, contentHash: "h2"},
			},
		},
		{
			name: "single delete",
			events: []*schema.Event{
				{FilePath: "a.go", Op: schema.OpDelete, PreviousHash: "h1"},
			},
			want: []fileChange{
				{filePath: "a.go", op: schema.OpDelete, contentHash: ""},
			},
		},
		{
			name: "create then delete cancels out",
			events: []*schema.Event{
				{FilePath: "a.go", Op: schema.OpCreate, ContentHash: "h1"},
				{FilePath: "a.go", Op: schema.OpDelete, PreviousHash: "h1"},
			},
			wantLen: 0,
		},
		{
			name: "create then modify is net create",
			events: []*schema.Event{
				{FilePath: "a.go", Op: schema.OpCreate, ContentHash: "h1"},
				{FilePath: "a.go", Op: schema.OpModify, ContentHash: "h2", PreviousHash: "h1"},
			},
			want: []fileChange{
				{filePath: "a.go", op: schema.OpCreate, contentHash: "h2"},
			},
		},
		{
			name: "modify then delete is net delete",
			events: []*schema.Event{
				{FilePath: "a.go", Op: schema.OpModify, ContentHash: "h2", PreviousHash: "h1"},
				{FilePath: "a.go", Op: schema.OpDelete, PreviousHash: "h2"},
			},
			want: []fileChange{
				{filePath: "a.go", op: schema.OpDelete, contentHash: ""},
			},
		},
		{
			name: "modify back to original is no-op",
			events: []*schema.Event{
				{FilePath: "a.go", Op: schema.OpModify, ContentHash: "h2", PreviousHash: "h1"},
				{FilePath: "a.go", Op: schema.OpModify, ContentHash: "h1", PreviousHash: "h2"},
			},
			wantLen: 0,
		},
		{
			name: "multiple files sorted by path",
			events: []*schema.Event{
				{FilePath: "z.go", Op: schema.OpCreate, ContentHash: "hz"},
				{FilePath: "a.go", Op: schema.OpCreate, ContentHash: "ha"},
				{FilePath: "m.go", Op: schema.OpCreate, ContentHash: "hm"},
			},
			want: []fileChange{
				{filePath: "a.go", op: schema.OpCreate, contentHash: "ha"},
				{filePath: "m.go", op: schema.OpCreate, contentHash: "hm"},
				{filePath: "z.go", op: schema.OpCreate, contentHash: "hz"},
			},
		},
		{
			name: "create modify modify is net create with last hash",
			events: []*schema.Event{
				{FilePath: "a.go", Op: schema.OpCreate, ContentHash: "h1"},
				{FilePath: "a.go", Op: schema.OpModify, ContentHash: "h2", PreviousHash: "h1"},
				{FilePath: "a.go", Op: schema.OpModify, ContentHash: "h3", PreviousHash: "h2"},
			},
			want: []fileChange{
				{filePath: "a.go", op: schema.OpCreate, contentHash: "h3"},
			},
		},
		{
			name:    "empty event list",
			events:  []*schema.Event{},
			wantLen: 0,
		},
		{
			name: "mixed files: one canceled, one kept",
			events: []*schema.Event{
				{FilePath: "temp.go", Op: schema.OpCreate, ContentHash: "ht"},
				{FilePath: "kept.go", Op: schema.OpCreate, ContentHash: "hk"},
				{FilePath: "temp.go", Op: schema.OpDelete, PreviousHash: "ht"},
			},
			want: []fileChange{
				{filePath: "kept.go", op: schema.OpCreate, contentHash: "hk"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeNetChanges(tt.events)

			if tt.want != nil {
				if len(got) != len(tt.want) {
					t.Fatalf("got %d changes, want %d", len(got), len(tt.want))
				}
				for i, w := range tt.want {
					if got[i].filePath != w.filePath {
						t.Errorf("change[%d].filePath = %q, want %q", i, got[i].filePath, w.filePath)
					}
					if got[i].op != w.op {
						t.Errorf("change[%d].op = %v, want %v", i, got[i].op, w.op)
					}
					if got[i].contentHash != w.contentHash {
						t.Errorf("change[%d].contentHash = %q, want %q", i, got[i].contentHash, w.contentHash)
					}
				}
			} else {
				if len(got) != tt.wantLen {
					t.Errorf("got %d changes, want %d", len(got), tt.wantLen)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildCommitMessage
// ---------------------------------------------------------------------------

func TestBuildCommitMessage_AllTypes(t *testing.T) {
	result := &CommitResult{
		FilesAdded:    2,
		FilesModified: 3,
		FilesDeleted:  1,
	}
	msg := buildCommitMessage("abc12345-long-session", result)

	if !strings.Contains(msg, "abc12345") {
		t.Error("message should contain truncated session ID")
	}
	if strings.Contains(msg, "abc12345-long-session") {
		t.Error("message should truncate long session IDs")
	}
	if !strings.Contains(msg, "2 added") {
		t.Error("message should mention added files")
	}
	if !strings.Contains(msg, "3 modified") {
		t.Error("message should mention modified files")
	}
	if !strings.Contains(msg, "1 deleted") {
		t.Error("message should mention deleted files")
	}
}

func TestBuildCommitMessage_OnlyAdded(t *testing.T) {
	result := &CommitResult{FilesAdded: 5}
	msg := buildCommitMessage("sessid", result)
	if !strings.Contains(msg, "5 added") {
		t.Errorf("expected '5 added' in message, got: %q", msg)
	}
	if strings.Contains(msg, "modified") || strings.Contains(msg, "deleted") {
		t.Errorf("unexpected operation in message: %q", msg)
	}
}

func TestBuildCommitMessage_OnlyDeleted(t *testing.T) {
	result := &CommitResult{FilesDeleted: 1}
	msg := buildCommitMessage("sessid", result)
	if !strings.Contains(msg, "1 deleted") {
		t.Errorf("expected '1 deleted' in message, got: %q", msg)
	}
}

func TestBuildCommitMessage_ShortSessionID(t *testing.T) {
	result := &CommitResult{FilesAdded: 1}
	msg := buildCommitMessage("abc", result)
	if !strings.Contains(msg, "abc") {
		t.Errorf("expected short session ID 'abc' in message, got: %q", msg)
	}
}

// ---------------------------------------------------------------------------
// appendBelayTrailers
// ---------------------------------------------------------------------------

func TestAppendBelayTrailers(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-10 * time.Minute)

	events := []*schema.Event{
		{TimestampNano: earlier.UnixNano()},
		{TimestampNano: now.UnixNano()},
	}

	msg := appendBelayTrailers("test commit", "sess-123", events)

	if !strings.HasPrefix(msg, "test commit") {
		t.Error("message should start with original text")
	}
	if !strings.Contains(msg, "Belay-Session: sess-123") {
		t.Errorf("missing Belay-Session trailer in: %q", msg)
	}
	if !strings.Contains(msg, "Belay-Events: 2") {
		t.Errorf("missing Belay-Events trailer in: %q", msg)
	}
	if !strings.Contains(msg, "Belay-Start:") {
		t.Errorf("missing Belay-Start trailer in: %q", msg)
	}
	if !strings.Contains(msg, "Belay-End:") {
		t.Errorf("missing Belay-End trailer in: %q", msg)
	}
}

func TestAppendBelayTrailers_SingleEvent(t *testing.T) {
	ts := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	events := []*schema.Event{
		{TimestampNano: ts.UnixNano()},
	}

	msg := appendBelayTrailers("msg", "s1", events)

	// With a single event, start and end should be the same
	startIdx := strings.Index(msg, "Belay-Start: ")
	endIdx := strings.Index(msg, "Belay-End: ")
	if startIdx < 0 || endIdx < 0 {
		t.Fatalf("missing trailers in: %q", msg)
	}

	// Extract the timestamps
	startLine := msg[startIdx:]
	startLine = startLine[len("Belay-Start: "):strings.Index(startLine, "\n")]
	endLine := msg[endIdx:]
	endLine = endLine[len("Belay-End: "):]

	if startLine != endLine {
		t.Errorf("single event: start %q != end %q", startLine, endLine)
	}
}

func TestAppendBelayTrailers_OrderIndependent(t *testing.T) {
	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)

	// Events out of order
	events := []*schema.Event{
		{TimestampNano: t2.UnixNano()},
		{TimestampNano: t1.UnixNano()},
		{TimestampNano: t3.UnixNano()},
	}

	msg := appendBelayTrailers("msg", "s1", events)

	// Timestamp() uses time.Unix(0, nano) which returns local time,
	// so format using the same path the code uses.
	expectedStart := time.Unix(0, t1.UnixNano()).Format(time.RFC3339)
	expectedEnd := time.Unix(0, t3.UnixNano()).Format(time.RFC3339)

	if !strings.Contains(msg, "Belay-Start: "+expectedStart) {
		t.Errorf("expected earliest time as start, got: %q", msg)
	}
	if !strings.Contains(msg, "Belay-End: "+expectedEnd) {
		t.Errorf("expected latest time as end, got: %q", msg)
	}
}

// ---------------------------------------------------------------------------
// truncate
// ---------------------------------------------------------------------------

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 3, "hel"},
		{"", 5, ""},
		{"ab", 0, ""},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.n)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// gitCmd
// ---------------------------------------------------------------------------

func TestGitCmd_Success(t *testing.T) {
	f := newFixture(t)
	out, err := gitCmd(f.projectDir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("gitCmd status: %v", err)
	}
	// Clean repo should produce empty status
	if out != "" {
		t.Errorf("expected empty status, got: %q", out)
	}
}

func TestGitCmd_TrimsOutput(t *testing.T) {
	f := newFixture(t)
	out, err := gitCmd(f.projectDir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		t.Fatalf("gitCmd: %v", err)
	}
	if out != "true" {
		t.Errorf("expected 'true', got: %q", out)
	}
}

func TestGitCmd_InvalidCommand(t *testing.T) {
	f := newFixture(t)
	_, err := gitCmd(f.projectDir, "not-a-real-command")
	if err == nil {
		t.Error("expected error for invalid git command")
	}
}

func TestGitCmd_InvalidDir(t *testing.T) {
	_, err := gitCmd("/tmp/belay-nonexistent-dir-xyz", "status")
	if err == nil {
		t.Error("expected error for non-existent directory")
	}
}

// ---------------------------------------------------------------------------
// CommitSession — DRY-RUN mode
// ---------------------------------------------------------------------------

func TestCommitSession_DryRun_NoSideEffects(t *testing.T) {
	f := newFixture(t)
	sessionID := "dry-run-session"
	hash := f.putContent("dry run content")
	f.addEvent(sessionID, "new-file.go", schema.OpCreate, hash, "")

	// Get hash before
	hashBefore, _ := gitCmd(f.projectDir, "rev-parse", "HEAD")

	result, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{
		SessionID: sessionID,
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("CommitSession dry-run: %v", err)
	}

	// Verify no actual commit was made
	hashAfter, _ := gitCmd(f.projectDir, "rev-parse", "HEAD")
	if hashBefore != hashAfter {
		t.Error("dry-run should not create a commit")
	}

	// Verify the file was NOT written
	filePath := filepath.Join(f.projectDir, "new-file.go")
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("dry-run should not write files")
	}

	// Verify the result has correct stats
	if result.FilesAdded != 1 {
		t.Errorf("FilesAdded = %d, want 1", result.FilesAdded)
	}
	if result.Message == "" {
		t.Error("result.Message should be set")
	}
}

func TestCommitSession_DryRun_CustomMessage(t *testing.T) {
	f := newFixture(t)
	sessionID := "custom-msg-session"
	hash := f.putContent("content")
	f.addEvent(sessionID, "file.go", schema.OpCreate, hash, "")

	result, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{
		SessionID: sessionID,
		Message:   "custom commit message",
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("CommitSession: %v", err)
	}

	if !strings.HasPrefix(result.Message, "custom commit message") {
		t.Errorf("message should start with custom message, got: %q", result.Message)
	}
}

func TestCommitSession_DryRun_NoMetadata(t *testing.T) {
	f := newFixture(t)
	sessionID := "no-meta-session"
	hash := f.putContent("content")
	f.addEvent(sessionID, "file.go", schema.OpCreate, hash, "")

	result, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{
		SessionID:  sessionID,
		DryRun:     true,
		NoMetadata: true,
	})
	if err != nil {
		t.Fatalf("CommitSession: %v", err)
	}

	if strings.Contains(result.Message, "Belay-Session") {
		t.Error("NoMetadata should suppress trailers")
	}
}

func TestCommitSession_DryRun_WithMetadata(t *testing.T) {
	f := newFixture(t)
	sessionID := "meta-session"
	hash := f.putContent("content")
	f.addEvent(sessionID, "file.go", schema.OpCreate, hash, "")

	result, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{
		SessionID: sessionID,
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("CommitSession: %v", err)
	}

	if !strings.Contains(result.Message, "Belay-Session") {
		t.Error("expected Belay trailers in message")
	}
}

func TestCommitSession_DryRun_FileFilter(t *testing.T) {
	f := newFixture(t)
	sessionID := "filter-session"
	hash1 := f.putContent("file1")
	hash2 := f.putContent("file2")
	f.addEvent(sessionID, "include.go", schema.OpCreate, hash1, "")
	f.addEvent(sessionID, "exclude.go", schema.OpCreate, hash2, "")

	result, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{
		SessionID: sessionID,
		Files:     []string{"include.go"},
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("CommitSession: %v", err)
	}

	if result.FilesAdded != 1 {
		t.Errorf("FilesAdded = %d, want 1 (only include.go)", result.FilesAdded)
	}
}

func TestCommitSession_DryRun_MixedOperations(t *testing.T) {
	f := newFixture(t)
	sessionID := "mixed-session"
	hashNew := f.putContent("new content")
	hashModOld := f.putContent("old mod")
	hashModNew := f.putContent("new mod")
	f.addEvent(sessionID, "created.go", schema.OpCreate, hashNew, "")
	f.addEvent(sessionID, "modified.go", schema.OpModify, hashModNew, hashModOld)
	f.addEvent(sessionID, "deleted.go", schema.OpDelete, "", "hash-old-del")

	result, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{
		SessionID: sessionID,
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("CommitSession: %v", err)
	}

	if result.FilesAdded != 1 {
		t.Errorf("FilesAdded = %d, want 1", result.FilesAdded)
	}
	if result.FilesModified != 1 {
		t.Errorf("FilesModified = %d, want 1", result.FilesModified)
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
}

// ---------------------------------------------------------------------------
// CommitSession — error cases
// ---------------------------------------------------------------------------

func TestCommitSession_EmptySessionID(t *testing.T) {
	f := newFixture(t)
	_, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{})
	if err == nil {
		t.Fatal("expected error for empty session ID")
	}
	if !strings.Contains(err.Error(), "session ID is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCommitSession_NotGitRepo(t *testing.T) {
	f := newFixture(t)
	hash := f.putContent("content")
	f.addEvent("s1", "file.go", schema.OpCreate, hash, "")

	dir := t.TempDir()
	_, err := CommitSession(f.idx, f.objStore, dir, CommitOptions{
		SessionID: "s1",
	})
	if err == nil {
		t.Fatal("expected error for non-git repo")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCommitSession_NoEvents(t *testing.T) {
	f := newFixture(t)
	_, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{
		SessionID: "nonexistent-session",
	})
	if err == nil {
		t.Fatal("expected error for session with no events")
	}
	if !strings.Contains(err.Error(), "no events found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCommitSession_NoNetChanges(t *testing.T) {
	f := newFixture(t)
	sessionID := "cancel-session"
	hash := f.putContent("temp")
	f.addEvent(sessionID, "temp.go", schema.OpCreate, hash, "")
	f.addEvent(sessionID, "temp.go", schema.OpDelete, "", hash)

	_, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{
		SessionID: sessionID,
	})
	if err == nil {
		t.Fatal("expected error when all changes cancel out")
	}
	if !strings.Contains(err.Error(), "no net changes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCommitSession_FilterToNoChanges(t *testing.T) {
	f := newFixture(t)
	sessionID := "filter-empty-session"
	hash := f.putContent("content")
	f.addEvent(sessionID, "exists.go", schema.OpCreate, hash, "")

	_, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{
		SessionID: sessionID,
		Files:     []string{"nonexistent.go"},
	})
	if err == nil {
		t.Fatal("expected error when file filter matches nothing")
	}
	if !strings.Contains(err.Error(), "no net changes") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CommitSession — execute mode (real git commit)
// ---------------------------------------------------------------------------

func TestCommitSession_Execute_CreateFile(t *testing.T) {
	f := newFixture(t)
	sessionID := "exec-create"
	content := "package main\n\nfunc main() {}\n"
	hash := f.putContent(content)
	f.addEvent(sessionID, "main.go", schema.OpCreate, hash, "")

	result, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("CommitSession execute: %v", err)
	}

	if result.Hash == "" {
		t.Error("expected commit hash")
	}
	if result.FilesAdded != 1 {
		t.Errorf("FilesAdded = %d, want 1", result.FilesAdded)
	}

	// Verify file exists on disk
	data, err := os.ReadFile(filepath.Join(f.projectDir, "main.go"))
	if err != nil {
		t.Fatalf("read created file: %v", err)
	}
	if string(data) != content {
		t.Errorf("file content = %q, want %q", data, content)
	}

	// Verify git log contains the commit
	logOutput, err := gitCmd(f.projectDir, "log", "--oneline", "-1")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !strings.Contains(logOutput, "belay:") {
		t.Errorf("commit message not found in log: %q", logOutput)
	}
}

func TestCommitSession_Execute_ModifyFile(t *testing.T) {
	f := newFixture(t)

	// Write original file and commit it
	origPath := filepath.Join(f.projectDir, "existing.go")
	origContent := "original content"
	if err := os.WriteFile(origPath, []byte(origContent), 0644); err != nil {
		t.Fatalf("write original: %v", err)
	}
	gitCmd(f.projectDir, "add", "existing.go")
	gitCmd(f.projectDir, "commit", "-m", "add existing.go")

	// Set up belay events for modify
	sessionID := "exec-modify"
	newContent := "modified content"
	hashOld := f.putContent(origContent)
	hashNew := f.putContent(newContent)
	f.addEvent(sessionID, "existing.go", schema.OpModify, hashNew, hashOld)

	result, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("CommitSession execute modify: %v", err)
	}

	if result.FilesModified != 1 {
		t.Errorf("FilesModified = %d, want 1", result.FilesModified)
	}

	data, err := os.ReadFile(origPath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != newContent {
		t.Errorf("content = %q, want %q", data, newContent)
	}
}

func TestCommitSession_Execute_DeleteFile(t *testing.T) {
	f := newFixture(t)

	// Create and commit a file first
	delPath := filepath.Join(f.projectDir, "to-delete.go")
	if err := os.WriteFile(delPath, []byte("delete me"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	gitCmd(f.projectDir, "add", "to-delete.go")
	gitCmd(f.projectDir, "commit", "-m", "add to-delete.go")

	sessionID := "exec-delete"
	hashOld := f.putContent("delete me")
	f.addEvent(sessionID, "to-delete.go", schema.OpDelete, "", hashOld)

	result, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("CommitSession execute delete: %v", err)
	}

	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}

	// File should no longer exist
	if _, err := os.Stat(delPath); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

func TestCommitSession_Execute_NestedDirs(t *testing.T) {
	f := newFixture(t)
	sessionID := "exec-nested"
	content := "deep content"
	hash := f.putContent(content)
	f.addEvent(sessionID, "a/b/c/deep.go", schema.OpCreate, hash, "")

	_, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("CommitSession: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(f.projectDir, "a", "b", "c", "deep.go"))
	if err != nil {
		t.Fatalf("read nested file: %v", err)
	}
	if string(data) != content {
		t.Errorf("content = %q, want %q", data, content)
	}
}

func TestCommitSession_Execute_CustomMessage(t *testing.T) {
	f := newFixture(t)
	sessionID := "exec-msg"
	hash := f.putContent("content")
	f.addEvent(sessionID, "msg-file.go", schema.OpCreate, hash, "")

	_, err := CommitSession(f.idx, f.objStore, f.projectDir, CommitOptions{
		SessionID:  sessionID,
		Message:    "my custom message",
		NoMetadata: true,
	})
	if err != nil {
		t.Fatalf("CommitSession: %v", err)
	}

	logOutput, err := gitCmd(f.projectDir, "log", "--format=%B", "-1")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !strings.Contains(logOutput, "my custom message") {
		t.Errorf("commit message = %q, expected to contain custom message", logOutput)
	}
}

// ---------------------------------------------------------------------------
// StashSession
// ---------------------------------------------------------------------------

func TestStashSession_EmptySessionID(t *testing.T) {
	f := newFixture(t)
	_, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, "")
	if err == nil {
		t.Fatal("expected error for empty session ID")
	}
	if !strings.Contains(err.Error(), "session ID is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStashSession_NoEvents(t *testing.T) {
	f := newFixture(t)
	_, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, "nonexistent-session")
	if err == nil {
		t.Fatal("expected error for session with no events")
	}
	if !strings.Contains(err.Error(), "no events found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStashSession_NoNetChanges(t *testing.T) {
	f := newFixture(t)
	sessionID := "stash-cancel"
	hash := f.putContent("temp")
	f.addEvent(sessionID, "temp.go", schema.OpCreate, hash, "")
	f.addEvent(sessionID, "temp.go", schema.OpDelete, "", hash)

	_, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, sessionID)
	if err == nil {
		t.Fatal("expected error when all changes cancel out")
	}
	if !strings.Contains(err.Error(), "no net changes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStashSession_CreatesManifest(t *testing.T) {
	f := newFixture(t)
	sessionID := "stash-create"
	content := "stash me"
	hash := f.putContent(content)
	f.addEvent(sessionID, "stashed.go", schema.OpCreate, hash, "")

	// Write the file to the project dir (it was "created" by the session)
	if err := os.WriteFile(filepath.Join(f.projectDir, "stashed.go"), []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	info, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, sessionID)
	if err != nil {
		t.Fatalf("StashSession: %v", err)
	}

	if info.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", info.SessionID, sessionID)
	}
	if len(info.Files) != 1 {
		t.Errorf("Files count = %d, want 1", len(info.Files))
	}

	// Verify manifest file was created
	manifestPath := filepath.Join(info.StashDir, "manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var manifest stashManifestFile
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if manifest.SessionID != sessionID {
		t.Errorf("manifest session = %q, want %q", manifest.SessionID, sessionID)
	}
	if len(manifest.Entries) != 1 {
		t.Errorf("manifest entries = %d, want 1", len(manifest.Entries))
	}
}

func TestStashSession_Create_RemovesFile(t *testing.T) {
	f := newFixture(t)
	sessionID := "stash-rm"
	content := "will be removed"
	hash := f.putContent(content)
	f.addEvent(sessionID, "created.go", schema.OpCreate, hash, "")

	// Write the file as if the session created it
	filePath := filepath.Join(f.projectDir, "created.go")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, sessionID)
	if err != nil {
		t.Fatalf("StashSession: %v", err)
	}

	// The created file should be removed from the project (stash reverses changes)
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("stash should remove files that were created by the session")
	}
}

func TestStashSession_Modify_RestoresPreviousVersion(t *testing.T) {
	f := newFixture(t)
	sessionID := "stash-mod"
	oldContent := "old version"
	newContent := "new version"
	hashOld := f.putContent(oldContent)
	hashNew := f.putContent(newContent)
	f.addEvent(sessionID, "modified.go", schema.OpModify, hashNew, hashOld)

	// Write the modified file
	filePath := filepath.Join(f.projectDir, "modified.go")
	if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, sessionID)
	if err != nil {
		t.Fatalf("StashSession: %v", err)
	}

	// File should be restored to old content
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != oldContent {
		t.Errorf("file content = %q, want %q (previous version)", data, oldContent)
	}
}

func TestStashSession_Delete_RestoresFile(t *testing.T) {
	f := newFixture(t)
	sessionID := "stash-del"
	content := "was deleted"
	hash := f.putContent(content)
	f.addEvent(sessionID, "deleted.go", schema.OpDelete, "", hash)

	// File has been "deleted" by the session, so it doesn't exist on disk
	filePath := filepath.Join(f.projectDir, "deleted.go")

	_, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, sessionID)
	if err != nil {
		t.Fatalf("StashSession: %v", err)
	}

	// Stash should restore the deleted file
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(data) != content {
		t.Errorf("restored content = %q, want %q", data, content)
	}
}

// ---------------------------------------------------------------------------
// PopStash
// ---------------------------------------------------------------------------

func TestPopStash_RestoresCreatedFiles(t *testing.T) {
	f := newFixture(t)
	sessionID := "pop-create"
	content := "stashed content"
	hash := f.putContent(content)
	f.addEvent(sessionID, "stashed.go", schema.OpCreate, hash, "")

	// Write file, then stash
	filePath := filepath.Join(f.projectDir, "stashed.go")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, sessionID)
	if err != nil {
		t.Fatalf("StashSession: %v", err)
	}

	// File removed by stash
	if _, statErr := os.Stat(filePath); !os.IsNotExist(statErr) {
		t.Fatal("file should be removed after stash")
	}

	// Pop the stash
	if err := PopStash(f.belayDir, sessionID, f.projectDir); err != nil {
		t.Fatalf("PopStash: %v", err)
	}

	// File should be back
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file after pop: %v", err)
	}
	if string(data) != content {
		t.Errorf("content = %q, want %q", data, content)
	}
}

func TestPopStash_RemovesStashDir(t *testing.T) {
	f := newFixture(t)
	sessionID := "pop-cleanup"
	hash := f.putContent("cleanup test")
	f.addEvent(sessionID, "file.go", schema.OpCreate, hash, "")

	filePath := filepath.Join(f.projectDir, "file.go")
	if err := os.WriteFile(filePath, []byte("cleanup test"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	info, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, sessionID)
	if err != nil {
		t.Fatalf("StashSession: %v", err)
	}

	stashDir := info.StashDir
	if _, err := os.Stat(stashDir); os.IsNotExist(err) {
		t.Fatal("stash dir should exist before pop")
	}

	if err := PopStash(f.belayDir, sessionID, f.projectDir); err != nil {
		t.Fatalf("PopStash: %v", err)
	}

	if _, err := os.Stat(stashDir); !os.IsNotExist(err) {
		t.Error("stash dir should be removed after pop")
	}
}

func TestPopStash_NonExistentStash(t *testing.T) {
	f := newFixture(t)
	err := PopStash(f.belayDir, "nonexistent-session", f.projectDir)
	if err == nil {
		t.Fatal("expected error for non-existent stash")
	}
}

func TestPopStash_DeleteEntry_RemovesFile(t *testing.T) {
	f := newFixture(t)
	sessionID := "pop-delete"
	content := "delete target"
	hash := f.putContent(content)
	f.addEvent(sessionID, "will-delete.go", schema.OpDelete, "", hash)

	// Stash will restore the file, pop should delete it again
	_, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, sessionID)
	if err != nil {
		t.Fatalf("StashSession: %v", err)
	}

	// After stash, the deleted file should be restored
	filePath := filepath.Join(f.projectDir, "will-delete.go")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatal("stash should have restored the deleted file")
	}

	// Pop stash re-applies the delete
	if err := PopStash(f.belayDir, sessionID, f.projectDir); err != nil {
		t.Fatalf("PopStash: %v", err)
	}

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file should be deleted after pop of delete stash")
	}
}

// ---------------------------------------------------------------------------
// ListStashes
// ---------------------------------------------------------------------------

func TestListStashes_Empty(t *testing.T) {
	f := newFixture(t)
	stashes, err := ListStashes(f.belayDir)
	if err != nil {
		t.Fatalf("ListStashes: %v", err)
	}
	if len(stashes) != 0 {
		t.Errorf("expected 0 stashes, got %d", len(stashes))
	}
}

func TestListStashes_ReturnsStashes(t *testing.T) {
	f := newFixture(t)

	// Create two stashes
	for _, sessID := range []string{"stash-list-1", "stash-list-2"} {
		hash := f.putContent("content-" + sessID)
		f.addEvent(sessID, sessID+".go", schema.OpCreate, hash, "")
		filePath := filepath.Join(f.projectDir, sessID+".go")
		if err := os.WriteFile(filePath, []byte("content-"+sessID), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if _, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, sessID); err != nil {
			t.Fatalf("StashSession %s: %v", sessID, err)
		}
	}

	stashes, err := ListStashes(f.belayDir)
	if err != nil {
		t.Fatalf("ListStashes: %v", err)
	}

	if len(stashes) != 2 {
		t.Fatalf("expected 2 stashes, got %d", len(stashes))
	}

	// Verify session IDs are present
	sessionIDs := make(map[string]bool)
	for _, s := range stashes {
		sessionIDs[s.SessionID] = true
	}
	for _, want := range []string{"stash-list-1", "stash-list-2"} {
		if !sessionIDs[want] {
			t.Errorf("missing stash for session %q", want)
		}
	}
}

func TestListStashes_SortedByTime(t *testing.T) {
	f := newFixture(t)

	// Create stashes with different times
	sessions := []string{"stash-old", "stash-new"}
	for _, sessID := range sessions {
		hash := f.putContent("content-" + sessID)
		f.addEvent(sessID, sessID+".go", schema.OpCreate, hash, "")
		filePath := filepath.Join(f.projectDir, sessID+".go")
		if err := os.WriteFile(filePath, []byte("content-"+sessID), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if _, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, sessID); err != nil {
			t.Fatalf("StashSession %s: %v", sessID, err)
		}
		// Small delay to ensure different timestamps
		time.Sleep(10 * time.Millisecond)
	}

	stashes, err := ListStashes(f.belayDir)
	if err != nil {
		t.Fatalf("ListStashes: %v", err)
	}

	// Should be sorted newest first
	if len(stashes) >= 2 {
		if stashes[0].CreatedAt.Before(stashes[1].CreatedAt) {
			t.Error("stashes should be sorted newest first")
		}
	}
}

func TestListStashes_SkipsNonDirs(t *testing.T) {
	f := newFixture(t)
	stashesDir := filepath.Join(f.belayDir, "stashes")
	if err := os.MkdirAll(stashesDir, 0755); err != nil {
		t.Fatalf("create stashes dir: %v", err)
	}

	// Create a regular file in the stashes directory (should be skipped)
	if err := os.WriteFile(filepath.Join(stashesDir, "not-a-stash.txt"), []byte("hi"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	stashes, err := ListStashes(f.belayDir)
	if err != nil {
		t.Fatalf("ListStashes: %v", err)
	}
	if len(stashes) != 0 {
		t.Errorf("expected 0 stashes (should skip non-dirs), got %d", len(stashes))
	}
}

func TestListStashes_SkipsInvalidManifests(t *testing.T) {
	f := newFixture(t)
	stashesDir := filepath.Join(f.belayDir, "stashes")
	badStashDir := filepath.Join(stashesDir, "bad-stash")
	if err := os.MkdirAll(badStashDir, 0755); err != nil {
		t.Fatalf("create bad stash dir: %v", err)
	}

	// Create an invalid manifest
	if err := os.WriteFile(filepath.Join(badStashDir, "manifest.json"), []byte("{invalid json"), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	stashes, err := ListStashes(f.belayDir)
	if err != nil {
		t.Fatalf("ListStashes: %v", err)
	}
	if len(stashes) != 0 {
		t.Errorf("expected 0 stashes (should skip invalid manifests), got %d", len(stashes))
	}
}

func TestListStashes_FilesSorted(t *testing.T) {
	f := newFixture(t)
	sessionID := "stash-files-sorted"

	// Create events for files in reverse alpha order
	hashZ := f.putContent("z content")
	hashA := f.putContent("a content")
	hashM := f.putContent("m content")
	f.addEvent(sessionID, "z.go", schema.OpCreate, hashZ, "")
	f.addEvent(sessionID, "a.go", schema.OpCreate, hashA, "")
	f.addEvent(sessionID, "m.go", schema.OpCreate, hashM, "")

	for _, name := range []string{"z.go", "a.go", "m.go"} {
		path := filepath.Join(f.projectDir, name)
		if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	_, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, sessionID)
	if err != nil {
		t.Fatalf("StashSession: %v", err)
	}

	stashes, err := ListStashes(f.belayDir)
	if err != nil {
		t.Fatalf("ListStashes: %v", err)
	}

	if len(stashes) != 1 {
		t.Fatalf("expected 1 stash, got %d", len(stashes))
	}

	files := stashes[0].Files
	if !sort.StringsAreSorted(files) {
		t.Errorf("files should be sorted, got: %v", files)
	}
}

// ---------------------------------------------------------------------------
// findPreviousHash
// ---------------------------------------------------------------------------

func TestFindPreviousHash_Found(t *testing.T) {
	events := []*schema.Event{
		{FilePath: "a.go", PreviousHash: "prev-a"},
		{FilePath: "b.go", PreviousHash: "prev-b"},
	}

	got := findPreviousHash(events, "a.go")
	if got != "prev-a" {
		t.Errorf("findPreviousHash(a.go) = %q, want %q", got, "prev-a")
	}
}

func TestFindPreviousHash_NotFound(t *testing.T) {
	events := []*schema.Event{
		{FilePath: "a.go", PreviousHash: "prev-a"},
	}

	got := findPreviousHash(events, "nonexistent.go")
	if got != "" {
		t.Errorf("findPreviousHash(nonexistent.go) = %q, want empty", got)
	}
}

func TestFindPreviousHash_ReturnsFirstMatch(t *testing.T) {
	events := []*schema.Event{
		{FilePath: "a.go", PreviousHash: "first"},
		{FilePath: "a.go", PreviousHash: "second"},
	}

	got := findPreviousHash(events, "a.go")
	if got != "first" {
		t.Errorf("findPreviousHash should return first match, got %q", got)
	}
}

func TestFindPreviousHash_EmptyEvents(t *testing.T) {
	got := findPreviousHash([]*schema.Event{}, "a.go")
	if got != "" {
		t.Errorf("findPreviousHash on empty events = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// extractCommitHash
// ---------------------------------------------------------------------------

func TestExtractCommitHash_ValidRepo(t *testing.T) {
	f := newFixture(t)
	hash := extractCommitHash(f.projectDir)
	if hash == "" {
		t.Error("expected non-empty commit hash for valid repo")
	}
	// Git SHA-1 hashes are 40 hex characters
	if len(hash) != 40 {
		t.Errorf("expected 40-char hash, got %d chars: %q", len(hash), hash)
	}
}

func TestExtractCommitHash_InvalidDir(t *testing.T) {
	hash := extractCommitHash(t.TempDir())
	if hash != "" {
		t.Errorf("expected empty hash for non-git dir, got %q", hash)
	}
}

// ---------------------------------------------------------------------------
// Stash + Pop roundtrip
// ---------------------------------------------------------------------------

func TestStashPopRoundtrip_Create(t *testing.T) {
	f := newFixture(t)
	sessionID := "roundtrip-create"
	content := "roundtrip content"
	hash := f.putContent(content)
	f.addEvent(sessionID, "roundtrip.go", schema.OpCreate, hash, "")

	filePath := filepath.Join(f.projectDir, "roundtrip.go")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Stash removes the created file
	_, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, sessionID)
	if err != nil {
		t.Fatalf("StashSession: %v", err)
	}
	if _, statErr := os.Stat(filePath); !os.IsNotExist(statErr) {
		t.Fatal("file should be removed after stash")
	}

	// Pop restores it
	if err := PopStash(f.belayDir, sessionID, f.projectDir); err != nil {
		t.Fatalf("PopStash: %v", err)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(data) != content {
		t.Errorf("content after pop = %q, want %q", data, content)
	}
}

func TestStashPopRoundtrip_Modify(t *testing.T) {
	f := newFixture(t)
	sessionID := "roundtrip-modify"
	oldContent := "old version"
	newContent := "new version"
	hashOld := f.putContent(oldContent)
	hashNew := f.putContent(newContent)
	f.addEvent(sessionID, "modified.go", schema.OpModify, hashNew, hashOld)

	filePath := filepath.Join(f.projectDir, "modified.go")
	if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Stash restores old content
	_, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, sessionID)
	if err != nil {
		t.Fatalf("StashSession: %v", err)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read after stash: %v", err)
	}
	if string(data) != oldContent {
		t.Errorf("after stash = %q, want %q", data, oldContent)
	}

	// Pop re-applies the modification
	if err := PopStash(f.belayDir, sessionID, f.projectDir); err != nil {
		t.Fatalf("PopStash: %v", err)
	}
	data, err = os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read after pop: %v", err)
	}
	if string(data) != newContent {
		t.Errorf("after pop = %q, want %q", data, newContent)
	}
}

// ---------------------------------------------------------------------------
// PopStash — nested directory creation
// ---------------------------------------------------------------------------

func TestPopStash_CreatesNestedDirs(t *testing.T) {
	f := newFixture(t)
	sessionID := "pop-nested"
	content := "nested content"
	hash := f.putContent(content)
	f.addEvent(sessionID, "a/b/c/nested.go", schema.OpCreate, hash, "")

	filePath := filepath.Join(f.projectDir, "a", "b", "c", "nested.go")
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		t.Fatalf("create dirs: %v", err)
	}
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := StashSession(f.idx, f.objStore, f.projectDir, f.belayDir, sessionID)
	if err != nil {
		t.Fatalf("StashSession: %v", err)
	}

	// Remove the directories entirely
	os.RemoveAll(filepath.Join(f.projectDir, "a"))

	// Pop should recreate nested dirs
	if err := PopStash(f.belayDir, sessionID, f.projectDir); err != nil {
		t.Fatalf("PopStash: %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read nested file: %v", err)
	}
	if string(data) != content {
		t.Errorf("content = %q, want %q", data, content)
	}
}

// ---------------------------------------------------------------------------
// ImportHistory
// ---------------------------------------------------------------------------

func TestImportHistory_EmptyProjectRoot(t *testing.T) {
	f := newFixture(t)
	_, err := ImportHistory(f.idx, f.objStore, ImportOptions{})
	if err == nil {
		t.Fatal("expected error for empty project root")
	}
	if !strings.Contains(err.Error(), "project root is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestImportHistory_NotGitRepo(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	_, err := ImportHistory(f.idx, f.objStore, ImportOptions{
		ProjectRoot: dir,
	})
	if err == nil {
		t.Fatal("expected error for non-git repo")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestImportHistory_EmptyRepo(t *testing.T) {
	// A repo with no commits matching --since in the future
	f := newFixture(t)
	result, err := ImportHistory(f.idx, f.objStore, ImportOptions{
		ProjectRoot: f.projectDir,
		Since:       "2099-01-01",
	})
	if err != nil {
		t.Fatalf("ImportHistory: %v", err)
	}
	if result.CommitsImported != 0 {
		t.Errorf("CommitsImported = %d, want 0", result.CommitsImported)
	}
	if result.EventsCreated != 0 {
		t.Errorf("EventsCreated = %d, want 0", result.EventsCreated)
	}
}

func TestImportHistory_ImportsCommits(t *testing.T) {
	f := newFixture(t)

	// Create a few commits to import
	// Commit 1: add file
	filePath := filepath.Join(f.projectDir, "imported.go")
	if err := os.WriteFile(filePath, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	gitCmd(f.projectDir, "add", "imported.go")
	gitCmd(f.projectDir, "commit", "-m", "add imported.go")

	// Commit 2: modify file
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("modify file: %v", err)
	}
	gitCmd(f.projectDir, "add", "imported.go")
	gitCmd(f.projectDir, "commit", "-m", "update imported.go")

	// Commit 3: add another file
	file2Path := filepath.Join(f.projectDir, "helper.go")
	if err := os.WriteFile(file2Path, []byte("package main\n\nfunc helper() {}\n"), 0644); err != nil {
		t.Fatalf("write file2: %v", err)
	}
	gitCmd(f.projectDir, "add", "helper.go")
	gitCmd(f.projectDir, "commit", "-m", "add helper.go")

	// Use a fresh index for import
	importDBPath := filepath.Join(f.tmpDir, "import-index.db")
	importIdx, err := index.Open(importDBPath)
	if err != nil {
		t.Fatalf("open import index: %v", err)
	}
	defer importIdx.Close()

	importObjDir := filepath.Join(f.tmpDir, "import-objects")
	importStore, err := store.NewStore(importObjDir, false)
	if err != nil {
		t.Fatalf("create import store: %v", err)
	}
	defer importStore.Close()

	result, err := ImportHistory(importIdx, importStore, ImportOptions{
		ProjectRoot: f.projectDir,
	})
	if err != nil {
		t.Fatalf("ImportHistory: %v", err)
	}

	// We have initial commit (README.md create) + 3 more commits
	// The initial commit adds README.md, then we add imported.go, modify it, and add helper.go
	if result.CommitsImported < 3 {
		t.Errorf("CommitsImported = %d, want >= 3", result.CommitsImported)
	}
	if result.EventsCreated < 3 {
		t.Errorf("EventsCreated = %d, want >= 3", result.EventsCreated)
	}

	// Verify events were indexed
	events, err := importIdx.QueryEvents(&index.Query{
		Sessions: []string{"git-import"},
	})
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if len(events) < 3 {
		t.Errorf("expected >= 3 indexed events, got %d", len(events))
	}

	// Verify events have metadata
	for _, e := range events {
		if e.SessionID != "git-import" {
			t.Errorf("event session = %q, want 'git-import'", e.SessionID)
		}
		if e.Metadata == nil {
			t.Error("event metadata should not be nil")
			continue
		}
		if e.Metadata["source"] != "git-import" {
			t.Errorf("event source = %q, want 'git-import'", e.Metadata["source"])
		}
		if e.Metadata["git_commit"] == "" {
			t.Error("event should have git_commit metadata")
		}
	}
}

func TestImportHistory_WithSinceFilter(t *testing.T) {
	f := newFixture(t)

	// Create a commit
	filePath := filepath.Join(f.projectDir, "recent.go")
	if err := os.WriteFile(filePath, []byte("recent\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	gitCmd(f.projectDir, "add", "recent.go")
	gitCmd(f.projectDir, "commit", "-m", "add recent.go")

	importDBPath := filepath.Join(f.tmpDir, "since-index.db")
	importIdx, err := index.Open(importDBPath)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer importIdx.Close()

	importObjDir := filepath.Join(f.tmpDir, "since-objects")
	importStore, err := store.NewStore(importObjDir, false)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer importStore.Close()

	// Use Since with a very old date to include all commits
	result, err := ImportHistory(importIdx, importStore, ImportOptions{
		ProjectRoot: f.projectDir,
		Since:       "2000-01-01",
	})
	if err != nil {
		t.Fatalf("ImportHistory: %v", err)
	}

	if result.CommitsImported == 0 {
		t.Error("expected some commits to be imported with old Since date")
	}
}

func TestImportHistory_DeletedFile(t *testing.T) {
	f := newFixture(t)

	// Create and commit a file
	filePath := filepath.Join(f.projectDir, "to-remove.go")
	if err := os.WriteFile(filePath, []byte("remove me\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	gitCmd(f.projectDir, "add", "to-remove.go")
	gitCmd(f.projectDir, "commit", "-m", "add to-remove.go")

	// Delete and commit
	os.Remove(filePath)
	gitCmd(f.projectDir, "add", "to-remove.go")
	gitCmd(f.projectDir, "commit", "-m", "remove to-remove.go")

	importDBPath := filepath.Join(f.tmpDir, "del-index.db")
	importIdx, err := index.Open(importDBPath)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer importIdx.Close()

	importObjDir := filepath.Join(f.tmpDir, "del-objects")
	importStore, err := store.NewStore(importObjDir, false)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer importStore.Close()

	result, err := ImportHistory(importIdx, importStore, ImportOptions{
		ProjectRoot: f.projectDir,
	})
	if err != nil {
		t.Fatalf("ImportHistory: %v", err)
	}

	// Should have imported commits with both ADD and DELETE operations
	if result.EventsCreated < 2 {
		t.Errorf("EventsCreated = %d, want >= 2 (add + delete)", result.EventsCreated)
	}

	// Check that we have a DELETE event
	events, err := importIdx.QueryEvents(&index.Query{
		Sessions:   []string{"git-import"},
		Operations: []string{"DELETE"},
	})
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if len(events) == 0 {
		t.Error("expected at least one DELETE event from import")
	}
}

func TestImportHistory_RenamedFile(t *testing.T) {
	f := newFixture(t)

	// Create a file
	filePath := filepath.Join(f.projectDir, "old-name.go")
	if err := os.WriteFile(filePath, []byte("renamed content\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	gitCmd(f.projectDir, "add", "old-name.go")
	gitCmd(f.projectDir, "commit", "-m", "add old-name.go")

	// Rename via git mv
	gitCmd(f.projectDir, "mv", "old-name.go", "new-name.go")
	gitCmd(f.projectDir, "commit", "-m", "rename old-name.go to new-name.go")

	importDBPath := filepath.Join(f.tmpDir, "rename-index.db")
	importIdx, err := index.Open(importDBPath)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer importIdx.Close()

	importObjDir := filepath.Join(f.tmpDir, "rename-objects")
	importStore, err := store.NewStore(importObjDir, false)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer importStore.Close()

	result, err := ImportHistory(importIdx, importStore, ImportOptions{
		ProjectRoot: f.projectDir,
	})
	if err != nil {
		t.Fatalf("ImportHistory: %v", err)
	}

	if result.CommitsImported < 2 {
		t.Errorf("CommitsImported = %d, want >= 2", result.CommitsImported)
	}
}

func TestImportHistory_ContentStoredInObjectStore(t *testing.T) {
	f := newFixture(t)

	content := "storable content\n"
	filePath := filepath.Join(f.projectDir, "stored.go")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	gitCmd(f.projectDir, "add", "stored.go")
	gitCmd(f.projectDir, "commit", "-m", "add stored.go")

	importDBPath := filepath.Join(f.tmpDir, "content-index.db")
	importIdx, err := index.Open(importDBPath)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer importIdx.Close()

	importObjDir := filepath.Join(f.tmpDir, "content-objects")
	importStore, err := store.NewStore(importObjDir, false)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer importStore.Close()

	_, err = ImportHistory(importIdx, importStore, ImportOptions{
		ProjectRoot: f.projectDir,
	})
	if err != nil {
		t.Fatalf("ImportHistory: %v", err)
	}

	// Find the CREATE event for stored.go
	events, err := importIdx.QueryEvents(&index.Query{
		Sessions:   []string{"git-import"},
		FilePaths:  []string{"stored.go"},
		Operations: []string{"CREATE"},
	})
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected CREATE event for stored.go")
	}

	// Verify content was stored and is retrievable
	ev := events[0]
	if ev.ContentHash == "" {
		t.Fatal("event should have a content hash")
	}

	data, err := importStore.Get(ev.ContentHash)
	if err != nil {
		t.Fatalf("get stored content: %v", err)
	}
	// gitCmd calls strings.TrimSpace on output, so stored content may have trailing newline stripped
	if !strings.Contains(content, string(data)) {
		t.Errorf("stored content = %q, want it to match %q", data, content)
	}
}

func TestImportHistory_ModifyHasPreviousHash(t *testing.T) {
	f := newFixture(t)

	// Create then modify a file
	filePath := filepath.Join(f.projectDir, "versioned.go")
	if err := os.WriteFile(filePath, []byte("version 1\n"), 0644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	gitCmd(f.projectDir, "add", "versioned.go")
	gitCmd(f.projectDir, "commit", "-m", "add versioned.go v1")

	if err := os.WriteFile(filePath, []byte("version 2\n"), 0644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	gitCmd(f.projectDir, "add", "versioned.go")
	gitCmd(f.projectDir, "commit", "-m", "update versioned.go to v2")

	importDBPath := filepath.Join(f.tmpDir, "prev-index.db")
	importIdx, err := index.Open(importDBPath)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer importIdx.Close()

	importObjDir := filepath.Join(f.tmpDir, "prev-objects")
	importStore, err := store.NewStore(importObjDir, false)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer importStore.Close()

	_, err = ImportHistory(importIdx, importStore, ImportOptions{
		ProjectRoot: f.projectDir,
	})
	if err != nil {
		t.Fatalf("ImportHistory: %v", err)
	}

	// Find the MODIFY event for versioned.go
	events, err := importIdx.QueryEvents(&index.Query{
		Sessions:   []string{"git-import"},
		FilePaths:  []string{"versioned.go"},
		Operations: []string{"MODIFY"},
	})
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected MODIFY event for versioned.go")
	}

	ev := events[0]
	if ev.ContentHash == "" {
		t.Error("MODIFY event should have content hash")
	}
	if ev.PreviousHash == "" {
		t.Error("MODIFY event should have previous hash")
	}
	if ev.ContentHash == ev.PreviousHash {
		t.Error("content hash and previous hash should differ")
	}

	// Verify both versions are in the store
	// Note: gitCmd trims trailing whitespace, so stored content may not have trailing newline
	v2Data, err := importStore.Get(ev.ContentHash)
	if err != nil {
		t.Fatalf("get v2: %v", err)
	}
	if !strings.HasPrefix(string(v2Data), "version 2") {
		t.Errorf("v2 = %q, want to start with 'version 2'", v2Data)
	}

	v1Data, err := importStore.Get(ev.PreviousHash)
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	if !strings.HasPrefix(string(v1Data), "version 1") {
		t.Errorf("v1 = %q, want to start with 'version 1'", v1Data)
	}
}
