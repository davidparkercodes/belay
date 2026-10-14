package api

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidparkercodes/belay/internal/apiclient"
	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/session"
	"github.com/davidparkercodes/belay/internal/store"
)

func TestProjectMiddleware(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	if err := os.MkdirAll(filepath.Join(root, config.BelayDir), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}

	srv := New(config.DefaultConfig(root), nil, nil, nil, log.New(io.Discard, "", 0), nil, "test", nil)
	handler := srv.projectMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusOK},
		{"same root", root, http.StatusOK},
		{"same root via symlink", link, http.StatusOK},
		{"other project", filepath.Join(base, "other"), http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/record", nil)
			if tc.header != "" {
				req.Header.Set(apiclient.ProjectHeader, tc.header)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if tc.want == http.StatusConflict {
				var body map[string]string
				if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["code"] != "project_mismatch" || body["project_root"] != root {
					t.Errorf("body = %v", body)
				}
			}
		})
	}
}

func TestServerFallsBackWhenPortTaken(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, config.BelayDir), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig(root)

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	takenPort := occupied.Addr().(*net.TCPAddr).Port
	cfg.API.Port = takenPort

	idx, err := index.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	objStore, err := store.NewStore(filepath.Join(root, "objects"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer objStore.Close()

	srv := New(cfg, idx, objStore, session.NewRegistry(), log.New(io.Discard, "", 0), nil, "test", nil)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start with taken port: %v", err)
	}
	if srv.Port() == takenPort || srv.Port() == 0 {
		t.Fatalf("Port() = %d, want a free port other than %d", srv.Port(), takenPort)
	}
	if got, ok := apiclient.ReadPortFile(cfg); !ok || got != srv.Port() {
		t.Fatalf("port file = %d (%v), want %d", got, ok, srv.Port())
	}

	var health map[string]interface{}
	if err := apiclient.New(cfg, 0).Get("/api/health", &health); err != nil {
		t.Fatalf("client could not reach fallback port: %v", err)
	}
	if health["project_root"] != root {
		t.Errorf("health project_root = %v, want %s", health["project_root"], root)
	}

	if err := srv.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, ok := apiclient.ReadPortFile(cfg); ok {
		t.Error("port file left behind after Stop")
	}
}
