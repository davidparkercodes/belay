package git

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/secrets"
)

func fakeToken() string {
	return strings.Join([]string{"gh", "p_", "Q7rT2mXk9LpZ4v", "Bn8WcY3hJd6FsA1eGu5NoK"}, "")
}

func secretBody() string {
	return "GITHUB_TOKEN=" + fakeToken() + "\n"
}

func appPasswordProse() string {
	groups := []string{"qzvx", "kpwj", "mfhy", "trbn"}
	return "the app " + "password for me@example.com is " + strings.Join(groups, " ") + "\n"
}

func (f *testFixture) refFile(t *testing.T, path string) (string, bool) {
	t.Helper()
	out, err := gitCmd(f.projectDir, "show", testRef+":"+path)
	if err != nil {
		return "", false
	}
	return out, true
}

func (f *testFixture) sessionCreate(session, path, content string) {
	f.addEvent(session, path, schema.OpCreate, f.putContent(content), "")
}

func (f *testFixture) sessionModify(session, path, prev, content string) {
	f.addEvent(session, path, schema.OpModify, f.putContent(content), f.putContent(prev))
}

func (f *testFixture) runSession(t *testing.T, session string, allow bool) *ProjectionReport {
	t.Helper()
	report, err := RunProjection(f.idx, f.objStore, f.projectDir, ProjectionJob{SessionID: session, Ref: testRef, AllowSecrets: allow})
	if err != nil {
		t.Fatalf("RunProjection: %v", err)
	}
	if report.SessionError != "" {
		t.Fatalf("session error: %s", report.SessionError)
	}
	return report
}

func heldPaths(held []HeldBackFile) []string {
	var out []string
	for _, h := range held {
		out = append(out, h.Path)
	}
	return out
}

func TestGuard_HoldsBackNewFile(t *testing.T) {
	f := newFixture(t)
	f.sessionCreate("s1", "clean.txt", "hello\n")
	f.sessionCreate("s1", "secret.env.txt", secretBody())

	report := f.runSession(t, "s1", false)
	if got := heldPaths(report.HeldBack); len(got) != 1 || got[0] != "secret.env.txt" {
		t.Fatalf("held back = %v", got)
	}
	if rules := report.HeldBack[0].Rules; len(rules) != 1 || rules[0] != secrets.RuleGitHubToken {
		t.Fatalf("rules = %v", rules)
	}
	if _, ok := f.refFile(t, "clean.txt"); !ok {
		t.Fatal("clean file should be projected")
	}
	if _, ok := f.refFile(t, "secret.env.txt"); ok {
		t.Fatal("secret file must not be projected")
	}
	if report.Session.FilesAdded != 1 || strings.Contains(report.Session.Message, "2 added") {
		t.Fatalf("counts/message should exclude held-back file: %+v", report.Session)
	}
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), fakeToken()) {
		t.Fatal("report JSON leaked the secret")
	}
	if !strings.Contains(string(raw), `"held_back"`) {
		t.Fatalf("report JSON missing held_back: %s", raw)
	}
	notice := HeldBackNotice(report.HeldBack)
	if notice != "belay: held back 1 file(s) with likely credentials: secret.env.txt (allow with .belayallow)" {
		t.Fatalf("notice = %q", notice)
	}
}

func TestGuard_ModifiedFileKeepsPriorVersion(t *testing.T) {
	f := newFixture(t)
	f.sessionCreate("s1", "config.yml", "name: demo\n")
	f.runSession(t, "s1", false)

	f.sessionModify("s2", "config.yml", "name: demo\n", "name: demo\n"+appPasswordProse())
	f.sessionCreate("s2", "other.txt", "fine\n")
	report := f.runSession(t, "s2", false)

	if got := heldPaths(report.HeldBack); len(got) != 1 || got[0] != "config.yml" {
		t.Fatalf("held back = %v", got)
	}
	if got, _ := f.refFile(t, "config.yml"); got != "name: demo" {
		t.Fatalf("config.yml should stay at prior version, got %q", got)
	}
	if _, ok := f.refFile(t, "other.txt"); !ok {
		t.Fatal("other.txt should be projected")
	}
	if report.Session.FilesModified != 0 || report.Session.FilesAdded != 1 {
		t.Fatalf("counts = %+v", report.Session)
	}

	f.sessionModify("s3", "config.yml", "name: demo\n"+appPasswordProse(), "name: demo\nport: 80\n")
	report = f.runSession(t, "s3", false)
	if len(report.HeldBack) != 0 {
		t.Fatalf("nothing should be held back once the secret is gone: %v", report.HeldBack)
	}
	if got, _ := f.refFile(t, "config.yml"); got != "name: demo\nport: 80" {
		t.Fatalf("cleaned file should project, got %q", got)
	}
}

