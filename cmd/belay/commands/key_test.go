package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"
	"github.com/davidparkercodes/belay/internal/storekey"
)

func useTestKeyFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keys", "store.key")
	prev := keyProvider
	keyProvider = func() storekey.Provider { return &storekey.FileProvider{Path: path} }
	t.Cleanup(func() { keyProvider = prev })
	return path
}

func chdirTemp(t *testing.T, dir string) {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func runRoot(t *testing.T, args ...string) error {
	t.Helper()
	root := NewRootCmd("test")
	root.SetArgs(args)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	return root.Execute()
}

func TestKeyImportExportRoundTrip(t *testing.T) {
	keyPath := useTestKeyFile(t)
	chdirTemp(t, t.TempDir())

	master, _ := storekey.Generate()
	in := filepath.Join(t.TempDir(), "backup.key")
	if err := os.WriteFile(in, []byte(storekey.Encode(master)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runRoot(t, "key", "import", in); err != nil {
		t.Fatalf("import: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key file not installed: %v", err)
	}
	out := filepath.Join(t.TempDir(), "exported.key")
	if err := runRoot(t, "key", "export", "--out", out); err != nil {
		t.Fatalf("export: %v", err)
	}
	data, _ := os.ReadFile(out)
	got, err := storekey.Decode(string(data))
	if err != nil || !bytes.Equal(got, master) {
		t.Fatal("exported key differs from the imported one")
	}
	if info, _ := os.Stat(out); info.Mode().Perm() != 0o600 {
		t.Errorf("export mode = %v", info.Mode().Perm())
	}

	other, _ := storekey.Generate()
	if err := os.WriteFile(in, []byte(storekey.Encode(other)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runRoot(t, "key", "import", in); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("import of a different key must be refused without --force: %v", err)
	}
	if err := runRoot(t, "key", "import", in, "--force"); err != nil {
		t.Fatalf("forced import: %v", err)
	}
}

func TestKeyExportWithoutKeyFails(t *testing.T) {
	useTestKeyFile(t)
	chdirTemp(t, t.TempDir())
	if err := runRoot(t, "key", "export"); err == nil {
		t.Fatal("export with no key must fail")
	}
}

func legacyProject(t *testing.T) (*config.Config, map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	cfg := config.DefaultConfig(root)
	cfg.Safety.AllowWrites = true
	if err := os.MkdirAll(cfg.BelayPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ConfigPath(), []byte(cfg.ToTOML()), 0o644); err != nil {
		t.Fatal(err)
	}

	legacy, err := store.NewStore(cfg.ObjectsDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := index.Open(cfg.IndexPath())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	contents := map[string][]byte{}
	prev := ""
	for i := 0; i < 3; i++ {
		data := []byte("config: value " + strings.Repeat("v", i+1) + "\n\x00binary\xff")
		id, _, _ := legacy.Put(data)
		ev := &schema.Event{EventID: schema.NewEventID(), Version: schema.SchemaVersion, FilePath: "conf.yml", Op: schema.OpModify, ContentHash: id, PreviousHash: prev}
		ev.SetTimestamp(time.Now().Add(time.Duration(i-10) * time.Second))
		if err := idx.IndexEvent(ev, "", 0); err != nil {
			t.Fatal(err)
		}
		contents[ev.EventID] = data
		prev = id
	}
	return cfg, contents
}

func TestKeyMigrateCommandAndRestoreByteExact(t *testing.T) {
	useTestKeyFile(t)
	cfg, contents := legacyProject(t)
	chdirTemp(t, cfg.ProjectRoot)

	if err := runRoot(t, "key", "migrate"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.ReadMigrationStatus(nil, cfg.StoreManifestPath())
	if st.Format != store.FormatEncrypted || st.Migration != store.MigrationComplete {
		t.Fatalf("manifest after migrate = %+v", st)
	}
	if _, err := os.Stat(cfg.PIDPath()); !os.IsNotExist(err) {
		t.Fatal("migrate must release the daemon lock")
	}

	objStore, err := openStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := index.Open(cfg.IndexPath())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	target := filepath.Join(cfg.ProjectRoot, "conf.yml")
	for id, want := range contents {
		ev, err := idx.GetEvent(id)
		if err != nil {
			t.Fatal(err)
		}
		if enc, _ := objStore.IsEncrypted(ev.ContentHash); !enc {
			t.Fatal("event still points at a legacy object")
		}
		if err := os.WriteFile(target, []byte("edited since"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeRestore(objStore, cfg, cfg.ProjectRoot, "conf.yml", ev, false, true); err != nil {
			t.Fatalf("restore: %v", err)
		}
		got, _ := os.ReadFile(target)
		if !bytes.Equal(got, want) {
			t.Fatalf("restore not byte-exact: %q vs %q", got, want)
		}
		if objStore.ContentID(got) != ev.ContentHash {
			t.Fatal("restored bytes must hash to the stored id through the store's hasher")
		}
	}
}

func TestKeyStatusJSON(t *testing.T) {
	useTestKeyFile(t)
	cfg, _ := legacyProject(t)
	chdirTemp(t, cfg.ProjectRoot)
	if err := runRoot(t, "key", "status", "--json"); err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := runRoot(t, "key", "status"); err != nil {
		t.Fatalf("status: %v", err)
	}
}
