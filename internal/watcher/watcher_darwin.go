//go:build darwin

package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/ignore"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"

	"github.com/fsnotify/fsevents"
)

// Watcher monitors filesystem changes using macOS FSEvents for efficient recursive watching.
type Watcher struct {
	watcherBase
	es           *fsevents.EventStream
	filteredMu   sync.Mutex
	filteredSeen map[string]bool
}

// New creates a new Watcher for the project root using FSEvents.
func New(cfg *config.Config, objStore *store.Store, matcher *ignore.Matcher) (*Watcher, error) {
	w := &Watcher{
		filteredSeen: make(map[string]bool),
	}
	initBase(&w.watcherBase, cfg, objStore, matcher)
	if resolved, err := filepath.EvalSymlinks(w.cfg.ProjectRoot); err == nil {
		w.cfg.ProjectRoot = resolved
	}
	return w, nil
}

// Start begins watching for filesystem changes via FSEvents.
func (w *Watcher) Start() error {
	dev, err := fsevents.DeviceForPath(w.cfg.ProjectRoot)
	if err != nil {
		w.health.setError(StatusError, err.Error())
		return err
	}

	w.es = &fsevents.EventStream{
		Paths:   []string{w.cfg.ProjectRoot},
		Latency: 200 * time.Millisecond,
		Device:  dev,
		Flags:   fsevents.FileEvents | fsevents.WatchRoot,
	}

	if err := w.es.Start(); err != nil {
		return fmt.Errorf("fsevents start: %w", err)
	}
	w.health.setStatus(StatusRunning)
	w.logger.Printf("watching %s via FSEvents (recursive, single stream)", w.cfg.ProjectRoot)

	w.ticker = time.NewTicker(w.debounceMs)

	w.wg.Add(2)
	go w.processEvents()
	go w.processPending()

	return nil
}

// Stop halts the FSEvents stream and flushes any pending events.
func (w *Watcher) Stop() error {
	w.health.mu.Lock()
	if w.health.status == StatusStopped {
		w.health.mu.Unlock()
		return nil
	}
	w.health.status = StatusStopped
	w.health.mu.Unlock()

	close(w.done)
	w.ticker.Stop()
	if w.es != nil {
		w.es.Stop()
	}
	w.wg.Wait()
	w.flushPending()
	return nil
}

func (w *Watcher) Restart() error {
	_ = w.Stop()
	w.resetForRestart()
	w.filteredMu.Lock()
	w.filteredSeen = make(map[string]bool)
	w.filteredMu.Unlock()
	return w.Start()
}

func (w *Watcher) processEvents() {
	defer w.wg.Done()

	for {
		select {
		case <-w.done:
			return
		case events, ok := <-w.es.Events:
			if !ok {
				w.health.setError(StatusError, "FSEvents channel closed unexpectedly")
				w.logger.Printf("ERROR: FSEvents channel closed unexpectedly")
				return
			}
			for _, ev := range events {
				w.handleFSEvent(ev)
			}
		}
	}
}

func (w *Watcher) handleFSEvent(ev fsevents.Event) {
	absPath := ev.Path
	if !filepath.IsAbs(absPath) {
		absPath = "/" + absPath
	}

	w.trackFSEvent()

	relPath, err := filepath.Rel(w.cfg.ProjectRoot, absPath)
	if err != nil || relPath == "." {
		return
	}

	if w.shouldIgnoreRel(relPath) {
		w.logFilteredOnce("ignored by .belayignore", relPath, ev.Flags)
		return
	}

	if ev.Flags&fsevents.ItemIsDir != 0 {
		w.logFilteredOnce("ItemIsDir flag", relPath, ev.Flags)
		return
	}

	if info, err := os.Lstat(absPath); err == nil && info.IsDir() {
		w.logFilteredOnce("lstat says directory", relPath, ev.Flags)
		return
	}

	op := mapFSEventOp(ev.Flags, absPath)
	if op == 0 {
		w.logFilteredOnce("mapFSEventOp returned 0", relPath, ev.Flags)
		return
	}

	w.queueEvent(relPath, op)
}

func (w *Watcher) logFilteredOnce(reason, relPath string, flags fsevents.EventFlags) {
	w.filteredMu.Lock()
	if w.filteredSeen[reason] {
		w.filteredMu.Unlock()
		return
	}
	w.filteredSeen[reason] = true
	w.filteredMu.Unlock()
	w.logger.Printf("first filtered event: reason=%q path=%s flags=0x%x", reason, relPath, uint32(flags))
}

func mapFSEventOp(flags fsevents.EventFlags, absPath string) schema.Operation {
	switch {
	case flags&fsevents.ItemRemoved != 0:
		return schema.OpDelete
	case flags&fsevents.ItemRenamed != 0:
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			return schema.OpDelete
		}
		return schema.OpModify
	case flags&fsevents.ItemCreated != 0:
		return schema.OpCreate
	case flags&fsevents.ItemModified != 0:
		return schema.OpModify
	case flags&(fsevents.ItemInodeMetaMod|fsevents.ItemChangeOwner|fsevents.ItemXattrMod) != 0:
		return 0
	default:
		if flags&fsevents.ItemIsFile != 0 {
			return schema.OpModify
		}
		return 0
	}
}

// WatchedDirs returns the directories being watched, relative to the project root.
func (w *Watcher) WatchedDirs() []string {
	if w.es == nil {
		return nil
	}
	var dirs []string
	for _, p := range w.es.Paths {
		rel, err := filepath.Rel(w.cfg.ProjectRoot, p)
		if err == nil {
			dirs = append(dirs, rel)
		}
	}
	return dirs
}
