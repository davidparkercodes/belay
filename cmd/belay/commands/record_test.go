package commands

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/apiclient"
	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/spool"
)

type fakeDaemon struct {
	root    string
	mu      sync.Mutex
	records []recordRequest
	srv     *httptest.Server
}

func newFakeDaemon(t *testing.T, root string) *fakeDaemon {
	t.Helper()
	f := &fakeDaemon{root: root}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if claimed := r.Header.Get(apiclient.ProjectHeader); claimed != "" && !config.SameRoot(claimed, f.root) {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "project_mismatch", "project_root": f.root, "error": "mismatch"})
			return
		}
		var req recordRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.records = append(f.records, req)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "recorded"})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDaemon) port() int {
	return f.srv.Listener.Addr().(*net.TCPAddr).Port
}

func (f *fakeDaemon) recorded() []recordRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordRequest(nil), f.records...)
}

func newBelayProject(t *testing.T, root string) *config.Config {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, config.BelayDir), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runBelay(t *testing.T, args ...string) error {
	t.Helper()
	root := NewRootCmd("test")
	root.SetArgs(args)
	return root.Execute()
}

func TestRecordNeverLandsInAnotherProjectsDaemon(t *testing.T) {
	t.Setenv("BELAY_NO_AUTOSTART", "1")
	base := t.TempDir()
	repoA := filepath.Join(base, "repoA")
	repoB := filepath.Join(base, "repoB")
	newBelayProject(t, repoA)
	cfgB := newBelayProject(t, repoB)
	writeFile(t, filepath.Join(repoA, "src", "routes.ts"), "repoA\n")
	writeFile(t, filepath.Join(repoB, "src", "routes.ts"), "repoB edit\n")

	daemonA := newFakeDaemon(t, repoA)
	if err := apiclient.WritePortFile(cfgB, daemonA.port()); err != nil {
		t.Fatal(err)
	}

	t.Chdir(repoA)
	if err := runBelay(t, "record", filepath.Join(repoB, "src", "routes.ts"), "--session", "agent-in-repoB", "--tool", "claude-code"); err != nil {
		t.Fatalf("record: %v", err)
	}

	if got := daemonA.recorded(); len(got) != 0 {
		t.Fatalf("repoA's daemon accepted repoB's edit: %+v", got)
	}

	var queued []*schema.Event
	if _, err := spool.Drain(cfgB.SpoolDir(), func(e *schema.Event) { queued = append(queued, e) }); err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 {
		t.Fatalf("repoB spool has %d events, want 1", len(queued))
	}
	if queued[0].FilePath != filepath.Join("src", "routes.ts") || queued[0].SessionID != "agent-in-repoB" {
		t.Errorf("queued event = %+v", queued[0])
	}
}

func TestRecordResolvesPathAgainstWorkingDirectory(t *testing.T) {
	t.Setenv("BELAY_NO_AUTOSTART", "1")
	repo := filepath.Join(t.TempDir(), "repo")
	cfg := newBelayProject(t, repo)
	writeFile(t, filepath.Join(repo, "a.ts"), "root\n")
	writeFile(t, filepath.Join(repo, "web", "a.ts"), "web\n")

	d := newFakeDaemon(t, repo)
	if err := apiclient.WritePortFile(cfg, d.port()); err != nil {
		t.Fatal(err)
	}

	t.Chdir(filepath.Join(repo, "web"))
	if err := runBelay(t, "record", "a.ts", "--session", "s1"); err != nil {
		t.Fatalf("record: %v", err)
	}

	got := d.recorded()
	if len(got) != 1 || got[0].FilePath != filepath.Join("web", "a.ts") {
		t.Fatalf("recorded = %+v, want web/a.ts", got)
	}
}

func TestRecordQueuesAndStartsDaemonWhenUnreachable(t *testing.T) {
	t.Setenv("BELAY_NO_AUTOSTART", "")
	repo := filepath.Join(t.TempDir(), "repo")
	cfg := newBelayProject(t, repo)
	writeFile(t, filepath.Join(repo, "a.ts"), "x\n")

	closed := httptest.NewServer(http.NotFoundHandler())
	port := closed.Listener.Addr().(*net.TCPAddr).Port
	closed.Close()
	if err := apiclient.WritePortFile(cfg, port); err != nil {
		t.Fatal(err)
	}

	var launched []string
	prevLauncher, prevWait := autoStartLauncher, autoStartWait
	autoStartLauncher = func(root string) (int, error) {
		launched = append(launched, root)
		return 1, nil
	}
	autoStartWait = 50 * time.Millisecond
	t.Cleanup(func() { autoStartLauncher, autoStartWait = prevLauncher, prevWait })

	t.Chdir(repo)
	if err := runBelay(t, "record", "a.ts"); err != nil {
		t.Fatalf("record: %v", err)
	}

	if len(launched) != 1 || !config.SameRoot(launched[0], repo) {
		t.Fatalf("launcher calls = %v, want one for %s", launched, repo)
	}
	if pending, _ := spool.Pending(cfg.SpoolDir()); len(pending) != 1 {
		t.Fatalf("spool has %d events, want 1", len(pending))
	}
	if _, err := os.Stat(cfg.StartLockPath()); !os.IsNotExist(err) {
		t.Errorf("start lock left behind: %v", err)
	}
}

func TestAutoStartRespectsFreshLock(t *testing.T) {
	t.Setenv("BELAY_NO_AUTOSTART", "")
	cfg := newBelayProject(t, filepath.Join(t.TempDir(), "repo"))
	if err := os.Mkdir(cfg.StartLockPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := autoStartLauncher
	autoStartLauncher = func(string) (int, error) {
		t.Error("launcher called while another shell holds the start lock")
		return 0, nil
	}
	t.Cleanup(func() { autoStartLauncher = prev })

	if autoStartDaemon(cfg) {
		t.Error("autoStartDaemon reported a start while locked")
	}
}

func TestAutoStartDisabledByEnv(t *testing.T) {
	t.Setenv("BELAY_NO_AUTOSTART", "1")
	cfg := newBelayProject(t, filepath.Join(t.TempDir(), "repo"))
	prev := autoStartLauncher
	autoStartLauncher = func(string) (int, error) {
		t.Error("launcher called with BELAY_NO_AUTOSTART=1")
		return 0, nil
	}
	t.Cleanup(func() { autoStartLauncher = prev })

	if autoStartDaemon(cfg) {
		t.Error("autoStartDaemon started a daemon with BELAY_NO_AUTOSTART=1")
	}
}
