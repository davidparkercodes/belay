// Package daemon manages the Belay daemon lifecycle, coordinating the watcher,
// event log, index, session registry, and API server.
package daemon

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/davidparkercodes/belay/internal/api"
	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/eventlog"
	"github.com/davidparkercodes/belay/internal/ignore"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/session"
	"github.com/davidparkercodes/belay/internal/store"
	"github.com/davidparkercodes/belay/internal/watcher"
)

// Daemon is the main Belay process that watches for file changes, attributes them
// to AI sessions, and persists events to the log and index.
type Daemon struct {
	cfg          *config.Config
	version      string
	objStore     *store.Store
	logWriter    *eventlog.Writer
	idx          *index.Index
	matcher      *ignore.Matcher
	watcher      *watcher.Watcher
	registry     *session.Registry
	apiServer    *api.Server
	logger       *log.Logger
	sessionFilesMu sync.RWMutex
	sessionFiles   map[string]map[string]bool
	stopCheckpoint chan struct{}
	checkpointDone chan struct{}
	stopCleanup    chan struct{}
	cleanupDone    chan struct{}
	stopWatchdog   chan struct{}
	watchdogDone   chan struct{}
	watchdogRestarts int
	burst          *burstDetector
}

// New creates a new Daemon with the given configuration.
func New(cfg *config.Config, version string) (*Daemon, error) {
	d := &Daemon{
		cfg:     cfg,
		version: version,
		logger:  log.New(os.Stderr, "[belay] ", log.LstdFlags),
	}
	return d, nil
}

// Run initializes all subsystems and blocks until a SIGINT or SIGTERM is received.
// On the first signal, a graceful shutdown begins with a configurable timeout.
// If a second signal arrives during cleanup, the daemon force-exits immediately.
func (d *Daemon) Run() error {
	d.logger.Println("starting Belay daemon...")

	if err := d.init(); err != nil {
		return fmt.Errorf("init: %w", err)
	}

	if err := d.writePID(); err != nil {
		d.cleanup()
		return fmt.Errorf("write PID: %w", err)
	}

	d.registry.Start()

	if err := d.watcher.Start(); err != nil {
		d.removePID()
		d.cleanup()
		return fmt.Errorf("start watcher: %w", err)
	}

	d.apiServer = api.New(d.cfg, d.idx, d.objStore, d.registry, d.logger, d.HandleRecordedEvent, d.version, d)
	if err := d.apiServer.Start(); err != nil {
		d.logger.Printf("warning: API server failed to start: %v", err)
	}

	d.stopWatchdog = make(chan struct{})
	d.watchdogDone = make(chan struct{})
	go d.runWatchdog()

	d.logger.Printf("daemon running (PID %d), watching %s", os.Getpid(), d.cfg.ProjectRoot)
	d.logger.Printf("session detectors: %v", d.registry.DetectorNames())

	// Use a buffered channel so the second signal is not lost even if we are
	// busy in the select below. signalChannel registers both SIGINT and SIGTERM;
	// the channel has capacity 2 so the first and second signals are queued.
	sigCh := shutdownSignalChannel()

	// Block until the first shutdown signal.
	sig := <-sigCh
	d.logger.Printf("received %v, starting graceful shutdown...", sig)

	// Determine shutdown timeout.
	timeout := time.Duration(d.cfg.Daemon.ShutdownTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = time.Duration(config.DefaultShutdownTimeoutSec) * time.Second
	}

	// Start cleanup in a goroutine so we can enforce the timeout and detect
	// a second signal (force-exit).
	cleanupDone := make(chan struct{})
	go func() {
		d.cleanup()
		d.removePID()
		close(cleanupDone)
	}()

	select {
	case <-cleanupDone:
		d.logger.Println("graceful shutdown complete")
		return nil
	case <-time.After(timeout):
		d.logger.Printf("shutdown timed out after %s, force-exiting", timeout)
		os.Exit(1)
		return nil // unreachable
	case sig := <-sigCh:
		d.logger.Printf("received second signal %v during shutdown, force-exiting", sig)
		os.Exit(1)
		return nil // unreachable
	}
}

