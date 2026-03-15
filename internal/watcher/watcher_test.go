package watcher

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/ignore"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"
)

// ─── Test Helpers ────────────────────────────────────────────────────────────

// newTestBase creates a watcherBase backed by real Config, Store, and Matcher
// instances rooted in a temp directory. The debounce is set to the given duration.
func newTestBase(t *testing.T, debounce time.Duration) *watcherBase {
	t.Helper()

	projectRoot := t.TempDir()

	// Create .belay/objects so the Store can be initialised.
	objectsDir := filepath.Join(projectRoot, ".belay", "objects")
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		t.Fatalf("create objects dir: %v", err)
	}

	cfg := config.DefaultConfig(projectRoot)
	cfg.Watcher.DebounceMs = int(debounce / time.Millisecond)
	if cfg.Watcher.DebounceMs == 0 {
		cfg.Watcher.DebounceMs = 1 // avoid zero-duration ticker
	}

	objStore, err := store.NewStore(objectsDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { objStore.Close() })

	matcher, err := ignore.NewMatcher(projectRoot)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	base := &watcherBase{}
	initBase(base, cfg, objStore, matcher)
	base.debounceMs = debounce
	return base
}

// writeTestFile creates a file under projectRoot with the given relative path
// and content, creating intermediate directories as needed.
func writeTestFile(t *testing.T, projectRoot, relPath, content string) string {
	t.Helper()
	abs := filepath.Join(projectRoot, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatalf("create dir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
	return abs
}

// collectEvents registers a handler on base and returns a function that
// retrieves all events received so far.
func collectEvents(base *watcherBase) func() []*schema.Event {
	var mu sync.Mutex
	var events []*schema.Event
	base.OnEvent(func(e *schema.Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})
	return func() []*schema.Event {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]*schema.Event, len(events))
		copy(cp, events)
		return cp
	}
}

// ─── isHidden() ──────────────────────────────────────────────────────────────

func TestIsHidden_DotPrefixedFile(t *testing.T) {
	tests := []struct {
		path   string
		hidden bool
	}{
		{".gitignore", true},
		{".env", true},
		{".hidden/file.txt", true},
		{"src/.secret", true},
		{"a/b/.c/d.txt", true},
		{"src/main.go", false},
		{"README.md", false},
		{"a/b/c.txt", false},
		{".", false},     // current dir is not hidden
		{"..", false},    // parent dir is not hidden
		{"./foo", false}, // leading ./ should not flag hidden
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isHidden(tt.path)
			if got != tt.hidden {
				t.Errorf("isHidden(%q) = %v, want %v", tt.path, got, tt.hidden)
			}
		})
	}
}

func TestIsHidden_NestedDotDirs(t *testing.T) {
	if !isHidden("a/.b/c/d.txt") {
		t.Error("expected path with hidden intermediate dir to be hidden")
	}
}

func TestIsHidden_NonDotPrefix(t *testing.T) {
	if isHidden("dotfile.txt") {
		t.Error("dotfile.txt should not be hidden")
	}
}

// ─── shouldIgnoreRel() ───────────────────────────────────────────────────────

func TestShouldIgnoreRel_HiddenFiles(t *testing.T) {
	// With ExcludeHidden=true, hidden files should be ignored
	base := newTestBase(t, 10*time.Millisecond)
	base.cfg.Watcher.ExcludeHidden = true

	tests := []struct {
		path   string
		ignore bool
	}{
		{".git/config", true},  // matched by default .git/ pattern AND hidden
		{".env", true},         // hidden file
		{".belay/index.db", true}, // matched by default .belay/ pattern AND hidden
		{"src/main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := base.shouldIgnoreRel(tt.path)
			if got != tt.ignore {
				t.Errorf("shouldIgnoreRel(%q) = %v, want %v", tt.path, got, tt.ignore)
			}
		})
	}
}

func TestShouldIgnoreRel_HiddenFilesAllowed(t *testing.T) {
	// With ExcludeHidden=false (default), hidden files are NOT ignored
	// unless they match a .belayignore pattern (like .git/ or .belay/)
	base := newTestBase(t, 10*time.Millisecond)
	base.cfg.Watcher.ExcludeHidden = false

	tests := []struct {
		path   string
		ignore bool
	}{
		{".git/config", true},     // matched by default .git/ pattern
		{".env", false},           // hidden but ExcludeHidden is false, no pattern match
		{".belay/index.db", true}, // matched by default .belay/ pattern
		{"src/main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := base.shouldIgnoreRel(tt.path)
			if got != tt.ignore {
				t.Errorf("shouldIgnoreRel(%q) = %v, want %v", tt.path, got, tt.ignore)
			}
		})
	}
}

func TestShouldIgnoreRel_MatcherPatterns(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)
	// The default matcher ignores node_modules/, .git/, build/, etc.

	tests := []struct {
		path   string
		ignore bool
	}{
		{"node_modules/react/index.js", true},
		{"build/output.js", true},
		{"dist/bundle.js", true},
		{"__pycache__/mod.pyc", true},
		{"src/app.tsx", false},
		{"cmd/main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := base.shouldIgnoreRel(tt.path)
			if got != tt.ignore {
				t.Errorf("shouldIgnoreRel(%q) = %v, want %v", tt.path, got, tt.ignore)
			}
		})
	}
}

func TestShouldIgnoreRel_CustomIgnoreFile(t *testing.T) {
	projectRoot := t.TempDir()

	// Write a custom .belayignore
	ignoreContent := "*.log\ntmp/\n"
	if err := os.WriteFile(filepath.Join(projectRoot, ".belayignore"), []byte(ignoreContent), 0644); err != nil {
		t.Fatalf("write .belayignore: %v", err)
	}

	objectsDir := filepath.Join(projectRoot, ".belay", "objects")
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		t.Fatalf("create objects dir: %v", err)
	}

	cfg := config.DefaultConfig(projectRoot)
	cfg.Watcher.DebounceMs = 10

	objStore, err := store.NewStore(objectsDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { objStore.Close() })

	matcher, err := ignore.NewMatcher(projectRoot)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	base := &watcherBase{}
	initBase(base, cfg, objStore, matcher)
	base.debounceMs = 10 * time.Millisecond

	if !base.shouldIgnoreRel("app.log") {
		t.Error("expected app.log to be ignored by custom pattern")
	}
	if !base.shouldIgnoreRel("tmp/cache.dat") {
		t.Error("expected tmp/cache.dat to be ignored by custom pattern")
	}
	if base.shouldIgnoreRel("src/main.go") {
		t.Error("src/main.go should not be ignored")
	}
}

