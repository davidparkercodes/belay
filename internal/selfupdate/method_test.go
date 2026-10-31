package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func envOf(vars map[string]string, home string) Env {
	return Env{Getenv: func(k string) string { return vars[k] }, HomeDir: home}
}

func TestDetect(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	checkout := filepath.Join(root, "src", "belay")
	if err := os.MkdirAll(filepath.Join(checkout, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "go.mod"), []byte("module "+ModulePath+"\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "src", "other")
	if err := os.MkdirAll(filepath.Join(other, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "go.mod"), []byte("module example.com/other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gobin := filepath.Join(root, "gobin")

	for _, tc := range []struct {
		name    string
		exe     string
		version string
		vars    map[string]string
		want    Method
	}{
		{"dev version", filepath.Join(root, "bin", "belay"), "dev", nil, MethodDev},
		{"source checkout", filepath.Join(checkout, "bin", "belay"), "v1.9.0", nil, MethodDev},
		{"cask", filepath.Join(root, "brew", "Caskroom", "belay", "1.9.0", "belay"), "v1.9.0", nil, MethodHomebrew},
		{"cellar", filepath.Join(root, "brew", "Cellar", "belay", "1.9.0", "bin", "belay"), "v1.9.0", nil, MethodHomebrew},
		{"homebrew prefix", filepath.Join(root, "hb", "bin", "belay"), "v1.9.0", map[string]string{"HOMEBREW_PREFIX": filepath.Join(root, "hb")}, MethodHomebrew},
		{"gobin", filepath.Join(gobin, "belay"), "v1.9.0", map[string]string{"GOBIN": gobin}, MethodGoInstall},
		{"gopath", filepath.Join(root, "gp", "bin", "belay"), "v1.9.0", map[string]string{"GOPATH": filepath.Join(root, "gp")}, MethodGoInstall},
		{"default gopath", filepath.Join(home, "go", "bin", "belay"), "v1.9.0", nil, MethodGoInstall},
		{"other module", filepath.Join(other, "bin", "belay"), "v1.9.0", nil, MethodRelease},
		{"release", filepath.Join(home, ".local", "bin", "belay"), "v1.9.0", nil, MethodRelease},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Detect(tc.exe, tc.version, envOf(tc.vars, home))
			if got.Method != tc.want {
				t.Fatalf("Detect(%s) = %s (%s), want %s", tc.exe, got.Method, got.Reason, tc.want)
			}
			if got.Path != filepath.Clean(tc.exe) {
				t.Fatalf("path = %s", got.Path)
			}
		})
	}
}

func TestDetectHomebrewPrefixFromCaskroom(t *testing.T) {
	got := Detect(filepath.FromSlash("/x/brew/Caskroom/belay/1.0.0/belay"), "v1.0.0", envOf(nil, ""))
	if got.Prefix != filepath.FromSlash("/x/brew") {
		t.Fatalf("prefix = %q", got.Prefix)
	}
}
