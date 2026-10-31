package store

import (
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/storekey"
)

type erroringKeys struct {
	keys    []storekey.Key
	creates int
}

func (p *erroringKeys) Keys() ([]storekey.Key, error) {
	return p.keys, storekey.ErrKeyUnavailable
}
func (p *erroringKeys) Create() (storekey.Key, error) {
	p.creates++
	return storekey.Key{}, errors.New("must not be called")
}
func (p *erroringKeys) Install([]byte, bool) (storekey.Key, error) {
	return storekey.Key{}, errors.New("must not be called")
}
func (p *erroringKeys) Describe() string { return "erroring" }

func TestOpenNewStoreRefusesKeyCreationWhenLookupFails(t *testing.T) {
	dir := t.TempDir()
	p := &erroringKeys{}
	_, err := Open(filepath.Join(dir, "objects"), Options{Encrypt: true, Keys: p, ManifestPath: filepath.Join(dir, "store.json")})
	if !errors.Is(err, storekey.ErrKeyUnavailable) {
		t.Fatalf("Open = %v, want ErrKeyUnavailable", err)
	}
	if p.creates != 0 {
		t.Fatal("a key must never be created after a failed lookup")
	}
	if _, err := os.Stat(filepath.Join(dir, "store.json")); !os.IsNotExist(err) {
		t.Fatal("nothing may be written when the store is unavailable")
	}
}

func TestOpenUsesMatchingKeyDespiteKeychainError(t *testing.T) {
	dir := t.TempDir()
	master := testKey(t)
	openEncrypted(t, dir, true, storekey.NewMemoryProvider(master))
	p := &erroringKeys{keys: []storekey.Key{{Master: master, Source: "file"}}}
	s, err := Open(filepath.Join(dir, "objects"), Options{Encrypt: true, Keys: p, ManifestPath: filepath.Join(dir, "store.json")})
	if err != nil {
		t.Fatalf("Open with the right key in the key file: %v", err)
	}
	if s.KeyID() != KeyID(master) {
		t.Fatal("wrong key loaded")
	}
}

func TestConcurrentFirstOpenSharesOneKey(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "store.key")
	const n = 12
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dir := t.TempDir()
			s, err := Open(filepath.Join(dir, "objects"), Options{Encrypt: true, Keys: &storekey.FileProvider{Path: keyPath}, ManifestPath: filepath.Join(dir, "store.json")})
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = s.KeyID()
		}(i)
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatal("concurrent first opens ended with different keys")
		}
	}
}

