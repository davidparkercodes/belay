// Package watcher monitors filesystem changes and emits events. It uses FSEvents on macOS
// and fsnotify on other platforms.
package watcher

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/ignore"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"
)

type WatcherStatus string

const (
	StatusRunning  WatcherStatus = "running"
	StatusStopped  WatcherStatus = "stopped"
	StatusError    WatcherStatus = "error"
	StatusDegraded WatcherStatus = "degraded"

	StaleEventThreshold = 30 * time.Minute
)

type WatcherHealth struct {
	Status      WatcherStatus `json:"status"`
	LastEventAt *time.Time    `json:"last_event_at,omitempty"`
	StartedAt   *time.Time    `json:"started_at,omitempty"`
	Error       string        `json:"error,omitempty"`
}

type healthState struct {
	mu          sync.RWMutex
	status      WatcherStatus
	lastEventAt *time.Time
	startedAt   *time.Time
	errMsg      string
}

func (h *healthState) setStatus(s WatcherStatus) {
	h.mu.Lock()
	h.status = s
	if s == StatusRunning {
		now := time.Now()
		h.startedAt = &now
	}
	h.mu.Unlock()
}

func (h *healthState) setError(s WatcherStatus, msg string) {
	h.mu.Lock()
	h.status = s
	h.errMsg = msg
	h.mu.Unlock()
}

func (h *healthState) recordEvent() {
	now := time.Now()
	h.mu.Lock()
	h.lastEventAt = &now
	h.mu.Unlock()
}

func (h *healthState) snapshot() WatcherHealth {
	h.mu.RLock()
	defer h.mu.RUnlock()
	wh := WatcherHealth{
		Status: h.status,
		Error:  h.errMsg,
	}
	if h.lastEventAt != nil {
		t := *h.lastEventAt
		wh.LastEventAt = &t
	}
	if h.startedAt != nil {
		t := *h.startedAt
		wh.StartedAt = &t
	}
	return wh
}

// EventHandler is a callback invoked when a filesystem event is captured.
type EventHandler func(event *schema.Event)

type pendingEvent struct {
	path      string
	op        schema.Operation
	timestamp time.Time
}

type watcherBase struct {
	cfg      *config.Config
	matcher  *ignore.Matcher
	objStore *store.Store

	debounceMs time.Duration
	pending    map[string]*pendingEvent
	pendingMu  sync.Mutex
	ticker     *time.Ticker

	handlers []EventHandler
	mu       sync.RWMutex

	done chan struct{}
	wg   sync.WaitGroup

	logger  *log.Logger
	health  *healthState
	wtTracker *worktreeTracker

	fsRawCount   int64
	fsQueueCount int64
	statsMu      sync.Mutex
}

func initBase(wb *watcherBase, cfg *config.Config, objStore *store.Store, matcher *ignore.Matcher) {
	wb.cfg = cfg
	wb.matcher = matcher
	wb.objStore = objStore
	wb.debounceMs = time.Duration(cfg.Watcher.DebounceMs) * time.Millisecond
	wb.pending = make(map[string]*pendingEvent)
	wb.done = make(chan struct{})
	wb.logger = log.New(os.Stderr, "[belay-watcher] ", log.LstdFlags)
	wb.health = &healthState{status: StatusStopped}
	wb.wtTracker = newWorktreeTracker(10*time.Second, 30*time.Second)
}

func (b *watcherBase) Health() WatcherHealth {
	return b.health.snapshot()
}

// OnEvent registers a handler to be called for each debounced filesystem event.
func (b *watcherBase) OnEvent(handler EventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, handler)
}

func (b *watcherBase) trackFSEvent() {
	b.statsMu.Lock()
	b.fsRawCount++
	count := b.fsRawCount
	b.statsMu.Unlock()
	if count == 1 {
		b.logger.Printf("first FSEvent received from OS")
	}
	if count%1000 == 0 {
		b.statsMu.Lock()
		queued := b.fsQueueCount
		b.statsMu.Unlock()
		b.logger.Printf("FSEvents stats: %d raw from OS, %d queued after filtering", count, queued)
	}
}