// ─── OnEvent() ───────────────────────────────────────────────────────────────

func TestOnEvent_RegistersHandler(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	called := false
	base.OnEvent(func(e *schema.Event) {
		called = true
	})

	if len(base.handlers) != 1 {
		t.Fatalf("expected 1 handler, got %d", len(base.handlers))
	}

	// Invoke handler directly to verify it's wired up.
	base.handlers[0](&schema.Event{})
	if !called {
		t.Error("handler was not called")
	}
}

func TestOnEvent_MultipleHandlers(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	counts := make([]int, 3)
	for i := 0; i < 3; i++ {
		idx := i
		base.OnEvent(func(e *schema.Event) {
			counts[idx]++
		})
	}

	if len(base.handlers) != 3 {
		t.Fatalf("expected 3 handlers, got %d", len(base.handlers))
	}

	// Simulate dispatching an event to all handlers.
	base.mu.RLock()
	for _, h := range base.handlers {
		h(&schema.Event{})
	}
	base.mu.RUnlock()

	for i, c := range counts {
		if c != 1 {
			t.Errorf("handler %d called %d times, want 1", i, c)
		}
	}
}

// ─── queueEvent() ────────────────────────────────────────────────────────────

func TestQueueEvent_AddsToPending(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	base.queueEvent("src/main.go", schema.OpCreate)

	base.pendingMu.Lock()
	pe, ok := base.pending["src/main.go"]
	base.pendingMu.Unlock()

	if !ok {
		t.Fatal("expected event in pending map")
	}
	if pe.op != schema.OpCreate {
		t.Errorf("op = %v, want OpCreate", pe.op)
	}
	if pe.path != "src/main.go" {
		t.Errorf("path = %q, want %q", pe.path, "src/main.go")
	}
}

func TestQueueEvent_OverwritesPreviousEvent(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	base.queueEvent("src/main.go", schema.OpCreate)
	time.Sleep(1 * time.Millisecond)
	base.queueEvent("src/main.go", schema.OpModify)

	base.pendingMu.Lock()
	pe := base.pending["src/main.go"]
	count := len(base.pending)
	base.pendingMu.Unlock()

	if count != 1 {
		t.Errorf("pending map has %d entries, want 1", count)
	}
	if pe.op != schema.OpModify {
		t.Errorf("op = %v, want OpModify (latest)", pe.op)
	}
}

func TestQueueEvent_MultipleDifferentPaths(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	base.queueEvent("a.go", schema.OpCreate)
	base.queueEvent("b.go", schema.OpModify)
	base.queueEvent("c.go", schema.OpDelete)

	base.pendingMu.Lock()
	count := len(base.pending)
	base.pendingMu.Unlock()

	if count != 3 {
		t.Errorf("pending map has %d entries, want 3", count)
	}
}

// ─── flushPending() ──────────────────────────────────────────────────────────

func TestFlushPending_FlushesReadyEvents(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)
	getEvents := collectEvents(base)

	// Create the file so captureContent can read it.
	writeTestFile(t, base.cfg.ProjectRoot, "hello.txt", "hello world")

	// Queue event with an old-enough timestamp.
	base.pendingMu.Lock()
	base.pending["hello.txt"] = &pendingEvent{
		path:      "hello.txt",
		op:        schema.OpCreate,
		timestamp: time.Now().Add(-50 * time.Millisecond), // well past debounce
	}
	base.pendingMu.Unlock()

	base.flushPending()

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 flushed event, got %d", len(events))
	}
	if events[0].FilePath != "hello.txt" {
		t.Errorf("FilePath = %q, want %q", events[0].FilePath, "hello.txt")
	}
	if events[0].Op != schema.OpCreate {
		t.Errorf("Op = %v, want OpCreate", events[0].Op)
	}

	// Pending map should now be empty.
	base.pendingMu.Lock()
	remaining := len(base.pending)
	base.pendingMu.Unlock()
	if remaining != 0 {
		t.Errorf("pending map has %d entries after flush, want 0", remaining)
	}
}

func TestFlushPending_KeepsRecentEvents(t *testing.T) {
	base := newTestBase(t, 100*time.Millisecond)
	getEvents := collectEvents(base)

	writeTestFile(t, base.cfg.ProjectRoot, "recent.txt", "content")

	// Queue event that is NOT old enough to flush.
	base.pendingMu.Lock()
	base.pending["recent.txt"] = &pendingEvent{
		path:      "recent.txt",
		op:        schema.OpModify,
		timestamp: time.Now(), // just queued
	}
	base.pendingMu.Unlock()

	base.flushPending()

	events := getEvents()
	if len(events) != 0 {
		t.Errorf("expected 0 flushed events (too recent), got %d", len(events))
	}

	base.pendingMu.Lock()
	remaining := len(base.pending)
	base.pendingMu.Unlock()
	if remaining != 1 {
		t.Errorf("pending map should still have 1 entry, got %d", remaining)
	}
}

func TestFlushPending_MixedReadyAndNotReady(t *testing.T) {
	base := newTestBase(t, 20*time.Millisecond)
	getEvents := collectEvents(base)

	writeTestFile(t, base.cfg.ProjectRoot, "old.txt", "old content")
	writeTestFile(t, base.cfg.ProjectRoot, "new.txt", "new content")

	base.pendingMu.Lock()
	base.pending["old.txt"] = &pendingEvent{
		path:      "old.txt",
		op:        schema.OpModify,
		timestamp: time.Now().Add(-100 * time.Millisecond),
	}
	base.pending["new.txt"] = &pendingEvent{
		path:      "new.txt",
		op:        schema.OpCreate,
		timestamp: time.Now(),
	}
	base.pendingMu.Unlock()

	base.flushPending()

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 flushed event (only old), got %d", len(events))
	}
	if events[0].FilePath != "old.txt" {
		t.Errorf("flushed FilePath = %q, want %q", events[0].FilePath, "old.txt")
	}

	base.pendingMu.Lock()
	remaining := len(base.pending)
	base.pendingMu.Unlock()
	if remaining != 1 {
		t.Errorf("pending should have 1 remaining entry, got %d", remaining)
	}
}