func corruptObject(t *testing.T, s *Store, id string) {
	t.Helper()
	p := s.objectPath(id)
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, bytes.Repeat([]byte{0xAB}, 80), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationRecopiesBrokenCopyBeforeRemap(t *testing.T) {
	f := newMigFixture(t, 4)
	s := f.open(t)
	m := f.migrator(s)
	m.Fail = func(point string) error {
		if point != "after-copy" {
			return nil
		}
		mapping, err := f.idx.MigrationMapping()
		if err != nil {
			return err
		}
		for _, newID := range mapping {
			corruptObject(t, s, newID)
		}
		return nil
	}
	if _, err := m.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	f.assertFinal(t, s)
}

func TestMigrationCleanupKeepsMappingUntilCopyVerifies(t *testing.T) {
	f := newMigFixture(t, 3)
	s := f.open(t)
	var victimOld, victimNew string
	m := f.migrator(s)
	m.Fail = func(point string) error {
		if point != "after-segments" {
			return nil
		}
		mapping, err := f.idx.MigrationMapping()
		if err != nil {
			return err
		}
		for o, n := range mapping {
			victimOld, victimNew = o, n
			break
		}
		p := s.objectPath(victimNew)
		if err := os.Remove(p); err != nil {
			return err
		}
		return os.Mkdir(p, 0o755)
	}
	if _, err := m.Run(); err == nil {
		t.Fatal("cleanup must fail while an encrypted copy cannot be repaired")
	}
	if p, _ := f.idx.GetMeta(index.MetaStoreMigrationPhase); p != PhaseCleanup {
		t.Fatalf("phase = %q, want cleanup", p)
	}
	if !f.idx.MigrationTableExists() || s.Manifest().Migration != MigrationPending {
		t.Fatal("migration must stay unfinished with its mapping")
	}
	if !s.Has(victimOld) {
		t.Fatal("legacy object of an unverified copy was deleted")
	}

	prevGrace := GCGracePeriod
	GCGracePeriod = 0
	t.Cleanup(func() { GCGracePeriod = prevGrace })
	if _, err := GarbageCollect(f.idx, s, false); err != nil {
		t.Fatal(err)
	}
	if !s.Has(victimOld) {
		t.Fatal("GC deleted a legacy object the unfinished migration still needs")
	}

	if err := os.Remove(s.objectPath(victimNew)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.migrator(s).Run(); err != nil {
		t.Fatalf("resumed Run: %v", err)
	}
	f.assertFinal(t, s)
}

func filesUnder(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(p)
		out[p] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertNoLegacyIDs(t *testing.T, root string, legacy []string) {
	t.Helper()
	for p, data := range filesUnder(t, root) {
		for _, id := range legacy {
			if bytes.Contains(data, []byte(id)) || strings.Contains(p, id[2:]) {
				t.Fatalf("legacy sha256 %s survives in %s", id[:12], p)
			}
		}
	}
}

func TestMigrationTombstonesIDsOfCollectedLegacyObjects(t *testing.T) {
	f := newMigFixture(t, 4)
	legacy := NewStoreForTest(t, f)
	events, err := f.idx.QueryEvents(&index.Query{})
	if err != nil {
		t.Fatal(err)
	}
	var pruned []string
	for _, e := range events {
		if e.FilePath == "src/file0.go" {
			pruned = append(pruned, e.EventID)
			if e.ContentHash != "" {
				if err := legacy.Delete(e.ContentHash); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if len(pruned) == 0 {
		t.Fatal("fixture has no file0 events")
	}
	if _, err := f.idx.DeleteEventsBatch(pruned); err != nil {
		t.Fatal(err)
	}
	f.eventIDs = f.eventIDs[:len(f.eventIDs)-len(pruned)]

	s := f.open(t)
	if _, err := f.migrator(s).Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	f.assertEveryEventRestores(t, s)
	if err := f.idx.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	assertNoLegacyIDs(t, f.dir, f.legacyIDs)

	rebuilt := filepath.Join(f.dir, "rebuilt.db")
	if _, err := index.Rebuild(rebuilt, f.eventsDir, log.New(io.Discard, "", 0)); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	ridx, err := index.Open(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	defer ridx.Close()
	all, err := ridx.QueryEvents(&index.Query{})
	if err != nil {
		t.Fatal(err)
	}
	missing := 0
	for _, e := range all {
		if e.ContentHash == "" {
			continue
		}
		if _, err := s.Get(e.ContentHash); err != nil {
			if !strings.Contains(err.Error(), "object not found") {
				t.Fatalf("tombstoned id must read as a missing object, got %v", err)
			}
			missing++
		}
	}
	if missing != len(pruned) {
		t.Fatalf("%d rebuilt events point at missing objects, want %d", missing, len(pruned))
	}
	if err := ridx.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	assertNoLegacyIDs(t, f.dir, f.legacyIDs)

	prev := GCGracePeriod
	GCGracePeriod = 0
	t.Cleanup(func() { GCGracePeriod = prev })
	if _, err := GarbageCollect(ridx, s, false); err != nil {
		t.Fatalf("GC with tombstoned ids: %v", err)
	}
	f.assertEveryEventRestores(t, s)
}

func NewStoreForTest(t *testing.T, f *migFixture) *Store {
	t.Helper()
	s, err := NewStore(f.objectsDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReopenMigrationWhenLegacyObjectsReappear(t *testing.T) {
	f := newMigFixture(t, 3)
	s := f.open(t)
	if _, err := f.migrator(s).Run(); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReopenMigrationIfLegacy(s, f.idx); ok {
		t.Fatal("a clean migrated store must not reopen")
	}

	copied := NewStoreForTest(t, f)
	oldID, _, err := copied.Put([]byte("older machine content"))
	if err != nil {
		t.Fatal(err)
	}
	s = f.open(t)
	found, ok := ReopenMigrationIfLegacy(s, f.idx)
	if !ok || found != oldID {
		t.Fatalf("reopen = %q %v, want %s", found, ok, oldID[:8])
	}
	if !s.NeedsMigration() {
		t.Fatal("manifest must be back to pending")
	}
	if _, err := f.migrator(s).Run(); err != nil {
		t.Fatal(err)
	}
	if s.Has(oldID) || s.Manifest().Migration != MigrationComplete {
		t.Fatal("re-run migration did not remove the copied legacy object")
	}
}

func TestMigrationKeepsPinnedLegacyObject(t *testing.T) {
	f := newMigFixture(t, 2)
	pinned := f.legacyIDs[0]
	s := f.open(t)
	if err := s.Pin(pinned, "scrub original"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.migrator(s).Run(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(pinned)
	if err != nil || !bytes.Equal(got, f.contents[pinned]) {
		t.Fatalf("pinned legacy object lost: %v", err)
	}
	if _, ok := ReopenMigrationIfLegacy(s, f.idx); ok {
		t.Fatal("a pinned legacy object kept on purpose must not reopen the migration")
	}
}

func TestReopenIgnoresCorruptLegacyObjects(t *testing.T) {
	f := newMigFixture(t, 2)
	bad := f.legacyIDs[0]
	if err := os.WriteFile(filepath.Join(f.objectsDir(), bad[:2], bad[2:]), []byte("rotted"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := f.open(t)
	if _, err := f.migrator(s).Run(); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReopenMigrationIfLegacy(s, f.idx); ok {
		t.Fatal("corrupt legacy objects kept on purpose must not reopen the migration")
	}
}
