package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/davidparkercodes/belay/internal/testenv"
)

func TestMain(m *testing.M) {
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