func TestFlushPending_EmptyMap(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)
	getEvents := collectEvents(base)

	// Flush with nothing pending should be a no-op.
	base.flushPending()

	events := getEvents()
	if len(events) != 0 {
		t.Errorf("expected 0 events from empty flush, got %d", len(events))
	}
}

// ─── processFileEvent() ─────────────────────────────────────────────────────

func TestProcessFileEvent_Create(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)
	getEvents := collectEvents(base)

	writeTestFile(t, base.cfg.ProjectRoot, "new.go", "package main")

	ts := time.Now()
	pe := &pendingEvent{
		path:      "new.go",
		op:        schema.OpCreate,
		timestamp: ts,
	}
	base.processFileEvent(pe)

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.FilePath != "new.go" {
		t.Errorf("FilePath = %q, want %q", ev.FilePath, "new.go")
	}
	if ev.Op != schema.OpCreate {
		t.Errorf("Op = %v, want OpCreate", ev.Op)
	}
	if ev.ContentHash == "" {
		t.Error("ContentHash should be set for create")
	}
	if ev.ContentSize != int64(len("package main")) {
		t.Errorf("ContentSize = %d, want %d", ev.ContentSize, len("package main"))
	}
	if ev.EventID == "" {
		t.Error("EventID should be set")
	}
	if ev.Version != schema.SchemaVersion {
		t.Errorf("Version = %d, want %d", ev.Version, schema.SchemaVersion)
	}
}

func TestProcessFileEvent_Modify(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)
	getEvents := collectEvents(base)

	writeTestFile(t, base.cfg.ProjectRoot, "app.go", "modified content")

	pe := &pendingEvent{
		path:      "app.go",
		op:        schema.OpModify,
		timestamp: time.Now(),
	}
	base.processFileEvent(pe)

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Op != schema.OpModify {
		t.Errorf("Op = %v, want OpModify", events[0].Op)
	}
	if events[0].ContentHash == "" {
		t.Error("ContentHash should be set for modify")
	}
}

func TestProcessFileEvent_Delete(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)
	getEvents := collectEvents(base)

	// Delete events do NOT call captureContent, so the file need not exist.
	pe := &pendingEvent{
		path:      "removed.go",
		op:        schema.OpDelete,
		timestamp: time.Now(),
	}
	base.processFileEvent(pe)

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Op != schema.OpDelete {
		t.Errorf("Op = %v, want OpDelete", ev.Op)
	}
	if ev.ContentHash != "" {
		t.Errorf("ContentHash should be empty for delete, got %q", ev.ContentHash)
	}
	if ev.ContentSize != 0 {
		t.Errorf("ContentSize should be 0 for delete, got %d", ev.ContentSize)
	}
}

func TestProcessFileEvent_FileNotFound(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)
	getEvents := collectEvents(base)

	// For create/modify, if the file is missing captureContent fails and no
	// event should be dispatched.
	pe := &pendingEvent{
		path:      "nonexistent.go",
		op:        schema.OpCreate,
		timestamp: time.Now(),
	}
	base.processFileEvent(pe)

	events := getEvents()
	if len(events) != 0 {
		t.Errorf("expected 0 events when file is missing, got %d", len(events))
	}
}

func TestProcessFileEvent_DispatchesToAllHandlers(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	writeTestFile(t, base.cfg.ProjectRoot, "multi.go", "package multi")

	var mu sync.Mutex
	handlerCalls := 0
	for i := 0; i < 5; i++ {
		base.OnEvent(func(e *schema.Event) {
			mu.Lock()
			handlerCalls++
			mu.Unlock()
		})
	}

	pe := &pendingEvent{path: "multi.go", op: schema.OpCreate, timestamp: time.Now()}
	base.processFileEvent(pe)

	mu.Lock()
	calls := handlerCalls
	mu.Unlock()

	if calls != 5 {
		t.Errorf("expected 5 handler calls, got %d", calls)
	}
}

// ─── captureContent() ───────────────────────────────────────────────────────

func TestCaptureContent_SetsHashAndSize(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	content := "hello belay watcher"
	absPath := writeTestFile(t, base.cfg.ProjectRoot, "capture.txt", content)

	event := &schema.Event{}
	if err := base.captureContent(absPath, event); err != nil {
		t.Fatalf("captureContent: %v", err)
	}

	if event.ContentHash == "" {
		t.Error("ContentHash should be set")
	}
	if event.ContentSize != int64(len(content)) {
		t.Errorf("ContentSize = %d, want %d", event.ContentSize, len(content))
	}

	// Verify the content was stored and is retrievable.
	data, err := base.objStore.Get(event.ContentHash)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if string(data) != content {
		t.Errorf("stored content = %q, want %q", string(data), content)
	}
}

func TestCaptureContent_EmptyFile(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	absPath := writeTestFile(t, base.cfg.ProjectRoot, "empty.txt", "")

	event := &schema.Event{}
	if err := base.captureContent(absPath, event); err != nil {
		t.Fatalf("captureContent empty file: %v", err)
	}

	if event.ContentHash == "" {
		t.Error("ContentHash should be set even for empty file")
	}
	if event.ContentSize != 0 {
		t.Errorf("ContentSize = %d, want 0", event.ContentSize)
	}
}

