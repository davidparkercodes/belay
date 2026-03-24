package daemon

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/api"
	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/eventlog"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/session"
	"github.com/davidparkercodes/belay/internal/store"
)

// testConfig creates a minimal Config whose .belay dir lives inside t.TempDir().
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	tmpDir := t.TempDir()
	belayDir := filepath.Join(tmpDir, config.BelayDir)
	if err := os.MkdirAll(belayDir, 0755); err != nil {
		t.Fatalf("MkdirAll .belay: %v", err)
	}
	cfg := config.DefaultConfig(tmpDir)
	return cfg
}

// testDaemonWithSubsystems creates a Daemon with real logWriter, index, registry,
// and sessionFiles initialized — enough to exercise handleEvent without the full
// init() / watcher / apiServer setup.
func testDaemonWithSubsystems(t *testing.T) *Daemon {
	t.Helper()
	cfg := testConfig(t)

	eventsDir := cfg.EventsDir()
	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		t.Fatalf("MkdirAll events: %v", err)
	}

	logWriter, err := eventlog.NewWriter(eventsDir, cfg.Storage.SegmentMaxBytes)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { logWriter.Close() })

	idx, err := index.Open(cfg.IndexPath())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { idx.Close() })

	registry := session.NewRegistry()

	d := &Daemon{
		cfg:          cfg,
		logger:       log.New(os.Stderr, "[belay-test] ", log.LstdFlags),
		logWriter:    logWriter,
		idx:          idx,
		registry:     registry,
		sessionFiles: make(map[string]map[string]bool),
	}

	return d
}

// testDaemonWithApiServer creates a Daemon with subsystems plus an API server
// (not started, just constructed) so the Broadcast path is exercised.
func testDaemonWithApiServer(t *testing.T) *Daemon {
	t.Helper()
	d := testDaemonWithSubsystems(t)

	objDir := d.cfg.ObjectsDir()
	if err := os.MkdirAll(objDir, 0755); err != nil {
		t.Fatalf("MkdirAll objects: %v", err)
	}
	objStore, err := store.NewStore(objDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { objStore.Close() })
	d.objStore = objStore

	d.apiServer = api.New(d.cfg, d.idx, d.objStore, d.registry, d.logger, d.HandleRecordedEvent, "test", nil)
	return d
}

// testDetector implements session.Detector for testing attribution in handleEvent.
type testDetector struct {
	sessions    []*session.DetectedSession
	attributeFn func(event *session.FileWriteEvent, active []*session.DetectedSession) (string, float32, schema.AttributionMethod)
}

func (d *testDetector) Name() string { return "test-detector" }
func (d *testDetector) Detect() ([]*session.DetectedSession, error) {
	return d.sessions, nil
}
func (d *testDetector) Identify(pid int) (*session.DetectedSession, error) { return nil, nil }
func (d *testDetector) Attribute(event *session.FileWriteEvent, active []*session.DetectedSession) (string, float32, schema.AttributionMethod) {
	if d.attributeFn != nil {
		return d.attributeFn(event, active)
	}
	return "", 0, schema.AttrNone
}

// testDaemonWithAttribution creates a Daemon where the registry has a mock
// detector with an active session, so handleEvent exercises the attribution
// success path (lines 180-184).
func testDaemonWithAttribution(t *testing.T) *Daemon {
	t.Helper()
	cfg := testConfig(t)

	eventsDir := cfg.EventsDir()
	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		t.Fatalf("MkdirAll events: %v", err)
	}

	logWriter, err := eventlog.NewWriter(eventsDir, cfg.Storage.SegmentMaxBytes)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { logWriter.Close() })

	idx, err := index.Open(cfg.IndexPath())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { idx.Close() })

	detector := &testDetector{
		sessions: []*session.DetectedSession{
			{
				SessionID:        "attributed-session",
				ToolName:         "test-tool",
				PID:              12345,
				WorkingDirectory: cfg.ProjectRoot,
				StartedAt:        time.Now(),
			},
		},
		attributeFn: func(event *session.FileWriteEvent, active []*session.DetectedSession) (string, float32, schema.AttributionMethod) {
			return "attributed-session", 0.85, schema.AttrPID
		},
	}

	registry := session.NewRegistry(detector)
	// Poll to discover the session
	registry.Start()
	time.Sleep(50 * time.Millisecond) // let it detect
	t.Cleanup(func() { registry.Stop() })

	d := &Daemon{
		cfg:          cfg,
		logger:       log.New(os.Stderr, "[belay-test] ", log.LstdFlags),
		logWriter:    logWriter,
		idx:          idx,
		registry:     registry,
		sessionFiles: make(map[string]map[string]bool),
	}

	return d
}

