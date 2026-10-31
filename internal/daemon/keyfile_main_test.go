package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidparkercodes/belay/internal/selfupdate"
	"github.com/davidparkercodes/belay/internal/testenv"
)

func TestMain(m *testing.M) {
	reexecProcess = func(string, []string, []string) error {
		return errors.New("re-exec disabled in tests")
	}
	probeBinary = func(string) (string, error) {
		return "", errors.New("binary probe disabled in tests")
	}
	if err := os.Setenv(selfupdate.DisableEnv, "1"); err != nil {
		panic(err)
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
