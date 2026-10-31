package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidparkercodes/belay/internal/schema"
)

const testRef = "refs/heads/belay-history"

func addBareRemote(t *testing.T, projectDir string) string {
	t.Helper()
	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	cmd := exec.Command("git", "init", "--bare", remoteDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %s: %v", out, err)
	}
	if _, err := gitCmd(projectDir, "remote", "add", "origin", remoteDir); err != nil {
		t.Fatalf("remote add: %v", err)
	}
	return remoteDir
}

func cloneForSecondMachine(t *testing.T, remoteDir string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "other")
	cmd := exec.Command("git", "clone", "--quiet", remoteDir, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %s: %v", out, err)
	}
	for _, args := range [][]string{
		{"config", "user.email", "other@belay.dev"},
		{"config", "user.name", "Other Machine"},
	} {
		if _, err := gitCmd(dir, args...); err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
	}
	return dir
}

func projectFileOnMachine(t *testing.T, dir, name, content string) *ProjectResult {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	res, err := ProjectWorkingTree(dir, ProjectWorkingTreeOptions{TargetRef: testRef, BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("ProjectWorkingTree on %s: %v", dir, err)
	}
	return res
}

func remoteTip(t *testing.T, remoteDir string) string {
	t.Helper()
	out, err := gitCmd(remoteDir, "rev-parse", "--verify", "--quiet", testRef)
	if err != nil {
		return ""
	}
	return out
}

func TestSyncRef_NoRemoteRefYet(t *testing.T) {
	f := newFixture(t)
	addBareRemote(t, f.projectDir)
	if _, err := gitCmd(f.projectDir, "push", "--quiet", "origin", "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("seed remote: %v", err)
	}

	res, err := SyncRef(f.projectDir, "origin", testRef)
	if err != nil {
		t.Fatalf("SyncRef: %v", err)
	}
	if res.Action != SyncNoRemoteRef {
		t.Fatalf("action = %s, want %s", res.Action, SyncNoRemoteRef)
	}
}

func TestSyncRef_CreatesLocalFromRemote(t *testing.T) {
	f := newFixture(t)
	remoteDir := addBareRemote(t, f.projectDir)
	if _, err := gitCmd(f.projectDir, "push", "--quiet", "origin", "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("seed remote: %v", err)
	}
	other := cloneForSecondMachine(t, remoteDir)
	projectFileOnMachine(t, other, "from-other.txt", "other")
	if _, err := PushRef(other, "origin", testRef+":"+testRef); err != nil {
		t.Fatalf("push from other: %v", err)
	}

	res, err := SyncRef(f.projectDir, "origin", testRef)
	if err != nil {
		t.Fatalf("SyncRef: %v", err)
	}
	if res.Action != SyncCreated {
		t.Fatalf("action = %s, want %s", res.Action, SyncCreated)
	}
	local, _ := gitCmd(f.projectDir, "rev-parse", testRef)
	if local != remoteTip(t, remoteDir) {
		t.Fatalf("local ref %s != remote tip %s", local, remoteTip(t, remoteDir))
	}
}

func TestSyncRef_ReplaysDivergedLocalCommitsOntoRemote(t *testing.T) {
	f := newFixture(t)
	remoteDir := addBareRemote(t, f.projectDir)
	if _, err := gitCmd(f.projectDir, "push", "--quiet", "origin", "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("seed remote: %v", err)
	}

	local1 := projectFileOnMachine(t, f.projectDir, "local-a.txt", "a")
	local2 := projectFileOnMachine(t, f.projectDir, "local-b.txt", "b")

	other := cloneForSecondMachine(t, remoteDir)
	projectFileOnMachine(t, other, "other.txt", "other")
	if _, err := PushRef(other, "origin", testRef+":"+testRef); err != nil {
		t.Fatalf("push from other: %v", err)
	}
	otherTip := remoteTip(t, remoteDir)

	if _, err := PushRef(f.projectDir, "origin", testRef+":"+testRef); err == nil {
		t.Fatal("expected the diverged push to be rejected")
	} else if !IsPushRejected(err) {
		t.Fatalf("rejection not recognized: %v", err)
	}

	res, err := SyncRef(f.projectDir, "origin", testRef)
	if err != nil {
		t.Fatalf("SyncRef: %v", err)
	}
	if res.Action != SyncRebased || res.Rebased != 2 {
		t.Fatalf("action = %s rebased = %d, want %s/2", res.Action, res.Rebased, SyncRebased)
	}
	if res.LocalAfter == local2.Hash {
		t.Fatal("local tip should have been rewritten")
	}
	if !isAncestor(f.projectDir, otherTip, res.LocalAfter) {
		t.Fatal("remote tip must be an ancestor of the replayed local tip")
	}
	count, _ := gitCmd(f.projectDir, "rev-list", "--count", otherTip+".."+res.LocalAfter)
	if count != "2" {
		t.Fatalf("replayed commit count = %s, want 2", count)
	}
	tree, _ := gitCmd(f.projectDir, "ls-tree", "-r", "--name-only", res.LocalAfter)
	for _, want := range []string{"local-a.txt", "local-b.txt"} {
		if !strings.Contains(tree, want) {
			t.Fatalf("replayed tip missing %s: %q", want, tree)
		}
	}
	firstReplayed, _ := gitCmd(f.projectDir, "rev-parse", res.LocalAfter+"^")
	msg, _ := gitCmd(f.projectDir, "log", "-1", "--format=%B", firstReplayed)
	wantMsg, _ := gitCmd(f.projectDir, "log", "-1", "--format=%B", local1.Hash)
	if msg != wantMsg {
		t.Fatalf("replayed message %q != original %q", msg, wantMsg)
	}

	if _, err := PushRef(f.projectDir, "origin", testRef+":"+testRef); err != nil {
		t.Fatalf("push after sync should fast-forward: %v", err)
	}
	if remoteTip(t, remoteDir) != res.LocalAfter {
		t.Fatal("remote should now be at the replayed local tip")
	}
}

func TestSyncRef_FastForwardsBehindLocal(t *testing.T) {
	f := newFixture(t)
	remoteDir := addBareRemote(t, f.projectDir)
	if _, err := gitCmd(f.projectDir, "push", "--quiet", "origin", "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("seed remote: %v", err)
	}
	projectFileOnMachine(t, f.projectDir, "a.txt", "a")
	if _, err := PushRef(f.projectDir, "origin", testRef+":"+testRef); err != nil {
		t.Fatalf("push: %v", err)
	}
	other := cloneForSecondMachine(t, remoteDir)
	if _, err := gitCmd(other, "fetch", "--quiet", "origin", "+"+testRef+":"+testRef); err != nil {
		t.Fatalf("fetch on other: %v", err)
	}
	projectFileOnMachine(t, other, "b.txt", "b")
	if _, err := PushRef(other, "origin", testRef+":"+testRef); err != nil {
		t.Fatalf("push from other: %v", err)
	}

	res, err := SyncRef(f.projectDir, "origin", testRef)
	if err != nil {
		t.Fatalf("SyncRef: %v", err)
	}
	if res.Action != SyncFastForwarded {
		t.Fatalf("action = %s, want %s", res.Action, SyncFastForwarded)
	}
	local, _ := gitCmd(f.projectDir, "rev-parse", testRef)
	if local != remoteTip(t, remoteDir) {
		t.Fatal("local should now equal remote tip")
	}
}

func TestRunProjection_SessionAndReconcileThenPush(t *testing.T) {
	f := newFixture(t)
	remoteDir := addBareRemote(t, f.projectDir)
	if _, err := gitCmd(f.projectDir, "push", "--quiet", "origin", "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("seed remote: %v", err)
	}

	h := f.putContent("session content")
	f.addEvent("s1", "session.txt", schema.OpCreate, h, "")
	for name, content := range map[string]string{"session.txt": "session content", "untracked.txt": "disk"} {
		if err := os.WriteFile(filepath.Join(f.projectDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	report, err := RunProjection(f.idx, f.objStore, f.projectDir, ProjectionJob{
		SessionID: "s1", WorkingTree: true, Remote: "origin",
	})
	if err != nil {
		t.Fatalf("RunProjection: %v (%s)", err, report.Summary())
	}
	if report.Session == nil || report.Session.Skipped {
		t.Fatalf("session projection missing: %s", report.Summary())
	}
	if report.WorkingTree == nil || report.WorkingTree.FilesAdded != 1 || report.WorkingTree.FilesDeleted != 0 {
		t.Fatalf("reconcile should add only the file the session did not cover: %s", report.Summary())
	}
	if !report.Pushed {
		t.Fatalf("expected a push: %s", report.Summary())
	}
	local, _ := gitCmd(f.projectDir, "rev-parse", testRef)
	if remoteTip(t, remoteDir) != local {
		t.Fatal("remote tip should match local after push")
	}
	tree, _ := gitCmd(f.projectDir, "ls-tree", "-r", "--name-only", testRef)
	if !strings.Contains(tree, "session.txt") || !strings.Contains(tree, "untracked.txt") {
		t.Fatalf("tree missing projected files: %q", tree)
	}
}

func TestRunProjection_UnknownSessionStillReconcilesAndPushes(t *testing.T) {
	f := newFixture(t)
	remoteDir := addBareRemote(t, f.projectDir)
	if _, err := gitCmd(f.projectDir, "push", "--quiet", "origin", "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("seed remote: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.projectDir, "x.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	report, err := RunProjection(f.idx, f.objStore, f.projectDir, ProjectionJob{
		SessionID: "never-seen", WorkingTree: true, Remote: "origin",
	})
	if err != nil {
		t.Fatalf("RunProjection: %v", err)
	}
	if report.SessionError == "" {
		t.Fatal("expected a session error for an unknown session")
	}
	if !report.Pushed || remoteTip(t, remoteDir) == "" {
		t.Fatalf("reconcile should still push: %s", report.Summary())
	}
}

func TestRunProjection_RecoversFromRejectedPush(t *testing.T) {
	f := newFixture(t)
	remoteDir := addBareRemote(t, f.projectDir)
	if _, err := gitCmd(f.projectDir, "push", "--quiet", "origin", "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("seed remote: %v", err)
	}
	projectFileOnMachine(t, f.projectDir, "mine.txt", "mine")

	other := cloneForSecondMachine(t, remoteDir)
	theirs := projectFileOnMachine(t, other, "theirs.txt", "theirs")
	if _, err := PushRef(other, "origin", testRef+":"+testRef); err != nil {
		t.Fatalf("push from other: %v", err)
	}

	report, err := RunProjection(f.idx, f.objStore, f.projectDir, ProjectionJob{WorkingTree: true, Remote: "origin"})
	if err != nil {
		t.Fatalf("RunProjection: %v (%s)", err, report.Summary())
	}
	if !report.Pushed {
		t.Fatalf("expected push to succeed after re-sync: %s", report.Summary())
	}
	if report.Sync == nil || report.Sync.Action != SyncRebased {
		t.Fatalf("expected the rejected push to trigger a replay: %s", report.Summary())
	}
	tip := remoteTip(t, remoteDir)
	if !isAncestor(remoteDir, theirs.Hash, tip) {
		t.Fatal("the other machine's projection must remain in the pushed history")
	}
	tree, _ := gitCmd(remoteDir, "ls-tree", "-r", "--name-only", testRef)
	if !strings.Contains(tree, "mine.txt") {
		t.Fatalf("remote tree should carry this machine's reconcile snapshot: %q", tree)
	}
}

func TestRunProjection_NoRemoteIsLocalOnly(t *testing.T) {
	f := newFixture(t)
	projectFileOnMachine(t, f.projectDir, "a.txt", "a")
	report, err := RunProjection(f.idx, f.objStore, f.projectDir, ProjectionJob{WorkingTree: true})
	if err != nil {
		t.Fatalf("RunProjection: %v", err)
	}
	if report.Pushed || report.Sync != nil {
		t.Fatalf("no remote should mean no sync and no push: %s", report.Summary())
	}
}

func TestProjectionJob_Equal(t *testing.T) {
	a := ProjectionJob{SessionID: "s", WorkingTree: true}
	b := ProjectionJob{SessionID: "s", WorkingTree: true, Ref: "refs/heads/belay-history", BaseRef: "HEAD"}
	if !a.Equal(b) {
		t.Fatal("jobs differing only by defaults should be equal")
	}
	c := ProjectionJob{SessionID: "t", WorkingTree: true}
	if a.Equal(c) {
		t.Fatal("different sessions must not be equal")
	}
}