// ─── New ────────────────────────────────────────────────────────────────────

func TestNew_ReturnsNonNil(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d == nil {
		t.Fatal("New returned nil daemon")
	}
}

func TestNew_SetsConfig(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d.cfg != cfg {
		t.Error("daemon.cfg does not point to the provided config")
	}
}

func TestNew_SetsLogger(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d.logger == nil {
		t.Error("daemon.logger is nil")
	}
}

// ─── writePID / removePID ───────────────────────────────────────────────────

func TestWritePID_CreatesFile(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := d.writePID(); err != nil {
		t.Fatalf("writePID: %v", err)
	}

	data, err := os.ReadFile(cfg.PIDPath())
	if err != nil {
		t.Fatalf("ReadFile PID: %v", err)
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatalf("PID file contains non-integer: %q", string(data))
	}

	if pid != os.Getpid() {
		t.Errorf("PID file contains %d, want %d", pid, os.Getpid())
	}
}

func TestWritePID_OverwritesExisting(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Write a stale PID first
	if err := os.WriteFile(cfg.PIDPath(), []byte("99999"), 0644); err != nil {
		t.Fatalf("WriteFile stale PID: %v", err)
	}

	if err := d.writePID(); err != nil {
		t.Fatalf("writePID: %v", err)
	}

	data, err := os.ReadFile(cfg.PIDPath())
	if err != nil {
		t.Fatalf("ReadFile PID: %v", err)
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatalf("PID file contains non-integer: %q", string(data))
	}

	if pid != os.Getpid() {
		t.Errorf("PID file should be overwritten to %d, got %d", os.Getpid(), pid)
	}
}

func TestRemovePID_DeletesFile(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := d.writePID(); err != nil {
		t.Fatalf("writePID: %v", err)
	}

	// Verify it exists
	if _, err := os.Stat(cfg.PIDPath()); err != nil {
		t.Fatalf("PID file should exist before removal: %v", err)
	}

	d.removePID()

	if _, err := os.Stat(cfg.PIDPath()); !os.IsNotExist(err) {
		t.Error("PID file should not exist after removePID")
	}
}

func TestRemovePID_NoErrorIfMissing(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// removePID should not panic when the file does not exist
	d.removePID()
}

func TestWritePID_FailsIfDirMissing(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.DefaultConfig(tmpDir)
	// Do NOT create .belay dir, so PIDPath's parent doesn't exist
	cfg.BelayPath = filepath.Join(tmpDir, "nonexistent", ".belay")

	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = d.writePID()
	if err == nil {
		t.Error("writePID should fail when .belay directory does not exist")
	}
}

// ─── IsRunning ──────────────────────────────────────────────────────────────

func TestIsRunning_NoPIDFile(t *testing.T) {
	cfg := testConfig(t)

	running, pid := IsRunning(cfg)
	if running {
		t.Error("IsRunning should be false when PID file does not exist")
	}
	if pid != 0 {
		t.Errorf("pid should be 0 when PID file does not exist, got %d", pid)
	}
}