func TestGuard_DeletionStillProjects(t *testing.T) {
	f := newFixture(t)
	f.sessionCreate("s1", "old.txt", "bye\n")
	f.runSession(t, "s1", false)

	f.addEvent("s2", "old.txt", schema.OpDelete, "", f.putContent("bye\n"))
	f.sessionCreate("s2", "key.txt", secretBody())
	report := f.runSession(t, "s2", false)

	if report.Session.Skipped {
		t.Fatal("deletion should still produce a commit")
	}
	if _, ok := f.refFile(t, "old.txt"); ok {
		t.Fatal("deletion should be projected")
	}
	if _, ok := f.refFile(t, "key.txt"); ok {
		t.Fatal("secret file must be held back")
	}
	if report.Session.FilesDeleted != 1 || report.Session.FilesAdded != 0 {
		t.Fatalf("counts = %+v", report.Session)
	}
}

func TestGuard_EverythingHeldBackIsEmptyProjection(t *testing.T) {
	f := newFixture(t)
	f.sessionCreate("s1", "a.txt", "a\n")
	f.runSession(t, "s1", false)
	before, _ := gitCmd(f.projectDir, "rev-parse", testRef)

	f.sessionCreate("s2", "key.txt", secretBody())
	report := f.runSession(t, "s2", false)
	if !report.Session.Skipped || len(report.Session.HeldBack) != 1 {
		t.Fatalf("want skipped with one held-back file, got %+v", report.Session)
	}
	after, _ := gitCmd(f.projectDir, "rev-parse", testRef)
	if before != after {
		t.Fatal("ref should not move when every change is held back")
	}
}

func TestGuard_BelayAllowPath(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.projectDir, AllowFileName), []byte("# fixtures\nfixtures/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	f.sessionCreate("s1", "fixtures/token.txt", secretBody())
	f.sessionCreate("s1", "real.txt", secretBody())
	report := f.runSession(t, "s1", false)
	if got := heldPaths(report.HeldBack); len(got) != 1 || got[0] != "real.txt" {
		t.Fatalf("held back = %v", got)
	}
	if _, ok := f.refFile(t, "fixtures/token.txt"); !ok {
		t.Fatal("allowed path should be projected")
	}
}

func TestGuard_BelayAllowRuleLines(t *testing.T) {
	f := newFixture(t)
	allow := "rule:" + secrets.RuleGitHubToken + "\nrule:no-such-rule\n"
	if err := os.WriteFile(filepath.Join(f.projectDir, AllowFileName), []byte(allow), 0644); err != nil {
		t.Fatal(err)
	}
	f.sessionCreate("s1", "gh.txt", "see "+fakeToken()+"\n")
	f.sessionCreate("s1", "notes.md", appPasswordProse())
	report := f.runSession(t, "s1", false)
	if got := heldPaths(report.HeldBack); len(got) != 1 || got[0] != "notes.md" {
		t.Fatalf("held back = %v", got)
	}
	if _, ok := f.refFile(t, "gh.txt"); !ok {
		t.Fatal("file flagged only by a disabled rule should be projected")
	}
	if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "no-such-rule") {
		t.Fatalf("want one unknown-rule warning, got %v", report.Warnings)
	}
}