func (b *watcherBase) queueEvent(relPath string, op schema.Operation) {
	b.statsMu.Lock()
	b.fsQueueCount++
	b.statsMu.Unlock()
	b.pendingMu.Lock()
	b.pending[relPath] = &pendingEvent{
		path:      relPath,
		op:        op,
		timestamp: time.Now(),
	}
	b.pendingMu.Unlock()
	b.health.recordEvent()
}

func (b *watcherBase) resetForRestart() {
	b.done = make(chan struct{})
	b.pending = make(map[string]*pendingEvent)
}

func (b *watcherBase) processPending() {
	defer b.wg.Done()

	for {
		select {
		case <-b.done:
			return
		case <-b.ticker.C:
			b.flushPending()
		}
	}
}

func (b *watcherBase) flushPending() {
	b.pendingMu.Lock()
	now := time.Now()
	var ready []*pendingEvent
	for path, pe := range b.pending {
		if now.Sub(pe.timestamp) >= b.debounceMs {
			ready = append(ready, pe)
			delete(b.pending, path)
		}
	}
	b.pendingMu.Unlock()

	for _, pe := range ready {
		b.processFileEvent(pe)
	}
}

func (b *watcherBase) processFileEvent(pe *pendingEvent) {
	filteredPath, wtMeta, skip := b.shouldFilterWorktreeEvent(pe.path, pe.op)
	if skip {
		return
	}

	absPath := filepath.Join(b.cfg.ProjectRoot, pe.path)

	event := &schema.Event{
		EventID:  schema.NewEventID(),
		Version:  schema.SchemaVersion,
		FilePath: filteredPath,
		Op:       pe.op,
	}
	event.SetTimestamp(pe.timestamp)

	if wtMeta != nil {
		event.Metadata = wtMeta
	}

	if pe.op != schema.OpDelete {
		if err := b.captureContent(absPath, event); err != nil {
			b.logger.Printf("warning: content capture failed for %s: %v", pe.path, err)
		}
	}

	b.mu.RLock()
	handlers := b.handlers
	b.mu.RUnlock()

	for _, h := range handlers {
		h(event)
	}
}