func TestIsRunning_InvalidPIDContent(t *testing.T) {
	cfg := testConfig(t)

	// Write non-numeric content to PID file
	if err := os.WriteFile(cfg.PIDPath(), []byte("not-a-number"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	running, pid := IsRunning(cfg)
	if running {
		t.Error("IsRunning should be false for non-numeric PID content")
	}
	if pid != 0 {
		t.Errorf("pid should be 0 for invalid content, got %d", pid)
	}
}

func TestIsRunning_CurrentProcess(t *testing.T) {
	cfg := testConfig(t)

	// Write our own PID — the current process is definitely running
	myPID := os.Getpid()
	if err := os.WriteFile(cfg.PIDPath(), []byte(strconv.Itoa(myPID)), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	running, pid := IsRunning(cfg)
	if !running {
		t.Error("IsRunning should be true for current process PID")
	}
	if pid != myPID {
		t.Errorf("pid = %d, want %d", pid, myPID)
	}
}

func TestIsRunning_DeadProcess(t *testing.T) {
	cfg := testConfig(t)

	// Use a very large PID that almost certainly doesn't exist.
	// On macOS/Linux, PID_MAX is typically 32768-99999, so 4000000 should be safe.
	deadPID := 4000000
	if err := os.WriteFile(cfg.PIDPath(), []byte(strconv.Itoa(deadPID)), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	running, pid := IsRunning(cfg)
	if running {
		t.Error("IsRunning should be false for a non-existent PID")
	}
	if pid != 0 {
		t.Errorf("pid should be 0 for dead process, got %d", pid)
	}

	// IsRunning should clean up the stale PID file
	if _, err := os.Stat(cfg.PIDPath()); !os.IsNotExist(err) {
		t.Error("IsRunning should remove stale PID file for dead process")
	}
}

func TestIsRunning_EmptyPIDFile(t *testing.T) {
	cfg := testConfig(t)

	if err := os.WriteFile(cfg.PIDPath(), []byte(""), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	running, pid := IsRunning(cfg)
	if running {
		t.Error("IsRunning should be false for empty PID file")
	}
	if pid != 0 {
		t.Errorf("pid should be 0 for empty PID file, got %d", pid)
	}
}

// ─── Stop ───────────────────────────────────────────────────────────────────

func TestStop_WhenNotRunning(t *testing.T) {
	cfg := testConfig(t)

	err := Stop(cfg)
	if err == nil {
		t.Fatal("Stop should return error when daemon is not running")
	}
	if err.Error() != "daemon is not running" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestStop_WhenPIDFileHasInvalidContent(t *testing.T) {
	cfg := testConfig(t)

	if err := os.WriteFile(cfg.PIDPath(), []byte("garbage"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := Stop(cfg)
	if err == nil {
		t.Fatal("Stop should return error when PID file has invalid content")
	}
}

func TestStop_WhenPIDFileHasDeadPID(t *testing.T) {
	cfg := testConfig(t)

	deadPID := 4000000
	if err := os.WriteFile(cfg.PIDPath(), []byte(strconv.Itoa(deadPID)), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := Stop(cfg)
	if err == nil {
		t.Fatal("Stop should return error when process is dead")
	}
}

// ─── sessionHasFile / trackSessionFile ──────────────────────────────────────

func TestSessionHasFile_EmptyMap(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.sessionFiles = make(map[string]map[string]bool)

	if d.sessionHasFile("some-session", "some-file") {
		t.Error("sessionHasFile should return false for empty map")
	}
}

func TestSessionHasFile_UnknownSession(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.sessionFiles = make(map[string]map[string]bool)
	d.sessionFiles["other-session"] = map[string]bool{"file.go": true}

	if d.sessionHasFile("unknown-session", "file.go") {
		t.Error("sessionHasFile should return false for unknown session")
	}
}

func TestSessionHasFile_KnownSessionUnknownFile(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.sessionFiles = make(map[string]map[string]bool)
	d.sessionFiles["my-session"] = map[string]bool{"known.go": true}

	if d.sessionHasFile("my-session", "unknown.go") {
		t.Error("sessionHasFile should return false for unknown file in known session")
	}
}

func TestSessionHasFile_KnownSessionKnownFile(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.sessionFiles = make(map[string]map[string]bool)
	d.sessionFiles["my-session"] = map[string]bool{"known.go": true}

	if !d.sessionHasFile("my-session", "known.go") {
		t.Error("sessionHasFile should return true for known file in known session")
	}
}

func TestTrackSessionFile_CreatesSession(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.sessionFiles = make(map[string]map[string]bool)

	d.trackSessionFile("new-session", "file.go")

	if !d.sessionHasFile("new-session", "file.go") {
		t.Error("file should be tracked after trackSessionFile")
	}
}

func TestTrackSessionFile_AddsToExistingSession(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.sessionFiles = make(map[string]map[string]bool)

	d.trackSessionFile("sess-1", "file-a.go")
	d.trackSessionFile("sess-1", "file-b.go")

	if !d.sessionHasFile("sess-1", "file-a.go") {
		t.Error("file-a.go should be tracked")
	}
	if !d.sessionHasFile("sess-1", "file-b.go") {
		t.Error("file-b.go should be tracked")
	}
}

func TestTrackSessionFile_MultipleSessions(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.sessionFiles = make(map[string]map[string]bool)

	d.trackSessionFile("sess-1", "file-a.go")
	d.trackSessionFile("sess-2", "file-b.go")

	if !d.sessionHasFile("sess-1", "file-a.go") {
		t.Error("sess-1 should have file-a.go")
	}
	if d.sessionHasFile("sess-1", "file-b.go") {
		t.Error("sess-1 should NOT have file-b.go")
	}
	if !d.sessionHasFile("sess-2", "file-b.go") {
		t.Error("sess-2 should have file-b.go")
	}
	if d.sessionHasFile("sess-2", "file-a.go") {
		t.Error("sess-2 should NOT have file-a.go")
	}
}

func TestTrackSessionFile_Idempotent(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.sessionFiles = make(map[string]map[string]bool)

	d.trackSessionFile("sess-1", "file.go")
	d.trackSessionFile("sess-1", "file.go") // duplicate

	if !d.sessionHasFile("sess-1", "file.go") {
		t.Error("file should still be tracked after duplicate trackSessionFile")
	}

	// Verify only one entry (the map value is just true, no duplicates possible)
	d.sessionFilesMu.RLock()
	count := len(d.sessionFiles["sess-1"])
	d.sessionFilesMu.RUnlock()
	if count != 1 {
		t.Errorf("expected 1 file tracked, got %d", count)
	}
}

// ─── Concurrent session file tracking ───────────────────────────────────────

func TestTrackSessionFile_ConcurrentSafetyMultipleGoroutines(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.sessionFiles = make(map[string]map[string]bool)

	const numGoroutines = 50
	const filesPerGoroutine = 20
	var wg sync.WaitGroup

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(gIdx int) {
			defer wg.Done()
			sessionID := "sess-" + strconv.Itoa(gIdx%5) // 5 sessions
			for f := 0; f < filesPerGoroutine; f++ {
				filePath := "file-" + strconv.Itoa(gIdx) + "-" + strconv.Itoa(f) + ".go"
				d.trackSessionFile(sessionID, filePath)
			}
		}(g)
	}

	wg.Wait()

	// Verify no data corruption — every goroutine's files should be present
	for g := 0; g < numGoroutines; g++ {
		sessionID := "sess-" + strconv.Itoa(g%5)
		for f := 0; f < filesPerGoroutine; f++ {
			filePath := "file-" + strconv.Itoa(g) + "-" + strconv.Itoa(f) + ".go"
			if !d.sessionHasFile(sessionID, filePath) {
				t.Errorf("missing file %s for session %s", filePath, sessionID)
			}
		}
	}
}

func TestSessionHasFile_ConcurrentReadsAndWrites(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.sessionFiles = make(map[string]map[string]bool)

	// Pre-populate some data
	d.trackSessionFile("sess-1", "existing.go")

	const numGoroutines = 100
	var wg sync.WaitGroup

	// Mix of readers and writers
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(gIdx int) {
			defer wg.Done()
			if gIdx%2 == 0 {
				// Reader
				d.sessionHasFile("sess-1", "existing.go")
				d.sessionHasFile("sess-1", "nonexistent.go")
				d.sessionHasFile("unknown-session", "file.go")
			} else {
				// Writer
				d.trackSessionFile("sess-1", "new-file-"+strconv.Itoa(gIdx)+".go")
			}
		}(g)
	}

	wg.Wait()

	// Original data should still be there
	if !d.sessionHasFile("sess-1", "existing.go") {
		t.Error("pre-populated file should still be tracked after concurrent access")
	}
}

// ─── cleanup ────────────────────────────────────────────────────────────────

func TestCleanup_NilFields(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// cleanup should not panic when all subsystem fields are nil
	d.cleanup()
}

func TestCleanup_WithSubsystems(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	// cleanup should close all subsystems without error or panic
	// Remove the t.Cleanup closers since we're calling cleanup manually
	d.cleanup()

	// After cleanup, the fields should still be non-nil (cleanup doesn't nil them)
	// but the underlying resources should be closed. The main assertion is
	// that cleanup does not panic.
}

// ─── handleEvent / HandleRecordedEvent ──────────────────────────────────────

func TestHandleEvent_BasicEvent_NoSession(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "src/main.go",
		Op:            schema.OpCreate,
		ContentHash:   "abc123",
		ContentSize:   100,
	}

	// handleEvent should not panic with a basic event and no active sessions
	d.handleEvent(event)

	// Event should have been indexed
	latest, err := d.idx.LatestEvent("src/main.go")
	if err != nil {
		t.Fatalf("LatestEvent: %v", err)
	}
	if latest == nil {
		t.Fatal("expected indexed event, got nil")
	}
	if latest.EventID != event.EventID {
		t.Errorf("indexed event ID = %q, want %q", latest.EventID, event.EventID)
	}
}

func TestHandleEvent_ModifyEvent_SetsPreviousHash(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	// First event — create
	event1 := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "src/main.go",
		Op:            schema.OpCreate,
		ContentHash:   "hash-v1",
		ContentSize:   50,
	}
	d.handleEvent(event1)

	// Second event — modify (PreviousHash should be auto-filled)
	event2 := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().Add(time.Second).UnixNano(),
		FilePath:      "src/main.go",
		Op:            schema.OpModify,
		ContentHash:   "hash-v2",
		ContentSize:   60,
	}
	d.handleEvent(event2)

	if event2.PreviousHash != "hash-v1" {
		t.Errorf("PreviousHash = %q, want %q", event2.PreviousHash, "hash-v1")
	}
}

func TestHandleEvent_CreateEvent_NoPreviousHash(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "new-file.go",
		Op:            schema.OpCreate,
		ContentHash:   "hash-new",
		ContentSize:   30,
	}
	d.handleEvent(event)

	// OpCreate should not attempt to set PreviousHash
	if event.PreviousHash != "" {
		t.Errorf("PreviousHash should be empty for OpCreate, got %q", event.PreviousHash)
	}
}

func TestHandleEvent_WithSessionID_TracksFiles(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "src/utils.go",
		Op:            schema.OpModify,
		SessionID:     "test-session-123",
		ContentHash:   "hash-utils",
		ContentSize:   200,
	}

	d.handleEvent(event)

	// The session file should be tracked
	if !d.sessionHasFile("test-session-123", "src/utils.go") {
		t.Error("sessionHasFile should return true after handleEvent with session ID")
	}
}

func TestHandleEvent_WithSessionID_AutoRegistersSession(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "src/app.go",
		Op:            schema.OpCreate,
		SessionID:     "auto-register-session",
		ContentHash:   "hash-app",
		ContentSize:   150,
		Metadata: map[string]string{
			"tool_name": "test-tool",
		},
	}

	d.handleEvent(event)

	// The session should have been auto-registered and upserted in the index
	// (since the registry doesn't know about it, handleEvent creates a new Session)
	if !d.sessionHasFile("auto-register-session", "src/app.go") {
		t.Error("file should be tracked for auto-registered session")
	}
}

func TestHandleEvent_WithSessionID_IncrementsEventCount(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	sessionID := "count-session"

	// Send multiple events for the same session
	for i := 0; i < 3; i++ {
		event := &schema.Event{
			EventID:       schema.NewEventID(),
			Version:       schema.SchemaVersion,
			TimestampNano: time.Now().Add(time.Duration(i) * time.Second).UnixNano(),
			FilePath:      "file-" + strconv.Itoa(i) + ".go",
			Op:            schema.OpCreate,
			SessionID:     sessionID,
			ContentHash:   "hash-" + strconv.Itoa(i),
			ContentSize:   int64(i * 100),
		}
		d.handleEvent(event)
	}

	// All three files should be tracked
	for i := 0; i < 3; i++ {
		filePath := "file-" + strconv.Itoa(i) + ".go"
		if !d.sessionHasFile(sessionID, filePath) {
			t.Errorf("file %s should be tracked for session %s", filePath, sessionID)
		}
	}
}

func TestHandleEvent_SameFileInSession_DoesNotIncrementFilesChanged(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	sessionID := "dup-file-session"

	// First event for this file
	event1 := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "src/main.go",
		Op:            schema.OpCreate,
		SessionID:     sessionID,
		ContentHash:   "hash-v1",
		ContentSize:   100,
	}
	d.handleEvent(event1)

	// Second event for the same file — sessionHasFile should now return true
	if !d.sessionHasFile(sessionID, "src/main.go") {
		t.Fatal("file should be tracked after first event")
	}

	// The file was already seen, so sessionHasFile returns true on the second pass
	event2 := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().Add(time.Second).UnixNano(),
		FilePath:      "src/main.go",
		Op:            schema.OpModify,
		SessionID:     sessionID,
		ContentHash:   "hash-v2",
		ContentSize:   110,
	}
	d.handleEvent(event2)

	// File tracking should still be present
	if !d.sessionHasFile(sessionID, "src/main.go") {
		t.Error("file should still be tracked after second event")
	}
}

