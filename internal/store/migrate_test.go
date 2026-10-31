package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/eventlog"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/storekey"
)

type migFixture struct {
	dir       string
	eventsDir string
	keys      storekey.Provider
	idx       *index.Index
	contents  map[string][]byte
	eventIDs  []string
	legacyIDs []string
}

func (f *migFixture) objectsDir() string   { return filepath.Join(f.dir, "objects") }
func (f *migFixture) manifestPath() string { return filepath.Join(f.dir, "store.json") }

func newMigFixture(t *testing.T, files int) *migFixture {
	t.Helper()
	dir := t.TempDir()
	f := &migFixture{
		dir:       dir,
		eventsDir: filepath.Join(dir, "events"),
		keys:      storekey.NewMemoryProvider(testKey(t)),
		contents:  map[string][]byte{},
	}
	legacy, err := NewStore(f.objectsDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := index.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	f.idx = idx

	w, err := eventlog.NewWriter(f.eventsDir, 2048)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	base := time.Now().Add(-time.Hour)
	n := 0
	for i := 0; i < files; i++ {
		path := fmt.Sprintf("src/file%d.go", i)
		prev := ""
		for v := 0; v < 3; v++ {
			data := []byte(fmt.Sprintf("package main // file %d version %d\n%s", i, v, strings.Repeat("x", i*7+v)))
			id, _, err := legacy.Put(data)
			if err != nil {
				t.Fatal(err)
			}
			f.contents[id] = data
			ev := &schema.Event{
				EventID:      schema.NewEventID(),
				Version:      schema.SchemaVersion,
				FilePath:     path,
				Op:           schema.OpModify,
				ContentHash:  id,
				PreviousHash: prev,
				ContentSize:  int64(len(data)),
				SessionID:    "s1",
			}
			if v == 0 {
				ev.Op = schema.OpCreate
			}
			ev.SetTimestamp(base.Add(time.Duration(n) * time.Second))
			n++
			if err := w.Append(ev); err != nil {
				t.Fatal(err)
			}
			if err := idx.IndexEvent(ev, w.CurrentSegment(), w.CurrentOffset()); err != nil {
				t.Fatal(err)
			}
			f.eventIDs = append(f.eventIDs, ev.EventID)
			prev = id
		}
	}
	meta := &schema.Event{EventID: schema.NewEventID(), Version: schema.SchemaVersion, FilePath: ".belay/sessions", Op: schema.OpCreate, SessionID: "s1",
		Metadata: map[string]string{"event_type": "session_start", "tool_name": "test"}}
	meta.SetTimestamp(base)
	w.Append(meta)
	for id := range f.contents {
		f.legacyIDs = append(f.legacyIDs, id)
	}
	return f
}

func (f *migFixture) open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(f.objectsDir(), Options{Compress: true, Encrypt: true, Keys: f.keys, ManifestPath: f.manifestPath()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func (f *migFixture) migrator(s *Store) *Migrator {
	return &Migrator{Store: s, Index: f.idx, EventsDir: f.eventsDir, LockPath: filepath.Join(f.dir, "migrate.lock")}
}

func (f *migFixture) byContent() map[string][]byte {
	out := map[string][]byte{}
	for _, c := range f.contents {
		out[string(c)] = c
	}
	return out
}

func (f *migFixture) assertEveryEventRestores(t *testing.T, s *Store) {
	t.Helper()
	events, err := f.idx.QueryEvents(&index.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != len(f.eventIDs) {
		t.Fatalf("index has %d events, want %d", len(events), len(f.eventIDs))
	}
	known := f.byContent()
	for _, e := range events {
		for _, id := range []string{e.ContentHash, e.PreviousHash} {
			if id == "" {
				continue
			}
			got, err := s.Get(id)
			if err != nil {
				t.Fatalf("event %s id %s: %v", e.EventID, id[:8], err)
			}
			if _, ok := known[string(got)]; !ok {
				t.Fatalf("event %s restored unknown bytes", e.EventID)
			}
		}
	}
}

func (f *migFixture) assertFinal(t *testing.T, s *Store) {
	t.Helper()
	f.assertEveryEventRestores(t, s)
	for _, old := range f.legacyIDs {
		if s.Has(old) {
			t.Fatalf("legacy object %s still present after migration", old[:8])
		}
	}
	if f.idx.MigrationTableExists() {
		t.Fatal("mapping table must be dropped at the end")
	}
	if p, _ := f.idx.GetMeta(index.MetaStoreMigrationPhase); p != PhaseDone {
		t.Fatalf("phase = %q", p)
	}
	if s.NeedsMigration() || s.Manifest().Migration != MigrationComplete {
		t.Fatalf("manifest = %+v", s.Manifest())
	}
	f.assertSegmentsClean(t)

	hashes, _ := s.ListHashes()
	for _, h := range hashes {
		enc, err := s.IsEncrypted(h)
		if err != nil || !enc {
			t.Fatalf("object %s is not encrypted after migration", h[:8])
		}
	}
}

func (f *migFixture) assertSegmentsClean(t *testing.T) {
	t.Helper()
	r, err := eventlog.NewReader(f.eventsDir)
	if err != nil {
		t.Fatal(err)
	}
	all, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	legacy := map[string]bool{}
	for _, id := range f.legacyIDs {
		legacy[id] = true
	}
	for _, e := range all {
		if legacy[e.ContentHash] || legacy[e.PreviousHash] {
			t.Fatalf("segment event %s still carries a legacy id", e.EventID)
		}
	}
}

func TestMigrationFullRun(t *testing.T) {
	f := newMigFixture(t, 6)
	s := f.open(t)
	if !s.NeedsMigration() {
		t.Fatal("expected pending migration")
	}
	res, err := f.migrator(s).Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Copied != len(f.legacyIDs) || res.LegacyDeleted != len(f.legacyIDs) || res.Corrupt != 0 {
		t.Fatalf("result = %+v", res)
	}
	if res.Segments == nil || res.Segments.SegmentsRewritten == 0 {
		t.Fatalf("segments were not rewritten: %+v", res.Segments)
	}
	f.assertFinal(t, s)

	again, err := f.migrator(s).Run()
	if err != nil || again.Copied != 0 {
		t.Fatalf("second run must be a no-op: %+v %v", again, err)
	}
}

func TestMigrationRestoreIsByteExact(t *testing.T) {
	f := newMigFixture(t, 4)
	want := map[string][]byte{}
	events, _ := f.idx.QueryEvents(&index.Query{})
	for _, e := range events {
		want[e.EventID] = f.contents[e.ContentHash]
	}
	s := f.open(t)
	if _, err := f.migrator(s).Run(); err != nil {
		t.Fatal(err)
	}
	for id, data := range want {
		e, err := f.idx.GetEvent(id)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(e.ContentHash)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("event %s not byte-exact: %v", id, err)
		}
		if e.ContentHash != s.ContentID(data) {
			t.Fatal("migrated id must equal the store's ContentID of the content")
		}
	}
}

func TestMigrationRebuildPointsAtEncryptedObjects(t *testing.T) {
	f := newMigFixture(t, 5)
	s := f.open(t)
	if _, err := f.migrator(s).Run(); err != nil {
		t.Fatal(err)
	}
	rebuilt := filepath.Join(f.dir, "rebuilt.db")
	if _, err := index.Rebuild(rebuilt, f.eventsDir, log.New(os.Stderr, "", 0)); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	idx2, err := index.Open(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	defer idx2.Close()
	all, _ := idx2.QueryEvents(&index.Query{})
	var events []*schema.Event
	for _, e := range all {
		if e.FilePath != ".belay/sessions" {
			events = append(events, e)
		}
	}
	if len(events) != len(f.eventIDs) {
		t.Fatalf("rebuilt %d events, want %d", len(events), len(f.eventIDs))
	}
	for _, e := range events {
		for _, id := range []string{e.ContentHash, e.PreviousHash} {
			if id == "" {
				continue
			}
			enc, err := s.IsEncrypted(id)
			if err != nil || !enc {
				t.Fatalf("rebuilt event %s points at %s which is not an encrypted object (%v)", e.EventID, id[:8], err)
			}
			if _, err := s.Get(id); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestMigrationCorruptLegacyObjectIsKept(t *testing.T) {
	f := newMigFixture(t, 3)
	bad := f.legacyIDs[0]
	legacy, _ := NewStore(f.objectsDir(), true)
	badPath := legacy.objectPath(bad)
	garbage := []byte("this is not the content you are looking for")
	os.WriteFile(badPath, garbage, 0644)

	s := f.open(t)
	res, err := f.migrator(s).Run()
	if err != nil {
		t.Fatal(err)
	}
	if res.Corrupt != 1 {
		t.Fatalf("corrupt = %d", res.Corrupt)
	}
	got, err := os.ReadFile(badPath)
	if err != nil || !bytes.Equal(got, garbage) {
		t.Fatal("corrupt legacy object must be left exactly as it was")
	}
	st := ReadMigrationStatus(f.idx, f.manifestPath())
	if len(st.CorruptIDs) != 1 || st.CorruptIDs[0] != bad {
		t.Fatalf("status corrupt ids = %v", st.CorruptIDs)
	}
	for _, old := range f.legacyIDs[1:] {
		if s.Has(old) {
			t.Fatal("verified legacy objects should be removed")
		}
	}
}

var crashPoints = []string{
	"copy-written:",
	"copy-recorded:",
	"after-copy",
	"after-remap",
	"segment-before-rename:",
	"after-segments",
	"cleanup-deleted:",
	"before-finish",
	"before-manifest",
}

var errCrash = errors.New("simulated crash")

func TestMigrationResumesAfterCrashAtEveryPoint(t *testing.T) {
	for _, point := range crashPoints {
		for _, nth := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s#%d", strings.TrimSuffix(point, ":"), nth), func(t *testing.T) {
				f := newMigFixture(t, 4)
				s := f.open(t)
				m := f.migrator(s)
				hits := 0
				m.Fail = func(p string) error {
					if strings.HasPrefix(p, point) {
						hits++
						if hits == nth {
							return errCrash
						}
					}
					return nil
				}
				_, err := m.Run()
				if !errors.Is(err, errCrash) {
					if nth > 1 && err == nil {
						t.Skip("point is only reached once")
					}
					t.Fatalf("expected simulated crash, got %v", err)
				}

				s2 := f.open(t)
				f.assertEveryEventRestores(t, s2)
				for id, data := range f.contents {
					if s2.Has(id) {
						continue
					}
					if !s2.Has(s2.ContentID(data)) {
						t.Fatalf("legacy object %s deleted without an encrypted copy", id[:8])
					}
				}

				if _, err := f.migrator(s2).Run(); err != nil {
					t.Fatalf("resume: %v", err)
				}
				f.assertFinal(t, s2)
			})
		}
	}
}

func TestMigrationRepeatedCrashesConverge(t *testing.T) {
	f := newMigFixture(t, 5)
	calls := 0
	for i := 0; i < 200; i++ {
		s := f.open(t)
		m := f.migrator(s)
		budget := i
		m.Fail = func(string) error {
			calls++
			if budget == 0 {
				return errCrash
			}
			budget--
			return nil
		}
		_, err := m.Run()
		if err == nil {
			f.assertFinal(t, s)
			return
		}
		if !errors.Is(err, errCrash) {
			t.Fatalf("iteration %d: %v", i, err)
		}
		f.assertEveryEventRestores(t, f.open(t))
	}
	t.Fatal("migration never completed")
}

func TestMigrationWritesDuringMigrationUseNewIDs(t *testing.T) {
	f := newMigFixture(t, 3)
	s := f.open(t)
	m := f.migrator(s)

	var lateEvent *schema.Event
	newData := []byte("written while the migration ran")
	m.Fail = func(p string) error {
		if p != "after-copy" {
			return nil
		}
		latest, err := f.idx.LatestEvent("src/file1.go")
		if err != nil {
			return err
		}
		id, size, err := s.Put(newData)
		if err != nil {
			return err
		}
		lateEvent = &schema.Event{EventID: schema.NewEventID(), Version: schema.SchemaVersion, FilePath: "src/file1.go", Op: schema.OpModify,
			ContentHash: id, PreviousHash: latest.ContentHash, ContentSize: size}
		lateEvent.SetTimestamp(time.Now())
		return f.idx.IndexEvent(lateEvent, "", 0)
	}
	if _, err := m.Run(); err != nil {
		t.Fatal(err)
	}
	f.eventIDs = append(f.eventIDs, lateEvent.EventID)
	f.contents[s.ContentID(newData)] = newData
	f.assertFinal(t, s)

	got, _ := f.idx.GetEvent(lateEvent.EventID)
	if got.ContentHash != s.ContentID(newData) {
		t.Fatal("new write must keep its new id")
	}
	prevData, err := s.Get(got.PreviousHash)
	if err != nil || !strings.Contains(string(prevData), "file 1 version 2") {
		t.Fatalf("previous_hash recorded mid-migration must be remapped: %v", err)
	}
}

func TestMigrationWithLiveWriterSkipsActiveSegment(t *testing.T) {
	f := newMigFixture(t, 3)
	s := f.open(t)
	w, err := eventlog.NewWriter(f.eventsDir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	m := f.migrator(s)
	rotations := 0
	m.Exclusive = func(fn func() error) error {
		if err := w.Rotate(); err != nil {
			return err
		}
		rotations++
		return fn()
	}
	m.ActiveSegment = w.CurrentSegment
	if _, err := m.Run(); err != nil {
		t.Fatal(err)
	}
	if rotations == 0 {
		t.Fatal("Exclusive was never used")
	}
	ev := &schema.Event{EventID: schema.NewEventID(), Version: schema.SchemaVersion, FilePath: "late.txt", Op: schema.OpCreate}
	ev.SetTimestamp(time.Now())
	if err := w.Append(ev); err != nil {
		t.Fatalf("live writer broken after migration: %v", err)
	}
	w.Sync()
	r, _ := eventlog.NewReader(f.eventsDir)
	all, _ := r.ReadAll()
	found := false
	for _, e := range all {
		if e.EventID == ev.EventID {
			found = true
		}
	}
	if !found {
		t.Fatal("event appended after migration was lost")
	}
	f.assertFinal(t, s)
}

func TestMigrationLockPreventsConcurrentRuns(t *testing.T) {
	f := newMigFixture(t, 1)
	s := f.open(t)
	unlock, err := index.LockFile(filepath.Join(f.dir, "migrate.lock"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.migrator(s).Run()
	unlock()
	if !errors.Is(err, ErrMigrationBusy) {
		t.Fatalf("want ErrMigrationBusy, got %v", err)
	}
	if _, err := f.migrator(s).Run(); err != nil {
		t.Fatal(err)
	}
}

func TestGCDuringMigrationKeepsEncryptedCopies(t *testing.T) {
	f := newMigFixture(t, 3)
	s := f.open(t)
	m := f.migrator(s)
	m.Fail = func(p string) error {
		if p == "after-copy" {
			return errCrash
		}
		return nil
	}
	if _, err := m.Run(); !errors.Is(err, errCrash) {
		t.Fatal(err)
	}
	if _, err := GarbageCollect(f.idx, s, false); err != nil {
		t.Fatal(err)
	}
	for _, data := range f.contents {
		if !s.Has(s.ContentID(data)) {
			t.Fatal("GC removed an encrypted copy that is still in the migration mapping")
		}
	}
	if _, err := f.migrator(s).Run(); err != nil {
		t.Fatal(err)
	}
	f.assertFinal(t, s)
}

func TestMigrationStopsAndResumes(t *testing.T) {
	f := newMigFixture(t, 3)
	s := f.open(t)
	stop := make(chan struct{})
	close(stop)
	m := f.migrator(s)
	m.Stop = stop
	if _, err := m.Run(); !errors.Is(err, ErrMigrationStopped) {
		t.Fatalf("want ErrMigrationStopped, got %v", err)
	}
	if _, err := f.migrator(s).Run(); err != nil {
		t.Fatal(err)
	}
	f.assertFinal(t, s)
}

func TestMigrationStatusProgress(t *testing.T) {
	f := newMigFixture(t, 2)
	s := f.open(t)
	m := f.migrator(s)
	m.Fail = func(p string) error {
		if p == "after-copy" {
			return errCrash
		}
		return nil
	}
	m.Run()
	st := ReadMigrationStatus(f.idx, f.manifestPath())
	if st.Format != FormatEncrypted || st.Migration != MigrationPending || st.Phase != PhaseRemap || st.Copied != len(f.legacyIDs) || st.Total != len(f.legacyIDs) {
		t.Fatalf("status = %+v", st)
	}
	f.migrator(s).Run()
	st = ReadMigrationStatus(f.idx, f.manifestPath())
	if !st.Done() || st.Phase != PhaseDone {
		t.Fatalf("final status = %+v", st)
	}
}

func TestMigrationSurvivesIndexRebuildMidway(t *testing.T) {
	for _, point := range []string{"copy-recorded:", "after-remap", "segment-before-rename:", "cleanup-deleted:"} {
		t.Run(strings.TrimSuffix(point, ":"), func(t *testing.T) {
			f := newMigFixture(t, 4)
			s := f.open(t)
			m := f.migrator(s)
			m.Fail = func(p string) error {
				if strings.HasPrefix(p, point) {
					return errCrash
				}
				return nil
			}
			if _, err := m.Run(); !errors.Is(err, errCrash) {
				t.Fatalf("expected crash, got %v", err)
			}

			f.idx.Close()
			dbPath := filepath.Join(f.dir, "index.db")
			for _, suffix := range []string{"", "-wal", "-shm"} {
				os.Remove(dbPath + suffix)
			}
			if _, err := index.Rebuild(dbPath, f.eventsDir, log.New(io.Discard, "", 0)); err != nil {
				t.Fatal(err)
			}
			idx, err := index.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { idx.Close() })
			f.idx = idx

			s2 := f.open(t)
			if _, err := f.migrator(s2).Run(); err != nil {
				t.Fatalf("resume after rebuild: %v", err)
			}
			events, _ := f.idx.QueryEvents(&index.Query{})
			for _, e := range events {
				if e.ContentHash == "" {
					continue
				}
				got, err := s2.Get(e.ContentHash)
				if err != nil {
					t.Fatalf("event %s unreadable after rebuild+resume: %v", e.EventID, err)
				}
				if e.ContentHash != s2.ContentID(got) {
					t.Fatalf("event %s still has a legacy id", e.EventID)
				}
			}
			f.assertSegmentsClean(t)
		})
	}
}
