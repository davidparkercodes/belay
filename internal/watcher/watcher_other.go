//go:build !darwin

package watcher

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

	watchCount, exhausted, err := w.addTree(w.cfg.ProjectRoot, false)
	if err != nil {
		w.health.setError(StatusError, err.Error())
		return fmt.Errorf("walk project root: %w", err)
	}

	if exhausted {
		w.health.setError(StatusDegraded, inotifyLimitMsg)
	} else {
		w.health.setStatus(StatusRunning)
	}
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
	w.wg.Wait()
	w.fsw = nil
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
				if _, exhausted, _ := w.addTree(path, true); exhausted {
					w.health.setError(StatusDegraded, inotifyLimitMsg)
				}
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

const inotifyLimitMsg = "inotify watch limit reached; some directories are not watched. Raise it with: sudo sysctl fs.inotify.max_user_watches=524288"

const maxWatchDepth = 32

// addTree watches root and every non-ignored directory below it. When queueFiles is
// set, files already present are recorded as creates, since they may have been written
// before the watch on their directory existed. exhausted reports an inotify limit hit.
func (w *Watcher) addTree(root string, queueFiles bool) (count int, exhausted bool, err error) {
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		relPath, relErr := filepath.Rel(w.cfg.ProjectRoot, path)
		if relErr != nil {
			return nil
		}

		if !d.IsDir() {
			if queueFiles && d.Type().IsRegular() && !w.shouldIgnoreRel(relPath) {
				w.queueEvent(relPath, schema.OpCreate)
			}
			return nil
		}

		if relPath != "." {
			if isHidden(relPath) || w.matcher.ShouldIgnore(relPath+"/") {
				return filepath.SkipDir
			}
			if strings.Count(relPath, string(filepath.Separator)) >= maxWatchDepth {
				return filepath.SkipDir
			}
		}

		if exhausted {
			return filepath.SkipDir
		}
		if addErr := w.fsw.Add(path); addErr != nil {
			if errors.Is(addErr, syscall.ENOSPC) {
				exhausted = true
				w.logger.Printf("warning: %s (stopped at %s)", inotifyLimitMsg, relPath)
				return filepath.SkipDir
			}
			w.logger.Printf("warning: cannot watch %s: %v", path, addErr)
			return nil
		}
		count++
		return nil
	})
	return count, exhausted, err
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