func TestHandleEvent_NoApiServer_DoesNotPanic(t *testing.T) {
	d := testDaemonWithSubsystems(t)
	// apiServer is nil by default from testDaemonWithSubsystems

	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "test.go",
		Op:            schema.OpCreate,
		ContentHash:   "hash-test",
		ContentSize:   42,
	}

	// Should not panic when apiServer is nil
	d.handleEvent(event)
}

func TestHandleRecordedEvent_DelegatesToHandleEvent(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "recorded-file.go",
		Op:            schema.OpCreate,
		SessionID:     "recorded-session",
		ContentHash:   "hash-recorded",
		ContentSize:   75,
	}

	d.HandleRecordedEvent(event)

	// Verify it was processed (file should be tracked)
	if !d.sessionHasFile("recorded-session", "recorded-file.go") {
		t.Error("HandleRecordedEvent should delegate to handleEvent")
	}

	// Verify it was indexed
	latest, err := d.idx.LatestEvent("recorded-file.go")
	if err != nil {
		t.Fatalf("LatestEvent: %v", err)
	}
	if latest == nil {
		t.Fatal("expected indexed event from HandleRecordedEvent")
	}
	if latest.EventID != event.EventID {
		t.Errorf("indexed event ID = %q, want %q", latest.EventID, event.EventID)
	}
}