func TestCaptureContent_MissingFile(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	event := &schema.Event{}
	err := base.captureContent("/nonexistent/path/file.go", event)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestCaptureContent_SameContentSameHash(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	content := "identical content"
	abs1 := writeTestFile(t, base.cfg.ProjectRoot, "file1.txt", content)
	abs2 := writeTestFile(t, base.cfg.ProjectRoot, "file2.txt", content)

	ev1 := &schema.Event{}
	ev2 := &schema.Event{}

	if err := base.captureContent(abs1, ev1); err != nil {
		t.Fatalf("captureContent file1: %v", err)
	}
	if err := base.captureContent(abs2, ev2); err != nil {
		t.Fatalf("captureContent file2: %v", err)
	}

	if ev1.ContentHash != ev2.ContentHash {
		t.Errorf("same content produced different hashes: %q vs %q", ev1.ContentHash, ev2.ContentHash)
	}
}

func TestCaptureContent_LargeFileSkipsContent(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	// Set a very small limit (1 MB) for testing.
	base.cfg.Watcher.MaxFileSizeMB = 1

	// Create a file larger than 1 MB.
	largeContent := make([]byte, 2*1024*1024) // 2 MB
	for i := range largeContent {
		largeContent[i] = 'x'
	}
	absPath := filepath.Join(base.cfg.ProjectRoot, "large.bin")
	if err := os.WriteFile(absPath, largeContent, 0644); err != nil {
		t.Fatalf("write large file: %v", err)
	}

	event := &schema.Event{FilePath: "large.bin"}
	if err := base.captureContent(absPath, event); err != nil {
		t.Fatalf("captureContent should not error for large file: %v", err)
	}

	// Content should NOT be captured.
	if event.ContentHash != "" {
		t.Errorf("ContentHash should be empty for oversized file, got %q", event.ContentHash)
	}
	if event.ContentSize != 0 {
		t.Errorf("ContentSize should be 0 for oversized file, got %d", event.ContentSize)
	}
}

func TestCaptureContent_FileUnderLimitCapturedNormally(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	// Set a 1 MB limit.
	base.cfg.Watcher.MaxFileSizeMB = 1

	// Create a small file well under the limit.
	content := "small file content"
	absPath := writeTestFile(t, base.cfg.ProjectRoot, "small.txt", content)

	event := &schema.Event{FilePath: "small.txt"}
	if err := base.captureContent(absPath, event); err != nil {
		t.Fatalf("captureContent: %v", err)
	}

	if event.ContentHash == "" {
		t.Error("ContentHash should be set for file under limit")
	}
	if event.ContentSize != int64(len(content)) {
		t.Errorf("ContentSize = %d, want %d", event.ContentSize, len(content))
	}
}

func TestCaptureContent_LargeFileStillEmitsEvent(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)
	getEvents := collectEvents(base)

	// Set a very small limit.
	base.cfg.Watcher.MaxFileSizeMB = 1

	// Create a file larger than 1 MB.
	largeContent := make([]byte, 2*1024*1024) // 2 MB
	for i := range largeContent {
		largeContent[i] = 'y'
	}
	absPath := filepath.Join(base.cfg.ProjectRoot, "big.dat")
	if err := os.WriteFile(absPath, largeContent, 0644); err != nil {
		t.Fatalf("write large file: %v", err)
	}

	// Process a file event — the event should still be emitted.
	pe := &pendingEvent{
		path:      "big.dat",
		op:        schema.OpCreate,
		timestamp: time.Now(),
	}
	base.processFileEvent(pe)

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event for large file, got %d", len(events))
	}

	ev := events[0]
	if ev.FilePath != "big.dat" {
		t.Errorf("FilePath = %q, want %q", ev.FilePath, "big.dat")
	}
	if ev.Op != schema.OpCreate {
		t.Errorf("Op = %v, want OpCreate", ev.Op)
	}
	// Content should not be captured.
	if ev.ContentHash != "" {
		t.Errorf("ContentHash should be empty for oversized file, got %q", ev.ContentHash)
	}
	if ev.ContentSize != 0 {
		t.Errorf("ContentSize should be 0 for oversized file, got %d", ev.ContentSize)
	}
}

func TestCaptureContent_FileExactlyAtLimitCaptured(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	// Set a 1 MB limit.
	base.cfg.Watcher.MaxFileSizeMB = 1

	// Create a file exactly at the limit (1 MB).
	exactContent := make([]byte, 1*1024*1024)
	for i := range exactContent {
		exactContent[i] = 'z'
	}
	absPath := filepath.Join(base.cfg.ProjectRoot, "exact.bin")
	if err := os.WriteFile(absPath, exactContent, 0644); err != nil {
		t.Fatalf("write exact file: %v", err)
	}

	event := &schema.Event{FilePath: "exact.bin"}
	if err := base.captureContent(absPath, event); err != nil {
		t.Fatalf("captureContent: %v", err)
	}

	// File at exactly the limit should be captured (only > limit is skipped).
	if event.ContentHash == "" {
		t.Error("ContentHash should be set for file at exactly the limit")
	}
	if event.ContentSize != int64(1*1024*1024) {
		t.Errorf("ContentSize = %d, want %d", event.ContentSize, 1*1024*1024)
	}
}

func TestCaptureContent_ZeroLimitDisablesGuard(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	// Set limit to 0 — should disable the guard entirely.
	base.cfg.Watcher.MaxFileSizeMB = 0

	// Create a large file.
	largeContent := make([]byte, 2*1024*1024)
	for i := range largeContent {
		largeContent[i] = 'w'
	}
	absPath := filepath.Join(base.cfg.ProjectRoot, "nolimit.bin")
	if err := os.WriteFile(absPath, largeContent, 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	event := &schema.Event{FilePath: "nolimit.bin"}
	if err := base.captureContent(absPath, event); err != nil {
		t.Fatalf("captureContent: %v", err)
	}

	// With limit 0, the guard is disabled so content should be captured.
	if event.ContentHash == "" {
		t.Error("ContentHash should be set when MaxFileSizeMB is 0 (guard disabled)")
	}
}

func TestCaptureContent_DifferentContentDifferentHash(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	abs1 := writeTestFile(t, base.cfg.ProjectRoot, "a.txt", "content A")
	abs2 := writeTestFile(t, base.cfg.ProjectRoot, "b.txt", "content B")

	ev1 := &schema.Event{}
	ev2 := &schema.Event{}

	if err := base.captureContent(abs1, ev1); err != nil {
		t.Fatalf("captureContent a.txt: %v", err)
	}
	if err := base.captureContent(abs2, ev2); err != nil {
		t.Fatalf("captureContent b.txt: %v", err)
	}

	if ev1.ContentHash == ev2.ContentHash {
		t.Error("different content should produce different hashes")
	}
}

// ─── processPending() ────────────────────────────────────────────────────────

func TestProcessPending_FlushesAfterDebounce(t *testing.T) {
	base := newTestBase(t, 15*time.Millisecond)
	getEvents := collectEvents(base)

	writeTestFile(t, base.cfg.ProjectRoot, "timed.go", "package timed")

	base.queueEvent("timed.go", schema.OpCreate)

	// Start ticker and processPending loop.
	base.ticker = time.NewTicker(base.debounceMs)
	base.wg.Add(1)
	go base.processPending()

	// Wait long enough for debounce + at least one tick.
	time.Sleep(80 * time.Millisecond)

	// Stop the loop.
	close(base.done)
	base.wg.Wait()
	base.ticker.Stop()

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event after debounce, got %d", len(events))
	}
	if events[0].FilePath != "timed.go" {
		t.Errorf("FilePath = %q, want %q", events[0].FilePath, "timed.go")
	}
}

