package commands

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	runErr := fn()
	w.Close()
	os.Stdout = prev
	return <-done, runErr
}

func TestProjectDryRunListsHeldBackFiles(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	newBelayProject(t, repo)
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	writeFile(t, filepath.Join(repo, "README.md"), "hi\n")
	cmd := exec.Command("git", "add", "README.md")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}
	cmd = exec.Command("git", "commit", "-q", "-m", "init")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s", out)
	}
	token := strings.Join([]string{"gh", "p_", "Q7rT2mXk9LpZ4v", "Bn8WcY3hJd6FsA1eGu5NoK"}, "")
	writeFile(t, filepath.Join(repo, "creds.txt"), "GITHUB_TOKEN="+token+"\n")

	t.Chdir(repo)
	out, err := captureStdout(t, func() error {
		return runBelay(t, "project", "--working-tree", "--dry-run", "--push", "")
	})
	if err != nil {
		t.Fatalf("project --dry-run: %v", err)
	}
	if !strings.Contains(out, "would hold back creds.txt") {
		t.Fatalf("dry-run output does not preview the held-back file:\n%s", out)
	}
	if strings.Contains(out, token) {
		t.Fatal("dry-run output contains the secret")
	}
}