func TestGuard_AllowSecretsJob(t *testing.T) {
	f := newFixture(t)
	f.sessionCreate("s1", "key.txt", secretBody())
	report := f.runSession(t, "s1", true)
	if len(report.HeldBack) != 0 {
		t.Fatalf("allow-secrets should hold nothing back: %v", report.HeldBack)
	}
	if _, ok := f.refFile(t, "key.txt"); !ok {
		t.Fatal("key.txt should be projected with allow-secrets")
	}
	raw, _ := json.Marshal(ProjectionJob{WorkingTree: true, AllowSecrets: true})
	if !strings.Contains(string(raw), `"allow_secrets":true`) {
		t.Fatalf("job JSON should carry allow_secrets for the daemon queue: %s", raw)
	}
}

func TestGuard_WorkingTreeReconcile(t *testing.T) {
	f := newFixture(t)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(f.projectDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	reconcile := func() *ProjectionReport {
		t.Helper()
		r, err := RunProjection(f.idx, f.objStore, f.projectDir, ProjectionJob{WorkingTree: true, Ref: testRef})
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		return r
	}

	write("app.cfg", "mode = dev\n")
	reconcile()

	write("app.cfg", "mode = dev\n"+secretBody())
	write("new.txt", secretBody())
	write("ok.txt", "ok\n")
	r := reconcile()
	if got := heldPaths(r.HeldBack); len(got) != 2 || got[0] != "app.cfg" || got[1] != "new.txt" {
		t.Fatalf("held back = %v", got)
	}
	if got, _ := f.refFile(t, "app.cfg"); got != "mode = dev" {
		t.Fatalf("app.cfg should stay at prior projected version, got %q", got)
	}
	if _, ok := f.refFile(t, "new.txt"); ok {
		t.Fatal("new secret file must not be projected")
	}
	if _, ok := f.refFile(t, "ok.txt"); !ok {
		t.Fatal("ok.txt should be projected")
	}

	r = reconcile()
	if !r.WorkingTree.Skipped || len(r.HeldBack) != 2 {
		t.Fatalf("unchanged secrets should give an empty reconcile that still reports them: %+v", r.WorkingTree)
	}

	write("app.cfg", "mode = prod\n")
	if err := os.Remove(filepath.Join(f.projectDir, "new.txt")); err != nil {
		t.Fatal(err)
	}
	r = reconcile()
	if len(r.HeldBack) != 0 {
		t.Fatalf("nothing should be held back: %v", r.HeldBack)
	}
	if got, _ := f.refFile(t, "app.cfg"); got != "mode = prod" {
		t.Fatalf("cleaned app.cfg should project, got %q", got)
	}
}

func TestGuard_DetectorFailureFailsOpen(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.projectDir, "key.txt"), []byte(secretBody()), 0644); err != nil {
		t.Fatal(err)
	}
	g := LoadSecretGuard(f.projectDir)
	g.scan = func([]byte) []secrets.Finding { panic("boom") }
	res, err := ProjectWorkingTree(f.projectDir, ProjectWorkingTreeOptions{TargetRef: testRef, Guard: g})
	if err != nil {
		t.Fatalf("projection must not fail: %v", err)
	}
	if len(res.HeldBack) != 0 {
		t.Fatalf("nothing should be held back on detector failure: %v", res.HeldBack)
	}
	if _, ok := f.refFile(t, "key.txt"); !ok {
		t.Fatal("file should be projected when the detector fails")
	}
	if len(res.GuardWarnings) == 0 || !strings.Contains(res.GuardWarnings[0], "key.txt") {
		t.Fatalf("want a warning naming the file, got %v", res.GuardWarnings)
	}
}

func TestGuard_SkipsBinaryAndLargeFiles(t *testing.T) {
	f := newFixture(t)
	bin := append([]byte{0, 1, 2}, []byte(secretBody())...)
	if err := os.WriteFile(filepath.Join(f.projectDir, "blob.bin"), bin, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.projectDir, "big.txt"), []byte(secretBody()+strings.Repeat("x\n", 64)), 0644); err != nil {
		t.Fatal(err)
	}
	g := LoadSecretGuard(f.projectDir)
	g.maxBytes = 32
	res, err := ProjectWorkingTree(f.projectDir, ProjectWorkingTreeOptions{TargetRef: testRef, Guard: g})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.HeldBack) != 0 {
		t.Fatalf("binary and oversized files are not scanned: %v", res.HeldBack)
	}
}
