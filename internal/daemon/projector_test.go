package daemon

import (
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gitbridge "github.com/davidparkercodes/belay/internal/git"
)

func projectorTestDaemon(t *testing.T, holdBack bool) *Daemon {
	t.Helper()
	cfg := testConfig(t)
	cfg.Projection.HoldBackSecrets = holdBack
	root := cfg.ProjectRoot
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
		}
	}
	run("init", "--quiet")
	run("config", "user.email", "test@belay.dev")
	run("config", "user.name", "Belay Test")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".belay/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", ".gitignore")
	run("commit", "--quiet", "-m", "init")
	token := strings.Join([]string{"gh", "p_", "Q7rT2mXk9LpZ4v", "Bn8WcY3hJd6FsA1eGu5NoK"}, "")
	if err := os.WriteFile(filepath.Join(root, "creds.txt"), []byte("TOKEN="+token+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return &Daemon{cfg: cfg, logger: log.New(io.Discard, "", 0)}
}

func runQueuedProjection(t *testing.T, d *Daemon, job gitbridge.ProjectionJob) projectionRecord {
	t.Helper()
	p := newProjector(d)
	p.Start()
	defer p.Stop()
	p.Enqueue(job)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if recent, _ := p.Status()["recent"].([]projectionRecord); len(recent) > 0 {
			return recent[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("projection did not finish")
	return projectionRecord{}
}

func TestProjector_SurfacesHeldBackFiles(t *testing.T) {
	d := projectorTestDaemon(t, true)
	rec := runQueuedProjection(t, d, gitbridge.ProjectionJob{WorkingTree: true})
	if rec.Error != "" {
		t.Fatalf("projection error: %s", rec.Error)
	}
	if len(rec.HeldBack) != 1 || rec.HeldBack[0].Path != "creds.txt" {
		t.Fatalf("held_back = %+v", rec.HeldBack)
	}
	if !strings.Contains(rec.Summary, "held back 1 file(s)") {
		t.Fatalf("summary = %q", rec.Summary)
	}
	cmd := exec.Command("git", "ls-tree", "-r", "--name-only", "refs/heads/belay-history")
	cmd.Dir = d.cfg.ProjectRoot
	out, _ := cmd.Output()
	if strings.Contains(string(out), "creds.txt") {
		t.Fatal("creds.txt must not be projected")
	}
}

func TestProjector_ConfigOffProjectsEverything(t *testing.T) {
	d := projectorTestDaemon(t, false)
	rec := runQueuedProjection(t, d, gitbridge.ProjectionJob{WorkingTree: true})
	if rec.Error != "" || len(rec.HeldBack) != 0 {
		t.Fatalf("hold_back_secrets=false should hold nothing back: %+v", rec)
	}
	cmd := exec.Command("git", "ls-tree", "-r", "--name-only", "refs/heads/belay-history")
	cmd.Dir = d.cfg.ProjectRoot
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "creds.txt") {
		t.Fatalf("creds.txt should be projected, tree: %s", out)
	}
}

func TestProjector_AllowSecretsJob(t *testing.T) {
	d := projectorTestDaemon(t, true)
	rec := runQueuedProjection(t, d, gitbridge.ProjectionJob{WorkingTree: true, AllowSecrets: true})
	if rec.Error != "" || len(rec.HeldBack) != 0 {
		t.Fatalf("allow_secrets job should hold nothing back: %+v", rec)
	}
}
