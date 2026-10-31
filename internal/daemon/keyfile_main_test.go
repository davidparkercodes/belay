package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "belay-key-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("BELAY_KEY_FILE", filepath.Join(dir, "store.key"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
