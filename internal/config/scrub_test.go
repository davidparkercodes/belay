package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScrubMode_DefaultIsNotice(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	if cfg.Scrub.Mode != ScrubNotice {
		t.Fatalf("default mode = %q", cfg.Scrub.Mode)
	}
}

func TestNormalizeScrubMode(t *testing.T) {
	cases := map[string]struct {
		want string
		ok   bool
	}{
		"off": {ScrubOff, true}, "notice": {ScrubNotice, true}, "auto": {ScrubAuto, true},
		" AUTO ": {ScrubAuto, true}, "": {ScrubNotice, true}, "always": {ScrubNotice, false},
	}
	for in, c := range cases {
		got, ok := NormalizeScrubMode(in)
		if got != c.want || ok != c.ok {
			t.Errorf("NormalizeScrubMode(%q) = %q, %v", in, got, ok)
		}
	}
}

func writeScrubConfig(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, BelayDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_ScrubModeValidation(t *testing.T) {
	for body, want := range map[string]string{
		"[scrub]\nmode = \"auto\"\n":       ScrubAuto,
		"[scrub]\nmode = \"off\"\n":        ScrubOff,
		"[scrub]\nmode = \"bogus\"\n":      ScrubNotice,
		"[daemon]\nlog_level = \"info\"\n": ScrubNotice,
	} {
		root := t.TempDir()
		writeScrubConfig(t, root, body)
		cfg, err := Load(root)
		if err != nil {
			t.Fatalf("Load(%q) error: %v", body, err)
		}
		if cfg.Scrub.Mode != want {
			t.Errorf("Load(%q) mode = %q, want %q", body, cfg.Scrub.Mode, want)
		}
	}
}

func TestToTOML_ScrubRoundTrip(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Scrub.Mode = ScrubAuto
	writeScrubConfig(t, root, cfg.ToTOML())
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Scrub.Mode != ScrubAuto {
		t.Fatalf("round trip mode = %q", loaded.Scrub.Mode)
	}
}
