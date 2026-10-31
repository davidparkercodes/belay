package commands

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/apiclient"
	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/spool"
	"github.com/davidparkercodes/belay/internal/store"
	"github.com/davidparkercodes/belay/internal/storekey"
)

type slowKeyProvider struct {
	inner storekey.Provider
	delay time.Duration
	calls atomic.Int32
}

func (p *slowKeyProvider) Keys() ([]storekey.Key, error) {
	p.calls.Add(1)
	time.Sleep(p.delay)
	return p.inner.Keys()
}
func (p *slowKeyProvider) Create() (storekey.Key, error) {
	p.calls.Add(1)
	time.Sleep(p.delay)
	return p.inner.Create()
}
func (p *slowKeyProvider) Install(m []byte, f bool) (storekey.Key, error) {
	return p.inner.Install(m, f)
}
func (p *slowKeyProvider) Describe() string { return "slow" }

func useSlowKeys(t *testing.T, delay time.Duration) (*slowKeyProvider, []byte) {
	t.Helper()
	master, err := storekey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	p := &slowKeyProvider{inner: storekey.NewMemoryProvider(master), delay: delay}
	prev := keyProvider
	keyProvider = func() storekey.Provider { return p }
	t.Cleanup(func() { keyProvider = prev })
	return p, master
}

func pointAtDeadDaemon(t *testing.T, cfg *config.Config) {
	t.Helper()
	closed := httptest.NewServer(http.NotFoundHandler())
	port := closed.Listener.Addr().(*net.TCPAddr).Port
	closed.Close()
	if err := apiclient.WritePortFile(cfg, port); err != nil {
		t.Fatal(err)
	}
}

