// Package watcher monitors filesystem changes and emits events. It uses FSEvents on macOS
// and fsnotify on other platforms.
package watcher

import (
	"fmt"
	"log"
	"os"
	"os/exec"
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
	wtCache *worktreeCache

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
	wb.wtCache = newWorktreeCache(3 * time.Second)
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
			b.logger.Printf("warning: cannot capture %s: %v", pe.path, err)
			return
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

type worktreeCache struct {
	mu         sync.RWMutex
	dirtyFiles map[string]map[string]bool
	lastCheck  map[string]time.Time
	ttl        time.Duration
}

func newWorktreeCache(ttl time.Duration) *worktreeCache {
	return &worktreeCache{
		dirtyFiles: make(map[string]map[string]bool),
		lastCheck:  make(map[string]time.Time),
		ttl:        ttl,
	}
}

func (wc *worktreeCache) isDirty(worktreeName, fileRelPath string) bool {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	if files, ok := wc.dirtyFiles[worktreeName]; ok {
		return files[fileRelPath]
	}
	return false
}

func (wc *worktreeCache) refresh(worktreeName, worktreeAbsPath string) {
	dirty := make(map[string]bool)

	diffCmd := exec.Command("git", "-C", worktreeAbsPath, "diff", "--name-only")
	if out, err := diffCmd.Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" {
				dirty[line] = true
			}
		}
	}

	diffStagedCmd := exec.Command("git", "-C", worktreeAbsPath, "diff", "--name-only", "--staged")
	if out, err := diffStagedCmd.Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" {
				dirty[line] = true
			}
		}
	}

	untrackedCmd := exec.Command("git", "-C", worktreeAbsPath, "ls-files", "--others", "--exclude-standard")
	if out, err := untrackedCmd.Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" {
				dirty[line] = true
			}
		}
	}

	wc.mu.Lock()
	wc.dirtyFiles[worktreeName] = dirty
	wc.lastCheck[worktreeName] = time.Now()
	wc.mu.Unlock()
}

func (wc *worktreeCache) needsRefresh(worktreeName string) bool {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	last, ok := wc.lastCheck[worktreeName]
	if !ok {
		return true
	}
	return time.Since(last) > wc.ttl
}

func (wc *worktreeCache) cleanup(worktreeName string) {
	wc.mu.Lock()
	delete(wc.dirtyFiles, worktreeName)
	delete(wc.lastCheck, worktreeName)
	wc.mu.Unlock()
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

	if op == schema.OpModify || op == schema.OpDelete {
		return canonicalPath, meta, false
	}

	// CREATE events: filter out checkout burst files using git-status
	if b.wtCache != nil {
		if b.wtCache.needsRefresh(worktreeName) {
			wtAbsPath := filepath.Join(b.cfg.ProjectRoot, worktreePrefix, worktreeName)
			b.wtCache.refresh(worktreeName, wtAbsPath)
		}
		if !b.wtCache.isDirty(worktreeName, canonicalPath) {
			return "", nil, true
		}
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
