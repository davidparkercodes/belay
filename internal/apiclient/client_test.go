package apiclient

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/davidparkercodes/belay/internal/config"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, config.BelayDir), 0o755); err != nil {
		t.Fatal(err)
	}
	return config.DefaultConfig(root)
}

func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

func TestPortResolution(t *testing.T) {
	cfg := testConfig(t)

	cfg.API.Port = 0
	if got := Port(cfg); got != config.DefaultAPIPort {
		t.Errorf("no port file, port 0: got %d, want %d", got, config.DefaultAPIPort)
	}

	cfg.API.Port = 40001
	if got := Port(cfg); got != 40001 {
		t.Errorf("no port file: got %d, want configured 40001", got)
	}

	if err := WritePortFile(cfg, 40002); err != nil {
		t.Fatal(err)
	}
	if got := Port(cfg); got != 40002 {
		t.Errorf("port file: got %d, want 40002", got)
	}

	if err := os.WriteFile(cfg.APIPortPath(), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Port(cfg); got != 40001 {
		t.Errorf("bad port file: got %d, want configured 40001", got)
	}
}

func TestRemovePortFileOnlyRemovesOwnPort(t *testing.T) {
	cfg := testConfig(t)
	if err := WritePortFile(cfg, 40010); err != nil {
		t.Fatal(err)
	}
	RemovePortFile(cfg, 40011)
	if _, ok := ReadPortFile(cfg); !ok {
		t.Fatal("port file for another port was removed")
	}
	RemovePortFile(cfg, 40010)
	if _, ok := ReadPortFile(cfg); ok {
		t.Fatal("own port file was not removed")
	}
}

func TestClientSendsProjectHeader(t *testing.T) {
	cfg := testConfig(t)
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get(ProjectHeader)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()
	if err := WritePortFile(cfg, serverPort(t, srv)); err != nil {
		t.Fatal(err)
	}

	if err := New(cfg, 0).PostJSON("/api/record", map[string]string{"file_path": "a"}, nil); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if !config.SameRoot(got, cfg.ProjectRoot) {
		t.Errorf("header = %q, want %q", got, cfg.ProjectRoot)
	}
}

func TestClientMismatchError(t *testing.T) {
	cfg := testConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "project_mismatch", "project_root": "/other/project", "error": "x"})
	}))
	defer srv.Close()
	if err := WritePortFile(cfg, serverPort(t, srv)); err != nil {
		t.Fatal(err)
	}

	err := New(cfg, 0).PostJSON("/api/record", map[string]string{}, nil)
	var mismatch *MismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %v, want MismatchError", err)
	}
	if mismatch.Served != "/other/project" {
		t.Errorf("Served = %q", mismatch.Served)
	}
	if IsUnreachable(err) {
		t.Error("mismatch must not count as unreachable")
	}
}

func TestClientStatusErrorAndUnreachable(t *testing.T) {
	cfg := testConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad"})
	}))
	port := serverPort(t, srv)
	if err := WritePortFile(cfg, port); err != nil {
		t.Fatal(err)
	}

	err := New(cfg, 0).PostJSON("/x", map[string]string{}, nil)
	var status *StatusError
	if !errors.As(err, &status) || status.Status != http.StatusBadRequest {
		t.Fatalf("err = %v, want StatusError 400", err)
	}
	if IsUnreachable(err) {
		t.Error("status error must not count as unreachable")
	}

	srv.Close()
	err = New(cfg, 0).PostJSON("/x", map[string]string{}, nil)
	if !IsUnreachable(err) {
		t.Fatalf("err = %v, want unreachable after server closed on port %s", err, strconv.Itoa(port))
	}
}
