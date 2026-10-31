package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadUserConfigDefaults(t *testing.T) {
	cfg, err := LoadUserConfigFrom(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Update.Mode != UpdateNotify {
		t.Fatalf("mode = %q", cfg.Update.Mode)
	}
}

func TestLoadUserConfigUnknownModeFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[update]\nmode = \"sometimes\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadUserConfigFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Update.Mode != UpdateNotify {
		t.Fatalf("mode = %q", cfg.Update.Mode)
	}
}

func TestSetUserUpdateModeKeepsOtherSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "belay", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[other]\nkeep = true\n\n[update]\nmode = \"notify\"\nextra = 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetUserUpdateMode(path, "AUTO"); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadUserConfigFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Update.Mode != UpdateAuto {
		t.Fatalf("mode = %q", cfg.Update.Mode)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "keep = true") || !strings.Contains(string(data), "extra = 3") {
		t.Fatalf("other settings lost:\n%s", data)
	}
}

func TestSetUserUpdateModeCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "config.toml")
	if err := SetUserUpdateMode(path, UpdateOff); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadUserConfigFrom(path)
	if err != nil || cfg.Update.Mode != UpdateOff {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}
}

func TestSetUserUpdateModeRejectsUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	for _, m := range []string{"", "sometimes"} {
		if err := SetUserUpdateMode(path, m); err == nil {
			t.Fatalf("mode %q accepted", m)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file written for a rejected mode")
	}
}
