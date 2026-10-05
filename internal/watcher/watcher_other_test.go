//go:build !darwin

package watcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/ignore"
	"github.com/davidparkercodes/belay/internal/store"
)

func startTestWatcher(t *testing.T) (*Watcher, string, func() []string) {
	t.Helper()
	projectRoot := t.TempDir()
	objectsDir := filepath.Join(projectRoot, ".belay", "objects")
	if err := os.MkdirAll(objectsDir, 0755); err != nil {
		t.Fatalf("create objects dir: %v", err)
	}
	cfg := config.DefaultConfig(projectRoot)
	cfg.Watcher.DebounceMs = 20
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
	events := collectEvents(&w.watcherBase)
	if err := w.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop() })
	paths := func() []string {
		var out []string
		for _, e := range events() {
			out = append(out, filepath.ToSlash(e.FilePath))
		}
		return out
	}
	return w, projectRoot, paths
}

func waitForPath(t *testing.T, paths func() []string, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range paths() {
			if p == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no event for %s; got %v", want, paths())
}

func TestWatcher_NestedDirectoriesCreatedAfterStart(t *testing.T) {
	_, root, paths := startTestWatcher(t)

	writeTestFile(t, root, "a/b/c/early.txt", "written with its directories")
	waitForPath(t, paths, "a/b/c/early.txt")

	time.Sleep(100 * time.Millisecond)
	writeTestFile(t, root, "a/b/c/later.txt", "written after the tree exists")
	waitForPath(t, paths, "a/b/c/later.txt")
}

func TestWatcher_WatchesDeepTrees(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "d1", "d2", "d3", "d4", "d5", "d6", "d7", "d8", "d9", "d10")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	w := &Watcher{}
	cfg := config.DefaultConfig(root)
	matcher, err := ignore.NewMatcher(root)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}
	initBase(&w.watcherBase, cfg, nil, matcher)
	if err := w.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	rel, _ := filepath.Rel(root, deep)
	for _, d := range w.WatchedDirs() {
		if d == rel {
			return
		}
	}
	t.Errorf("%s not watched; watched %v", rel, w.WatchedDirs())
}
