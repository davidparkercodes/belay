package commands

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidparkercodes/belay/internal/selfupdate"
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
	updateNewClient = func(string) *selfupdate.Client {
		c := selfupdate.NewClient("belay-test")
		c.HTTP = &http.Client{Transport: noNetwork{}}
		return c
	}
	selfupdate.RunCommand = func(context.Context, []string, string, ...string) ([]byte, error) {
		return nil, errors.New("commands disabled in tests")
	}
	selfupdate.LookPath = func(string) (string, error) {
		return "", errors.New("lookups disabled in tests")
	}
	updateIsTerminal = func() bool { return false }
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

type noNetwork struct{}

func (noNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("network disabled in tests")
}
