//go:build !darwin

package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/ignore"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors filesystem changes using fsnotify for cross-platform support.
type Watcher struct {
	watcherBase
	fsw *fsnotify.Watcher
}

// New creates a new Watcher for the project root using fsnotify.
func New(cfg *config.Config, objStore *store.Store, matcher *ignore.Matcher) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}

	w := &Watcher{fsw: fsw}
	initBase(&w.watcherBase, cfg, objStore, matcher)
	return w, nil
}

// Start begins watching for filesystem changes by walking the project tree and adding watchers.
func (w *Watcher) Start() error {
	if w.fsw == nil {
		fsw, err := fsnotify.NewWatcher()
		if err != nil {
			w.health.setError(StatusError, err.Error())
			return fmt.Errorf("create fsnotify watcher: %w", err)
		}
		w.fsw = fsw
	}

	const maxWatchDirs = 2048

	watchCount := 0
	err := filepath.Walk(w.cfg.ProjectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if !info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(w.cfg.ProjectRoot, path)
		if err != nil {
			return nil
		}

		if relPath != "." && isHidden(relPath) {
			return filepath.SkipDir
		}

		if relPath != "." && w.matcher.ShouldIgnore(relPath+"/") {
			return filepath.SkipDir
		}

		depth := strings.Count(relPath, string(filepath.Separator))
		if relPath != "." && depth > 6 {
			return filepath.SkipDir
		}

		if watchCount >= maxWatchDirs {
			return filepath.SkipDir
		}

		if err := w.fsw.Add(path); err != nil {
			w.logger.Printf("warning: cannot watch %s: %v", path, err)
			return nil
		}
		watchCount++
		return nil
	})
	if err != nil {
		w.health.setError(StatusError, err.Error())
		return fmt.Errorf("walk project root: %w", err)
	}

	w.health.setStatus(StatusRunning)
	w.logger.Printf("watching %d directories via fsnotify", watchCount)

	w.ticker = time.NewTicker(w.debounceMs)

	w.wg.Add(2)
	go w.processEvents()
	go w.processPending()

	return nil
}

// Stop halts the fsnotify watcher and flushes any pending events.
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
	w.fsw.Close()
	w.fsw = nil
	w.wg.Wait()
	w.flushPending()
	return nil
}

func (w *Watcher) Restart() error {
	_ = w.Stop()
	w.resetForRestart()
	return w.Start()
}

func (w *Watcher) processEvents() {
	defer w.wg.Done()

	for {
		select {
		case <-w.done:
			return
		case event, ok := <-w.fsw.Events:
			if !ok {
				w.health.setError(StatusError, "fsnotify events channel closed unexpectedly")
				w.logger.Printf("ERROR: fsnotify events channel closed unexpectedly")
				return
			}
			w.handleRawEvent(event)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				w.health.setError(StatusError, "fsnotify errors channel closed unexpectedly")
				w.logger.Printf("ERROR: fsnotify errors channel closed unexpectedly")
				return
			}
			w.health.setError(StatusError, err.Error())
			w.logger.Printf("watcher error: %v", err)
		}
	}
}

func (w *Watcher) handleRawEvent(event fsnotify.Event) {
	path := event.Name

	relPath, err := filepath.Rel(w.cfg.ProjectRoot, path)
	if err != nil {
		return
	}

	if w.shouldIgnoreRel(relPath) {
		return
	}

	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			if !w.matcher.ShouldIgnore(relPath + "/") {
				_ = w.fsw.Add(path)
			}
			return
		}
	}

	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return
	}

	op := mapFsnotifyOp(event.Op)
	if op == 0 {
		return
	}

	w.queueEvent(relPath, op)
}

func mapFsnotifyOp(op fsnotify.Op) schema.Operation {
	switch {
	case op.Has(fsnotify.Remove):
		return schema.OpDelete
	case op.Has(fsnotify.Rename):
		return schema.OpDelete
	case op.Has(fsnotify.Create):
		return schema.OpCreate
	case op.Has(fsnotify.Write):
		return schema.OpModify
	default:
		return 0
	}
}

// WatchedDirs returns the directories being watched, relative to the project root.
func (w *Watcher) WatchedDirs() []string {
	list := w.fsw.WatchList()
	var relPaths []string
	for _, p := range list {
		rel, err := filepath.Rel(w.cfg.ProjectRoot, p)
		if err == nil {
			relPaths = append(relPaths, rel)
		}
	}
	return relPaths
}