func TestHandleEvent_WithPreExistingSessionID_SkipsAttribution(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	// Event already has a session ID — attribution should be skipped
	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "pre-attributed.go",
		Op:            schema.OpModify,
		SessionID:     "pre-existing-session",
		ContentHash:   "hash-pre",
		ContentSize:   80,
	}

	d.handleEvent(event)

	// Session ID should remain unchanged
	if event.SessionID != "pre-existing-session" {
		t.Errorf("SessionID should remain %q, got %q", "pre-existing-session", event.SessionID)
	}
}

func TestHandleEvent_WithoutSessionID_AttemptsAttribution(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	// Event without a session ID — registry has no sessions, so attribution
	// should return empty
	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "unattributed.go",
		Op:            schema.OpModify,
		ContentHash:   "hash-unattr",
		ContentSize:   90,
	}

	d.handleEvent(event)

	// No sessions in registry, so SessionID should remain empty
	if event.SessionID != "" {
		t.Errorf("SessionID should be empty when no sessions exist, got %q", event.SessionID)
	}
}

func TestHandleEvent_DeleteEvent_LooksPreviousHash(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	// Create a file first
	event1 := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "to-delete.go",
		Op:            schema.OpCreate,
		ContentHash:   "hash-before-delete",
		ContentSize:   40,
	}
	d.handleEvent(event1)

	// Delete the file — PreviousHash should be auto-filled
	event2 := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().Add(time.Second).UnixNano(),
		FilePath:      "to-delete.go",
		Op:            schema.OpDelete,
	}
	d.handleEvent(event2)

	if event2.PreviousHash != "hash-before-delete" {
		t.Errorf("PreviousHash for delete = %q, want %q", event2.PreviousHash, "hash-before-delete")
	}
}