func (d *Daemon) init() error {
	var err error

	d.objStore, err = store.NewStore(d.cfg.ObjectsDir(), d.cfg.Storage.CompressionEnabled)
	if err != nil {
		return fmt.Errorf("init object store: %w", err)
	}

	d.logWriter, err = eventlog.NewWriter(d.cfg.EventsDir(), d.cfg.Storage.SegmentMaxBytes)
	if err != nil {
		return fmt.Errorf("init event log: %w", err)
	}

	d.idx, err = index.Open(d.cfg.IndexPath())
	if err != nil {
		return fmt.Errorf("init index: %w", err)
	}

	// Check index integrity; auto-rebuild if corrupted
	if err := index.CheckIntegrity(d.cfg.IndexPath()); err != nil {
		d.logger.Printf("WARNING: index integrity check failed: %v", err)
		d.logger.Printf("auto-rebuilding index from event log...")
		d.idx.Close()
		d.idx = nil

		result, rebuildErr := index.Rebuild(d.cfg.IndexPath(), d.cfg.EventsDir(), d.logger)
		if rebuildErr != nil {
			return fmt.Errorf("auto-rebuild index failed: %w", rebuildErr)
		}

		d.logger.Printf("index rebuilt: %d events, %d sessions, %d corrupted frames skipped (%s)",
			result.EventsIndexed, result.SessionsRebuilt, result.CorruptedSkipped, result.Elapsed.Round(time.Millisecond))

		// Re-open the freshly rebuilt index
		d.idx, err = index.Open(d.cfg.IndexPath())
		if err != nil {
			return fmt.Errorf("re-open rebuilt index: %w", err)
		}
	}

	d.matcher, err = ignore.NewMatcher(d.cfg.ProjectRoot)
	if err != nil {
		return fmt.Errorf("init ignore matcher: %w", err)
	}

	d.sessionFiles = make(map[string]map[string]bool)

	claudeDetector := session.NewClaudeDetector(d.cfg.ProjectRoot)
	d.registry = session.NewRegistry(claudeDetector)

	d.registry.SetOnSessionStart(func(s *schema.Session) {
		d.logger.Printf("session started: %s (%s, PID %d)", s.SessionID, s.ToolName, s.PID)
		if err := d.idx.UpsertSession(s); err != nil {
			d.logger.Printf("warning: index session start failed: %v", err)
		}

		metaEvent := &schema.Event{
			EventID:   schema.NewEventID(),
			Version:   schema.SchemaVersion,
			FilePath:  ".belay/sessions",
			Op:        schema.OpCreate,
			SessionID: s.SessionID,
			Metadata: map[string]string{
				"event_type": "session_start",
				"tool_name":  s.ToolName,
				"pid":        strconv.Itoa(s.PID),
			},
		}
		metaEvent.SetTimestamp(time.Now())
		d.logWriter.Append(metaEvent)
	})

	d.registry.SetOnSessionEnd(func(s *schema.Session) {
		d.logger.Printf("session ended: %s (%s, status: %s)", s.SessionID, s.ToolName, s.Status.String())
		if err := d.idx.UpsertSession(s); err != nil {
			d.logger.Printf("warning: index session end failed: %v", err)
		}

		metaEvent := &schema.Event{
			EventID:   schema.NewEventID(),
			Version:   schema.SchemaVersion,
			FilePath:  ".belay/sessions",
			Op:        schema.OpModify,
			SessionID: s.SessionID,
			Metadata: map[string]string{
				"event_type": "session_end",
				"tool_name":  s.ToolName,
				"status":     s.Status.String(),
			},
		}
		metaEvent.SetTimestamp(time.Now())
		d.logWriter.Append(metaEvent)
	})

	d.burst = newBurstDetector(func(event *schema.Event) {
		d.processEvent(event)
	})

	d.watcher, err = watcher.New(d.cfg, d.objStore, d.matcher)
	if err != nil {
		return fmt.Errorf("init watcher: %w", err)
	}

	d.watcher.OnEvent(func(event *schema.Event) {
		d.handleEvent(event)
	})

	d.stopCheckpoint = make(chan struct{})
	d.checkpointDone = make(chan struct{})
	go d.runCheckpointLoop()

	d.cleanupStaleSessions()

	d.stopCleanup = make(chan struct{})
	d.cleanupDone = make(chan struct{})
	go d.runSessionCleanupLoop()

	return nil
}

