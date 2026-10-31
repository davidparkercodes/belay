package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/spool"
	"github.com/davidparkercodes/belay/internal/store"
	"github.com/davidparkercodes/belay/internal/storekey"
)

func daemonWithStore(t *testing.T) *Daemon {
	t.Helper()
	d := testDaemonWithSubsystems(t)
	master, err := storekey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	d.objStore, err = store.OpenConfigWithKeys(d.cfg, storekey.NewMemoryProvider(master))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDrainSpool_IngestsSealedContent(t *testing.T) {
	d := daemonWithStore(t)
	d.drainSpool()

	r, ok, err := spool.LoadRecipient(d.cfg.SpoolDir())
	if err != nil || !ok || r.KeyID != d.objStore.KeyID() {
		t.Fatalf("recipient = %+v, %v, %v", r, ok, err)
	}

	content := []byte("hello sealed world\n")
	sealed, err := store.Seal(r.PublicKey, content)
	if err != nil {
		t.Fatal(err)
	}
	ev := hookEvent("s1", "a.go", "", time.Now())
	if err := spool.WriteEntry(d.cfg.SpoolDir(), &spool.Entry{Event: ev, Sealed: sealed}); err != nil {
		t.Fatal(err)
	}
	d.drainSpool()

	latest, err := d.idx.LatestEvent("a.go")
	if err != nil || latest == nil {
		t.Fatalf("event not indexed: %v", err)
	}
	if latest.ContentHash != d.objStore.ContentID(content) || latest.ContentSize != int64(len(content)) {
		t.Fatalf("indexed hash/size = %s/%d", latest.ContentHash, latest.ContentSize)
	}
	got, err := d.objStore.Get(latest.ContentHash)
	if err != nil || string(got) != string(content) {
		t.Fatalf("stored content = %q, %v", got, err)
	}
	if pending, _ := spool.Pending(d.cfg.SpoolDir()); len(pending) != 0 {
		t.Fatalf("spool not drained: %v", pending)
	}
}

func TestDrainSpool_KeepsUnopenableSealedEntry(t *testing.T) {
	d := daemonWithStore(t)
	other, _ := storekey.Generate()
	otherStore, err := store.Open(t.TempDir(), store.Options{Encrypt: true, Keys: storekey.NewMemoryProvider(other)})
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := otherStore.SealRecipient()
	sealed, _ := store.Seal(pub, []byte("x"))
	ev := hookEvent("s1", "b.go", "", time.Now())
	if err := spool.WriteEntry(d.cfg.SpoolDir(), &spool.Entry{Event: ev, Sealed: sealed}); err != nil {
		t.Fatal(err)
	}
	d.drainSpool()

	if n := countFileEvents(t, d, "b.go"); n != 0 {
		t.Fatalf("unopenable entry was indexed %d times", n)
	}
	if _, err := os.Stat(filepath.Join(d.cfg.SpoolDir(), ev.EventID+".json.bad")); err != nil {
		t.Fatalf("entry not kept as .bad: %v", err)
	}
}

func TestDrainSpool_PlainEventsStillIngest(t *testing.T) {
	d := daemonWithStore(t)
	ev := hookEvent("s1", "c.go", "abc", time.Now())
	ev.Op = schema.OpDelete
	if err := spool.Write(d.cfg.SpoolDir(), ev); err != nil {
		t.Fatal(err)
	}
	d.drainSpool()
	if n := countFileEvents(t, d, "c.go"); n != 1 {
		t.Fatalf("plain spooled event indexed %d times, want 1", n)
	}
}