func TestProcessPending_StopsOnDone(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	base.ticker = time.NewTicker(base.debounceMs)
	base.wg.Add(1)
	go base.processPending()

	// Close immediately.
	close(base.done)
	base.wg.Wait()
	base.ticker.Stop()

	// If processPending didn't exit, the test will hang.
}

// ─── Debounce Behavior ──────────────────────────────────────────────────────

func TestDebounce_RapidWritesCollapse(t *testing.T) {
	base := newTestBase(t, 30*time.Millisecond)
	getEvents := collectEvents(base)

	writeTestFile(t, base.cfg.ProjectRoot, "rapid.go", "final version")

	// Simulate rapid writes: each queueEvent overwrites the pending entry.
	for i := 0; i < 10; i++ {
		base.queueEvent("rapid.go", schema.OpModify)
		time.Sleep(1 * time.Millisecond)
	}

	// Only one entry should be in pending (all writes to the same key).
	base.pendingMu.Lock()
	pendingCount := len(base.pending)
	base.pendingMu.Unlock()

	if pendingCount != 1 {
		t.Errorf("pending map should have 1 entry after rapid writes, got %d", pendingCount)
	}

	// Wait for debounce and flush.
	time.Sleep(50 * time.Millisecond)
	base.flushPending()

	events := getEvents()
	if len(events) != 1 {
		t.Errorf("expected 1 event after debounced rapid writes, got %d", len(events))
	}
}

func TestDebounce_DifferentFilesFlushIndependently(t *testing.T) {
	base := newTestBase(t, 15*time.Millisecond)
	getEvents := collectEvents(base)

	writeTestFile(t, base.cfg.ProjectRoot, "x.go", "x content")
	writeTestFile(t, base.cfg.ProjectRoot, "y.go", "y content")

	base.queueEvent("x.go", schema.OpCreate)
	base.queueEvent("y.go", schema.OpModify)

	// Wait for debounce.
	time.Sleep(30 * time.Millisecond)
	base.flushPending()

	events := getEvents()
	if len(events) != 2 {
		t.Errorf("expected 2 events (one per file), got %d", len(events))
	}
}

func TestDebounce_LatestOpWins(t *testing.T) {
	base := newTestBase(t, 15*time.Millisecond)

	// Create then modify the same file in quick succession.
	base.queueEvent("flip.go", schema.OpCreate)
	base.queueEvent("flip.go", schema.OpModify)

	base.pendingMu.Lock()
	pe := base.pending["flip.go"]
	base.pendingMu.Unlock()

	if pe.op != schema.OpModify {
		t.Errorf("expected latest op (OpModify), got %v", pe.op)
	}
}

func TestDebounce_DeleteOverwritesPrevious(t *testing.T) {
	base := newTestBase(t, 15*time.Millisecond)
	getEvents := collectEvents(base)

	// Create, then delete. Delete should be the final op.
	base.queueEvent("doomed.go", schema.OpCreate)
	base.queueEvent("doomed.go", schema.OpDelete)

	time.Sleep(30 * time.Millisecond)
	base.flushPending()

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Op != schema.OpDelete {
		t.Errorf("expected OpDelete, got %v", events[0].Op)
	}
}

// ─── newBase() ───────────────────────────────────────────────────────────────

func TestNewBase_InitializesFields(t *testing.T) {
	base := newTestBase(t, 25*time.Millisecond)

	if base.cfg == nil {
		t.Error("cfg should not be nil")
	}
	if base.matcher == nil {
		t.Error("matcher should not be nil")
	}
	if base.objStore == nil {
		t.Error("objStore should not be nil")
	}
	if base.pending == nil {
		t.Error("pending map should be initialized")
	}
	if base.done == nil {
		t.Error("done channel should be initialized")
	}
	if base.logger == nil {
		t.Error("logger should be initialized")
	}
	if base.debounceMs != 25*time.Millisecond {
		t.Errorf("debounceMs = %v, want %v", base.debounceMs, 25*time.Millisecond)
	}
}

// ─── Event Field Correctness ─────────────────────────────────────────────────

func TestProcessFileEvent_SetsTimestamp(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)
	getEvents := collectEvents(base)

	writeTestFile(t, base.cfg.ProjectRoot, "ts.go", "package ts")

	ts := time.Now().Add(-5 * time.Second) // a specific past time
	pe := &pendingEvent{
		path:      "ts.go",
		op:        schema.OpCreate,
		timestamp: ts,
	}
	base.processFileEvent(pe)

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	eventTime := events[0].Timestamp()
	if !eventTime.Equal(ts) {
		t.Errorf("event timestamp = %v, want %v", eventTime, ts)
	}
}