func (d *Daemon) handleEvent(event *schema.Event) {
	if event.SessionID == "" {
		writeEvent := &session.FileWriteEvent{
			FilePath:  event.FilePath,
			Operation: event.Op,
			Timestamp: event.Timestamp(),
			Size:      event.ContentSize,
		}
		sid, conf, method := d.registry.Attribute(writeEvent)
		if sid != "" {
			event.SessionID = sid
			event.AttributionConfidence = conf
			event.Attribution = method
		}
	}

	if d.burst != nil && d.burst.handle(event) {
		return
	}

	d.processEvent(event)
}

func (d *Daemon) processEvent(event *schema.Event) {
	if event.PreviousHash == "" && event.Op != schema.OpCreate {
		if prev, err := d.idx.LatestEvent(event.FilePath); err == nil && prev != nil {
			event.PreviousHash = prev.ContentHash
		}
	}

	if err := d.logWriter.Append(event); err != nil {
		d.logger.Printf("error writing event for %s: %v", event.FilePath, err)
		return
	}

	segFile := d.logWriter.CurrentSegment()
	segOffset := d.logWriter.CurrentOffset()
	if err := d.idx.IndexEvent(event, segFile, segOffset); err != nil {
		d.logger.Printf("warning: index event failed: %v", err)
	}

	if event.SessionID != "" {
		s := d.registry.GetSession(event.SessionID)
		if s == nil {
			toolName := "unknown"
			if event.Metadata != nil {
				if tn, ok := event.Metadata["tool_name"]; ok {
					toolName = tn
				}
			}
			s = &schema.Session{
				SessionID: event.SessionID,
				ToolName:  toolName,
				Status:    schema.SessionActive,
				StartedAt: event.Timestamp(),
			}
			d.logger.Printf("auto-registered hook session: %s (%s)", event.SessionID, toolName)
		}
		s.EventCount++
		if !d.sessionHasFile(event.SessionID, event.FilePath) {
			s.FilesChanged++
		}
		d.trackSessionFile(event.SessionID, event.FilePath)
		d.idx.UpsertSession(s)
	}

	if d.apiServer != nil {
		d.apiServer.Broadcast(&api.EventMessage{
			Type:      "file_event",
			Timestamp: event.TimestampNano,
			Data: map[string]interface{}{
				"event_id":   event.EventID,
				"file_path":  event.FilePath,
				"op":         event.Op.String(),
				"session_id": event.SessionID,
				"size":       event.ContentSize,
			},
		})
	}
}

func (d *Daemon) sessionHasFile(sessionID, filePath string) bool {
	d.sessionFilesMu.RLock()
	defer d.sessionFilesMu.RUnlock()
	files, ok := d.sessionFiles[sessionID]
	if !ok {
		return false
	}
	return files[filePath]
}

func (d *Daemon) trackSessionFile(sessionID, filePath string) {
	d.sessionFilesMu.Lock()
	defer d.sessionFilesMu.Unlock()
	files, ok := d.sessionFiles[sessionID]
	if !ok {
		files = make(map[string]bool)
		d.sessionFiles[sessionID] = files
	}
	files[filePath] = true
}

// HandleRecordedEvent processes an event pushed via the HTTP API record endpoint.
func (d *Daemon) HandleRecordedEvent(event *schema.Event) {
	d.handleEvent(event)
}

func (d *Daemon) runCheckpointLoop() {
	defer close(d.checkpointDone)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			start := time.Now()
			if err := d.idx.Checkpoint(); err != nil {
				d.logger.Printf("warning: WAL checkpoint failed: %v", err)
			} else {
				d.logger.Printf("WAL checkpoint completed in %s", time.Since(start).Round(time.Millisecond))
			}
		case <-d.stopCheckpoint:
			return
		}
	}
}

func (d *Daemon) runSessionCleanupLoop() {
	defer close(d.cleanupDone)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.cleanupStaleSessions()
		case <-d.stopCleanup:
			return
		}
	}
}

