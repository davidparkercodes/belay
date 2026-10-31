package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/scrub"
)

func scrubTestToken() string {
	return "gh" + "p_" + strings.Repeat("Zx9Yw8Vu7T", 3) + "s6Rq5P"
}

func setupScrubProject(t *testing.T, args ...string) (root, logPath, content string) {
	t.Helper()
	root = t.TempDir()
	oldWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd("test")
	cmd.SetArgs(append([]string{"init", "-y"}, args...))
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	root, _ = os.Getwd()

	projectsDir := filepath.Join(t.TempDir(), "projects")
	oldSources := scrubSources
	scrubSources = func(projectRoot string) []scrub.Source {
		return []scrub.Source{&scrub.ClaudeCode{ProjectsDir: projectsDir, ProjectRoot: projectRoot}}
	}
	t.Cleanup(func() { scrubSources = oldSources })

	dir := filepath.Join(projectsDir, strings.ReplaceAll(filepath.ToSlash(root), "/", "-"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(map[string]string{"type": "user", "content": "token " + scrubTestToken()})
	content = string(line) + "\n" + `{"type":"assistant","content":"ok"}` + "\n"
	logPath = filepath.Join(dir, "sess-1.jsonl")
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(logPath, old, old); err != nil {
		t.Fatal(err)
	}
	return root, logPath, content
}

func runScrubCmd(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := NewRootCmd("test")
	cmd.SetArgs(append([]string{"scrub"}, args...))
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scrub %v: %v", args, err)
	}
	return out.String()
}

func TestInit_ScrubDefaultsToNotice(t *testing.T) {
	root, _, _ := setupScrubProject(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Scrub.Mode != config.ScrubNotice {
		t.Fatalf("mode = %q", cfg.Scrub.Mode)
	}
}

func TestInit_ScrubFlag(t *testing.T) {
	root, _, _ := setupScrubProject(t, "--scrub", "auto")
	cfg, _ := config.Load(root)
	if cfg.Scrub.Mode != config.ScrubAuto {
		t.Fatalf("mode = %q", cfg.Scrub.Mode)
	}
}

func TestInit_ScrubFlagInvalid(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cmd := NewRootCmd("test")
	cmd.SetArgs([]string{"init", "-y", "--scrub", "sometimes"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for invalid --scrub")
	}
}

func TestInitScrubMode_QuestionDefaultsNo(t *testing.T) {
	if got := initScrubMode("", false, false); got != config.ScrubNotice {
		t.Fatalf("unanswered = %q", got)
	}
	if got := initScrubMode("", false, true); got != config.ScrubAuto {
		t.Fatalf("yes = %q", got)
	}
	if got := initScrubMode(config.ScrubOff, true, true); got != config.ScrubOff {
		t.Fatalf("flag = %q", got)
	}
}

func TestScrubNoticeLine(t *testing.T) {
	root, _, _ := setupScrubProject(t)
	cfg, _ := config.Load(root)
	want := "1 credentials in plain text in agent logs. Run belay scrub to redact."
	if got := scrubNoticeLine(cfg); got != want {
		t.Fatalf("notice = %q", got)
	}
	cfg.Scrub.Mode = config.ScrubOff
	if got := scrubNoticeLine(cfg); got != "" {
		t.Fatalf("off notice = %q", got)
	}
	cfg.Scrub.Mode = config.ScrubAuto
	if got := scrubNoticeLine(cfg); got != "" {
		t.Fatalf("auto notice = %q", got)
	}
}

func TestScrubCmd_DryRunRunUndo(t *testing.T) {
	_, logPath, content := setupScrubProject(t)

	out := runScrubCmd(t, "--dry-run", "--json")
	if strings.Contains(out, scrubTestToken()) {
		t.Fatal("output leaks the secret")
	}
	var rep scrub.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(rep.Files) != 1 || rep.Files[0].Status != scrub.StatusWouldRedact {
		t.Fatalf("dry run = %+v", rep)
	}
	if b, _ := os.ReadFile(logPath); string(b) != content {
		t.Fatal("dry run modified the log")
	}

	out = runScrubCmd(t)
	if !strings.Contains(out, "github-token 1") || strings.Contains(out, scrubTestToken()) {
		t.Fatalf("scrub output = %s", out)
	}
	if b, _ := os.ReadFile(logPath); strings.Contains(string(b), scrubTestToken()) {
		t.Fatal("secret survived")
	}

	runScrubCmd(t, "--undo", logPath)
	if b, _ := os.ReadFile(logPath); string(b) != content {
		t.Fatal("undo not byte-exact")
	}
}