func TestProcessFileEvent_UniqueEventIDs(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)
	getEvents := collectEvents(base)

	writeTestFile(t, base.cfg.ProjectRoot, "id1.go", "content1")
	writeTestFile(t, base.cfg.ProjectRoot, "id2.go", "content2")

	base.processFileEvent(&pendingEvent{path: "id1.go", op: schema.OpCreate, timestamp: time.Now()})
	base.processFileEvent(&pendingEvent{path: "id2.go", op: schema.OpCreate, timestamp: time.Now()})

	events := getEvents()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].EventID == events[1].EventID {
		t.Error("event IDs should be unique")
	}
}

// ─── Integration: Full Pipeline ──────────────────────────────────────────────

func TestFullPipeline_CreateFile(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)
	getEvents := collectEvents(base)

	content := "package main\n\nfunc main() {}\n"
	writeTestFile(t, base.cfg.ProjectRoot, "cmd/main.go", content)

	base.queueEvent("cmd/main.go", schema.OpCreate)

	// Wait for debounce then flush.
	time.Sleep(20 * time.Millisecond)
	base.flushPending()

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	ev := events[0]
	if ev.FilePath != "cmd/main.go" {
		t.Errorf("FilePath = %q, want %q", ev.FilePath, "cmd/main.go")
	}
	if ev.Op != schema.OpCreate {
		t.Errorf("Op = %v, want OpCreate", ev.Op)
	}
	if ev.ContentHash == "" {
		t.Error("ContentHash should be populated")
	}
	if ev.ContentSize != int64(len(content)) {
		t.Errorf("ContentSize = %d, want %d", ev.ContentSize, len(content))
	}

	// Verify content is in the object store.
	stored, err := base.objStore.Get(ev.ContentHash)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if string(stored) != content {
		t.Errorf("stored content mismatch")
	}
}

func TestFullPipeline_ModifyFile(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)
	getEvents := collectEvents(base)

	writeTestFile(t, base.cfg.ProjectRoot, "readme.md", "# Hello")

	base.queueEvent("readme.md", schema.OpModify)

	time.Sleep(20 * time.Millisecond)
	base.flushPending()

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Op != schema.OpModify {
		t.Errorf("Op = %v, want OpModify", events[0].Op)
	}
}

func TestFullPipeline_DeleteFile(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)
	getEvents := collectEvents(base)

	// File doesn't need to exist for delete events.
	base.queueEvent("gone.txt", schema.OpDelete)

	time.Sleep(20 * time.Millisecond)
	base.flushPending()

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Op != schema.OpDelete {
		t.Errorf("Op = %v, want OpDelete", events[0].Op)
	}
	if events[0].ContentHash != "" {
		t.Error("delete event should have no content hash")
	}
}

func TestFullPipeline_IgnoredFileSkipped(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)
	getEvents := collectEvents(base)

	// .git/ is ignored by default patterns.
	writeTestFile(t, base.cfg.ProjectRoot, ".git/config", "gitconfig")

	// shouldIgnoreRel should return true, so a real watcher would never
	// queue this event. Verify the ignore check.
	if !base.shouldIgnoreRel(".git/config") {
		t.Error(".git/config should be ignored")
	}

	// But if we manually queue and flush, the event does go through
	// (the ignore happens at the watcher level, not in processFileEvent).
	base.queueEvent(".git/config", schema.OpCreate)
	time.Sleep(20 * time.Millisecond)
	base.flushPending()

	events := getEvents()
	// Events are processed because queueEvent doesn't check ignore;
	// the real watcher checks before calling queueEvent.
	if len(events) != 1 {
		t.Fatalf("expected 1 event (queueEvent doesn't filter), got %d", len(events))
	}
}

// ─── Concurrent Safety ──────────────────────────────────────────────────────

func TestConcurrent_QueueAndFlush(t *testing.T) {
	base := newTestBase(t, 5*time.Millisecond)
	getEvents := collectEvents(base)

	// Create test files for all paths.
	for i := 0; i < 50; i++ {
		relPath := filepath.Join("src", filepath.Base(t.Name())+"-"+string(rune('a'+i%26))+".go")
		writeTestFile(t, base.cfg.ProjectRoot, relPath, "package src")
	}

	var wg sync.WaitGroup
	// Queue events concurrently.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			relPath := filepath.Join("src", filepath.Base(t.Name())+"-"+string(rune('a'+idx%26))+".go")
			base.queueEvent(relPath, schema.OpModify)
		}(i)
	}
	wg.Wait()

	// Flush concurrently too.
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			base.flushPending()
		}()
	}
	wg.Wait()

	events := getEvents()
	// 26 unique paths (a-z), some may have been overwritten by concurrent queues.
	if len(events) == 0 {
		t.Error("expected some events to be flushed")
	}
}

func TestConcurrent_OnEvent(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	var wg sync.WaitGroup
	// Register handlers concurrently.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			base.OnEvent(func(e *schema.Event) {})
		}()
	}
	wg.Wait()

	base.mu.RLock()
	count := len(base.handlers)
	base.mu.RUnlock()

	if count != 20 {
		t.Errorf("expected 20 handlers, got %d", count)
	}
}

// ─── Platform-specific: New() and WatchedDirs() ─────────────────────────────

func TestNew_CreatesWatcher(t *testing.T) {
	projectRoot := t.TempDir()

	objectsDir := filepath.Join(projectRoot, ".belay", "objects")
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		t.Fatalf("create objects dir: %v", err)
	}

	cfg := config.DefaultConfig(projectRoot)
	objStore, err := store.NewStore(objectsDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { objStore.Close() })

	matcher, err := ignore.NewMatcher(projectRoot)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	w, err := New(cfg, objStore, matcher)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if w == nil {
		t.Fatal("New returned nil watcher")
	}
}