func (d *Daemon) cleanupStaleSessions() {
	active, err := d.idx.AllActiveSessions()
	if err != nil {
		d.logger.Printf("[SESSION-CLEANUP] error querying active sessions: %v", err)
		return
	}

	if len(active) == 0 {
		return
	}

	now := time.Now()
	cleaned := 0
	for _, s := range active {
		if s.PID == 0 {
			if err := d.idx.MarkSessionCrashed(s.SessionID, now); err != nil {
				d.logger.Printf("[SESSION-CLEANUP] error marking PID-0 session %s: %v", s.SessionID, err)
				continue
			}
			cleaned++
		} else if !isProcessAlive(s.PID) {
			if err := d.idx.MarkSessionCrashed(s.SessionID, now); err != nil {
				d.logger.Printf("[SESSION-CLEANUP] error marking session %s: %v", s.SessionID, err)
				continue
			}
			cleaned++
		}
	}

	if cleaned > 0 {
		d.logger.Printf("session cleanup: marked %d stale sessions as crashed", cleaned)
	}
}

func (d *Daemon) WatcherHealth() map[string]interface{} {
	h := d.watcher.Health()
	status, errMsg := d.evaluateWatcherHealth(h)
	result := map[string]interface{}{
		"status": string(status),
	}
	if h.LastEventAt != nil {
		result["last_event_at"] = h.LastEventAt.Format(time.RFC3339Nano)
	}
	if h.StartedAt != nil {
		result["started_at"] = h.StartedAt.Format(time.RFC3339Nano)
	}
	if errMsg != "" {
		result["error"] = errMsg
	} else if h.Error != "" {
		result["error"] = h.Error
	}
	return result
}

func (d *Daemon) OverallStatus() string {
	h := d.watcher.Health()
	status, _ := d.evaluateWatcherHealth(h)
	if status == watcher.StatusRunning {
		return "ok"
	}
	return "degraded"
}

func (d *Daemon) evaluateWatcherHealth(h watcher.WatcherHealth) (watcher.WatcherStatus, string) {
	if h.Status != watcher.StatusRunning {
		return h.Status, h.Error
	}

	now := time.Now()

	if h.LastEventAt != nil {
		if now.Sub(*h.LastEventAt) > watcher.StaleEventThreshold {
			return watcher.StatusDegraded, "no file events detected in over 30 minutes - watcher may be stalled"
		}
		return watcher.StatusRunning, ""
	}

	if h.StartedAt != nil && now.Sub(*h.StartedAt) > watcher.StaleEventThreshold {
		return watcher.StatusDegraded, "no file events detected since startup over 30 minutes ago - watcher may be stalled"
	}

	return watcher.StatusRunning, ""
}

func (d *Daemon) runWatchdog() {
	defer close(d.watchdogDone)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			h := d.watcher.Health()
			if h.Status == watcher.StatusRunning {
				continue
			}

			d.watchdogRestarts++
			d.logger.Printf("watchdog: watcher is %s (error: %s), attempting restart #%d",
				h.Status, h.Error, d.watchdogRestarts)

			if err := d.watcher.Restart(); err != nil {
				d.logger.Printf("watchdog: restart failed: %v", err)
			} else {
				d.logger.Printf("watchdog: watcher restarted successfully (attempt #%d)", d.watchdogRestarts)
			}
		case <-d.stopWatchdog:
			return
		}
	}
}

