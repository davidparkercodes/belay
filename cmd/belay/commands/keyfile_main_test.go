package commands

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidparkercodes/belay/internal/testenv"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		os.Exit(0)
	}
	if err := os.Setenv("BELAY_NO_AUTOSTART", "1"); err != nil {
		panic(err)
	}
	autoStartLauncher = func(string) (int, error) {
		return 0, errors.New("daemon launch disabled in tests")
	}
	restoreGit, err := testenv.IsolateGit()
	if err != nil {
		panic(err)
	}
	dir, err := os.MkdirTemp("", "belay-key-test-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("BELAY_KEY_FILE", filepath.Join(dir, "store.key")); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	restoreGit()
	os.Exit(code)
}