func TestWatcher_StartStop(t *testing.T) {
	projectRoot := t.TempDir()

	// Create a small directory tree so the watcher has something to watch.
	for _, dir := range []string{"src", "cmd", "internal"} {
		os.MkdirAll(filepath.Join(projectRoot, dir), 0755)
	}

	objectsDir := filepath.Join(projectRoot, ".belay", "objects")
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		t.Fatalf("create objects dir: %v", err)
	}

	cfg := config.DefaultConfig(projectRoot)
	cfg.Watcher.DebounceMs = 10

	objStore, err := store.NewStore(objectsDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { objStore.Close() })

	matcher, err := ignore.NewMatcher(projectRoot)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	w, err := New(cfg, objStore, matcher)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := w.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give the goroutines time to spin up.
	time.Sleep(20 * time.Millisecond)

	if err := w.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestWatcher_WatchedDirs(t *testing.T) {
	projectRoot := t.TempDir()

	// Create subdirectories.
	for _, dir := range []string{"src", "cmd"} {
		os.MkdirAll(filepath.Join(projectRoot, dir), 0755)
	}

	objectsDir := filepath.Join(projectRoot, ".belay", "objects")
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		t.Fatalf("create objects dir: %v", err)
	}

	cfg := config.DefaultConfig(projectRoot)
	cfg.Watcher.DebounceMs = 10

	objStore, err := store.NewStore(objectsDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { objStore.Close() })

	matcher, err := ignore.NewMatcher(projectRoot)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	w, err := New(cfg, objStore, matcher)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := w.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	dirs := w.WatchedDirs()
	if len(dirs) == 0 {
		t.Error("WatchedDirs should return at least 1 directory")
	}

	// The project root itself should be in the list (as ".").
	found := false
	for _, d := range dirs {
		if d == "." {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("WatchedDirs should include project root '.'; got %v", dirs)
	}

	if err := w.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestWatcher_FileEventIntegration(t *testing.T) {
	projectRoot := t.TempDir()

	objectsDir := filepath.Join(projectRoot, ".belay", "objects")
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		t.Fatalf("create objects dir: %v", err)
	}

	cfg := config.DefaultConfig(projectRoot)
	cfg.Watcher.DebounceMs = 10

	objStore, err := store.NewStore(objectsDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { objStore.Close() })

	matcher, err := ignore.NewMatcher(projectRoot)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	w, err := New(cfg, objStore, matcher)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var mu sync.Mutex
	var received []*schema.Event
	w.OnEvent(func(e *schema.Event) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	})

	if err := w.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { w.Stop() })

	testFile := filepath.Join(projectRoot, "integration-test.txt")
	if err := os.WriteFile(testFile, []byte("integration test content"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatal("FAIL: no file events received within 5s — the OS watcher pipeline is broken")
		case <-ticker.C:
			mu.Lock()
			n := len(received)
			mu.Unlock()
			if n > 0 {
				mu.Lock()
				ev := received[0]
				mu.Unlock()
				if ev.FilePath != "integration-test.txt" {
					t.Errorf("FilePath = %q, want %q", ev.FilePath, "integration-test.txt")
				}
				if ev.ContentHash == "" {
					t.Error("ContentHash should be set")
				}
				return
			}
		}
	}
}

func TestWatcher_MultiFileIntegration(t *testing.T) {
	projectRoot := t.TempDir()

	objectsDir := filepath.Join(projectRoot, ".belay", "objects")
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		t.Fatalf("create objects dir: %v", err)
	}

	cfg := config.DefaultConfig(projectRoot)
	cfg.Watcher.DebounceMs = 10

	objStore, err := store.NewStore(objectsDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { objStore.Close() })

	matcher, err := ignore.NewMatcher(projectRoot)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	w, err := New(cfg, objStore, matcher)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var mu sync.Mutex
	var received []*schema.Event
	w.OnEvent(func(e *schema.Event) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	})

	if err := w.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { w.Stop() })

	wantFiles := map[string]bool{
		"file-a.txt": false,
		"file-b.txt": false,
		"file-c.txt": false,
	}
	for name := range wantFiles {
		path := filepath.Join(projectRoot, name)
		if err := os.WriteFile(path, []byte("content-"+name), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			mu.Lock()
			got := len(received)
			mu.Unlock()
			t.Fatalf("FAIL: only %d/3 file events received within 5s", got)
		case <-ticker.C:
			mu.Lock()
			for _, ev := range received {
				wantFiles[ev.FilePath] = true
			}
			allSeen := true
			for _, seen := range wantFiles {
				if !seen {
					allSeen = false
					break
				}
			}
			mu.Unlock()
			if allSeen {
				return
			}
		}
	}
}

func TestWatcher_ModifyAndDeleteIntegration(t *testing.T) {
	projectRoot := t.TempDir()

	objectsDir := filepath.Join(projectRoot, ".belay", "objects")
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		t.Fatalf("create objects dir: %v", err)
	}

	cfg := config.DefaultConfig(projectRoot)
	cfg.Watcher.DebounceMs = 10

	objStore, err := store.NewStore(objectsDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { objStore.Close() })

	matcher, err := ignore.NewMatcher(projectRoot)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	w, err := New(cfg, objStore, matcher)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var mu sync.Mutex
	var received []*schema.Event
	w.OnEvent(func(e *schema.Event) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	})

	testFile := filepath.Join(projectRoot, "lifecycle.txt")
	if err := os.WriteFile(testFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	if err := w.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { w.Stop() })

	if err := os.WriteFile(testFile, []byte("modified content"), 0644); err != nil {
		t.Fatalf("write modified: %v", err)
	}

	waitForEvents := func(minCount int, timeout time.Duration) {
		deadline := time.After(timeout)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-deadline:
				mu.Lock()
				got := len(received)
				mu.Unlock()
				t.Fatalf("FAIL: wanted %d events, got %d within %v", minCount, got, timeout)
			case <-ticker.C:
				mu.Lock()
				n := len(received)
				mu.Unlock()
				if n >= minCount {
					return
				}
			}
		}
	}

	waitForEvents(1, 5*time.Second)

	mu.Lock()
	firstOp := received[0].Op
	mu.Unlock()
	if firstOp != schema.OpModify && firstOp != schema.OpCreate {
		t.Errorf("first event op = %v, want Modify or Create", firstOp)
	}

	if err := os.Remove(testFile); err != nil {
		t.Fatalf("remove: %v", err)
	}

	waitForEvents(2, 5*time.Second)

	mu.Lock()
	var gotDelete bool
	for _, ev := range received {
		if ev.Op == schema.OpDelete {
			gotDelete = true
		}
	}
	mu.Unlock()

	if !gotDelete {
		t.Error("FAIL: no delete event received after file removal")
	}
}

