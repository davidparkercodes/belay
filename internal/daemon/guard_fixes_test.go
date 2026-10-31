package daemon

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/spool"
	"github.com/davidparkercodes/belay/internal/store"
	"github.com/davidparkercodes/belay/internal/storekey"
)

func TestRequeueBadSpool_RetriesSealedEntryWithMatchingKey(t *testing.T) {
	d := daemonWithStore(t)
	pub, _ := d.objStore.SealRecipient()
	content := []byte("queued while the key was elsewhere\n")
	sealed, err := store.Seal(pub, content)
	if err != nil {
		t.Fatal(err)
	}
	ev := hookEvent("s1", "late.go", "", time.Now())
	if err := spool.WriteEntry(d.cfg.SpoolDir(), &spool.Entry{Event: ev, Sealed: sealed}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(d.cfg.SpoolDir(), ev.EventID+".json")
	if err := os.Rename(p, p+".bad"); err != nil {
		t.Fatal(err)
	}

	d.requeueBadSpool()
	d.drainSpool()

	latest, err := d.idx.LatestEvent("late.go")
	if err != nil || latest == nil || latest.ContentHash != d.objStore.ContentID(content) {
		t.Fatalf("requeued entry not ingested: %+v %v", latest, err)
	}
}

func TestDrainSpool_TransientStoreErrorRetriesInsteadOfSettingAside(t *testing.T) {
	d := daemonWithStore(t)
	d.drainSpool()
	r, _, _ := spool.LoadRecipient(d.cfg.SpoolDir())
	sealed, _ := store.Seal(r.PublicKey, []byte("x\n"))
	ev := hookEvent("s1", "t.go", "", time.Now())
	if err := spool.WriteEntry(d.cfg.SpoolDir(), &spool.Entry{Event: ev, Sealed: sealed}); err != nil {
		t.Fatal(err)
	}
	objects := d.cfg.ObjectsDir()
	if err := os.Chmod(objects, 0o500); err != nil {
		t.Fatal(err)
	}
	d.drainSpool()
	os.Chmod(objects, 0o755)

	if bad, _ := spool.Bad(d.cfg.SpoolDir()); len(bad) != 0 {
		t.Fatalf("a store write failure set the entry aside: %v", bad)
	}
	d.drainSpool()
	if latest, _ := d.idx.LatestEvent("t.go"); latest == nil || latest.ContentHash == "" {
		t.Fatal("entry was not ingested on the next drain")
	}
}

func TestDrainSpool_StoresDeferredContent(t *testing.T) {
	d := daemonWithStore(t)
	content := "written while the store key was unavailable\n"
	if err := os.WriteFile(filepath.Join(d.cfg.ProjectRoot, "d.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ev := hookEvent("s1", "d.go", "", time.Now())
	spool.DeferContent(ev)
	if err := spool.WriteEntry(d.cfg.SpoolDir(), &spool.Entry{Event: ev}); err != nil {
		t.Fatal(err)
	}
	d.drainSpool()
	latest, err := d.idx.LatestEvent("d.go")
	if err != nil || latest == nil || latest.ContentHash != d.objStore.ContentID([]byte(content)) {
		t.Fatalf("deferred content not stored: %+v %v", latest, err)
	}
	if spool.IsDeferred(latest) {
		t.Fatal("deferred marker must be cleared once the content is stored")
	}
}

func TestWaitForStore_WrongKeyStaysUpWithoutWriting(t *testing.T) {
	d := testDaemonWithSubsystems(t)
	d.objStore = nil
	right, _ := storekey.Generate()
	if _, err := store.OpenConfigWithKeys(d.cfg, storekey.NewMemoryProvider(right)); err != nil {
		t.Fatal(err)
	}
	manifestBefore, _ := os.ReadFile(d.cfg.StoreManifestPath())

	keyFile := filepath.Join(t.TempDir(), "other.key")
	wrong, _ := storekey.Generate()
	if _, err := (&storekey.FileProvider{Path: keyFile}).Install(wrong, false); err != nil {
		t.Fatal(err)
	}
	t.Setenv(storekey.EnvKeyFile, keyFile)
	keyBefore, _ := os.ReadFile(keyFile)

	var logBuf bytes.Buffer
	d.logger = log.New(&logBuf, "", 0)
	sig := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() { done <- d.waitForStore(sig, 10*time.Millisecond) }()
	time.Sleep(80 * time.Millisecond)
	sig <- syscall.SIGTERM
	err := <-done

	if !errors.Is(err, errShutdownWhileLocked) {
		t.Fatalf("waitForStore = %v, want a clean shutdown while locked", err)
	}
	if d.objStore != nil {
		t.Fatal("store opened with the wrong key")
	}
	if !strings.Contains(logBuf.String(), "belay key import") {
		t.Fatalf("log lacks key import guidance: %s", logBuf.String())
	}
	keyAfter, _ := os.ReadFile(keyFile)
	manifestAfter, _ := os.ReadFile(d.cfg.StoreManifestPath())
	if !bytes.Equal(keyBefore, keyAfter) || !bytes.Equal(manifestBefore, manifestAfter) {
		t.Fatal("the daemon changed the key or the manifest while locked")
	}
}

func TestDetectLegacyObjectsReopensMigration(t *testing.T) {
	d := daemonWithStore(t)
	if _, _, err := d.objStore.Put([]byte("encrypted")); err != nil {
		t.Fatal(err)
	}
	legacy, err := store.NewStore(d.cfg.ObjectsDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := legacy.Put([]byte("synced from an older copy")); err != nil {
		t.Fatal(err)
	}
	if d.objStore.NeedsMigration() {
		t.Fatal("fixture: migration should start complete")
	}
	d.detectLegacyObjects()
	if !d.objStore.NeedsMigration() {
		t.Fatal("legacy object did not reopen the migration")
	}
	m, _, _ := store.ReadManifest(d.cfg.StoreManifestPath())
	if m.Migration != store.MigrationPending {
		t.Fatalf("manifest = %+v", m)
	}
}
