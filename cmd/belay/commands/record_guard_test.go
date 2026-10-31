package commands

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/davidparkercodes/belay/internal/spool"
	"github.com/davidparkercodes/belay/internal/store"
	"github.com/davidparkercodes/belay/internal/storekey"
)

type unavailableKeys struct{ creates int }

func (p *unavailableKeys) Keys() ([]storekey.Key, error) { return nil, storekey.ErrKeyUnavailable }
func (p *unavailableKeys) Create() (storekey.Key, error) {
	p.creates++
	return storekey.Key{}, errors.New("must not be called")
}
func (p *unavailableKeys) Install([]byte, bool) (storekey.Key, error) {
	return storekey.Key{}, errors.New("must not be called")
}
func (p *unavailableKeys) Describe() string { return "unavailable" }

func TestRecordQueuesWhenStoreKeyIsUnavailable(t *testing.T) {
	t.Setenv("BELAY_NO_AUTOSTART", "1")
	p := &unavailableKeys{}
	prev := keyProvider
	keyProvider = func() storekey.Provider { return p }
	t.Cleanup(func() { keyProvider = prev })

	repo := filepath.Join(t.TempDir(), "repo")
	cfg := newBelayProject(t, repo)
	writeFile(t, filepath.Join(repo, "a.ts"), "x\n")
	pointAtDeadDaemon(t, cfg)

	t.Chdir(repo)
	if err := runBelay(t, "record", "a.ts"); err != nil {
		t.Fatalf("record must not fail when the key is unavailable: %v", err)
	}
	if p.creates != 0 {
		t.Fatal("record created a key after a failed lookup")
	}
	var got *spool.Entry
	if _, err := spool.DrainEntries(cfg.SpoolDir(), func(e *spool.Entry) error { got = e; return nil }); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Sealed != nil || got.Event.ContentHash != "" || !spool.IsDeferred(got.Event) {
		t.Fatalf("entry = %+v, want a deferred-content event", got)
	}
}

func TestRecordRefusesRecipientWithUnboundPublicKey(t *testing.T) {
	t.Setenv("BELAY_NO_AUTOSTART", "1")
	_, master := useSlowKeys(t, 0)
	repo := filepath.Join(t.TempDir(), "repo")
	cfg := newBelayProject(t, repo)
	writeFile(t, filepath.Join(repo, "a.ts"), "x\n")
	st := daemonSideStore(t, cfg, master, false)

	other, _ := storekey.Generate()
	otherStore, err := store.Open(t.TempDir(), store.Options{Encrypt: true, Keys: storekey.NewMemoryProvider(other)})
	if err != nil {
		t.Fatal(err)
	}
	wrongPub, _ := otherStore.SealRecipient()
	if err := spool.PublishRecipient(cfg.SpoolDir(), spool.Recipient{KeyID: st.KeyID(), PublicKey: wrongPub}); err != nil {
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
		t.Fatalf("content was sealed to an unverified key: %+v", got)
	}
}