// ─── Worktree Path Detection ─────────────────────────────────────────────────

func TestIsWorktreePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{".claude/worktrees/agent-abc/src/App.tsx", true},
		{".claude/worktrees/agent-xyz/file.go", true},
		{".claude/settings.json", false},
		{"src/main.go", false},
		{".claude/worktrees/", true},
		{".claude/worktrees/agent-abc", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isWorktreePath(tt.path)
			if got != tt.want {
				t.Errorf("isWorktreePath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestParseWorktreePath(t *testing.T) {
	tests := []struct {
		path          string
		wantName      string
		wantCanonical string
		wantOk        bool
	}{
		{".claude/worktrees/agent-abc/src/App.tsx", "agent-abc", "src/App.tsx", true},
		{".claude/worktrees/agent-xyz/domains/service/file.go", "agent-xyz", "domains/service/file.go", true},
		{".claude/worktrees/agent-abc/README.md", "agent-abc", "README.md", true},
		{".claude/settings.json", "", "", false},
		{"src/main.go", "", "", false},
		{".claude/worktrees/agent-abc", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			name, canonical, ok := parseWorktreePath(tt.path)
			if ok != tt.wantOk {
				t.Fatalf("parseWorktreePath(%q) ok = %v, want %v", tt.path, ok, tt.wantOk)
			}
			if !ok {
				return
			}
			if name != tt.wantName {
				t.Errorf("worktreeName = %q, want %q", name, tt.wantName)
			}
			if canonical != tt.wantCanonical {
				t.Errorf("canonicalPath = %q, want %q", canonical, tt.wantCanonical)
			}
		})
	}
}

func TestWorktreeCache(t *testing.T) {
	wc := newWorktreeCache(50 * time.Millisecond)

	if !wc.needsRefresh("agent-abc") {
		t.Error("new cache should need refresh for unknown worktree")
	}

	wc.mu.Lock()
	wc.dirtyFiles["agent-abc"] = map[string]bool{
		"src/App.tsx": true,
		"src/main.go": true,
	}
	wc.lastCheck["agent-abc"] = time.Now()
	wc.mu.Unlock()

	if wc.needsRefresh("agent-abc") {
		t.Error("recently refreshed cache should not need refresh")
	}

	if !wc.isDirty("agent-abc", "src/App.tsx") {
		t.Error("src/App.tsx should be dirty")
	}
	if wc.isDirty("agent-abc", "src/other.go") {
		t.Error("src/other.go should not be dirty")
	}
	if wc.isDirty("agent-xyz", "src/App.tsx") {
		t.Error("unknown worktree should not have dirty files")
	}

	time.Sleep(60 * time.Millisecond)
	if !wc.needsRefresh("agent-abc") {
		t.Error("cache should need refresh after TTL expires")
	}

	wc.cleanup("agent-abc")
	if wc.isDirty("agent-abc", "src/App.tsx") {
		t.Error("after cleanup, no files should be dirty")
	}
	if !wc.needsRefresh("agent-abc") {
		t.Error("after cleanup, cache should need refresh")
	}
}

func TestShouldFilterWorktreeEvent_NonWorktreePath(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	filteredPath, meta, skip := base.shouldFilterWorktreeEvent("src/main.go", schema.OpModify)
	if skip {
		t.Error("non-worktree path should not be skipped")
	}
	if filteredPath != "src/main.go" {
		t.Errorf("filteredPath = %q, want %q", filteredPath, "src/main.go")
	}
	if meta != nil {
		t.Error("metadata should be nil for non-worktree path")
	}
}

func TestShouldFilterWorktreeEvent_ModifyAlwaysAllowed(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	filteredPath, meta, skip := base.shouldFilterWorktreeEvent(
		".claude/worktrees/agent-abc/src/App.tsx", schema.OpModify)
	if skip {
		t.Error("MODIFY events in worktrees should never be skipped")
	}
	if filteredPath != "src/App.tsx" {
		t.Errorf("filteredPath = %q, want %q", filteredPath, "src/App.tsx")
	}
	if meta == nil || meta["worktree"] != "agent-abc" {
		t.Errorf("metadata should contain worktree=agent-abc, got %v", meta)
	}
}

func TestShouldFilterWorktreeEvent_DeleteAlwaysAllowed(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	filteredPath, meta, skip := base.shouldFilterWorktreeEvent(
		".claude/worktrees/agent-xyz/domains/service/file.go", schema.OpDelete)
	if skip {
		t.Error("DELETE events in worktrees should never be skipped")
	}
	if filteredPath != "domains/service/file.go" {
		t.Errorf("filteredPath = %q, want %q", filteredPath, "domains/service/file.go")
	}
	if meta == nil || meta["worktree"] != "agent-xyz" {
		t.Errorf("metadata should contain worktree=agent-xyz, got %v", meta)
	}
}

func TestShouldFilterWorktreeEvent_CreateFiltered(t *testing.T) {
	base := newTestBase(t, 10*time.Millisecond)

	base.wtCache.mu.Lock()
	base.wtCache.dirtyFiles["agent-abc"] = map[string]bool{
		"src/App.tsx": true,
	}
	base.wtCache.lastCheck["agent-abc"] = time.Now()
	base.wtCache.mu.Unlock()

	_, _, skip := base.shouldFilterWorktreeEvent(
		".claude/worktrees/agent-abc/src/other.go", schema.OpCreate)
	if !skip {
		t.Error("CREATE event for clean file should be skipped (checkout burst)")
	}

	filteredPath, meta, skip := base.shouldFilterWorktreeEvent(
		".claude/worktrees/agent-abc/src/App.tsx", schema.OpCreate)
	if skip {
		t.Error("CREATE event for dirty file should not be skipped")
	}
	if filteredPath != "src/App.tsx" {
		t.Errorf("filteredPath = %q, want %q", filteredPath, "src/App.tsx")
	}
	if meta == nil || meta["worktree"] != "agent-abc" {
		t.Errorf("metadata should contain worktree=agent-abc, got %v", meta)
	}
}