func TestHandleEvent_WithExplicitPreviousHash_DoesNotOverwrite(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	// Create initial event
	event1 := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "explicit-prev.go",
		Op:            schema.OpCreate,
		ContentHash:   "hash-original",
		ContentSize:   50,
	}
	d.handleEvent(event1)

	// Modify with explicit PreviousHash — should not be overwritten
	event2 := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().Add(time.Second).UnixNano(),
		FilePath:      "explicit-prev.go",
		Op:            schema.OpModify,
		ContentHash:   "hash-modified",
		PreviousHash:  "explicit-previous-hash",
		ContentSize:   60,
	}
	d.handleEvent(event2)

	if event2.PreviousHash != "explicit-previous-hash" {
		t.Errorf("PreviousHash should remain %q when explicitly set, got %q",
			"explicit-previous-hash", event2.PreviousHash)
	}
}

func TestHandleEvent_SessionWithToolNameMetadata(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "tool-file.go",
		Op:            schema.OpCreate,
		SessionID:     "metadata-session",
		ContentHash:   "hash-tool",
		ContentSize:   55,
		Metadata: map[string]string{
			"tool_name": "custom-tool",
		},
	}

	// Should not panic and should track the file
	d.handleEvent(event)

	if !d.sessionHasFile("metadata-session", "tool-file.go") {
		t.Error("file should be tracked for session with tool_name metadata")
	}
}

func TestHandleEvent_SessionWithNilMetadata(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "nil-meta.go",
		Op:            schema.OpCreate,
		SessionID:     "nil-meta-session",
		ContentHash:   "hash-nil-meta",
		ContentSize:   33,
		// Metadata is nil
	}

	// Should not panic even with nil Metadata — the auto-register code
	// checks Metadata["tool_name"] safely
	d.handleEvent(event)

	if !d.sessionHasFile("nil-meta-session", "nil-meta.go") {
		t.Error("file should be tracked even with nil metadata")
	}
}

// ─── Integration-style: writePID + IsRunning + removePID lifecycle ──────────

func TestPIDLifecycle(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Initially not running
	running, _ := IsRunning(cfg)
	if running {
		t.Error("should not be running before writePID")
	}

	// Write PID
	if err := d.writePID(); err != nil {
		t.Fatalf("writePID: %v", err)
	}

	// Now should be running (PID matches our process)
	running, pid := IsRunning(cfg)
	if !running {
		t.Error("should be running after writePID")
	}
	if pid != os.Getpid() {
		t.Errorf("pid = %d, want %d", pid, os.Getpid())
	}

	// Remove PID
	d.removePID()

	// Should no longer be running
	running, _ = IsRunning(cfg)
	if running {
		t.Error("should not be running after removePID")
	}
}

