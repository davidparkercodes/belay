package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGuard_SessionDryRunPreviewsHeldBack(t *testing.T) {
	f := newFixture(t)
	f.sessionCreate("s1", "clean.txt", "hello\n")
	f.sessionCreate("s1", "secret.env.txt", secretBody())
	before, _ := gitCmd(f.projectDir, "rev-parse", "--verify", "--quiet", testRef)

	res, err := ProjectSession(f.idx, f.objStore, f.projectDir, ProjectOptions{
		SessionID: "s1", TargetRef: testRef, DryRun: true, Guard: LoadSecretGuard(f.projectDir),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := heldPaths(res.HeldBack); len(got) != 1 || got[0] != "secret.env.txt" {
		t.Fatalf("dry-run held back = %v", got)
	}
	if res.FilesAdded != 1 {
		t.Fatalf("dry-run counts the held-back file: +%d", res.FilesAdded)
	}
	after, _ := gitCmd(f.projectDir, "rev-parse", "--verify", "--quiet", testRef)
	if before != after {
		t.Fatal("dry-run moved the target ref")
	}
}

func TestGuard_WorkingTreeDryRunPreviewsHeldBack(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.projectDir, "token.txt"), []byte(secretBody()), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ProjectWorkingTree(f.projectDir, ProjectWorkingTreeOptions{
		TargetRef: testRef, DryRun: true, Guard: LoadSecretGuard(f.projectDir),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := heldPaths(res.HeldBack); len(got) != 1 || got[0] != "token.txt" {
		t.Fatalf("dry-run held back = %v", got)
	}
	if out, _ := gitCmd(f.projectDir, "rev-parse", "--verify", "--quiet", testRef); out != "" {
		t.Fatal("dry-run created the target ref")
	}
}