// cleanup shuts down all subsystems in a safe order:
//  1. Stop the watcher (no new filesystem events)
//  2. Stop the API server (no new HTTP requests)
//  3. Stop the session registry (no new session callbacks)
//  4. Stop the periodic checkpoint loop
//  5. Flush the event log writer (fsync buffered writes to disk)
//  6. Run a final WAL checkpoint on the SQLite index
//  7. Close the SQLite index
//  8. Close the object store
//
// Each step is logged so crash investigation can see how far cleanup got.
func (d *Daemon) cleanup() {
	start := time.Now()

	if d.stopWatchdog != nil {
		d.logger.Println("shutdown: stopping watchdog...")
		close(d.stopWatchdog)
		<-d.watchdogDone
		d.logger.Println("shutdown: watchdog stopped")
	}

	if d.watcher != nil {
		d.logger.Println("shutdown: stopping file watcher...")
		if err := d.watcher.Stop(); err != nil {
			d.logger.Printf("shutdown: watcher stop error: %v", err)
		} else {
			d.logger.Println("shutdown: file watcher stopped")
		}
	}

	if d.burst != nil {
		d.logger.Println("shutdown: flushing burst buffer...")
		d.burst.stop()
		d.logger.Println("shutdown: burst buffer flushed")
	}

	// 2. Stop the API server so no new requests arrive.
	if d.apiServer != nil {
		d.logger.Println("shutdown: stopping API server...")
		if err := d.apiServer.Stop(); err != nil {
			d.logger.Printf("shutdown: API server stop error: %v", err)
		} else {
			d.logger.Println("shutdown: API server stopped")
		}
	}

	// 3. Stop the session registry (stops polling, fires final session-end callbacks
	// which may append events to the log writer -- this must happen before log close).
	if d.registry != nil {
		d.logger.Println("shutdown: stopping session registry...")
		d.registry.Stop()
		d.logger.Println("shutdown: session registry stopped")
	}

	if d.stopCleanup != nil {
		d.logger.Println("shutdown: stopping session cleanup loop...")
		close(d.stopCleanup)
		<-d.cleanupDone
		d.logger.Println("shutdown: session cleanup loop stopped")
	}

	// 4. Stop the periodic WAL checkpoint loop.
	if d.stopCheckpoint != nil {
		d.logger.Println("shutdown: stopping checkpoint loop...")
		close(d.stopCheckpoint)
		<-d.checkpointDone
		d.logger.Println("shutdown: checkpoint loop stopped")
	}

	// 5. Flush and close the event log writer (fsync to ensure all events are on disk).
	if d.logWriter != nil {
		d.logger.Println("shutdown: flushing event log...")
		if err := d.logWriter.Sync(); err != nil {
			d.logger.Printf("shutdown: event log sync error: %v", err)
		}
		if err := d.logWriter.Close(); err != nil {
			d.logger.Printf("shutdown: event log close error: %v", err)
		} else {
			d.logger.Println("shutdown: event log flushed and closed")
		}
	}

	// 6. Final WAL checkpoint + close the SQLite index.
	if d.idx != nil {
		d.logger.Println("shutdown: running final WAL checkpoint...")
		if err := d.idx.Checkpoint(); err != nil {
			d.logger.Printf("shutdown: final WAL checkpoint error: %v", err)
		} else {
			d.logger.Println("shutdown: final WAL checkpoint completed")
		}

		d.logger.Println("shutdown: closing index...")
		if err := d.idx.Close(); err != nil {
			d.logger.Printf("shutdown: index close error: %v", err)
		} else {
			d.logger.Println("shutdown: index closed")
		}
	}

	// 7. Close the object store.
	if d.objStore != nil {
		d.logger.Println("shutdown: closing object store...")
		d.objStore.Close()
		d.logger.Println("shutdown: object store closed")
	}

	d.logger.Printf("shutdown: cleanup completed in %s", time.Since(start).Round(time.Millisecond))
}

func (d *Daemon) writePID() error {
	pid := os.Getpid()
	return os.WriteFile(d.cfg.PIDPath(), []byte(strconv.Itoa(pid)), 0644)
}

func (d *Daemon) removePID() {
	os.Remove(d.cfg.PIDPath())
}

// IsRunning checks whether a daemon is currently running by inspecting the PID file.
func IsRunning(cfg *config.Config) (bool, int) {
	data, err := os.ReadFile(cfg.PIDPath())
	if err != nil {
		return false, 0
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return false, 0
	}

	if !isProcessAlive(pid) {
		os.Remove(cfg.PIDPath())
		return false, 0
	}

	return true, pid
}

// Stop terminates the running daemon process.
func Stop(cfg *config.Config) error {
	running, pid := IsRunning(cfg)
	if !running {
		return fmt.Errorf("daemon is not running")
	}

	if err := terminateProcess(pid); err != nil {
		return fmt.Errorf("terminate process %d: %w", pid, err)
	}

	return nil
}
