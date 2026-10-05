package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/index"
)

func TestMain(m *testing.M) {
	GCGracePeriod = 0
	os.Exit(m.Run())
}

func TestGarbageCollect_KeepsFreshUnreferencedObjects(t *testing.T) {
	dir := t.TempDir()
	objStore, err := NewStore(filepath.Join(dir, "objects"), false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer objStore.Close()

	idx, err := index.Open(filepath.Join(dir, "gc-grace.db"))
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	defer idx.Close()

	fresh, _, _ := objStore.Put([]byte("in-flight content"))
	stale, _, _ := objStore.Put([]byte("long orphaned content"))
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(objStore.objectPath(stale), old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	prev := GCGracePeriod
	GCGracePeriod = 10 * time.Minute
	defer func() { GCGracePeriod = prev }()

	result, err := GarbageCollect(idx, objStore, false)
	if err != nil {
		t.Fatalf("GarbageCollect: %v", err)
	}
	if result.OrphanedObjects != 1 {
		t.Errorf("OrphanedObjects = %d, want 1", result.OrphanedObjects)
	}
	if !objStore.Has(fresh) {
		t.Error("object written inside the grace window must survive GC")
	}
	if objStore.Has(stale) {
		t.Error("object older than the grace window should be collected")
	}
}