func (b *watcherBase) captureContent(absPath string, event *schema.Event) error {
	// Check file size before reading to avoid memory issues with large files.
	maxBytes := int64(b.cfg.Watcher.MaxFileSizeMB) * 1024 * 1024
	if maxBytes > 0 {
		info, err := os.Stat(absPath)
		if err != nil {
			return fmt.Errorf("stat file: %w", err)
		}
		if info.Size() > maxBytes {
			b.logger.Printf("skipping content capture for %s (%d MB exceeds %d MB limit)",
				event.FilePath, info.Size()/(1024*1024), b.cfg.Watcher.MaxFileSizeMB)
			return nil
		}
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	hash, size, err := b.objStore.Put(data)
	if err != nil {
		return fmt.Errorf("store content: %w", err)
	}

	event.ContentHash = hash
	event.ContentSize = size
	return nil
}

func (b *watcherBase) shouldIgnoreRel(relPath string) bool {
	if b.cfg.Watcher.ExcludeHidden && isHidden(relPath) {
		return true
	}
	return b.matcher.ShouldIgnore(relPath)
}

const worktreePrefix = ".claude/worktrees/"

type worktreeTracker struct {
	mu            sync.RWMutex
	firstSeen     map[string]time.Time
	cleanedUpAt   map[string]time.Time
	burstWindow   time.Duration
	cleanupWindow time.Duration
}

func newWorktreeTracker(burstWindow, cleanupWindow time.Duration) *worktreeTracker {
	return &worktreeTracker{
		firstSeen:     make(map[string]time.Time),
		cleanedUpAt:   make(map[string]time.Time),
		burstWindow:   burstWindow,
		cleanupWindow: cleanupWindow,
	}
}

func (wt *worktreeTracker) isInBurstWindow(worktreeName string) bool {
	wt.mu.Lock()
	defer wt.mu.Unlock()
	first, ok := wt.firstSeen[worktreeName]
	if !ok {
		wt.firstSeen[worktreeName] = time.Now()
		return true
	}
	return time.Since(first) < wt.burstWindow
}

func (wt *worktreeTracker) recordCleanup(worktreeName string) {
	wt.mu.Lock()
	defer wt.mu.Unlock()
	wt.cleanedUpAt[worktreeName] = time.Now()
	delete(wt.firstSeen, worktreeName)
}

func (wt *worktreeTracker) isRecentlyCleanedUp(worktreeName string) bool {
	wt.mu.RLock()
	defer wt.mu.RUnlock()
	t, ok := wt.cleanedUpAt[worktreeName]
	if !ok {
		return false
	}
	return time.Since(t) < wt.cleanupWindow
}

func (wt *worktreeTracker) cleanup(worktreeName string) {
	wt.mu.Lock()
	delete(wt.firstSeen, worktreeName)
	delete(wt.cleanedUpAt, worktreeName)
	wt.mu.Unlock()
}

func isWorktreePath(relPath string) bool {
	return strings.HasPrefix(filepath.ToSlash(relPath), worktreePrefix)
}

func parseWorktreePath(relPath string) (worktreeName string, canonicalPath string, ok bool) {
	normalized := filepath.ToSlash(relPath)
	if !strings.HasPrefix(normalized, worktreePrefix) {
		return "", "", false
	}
	rest := normalized[len(worktreePrefix):]
	idx := strings.Index(rest, "/")
	if idx < 0 {
		return "", "", false
	}
	worktreeName = rest[:idx]
	canonicalPath = rest[idx+1:]
	if canonicalPath == "" {
		return "", "", false
	}
	return worktreeName, canonicalPath, true
}

func (b *watcherBase) shouldFilterWorktreeEvent(relPath string, op schema.Operation) (filteredPath string, metadata map[string]string, skip bool) {
	if !isWorktreePath(relPath) {
		return relPath, nil, false
	}

	b.logger.Printf("worktree event: %s %s", op, relPath)

	worktreeName, canonicalPath, ok := parseWorktreePath(relPath)
	if !ok {
		return relPath, nil, false
	}

	meta := map[string]string{"worktree": worktreeName}

	if op == schema.OpModify {
		return canonicalPath, meta, false
	}

	if op == schema.OpDelete {
		// Worktree removal (`git worktree remove`) deletes every file in the worktree,
		// which would otherwise get mapped to the canonical path and recorded as phantom
		// DELETE events against files that still exist on main. When the worktree root
		// is already gone — or we recently observed it going away — treat subsequent
		// DELETEs as cleanup cascade and suppress them.
		if b.wtTracker != nil && b.wtTracker.isRecentlyCleanedUp(worktreeName) {
			return "", nil, true
		}
		wtRootAbs := filepath.Join(b.cfg.ProjectRoot, worktreePrefix, worktreeName)
		if _, err := os.Stat(wtRootAbs); os.IsNotExist(err) {
			if b.wtTracker != nil {
				b.wtTracker.recordCleanup(worktreeName)
			}
			return "", nil, true
		}
		return canonicalPath, meta, false
	}

	// CREATE events: filter out checkout burst during initial worktree population.
	// git worktree add produces thousands of CREATE events as it checks out
	// files. We suppress CREATEs for a burst window after first seeing a
	// worktree, then let everything through so agent-written files are captured.
	if b.wtTracker != nil && b.wtTracker.isInBurstWindow(worktreeName) {
		return "", nil, true
	}

	return canonicalPath, meta, false
}

func isHidden(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
	}
	return false
}