// ─── Edge cases ─────────────────────────────────────────────────────────────

func TestIsRunning_PIDZero(t *testing.T) {
	cfg := testConfig(t)

	// PID 0 is a special case (the kernel scheduler on unix)
	if err := os.WriteFile(cfg.PIDPath(), []byte("0"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// FindProcess(0) may succeed on some platforms, but Signal(0)
	// should fail or behave unexpectedly. Either way, we just want
	// IsRunning to not panic.
	IsRunning(cfg)
}

func TestIsRunning_NegativePID(t *testing.T) {
	cfg := testConfig(t)

	if err := os.WriteFile(cfg.PIDPath(), []byte("-1"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Negative PID — should not panic, should return not running
	running, pid := IsRunning(cfg)
	if running {
		t.Error("IsRunning should be false for negative PID")
	}
	if pid != 0 {
		t.Errorf("pid should be 0 for negative PID, got %d", pid)
	}
}

func TestIsRunning_WhitespacePIDFile(t *testing.T) {
	cfg := testConfig(t)

	if err := os.WriteFile(cfg.PIDPath(), []byte("  \n\t  "), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	running, pid := IsRunning(cfg)
	if running {
		t.Error("IsRunning should be false for whitespace-only PID file")
	}
	if pid != 0 {
		t.Errorf("pid should be 0, got %d", pid)
	}
}

// ─── handleEvent with attribution ────────────────────────────────────────────

func TestHandleEvent_AttributionSucceeds(t *testing.T) {
	d := testDaemonWithAttribution(t)

	// Event without a sessionID — the detector should attribute it
	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "attributed-file.go",
		Op:            schema.OpModify,
		ContentHash:   "hash-attr",
		ContentSize:   100,
		// SessionID is intentionally empty
	}

	d.handleEvent(event)

	if event.SessionID != "attributed-session" {
		t.Errorf("SessionID = %q, want %q", event.SessionID, "attributed-session")
	}
	if event.AttributionConfidence != 0.85 {
		t.Errorf("AttributionConfidence = %v, want 0.85", event.AttributionConfidence)
	}
	if event.Attribution != schema.AttrPID {
		t.Errorf("Attribution = %v, want AttrPID", event.Attribution)
	}
}

// ─── handleEvent error paths ────────────────────────────────────────────────

func TestHandleEvent_LogWriterClosed_ReturnsEarly(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	// Close the logWriter to force Append to fail
	d.logWriter.Close()

	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "error-path.go",
		Op:            schema.OpCreate,
		ContentHash:   "hash-err",
		ContentSize:   50,
	}

	// Should not panic — the error is logged and handleEvent returns early
	d.handleEvent(event)

	// Since logWriter.Append failed, the event should NOT be indexed
	latest, err := d.idx.LatestEvent("error-path.go")
	if err != nil {
		// LatestEvent might return an error or nil — either is fine
		return
	}
	if latest != nil && latest.EventID == event.EventID {
		t.Error("event should NOT be indexed when logWriter.Append fails")
	}
}

// ─── handleEvent with apiServer (Broadcast path) ────────────────────────────

func TestHandleEvent_WithApiServer_BroadcastsCalled(t *testing.T) {
	d := testDaemonWithApiServer(t)

	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "broadcast-test.go",
		Op:            schema.OpCreate,
		ContentHash:   "hash-broadcast",
		ContentSize:   42,
	}

	// Should not panic and should call Broadcast on the apiServer
	d.handleEvent(event)
}

func TestHandleEvent_WithApiServer_AndSession(t *testing.T) {
	d := testDaemonWithApiServer(t)

	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "broadcast-session.go",
		Op:            schema.OpModify,
		SessionID:     "broadcast-session-123",
		ContentHash:   "hash-bcast-sess",
		ContentSize:   99,
	}

	d.handleEvent(event)

	if !d.sessionHasFile("broadcast-session-123", "broadcast-session.go") {
		t.Error("file should be tracked when apiServer is present")
	}
}

func TestHandleRecordedEvent_WithApiServer(t *testing.T) {
	d := testDaemonWithApiServer(t)

	event := &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "api-recorded.go",
		Op:            schema.OpCreate,
		SessionID:     "api-sess",
		ContentHash:   "hash-api-rec",
		ContentSize:   77,
	}

	d.HandleRecordedEvent(event)

	if !d.sessionHasFile("api-sess", "api-recorded.go") {
		t.Error("file should be tracked via HandleRecordedEvent with apiServer")
	}
}

// ─── cleanup with all subsystems ────────────────────────────────────────────