func daemonSideStore(t *testing.T, cfg *config.Config, master []byte, publish bool) *store.Store {
	t.Helper()
	st, err := store.OpenConfigWithKeys(cfg, storekey.NewMemoryProvider(master))
	if err != nil {
		t.Fatal(err)
	}
	if publish {
		pub, ok := st.SealRecipient()
		if !ok {
			t.Fatal("encrypting store has no seal recipient")
		}
		if err := spool.PublishRecipient(cfg.SpoolDir(), spool.Recipient{KeyID: st.KeyID(), PublicKey: pub}); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestRecordWithDaemonUpNeverLoadsKey(t *testing.T) {
	t.Setenv("BELAY_NO_AUTOSTART", "1")
	keys, _ := useSlowKeys(t, 0)
	repo := filepath.Join(t.TempDir(), "repo")
	cfg := newBelayProject(t, repo)
	writeFile(t, filepath.Join(repo, "a.ts"), "x\n")
	d := newFakeDaemon(t, repo)
	if err := apiclient.WritePortFile(cfg, d.port()); err != nil {
		t.Fatal(err)
	}

	t.Chdir(repo)
	for i := 0; i < 3; i++ {
		if err := runBelay(t, "record", "a.ts", "--session", "s1"); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	if n := len(d.recorded()); n != 3 {
		t.Fatalf("daemon got %d records, want 3", n)
	}
	if c := keys.calls.Load(); c != 0 {
		t.Fatalf("record loaded the store key %d times with the daemon up", c)
	}
}

func TestRecordWithDaemonDownSealsWithoutKey(t *testing.T) {
	t.Setenv("BELAY_NO_AUTOSTART", "1")
	keys, master := useSlowKeys(t, 0)
	repo := filepath.Join(t.TempDir(), "repo")
	cfg := newBelayProject(t, repo)
	secret := "api_key = do-not-store-in-plaintext\n"
	writeFile(t, filepath.Join(repo, "a.ts"), secret)
	st := daemonSideStore(t, cfg, master, true)
	pointAtDeadDaemon(t, cfg)

	t.Chdir(repo)
	if err := runBelay(t, "record", "a.ts", "--session", "s1"); err != nil {
		t.Fatalf("record: %v", err)
	}
	if c := keys.calls.Load(); c != 0 {
		t.Fatalf("record loaded the store key %d times while spooling", c)
	}

	pending, _ := spool.Pending(cfg.SpoolDir())
	if len(pending) != 1 {
		t.Fatalf("spool has %d entries, want 1", len(pending))
	}
	raw, _ := os.ReadFile(pending[0])
	if bytes.Contains(raw, []byte("do-not-store-in-plaintext")) {
		t.Fatal("spooled entry holds plaintext content")
	}

	var got *spool.Entry
	if _, err := spool.DrainEntries(cfg.SpoolDir(), func(e *spool.Entry) error { got = e; return nil }); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Sealed == nil || got.Event.ContentHash != "" {
		t.Fatalf("entry = %+v, want sealed content and no content id", got)
	}
	plain, err := st.OpenSealed(got.Sealed)
	if err != nil || string(plain) != secret {
		t.Fatalf("OpenSealed = %q, %v", plain, err)
	}
}

func TestRecordFallsBackToKeyWhenRecipientIsStale(t *testing.T) {
	t.Setenv("BELAY_NO_AUTOSTART", "1")
	keys, master := useSlowKeys(t, 0)
	repo := filepath.Join(t.TempDir(), "repo")
	cfg := newBelayProject(t, repo)
	writeFile(t, filepath.Join(repo, "a.ts"), "x\n")
	st := daemonSideStore(t, cfg, master, false)
	other, _ := storekey.Generate()
	otherStore, err := store.Open(t.TempDir(), store.Options{Encrypt: true, Keys: storekey.NewMemoryProvider(other)})
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := otherStore.SealRecipient()
	if err := spool.PublishRecipient(cfg.SpoolDir(), spool.Recipient{KeyID: otherStore.KeyID(), PublicKey: pub}); err != nil {
		t.Fatal(err)
	}
	pointAtDeadDaemon(t, cfg)

	t.Chdir(repo)
	if err := runBelay(t, "record", "a.ts"); err != nil {
		t.Fatalf("record: %v", err)
	}
	var got *spool.Entry
	_, _ = spool.DrainEntries(cfg.SpoolDir(), func(e *spool.Entry) error { got = e; return nil })
	if got == nil || got.Sealed != nil || got.Event.ContentHash != st.ContentID([]byte("x\n")) {
		t.Fatalf("stale recipient must fall back to a stored object, got %+v", got)
	}
	if keys.calls.Load() == 0 {
		t.Fatal("fallback path should have loaded the key")
	}
}

func TestRecordUnencryptedStoreNeverLoadsKey(t *testing.T) {
	t.Setenv("BELAY_NO_AUTOSTART", "1")
	keys, _ := useSlowKeys(t, 0)
	repo := filepath.Join(t.TempDir(), "repo")
	newBelayProject(t, repo)
	if err := os.WriteFile(filepath.Join(repo, config.BelayDir, "config.toml"), []byte("[store]\nencryption = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(repo)
	if err != nil || cfg.Store.Encryption {
		t.Fatalf("config: %v, encryption=%v", err, cfg.Store.Encryption)
	}
	writeFile(t, filepath.Join(repo, "a.ts"), "x\n")
	pointAtDeadDaemon(t, cfg)

	t.Chdir(repo)
	if err := runBelay(t, "record", "a.ts"); err != nil {
		t.Fatalf("record: %v", err)
	}
	if c := keys.calls.Load(); c != 0 {
		t.Fatalf("unencrypted store loaded the key %d times", c)
	}
	var got *spool.Entry
	_, _ = spool.DrainEntries(cfg.SpoolDir(), func(e *spool.Entry) error { got = e; return nil })
	if got == nil || got.Event.ContentHash != schema.ContentHashForBytes([]byte("x\n")) {
		t.Fatalf("entry = %+v", got)
	}
}

func TestCheckpointWithDaemonDownQueuesWithoutKeyOrWait(t *testing.T) {
	t.Setenv("BELAY_NO_AUTOSTART", "1")
	keys, _ := useSlowKeys(t, 0)
	repo := filepath.Join(t.TempDir(), "repo")
	cfg := newBelayProject(t, repo)
	pointAtDeadDaemon(t, cfg)

	t.Chdir(repo)
	start := time.Now()
	if err := runBelay(t, "checkpoint", "--label", "pre-bash: rm -rf build", "--reason", "hook", "--tool", "claude-code", "--quiet"); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("checkpoint took %s with the daemon down", el)
	}
	if c := keys.calls.Load(); c != 0 {
		t.Fatalf("checkpoint loaded the key %d times", c)
	}
	var got []*schema.Event
	_, _ = spool.Drain(cfg.SpoolDir(), func(e *schema.Event) { got = append(got, e) })
	if len(got) != 1 || got[0].Op != schema.OpCheckpoint || got[0].Metadata["label"] != "pre-bash: rm -rf build" || got[0].Metadata["tool_name"] != "claude-code" {
		t.Fatalf("queued = %+v", got)
	}
}

func TestRecordHookLatencyWithSlowKeychain(t *testing.T) {
	t.Setenv("BELAY_NO_AUTOSTART", "1")
	const delay = 250 * time.Millisecond
	keys, master := useSlowKeys(t, delay)
	repo := filepath.Join(t.TempDir(), "repo")
	cfg := newBelayProject(t, repo)
	writeFile(t, filepath.Join(repo, "a.ts"), "x\n")
	daemonSideStore(t, cfg, master, false)
	pointAtDeadDaemon(t, cfg)
	t.Chdir(repo)

	const runs = 5
	measure := func() time.Duration {
		var total time.Duration
		for i := 0; i < runs; i++ {
			start := time.Now()
			if err := runBelay(t, "record", "a.ts"); err != nil {
				t.Fatalf("record: %v", err)
			}
			total += time.Since(start)
		}
		return total / runs
	}

	before := measure()
	beforeCalls := keys.calls.Load()

	daemonSideStore(t, cfg, master, true)
	keys.calls.Store(0)
	after := measure()

	t.Logf("record, daemon down, keychain %s: key path %s/call (%d key loads), sealed spool %s/call (%d key loads)",
		delay, before, beforeCalls, after, keys.calls.Load())
	if keys.calls.Load() != 0 {
		t.Fatal("sealed path loaded the key")
	}
	if after >= delay {
		t.Fatalf("sealed path took %s, not faster than one keychain call", after)
	}
}
