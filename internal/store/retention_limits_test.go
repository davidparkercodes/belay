package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/ignore"
	"github.com/davidparkercodes/belay/internal/schema"
)

func putSized(t *testing.T, s *Store, seed string, n int) string {
	t.Helper()
	data := make([]byte, n)
	copy(data, seed)
	h, _, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	return h
}

func TestCompactor_StorageLimitEvictsOldestFirst(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)
	retention.ArchiveDays = 0
	retention.ColdDays = 3650
	retention.WarmDays = 3650
	now := time.Now()

	var hashes []string
	for day := 0; day < 8; day++ {
		h := putSized(t, objStore, fmt.Sprintf("day-%d", day), 4096)
		hashes = append(hashes, h)
		ts := now.Add(-time.Duration(80-day*10) * 24 * time.Hour)
		insertEvent(t, idx, fmt.Sprintf("e-%d", day), ts, fmt.Sprintf("f%d.go", day), "s1", schema.OpCreate, h, "")
	}
	hot := putSized(t, objStore, "hot", 4096)
	insertEvent(t, idx, "hot", now.Add(-time.Hour), "hot.go", "s1", schema.OpCreate, hot, "")

	total, _, _ := objStore.Size()
	c := NewCompactor(idx, objStore, retention, false)
	c.now = now
	c.maxBytes = total - 3*4096

	removed, _, freed, err := c.enforceStorageLimit()
	if err != nil {
		t.Fatalf("enforceStorageLimit: %v", err)
	}
	if removed == 0 || freed == 0 {
		t.Fatalf("expected eviction, removed=%d freed=%d", removed, freed)
	}
	after, _, _ := objStore.Size()
	if after > c.maxBytes {
		t.Errorf("store %d bytes still over budget %d", after, c.maxBytes)
	}
	if objStore.Has(hashes[0]) {
		t.Error("oldest object should be evicted first")
	}
	if !objStore.Has(hashes[len(hashes)-1]) {
		t.Error("newest pre-hot object should survive a small overage")
	}
	if !objStore.Has(hot) {
		t.Error("hot tier must never be evicted")
	}
}

func TestCompactor_PurgeIgnored(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".belayignore"), []byte("logs/\n*.png\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ignore.NewMatcher(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	keep := putSized(t, objStore, "src", 64)
	logH := putSized(t, objStore, "log", 64)
	pngH := putSized(t, objStore, "png", 64)
	insertEvent(t, idx, "src", now.Add(-time.Minute), "src/main.go", "s1", schema.OpModify, keep, "")
	insertEvent(t, idx, "log", now.Add(-time.Minute), "logs/build.ndjson", "s1", schema.OpModify, logH, "")
	insertEvent(t, idx, "png", now.Add(-time.Minute), "shots/a.png", "s1", schema.OpModify, pngH, "")

	c := NewCompactor(idx, objStore, retention, false)
	c.now = now
	c.SetPurgeIgnored(m)
	result, err := c.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}
	if result.TierBreakdown["ignored_purged"] != 2 {
		t.Errorf("ignored_purged = %d, want 2", result.TierBreakdown["ignored_purged"])
	}
	if !objStore.Has(keep) || objStore.Has(logH) || objStore.Has(pngH) {
		t.Error("ignored paths' objects should be collected and tracked ones kept")
	}
}
