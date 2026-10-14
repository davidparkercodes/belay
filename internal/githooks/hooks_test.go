package githooks

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHookScriptsTargetDiscoveredPortWithIdentity(t *testing.T) {
	for _, name := range supportedHooks {
		script := hookScript(name)
		for _, want := range []string{".belay/daemon.port", "/api/health", "X-Belay-Project", "/api/events/git"} {
			if !strings.Contains(script, want) {
				t.Errorf("%s hook missing %q", name, want)
			}
		}
		if strings.Contains(script, "localhost:") {
			t.Errorf("%s hook still hardcodes the old endpoint", name)
		}
	}
}

func TestPostCheckoutHookReportsToThisProjectsDaemon(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not available")
	}
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".belay"), 0o755); err != nil {
		t.Fatal(err)
	}
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var paths, projects []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		projects = append(projects, r.Header.Get("X-Belay-Project"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(filepath.Join(repo, ".belay", "daemon.port"), []byte(strconv.Itoa(port)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	hook := filepath.Join(t.TempDir(), "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n"+postCheckoutScript()+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", hook, "aaa", "bbb", "1")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v\n%s", err, out)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(paths)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(paths) < 2 || paths[0] != "GET /api/health" || paths[1] != "POST /api/events/git" {
		t.Fatalf("requests = %v, want health probe then git event", paths)
	}
	for _, p := range projects {
		if p != realRepo {
			t.Errorf("X-Belay-Project = %q, want %q", p, realRepo)
		}
	}
}