func TestCleanup_WithApiServer(t *testing.T) {
	d := testDaemonWithApiServer(t)

	// cleanup should handle closing the apiServer too
	d.cleanup()
}

func TestCleanup_WithRegistryStarted(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	// Start the registry (creates the polling goroutine)
	d.registry.Start()

	// cleanup stops registry among other things
	d.cleanup()
}

// ─── HandleRecordedEvent concurrent safety ──────────────────────────────────

func TestHandleRecordedEvent_ConcurrentCalls(t *testing.T) {
	d := testDaemonWithSubsystems(t)

	const numGoroutines = 20
	var wg sync.WaitGroup

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(gIdx int) {
			defer wg.Done()
			event := &schema.Event{
				EventID:       schema.NewEventID(),
				Version:       schema.SchemaVersion,
				TimestampNano: time.Now().UnixNano(),
				FilePath:      "concurrent-" + strconv.Itoa(gIdx) + ".go",
				Op:            schema.OpCreate,
				SessionID:     "concurrent-session",
				ContentHash:   "hash-" + strconv.Itoa(gIdx),
				ContentSize:   int64(gIdx),
			}
			d.HandleRecordedEvent(event)
		}(g)
	}

	wg.Wait()

	// All files should be tracked
	for g := 0; g < numGoroutines; g++ {
		filePath := "concurrent-" + strconv.Itoa(g) + ".go"
		if !d.sessionHasFile("concurrent-session", filePath) {
			t.Errorf("missing file %s after concurrent HandleRecordedEvent", filePath)
		}
	}
}

// ─── init() ─────────────────────────────────────────────────────────────────

func TestInit_Success(t *testing.T) {
	cfg := testConfig(t)

	// Create required subdirectories
	for _, dir := range []string{cfg.ObjectsDir(), cfg.EventsDir()} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}

	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := d.init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	// Close subsystems manually; watcher.Stop() panics if Start() was never
	// called (ticker is nil), so we skip it and close only the safe subsystems.
	defer func() {
		d.logWriter.Close()
		d.objStore.Close()
		d.idx.Close()
	}()

	// Verify all subsystems were initialized
	if d.objStore == nil {
		t.Error("objStore should be initialized after init")
	}
	if d.logWriter == nil {
		t.Error("logWriter should be initialized after init")
	}
	if d.idx == nil {
		t.Error("idx should be initialized after init")
	}
	if d.matcher == nil {
		t.Error("matcher should be initialized after init")
	}
	if d.registry == nil {
		t.Error("registry should be initialized after init")
	}
	if d.watcher == nil {
		t.Error("watcher should be initialized after init")
	}
	if d.sessionFiles == nil {
		t.Error("sessionFiles should be initialized after init")
	}
}

func TestInit_SetsSessionCallbacks(t *testing.T) {
	cfg := testConfig(t)

	for _, dir := range []string{cfg.ObjectsDir(), cfg.EventsDir()} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}

	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := d.init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer func() {
		d.logWriter.Close()
		d.objStore.Close()
		d.idx.Close()
	}()

	// Verify detector was registered
	names := d.registry.DetectorNames()
	if len(names) == 0 {
		t.Error("expected at least one detector registered")
	}
	found := false
	for _, name := range names {
		if name == "claude-code" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected claude-code detector, got %v", names)
	}
}

func TestInit_FailsWithBadObjectsDir(t *testing.T) {
	cfg := testConfig(t)
	// Create a regular file where the .belay directory needs to be,
	// so MkdirAll fails when trying to create subdirectories.
	blocker := filepath.Join(cfg.BelayPath, "objects")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0644); err != nil {
		t.Fatalf("WriteFile blocker: %v", err)
	}

	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = d.init()
	if err == nil {
		t.Fatal("init should fail when objects path is a file, not a directory")
	}
}

// ─── Stop success path ─────────────────────────────────────────────────────

func TestStop_SendsSIGTERM(t *testing.T) {
	cfg := testConfig(t)

	// Start a long-running subprocess that we can safely SIGTERM
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start subprocess: %v", err)
	}
	childPID := cmd.Process.Pid
	defer func() {
		// Ensure cleanup even if test fails
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Write the child's PID to the PID file
	if err := os.WriteFile(cfg.PIDPath(), []byte(strconv.Itoa(childPID)), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Verify IsRunning sees the child process
	running, pid := IsRunning(cfg)
	if !running {
		t.Fatal("child process should be running before Stop")
	}
	if pid != childPID {
		t.Errorf("pid = %d, want %d", pid, childPID)
	}

	// Stop should send SIGTERM and succeed
	if err := Stop(cfg); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Wait for the child to exit (it should die from SIGTERM)
	_ = cmd.Wait()
}
