package daemon

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/eventlog"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"
)

func TestDaemonMigratesLegacyStoreWhileWriting(t *testing.T) {
	d := testDaemonWithSubsystems(t)
	legacy, err := store.NewStore(d.cfg.ObjectsDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	d.objStore = legacy

	put := func(path string, data []byte) {
		id, size, err := d.objStore.Put(data)
		if err != nil {
			t.Error(err)
			return
		}
		ev := &schema.Event{EventID: schema.NewEventID(), Version: schema.SchemaVersion, FilePath: path, Op: schema.OpModify, ContentHash: id, ContentSize: size}
		ev.SetTimestamp(time.Now())
		d.processEvent(ev)
	}
	for i := 0; i < 20; i++ {
		put(fmt.Sprintf("f%d.txt", i%4), []byte(fmt.Sprintf("legacy content %d", i)))
	}

	enc, err := store.OpenConfig(d.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !enc.NeedsMigration() {
		t.Fatal("expected pending migration")
	}
	d.objStore = enc

	prev := migrationSettle
	migrationSettle = 0
	defer func() { migrationSettle = prev }()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			put(fmt.Sprintf("f%d.txt", i%4), []byte(fmt.Sprintf("new content %d", i)))
		}
	}()
	d.startMigration()
	select {
	case <-d.migrationDone:
	case <-time.After(60 * time.Second):
		t.Fatal("migration did not finish")
	}
	wg.Wait()

	if enc.NeedsMigration() {
		t.Fatalf("manifest = %+v", enc.Manifest())
	}
	if p, _ := d.idx.GetMeta(index.MetaStoreMigrationPhase); p != store.PhaseDone {
		t.Fatalf("phase = %q", p)
	}
	events, _ := d.idx.QueryEvents(&index.Query{})
	if len(events) != 40 {
		t.Fatalf("events = %d", len(events))
	}
	for _, e := range events {
		for _, id := range []string{e.ContentHash, e.PreviousHash} {
			if id == "" {
				continue
			}
			data, err := enc.Get(id)
			if err != nil {
				t.Fatalf("event %s: %v", e.EventID, err)
			}
			if enc.ContentID(data) != id {
				t.Fatalf("event %s still references a legacy id", e.EventID)
			}
		}
	}

	d.logWriter.Sync()
	r, _ := eventlog.NewReader(d.cfg.EventsDir())
	all, _ := r.ReadAll()
	for _, e := range all {
		if e.ContentHash == "" {
			continue
		}
		data, err := enc.Get(e.ContentHash)
		if err != nil || enc.ContentID(data) != e.ContentHash {
			t.Fatalf("segment event %s points at a non-migrated object: %v", e.EventID, err)
		}
	}
}
