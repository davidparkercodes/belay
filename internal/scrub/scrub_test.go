package scrub

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/secrets"
	"github.com/davidparkercodes/belay/internal/store"
	"github.com/davidparkercodes/belay/internal/storekey"
)

func fakeGitHubToken() string {
	return "gh" + "p_" + strings.Repeat("aB3dE5fG7h", 3) + "J9kL1m"
}

func fakeAppPassword() string {
	return strings.Join([]string{"qwrt", "zxvb", "hjkl", "pomn"}, " ")
}

type fixture struct {
	t           *testing.T
	projectRoot string
	belayPath   string
	logDir      string
	store       *store.Store
	scrubber    *Scrubber
	now         time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{t: t, now: time.Now()}
	f.projectRoot = filepath.Join(base, "work", "proj")
	f.belayPath = filepath.Join(f.projectRoot, ".belay")
	projectsDir := filepath.Join(base, "agent", "projects")
	f.logDir = filepath.Join(projectsDir, "-"+strings.ReplaceAll(strings.TrimPrefix(filepath.ToSlash(f.projectRoot), "/"), "/", "-"))
	for _, d := range []string{f.belayPath, f.logDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	s, err := store.Open(filepath.Join(f.belayPath, "objects"), store.Options{
		Compress: true, Encrypt: true, Keys: storekey.NewMemoryProvider(key),
		ManifestPath: filepath.Join(f.belayPath, "store.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.store = s
	f.scrubber = &Scrubber{
		BelayPath: f.belayPath,
		Store:     s,
		Sources:   []Source{&ClaudeCode{ProjectsDir: projectsDir, ProjectRoot: f.projectRoot}},
		Scanner:   secrets.NewScanner(secrets.Options{}),
		Active:    func() map[string]bool { return nil },
		Now:       func() time.Time { return f.now },
	}
	return f
}

func (f *fixture) writeLog(name, content string, age time.Duration) string {
	f.t.Helper()
	p := filepath.Join(f.logDir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
		f.t.Fatal(err)
	}
	mt := f.now.Add(-age)
	if err := os.Chtimes(p, mt, mt); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func jsonLine(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func transcript(t *testing.T) string {
	tok := fakeGitHubToken()
	return jsonLine(t, map[string]interface{}{"type": "user", "message": map[string]interface{}{"content": "use token " + tok + " for the \"api\"\n\tthanks <ok> & done"}}) +
		jsonLine(t, map[string]interface{}{"type": "assistant", "message": map[string]interface{}{"content": "here is the app password: " + fakeAppPassword()}}) +
		jsonLine(t, map[string]interface{}{"type": "assistant", "message": map[string]interface{}{"content": "nothing secret here"}})
}

func TestRedactLog_JSONLStaysValidWithEscapes(t *testing.T) {
	tok := fakeGitHubToken()
	in := `{"a":"x \"q\" \\ é \n ` + tok + `","n":1,"arr":["` + tok + `", true, null]}` + "\r\n" +
		"plain text line with " + tok + "\n" +
		`{"untouched":"<html>","k":  "v"}` + "\n"
	out, counts, err := RedactLog([]byte(in), secrets.NewScanner(secrets.Options{}), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(tok)) {
		t.Fatalf("token survived: %s", out)
	}
	if counts[secrets.RuleGitHubToken] != 3 {
		t.Fatalf("counts = %v", counts)
	}
	lines := strings.Split(string(out), "\n")
	if !json.Valid([]byte(strings.TrimSuffix(lines[0], "\r"))) || !strings.HasSuffix(lines[0], "\r") {
		t.Fatalf("line 0 invalid or lost CRLF: %q", lines[0])
	}
	var obj struct {
		A   string        `json:"a"`
		Arr []interface{} `json:"arr"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSuffix(lines[0], "\r")), &obj); err != nil {
		t.Fatal(err)
	}
	if obj.A != "x \"q\" \\ é \n [redacted:github-token]" {
		t.Fatalf("decoded value = %q", obj.A)
	}
	if obj.Arr[0] != "[redacted:github-token]" {
		t.Fatalf("array value = %v", obj.Arr[0])
	}
	if lines[1] != "plain text line with [redacted:github-token]" {
		t.Fatalf("text line = %q", lines[1])
	}
	if lines[2] != `{"untouched":"<html>","k":  "v"}` {
		t.Fatalf("unchanged line was re-encoded: %q", lines[2])
	}
}

func TestRedactLog_ProseAppPassword(t *testing.T) {
	in := jsonLine(t, map[string]string{"text": "here is the app password: " + fakeAppPassword()})
	out, counts, err := RedactLog([]byte(in), secrets.NewScanner(secrets.Options{}), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if counts[secrets.RuleGoogleAppPassword] != 1 || strings.Contains(string(out), fakeAppPassword()) {
		t.Fatalf("app password not redacted: %s %v", out, counts)
	}
	again, c2, _ := RedactLog(out, secrets.NewScanner(secrets.Options{}), time.Time{})
	if c2.Total() != 0 || !bytes.Equal(again, out) {
		t.Fatalf("redacted output is flagged again: %v", c2)
	}
}

func TestRedactLog_KeyContext(t *testing.T) {
	v := "Qz7" + "pL9xVw2" + "mN4kT8rY"
	in := `{"api_key":"` + v + `","note":"` + v + `"}` + "\n"
	out, counts, _ := RedactLog([]byte(in), secrets.NewScanner(secrets.Options{}), time.Time{})
	if counts[secrets.RuleAssignment] != 1 {
		t.Fatalf("counts = %v (%s)", counts, out)
	}
	if !strings.Contains(string(out), `"api_key":"[redacted:credential-assignment]"`) || !strings.Contains(string(out), `"note":"`+v+`"`) {
		t.Fatalf("out = %s", out)
	}
}

func TestRedactLog_Budget(t *testing.T) {
	_, _, err := RedactLog([]byte(strings.Repeat("line\n", 1000)), secrets.NewScanner(secrets.Options{}), time.Now().Add(-time.Second))
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("err = %v", err)
	}
}

func TestRun_RedactsIdleAndUndoIsByteExact(t *testing.T) {
	f := newFixture(t)
	content := transcript(t)
	p := f.writeLog("s1.jsonl", content, time.Hour)
	before, _ := os.Stat(p)

	rep, err := f.scrubber.Run(RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Redacted != 1 || rep.Findings != 2 {
		t.Fatalf("report = %+v", rep)
	}
	got, _ := os.ReadFile(p)
	if strings.Contains(string(got), fakeGitHubToken()) || strings.Contains(string(got), fakeAppPassword()) {
		t.Fatal("secret survived")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(got)), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatalf("invalid JSONL line: %s", line)
		}
	}
	after, _ := os.Stat(p)
	if after.Mode().Perm() != 0o640 || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("mode/mtime not kept: %v %v", after.Mode(), after.ModTime())
	}
	if entries, _ := os.ReadDir(f.logDir); len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
	if b, _ := os.ReadFile(filepath.Join(Dir(f.belayPath), "backups.jsonl")); bytes.Contains(b, []byte(fakeGitHubToken())) {
		t.Fatal("backup record holds the secret")
	}

	res, err := f.scrubber.Undo(p)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Restored {
		t.Fatalf("undo result = %+v", res)
	}
	restored, _ := os.ReadFile(p)
	if !bytes.Equal(restored, []byte(content)) {
		t.Fatal("undo is not byte-exact")
	}
	if _, err := f.scrubber.Undo(p); !errors.Is(err, ErrNoBackup) {
		t.Fatalf("second undo err = %v", err)
	}
}

func TestUndo_KeepsLinesAppendedAfterScrub(t *testing.T) {
	f := newFixture(t)
	content := transcript(t)
	p := f.writeLog("s1.jsonl", content, time.Hour)
	if _, err := f.scrubber.Run(RunOptions{}); err != nil {
		t.Fatal(err)
	}
	extra := `{"type":"user","message":"resumed"}` + "\n"
	fh, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.WriteString(extra); err != nil {
		t.Fatal(err)
	}
	if err := fh.Close(); err != nil {
		t.Fatal(err)
	}
	res, err := f.scrubber.Undo(p)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != content+extra || res.KeptAppended != int64(len(extra)) {
		t.Fatalf("got %q kept=%d", got, res.KeptAppended)
	}
}

func TestUndo_RefusesWhenRewritten(t *testing.T) {
	f := newFixture(t)
	p := f.writeLog("s1.jsonl", transcript(t), time.Hour)
	if _, err := f.scrubber.Run(RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("something else\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := f.scrubber.Undo(p); !errors.Is(err, ErrChangedSince) {
		t.Fatalf("err = %v", err)
	}
}

func TestRun_SkipsRecentAndActiveLogs(t *testing.T) {
	f := newFixture(t)
	content := transcript(t)
	recent := f.writeLog("recent.jsonl", content, 5*time.Minute)
	active := f.writeLog("active-sess.jsonl", content, 2*time.Hour)
	sub := f.writeLog(filepath.Join("active-sess", "subagents", "agent-1.jsonl"), content, 2*time.Hour)
	f.scrubber.Active = func() map[string]bool { return map[string]bool{"active-sess": true} }

	rep, err := f.scrubber.Run(RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, r := range rep.Files {
		status[r.Path] = r.Status
	}
	if status[recent] != StatusRecent || status[active] != StatusActive || status[sub] != StatusActive {
		t.Fatalf("statuses = %v", status)
	}
	for _, p := range []string{recent, active, sub} {
		if b, _ := os.ReadFile(p); string(b) != content {
			t.Fatalf("%s was modified", p)
		}
	}
}

func TestRun_DryRunChangesNothing(t *testing.T) {
	f := newFixture(t)
	content := transcript(t)
	p := f.writeLog("s1.jsonl", content, time.Hour)
	f.scrubber.Store = nil
	rep, err := f.scrubber.Run(RunOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Files) != 1 || rep.Files[0].Status != StatusWouldRedact || rep.Files[0].Counts[secrets.RuleGitHubToken] != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if b, _ := os.ReadFile(p); string(b) != content {
		t.Fatal("dry run modified the log")
	}
	out, _ := json.Marshal(rep)
	if bytes.Contains(out, []byte(fakeGitHubToken())) || bytes.Contains(out, []byte(fakeAppPassword())) {
		t.Fatal("report leaks a secret")
	}
}

func TestRun_AbortsWhenLogChangesDuringScrub(t *testing.T) {
	f := newFixture(t)
	content := transcript(t)
	p := f.writeLog("s1.jsonl", content, time.Hour)
	appended := `{"type":"user","message":"late write"}` + "\n"
	f.scrubber.beforeRename = func(path string) error {
		fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer fh.Close()
		_, err = fh.WriteString(appended)
		return err
	}
	rep, err := f.scrubber.Run(RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files[0].Status != StatusChanged {
		t.Fatalf("status = %s", rep.Files[0].Status)
	}
	if b, _ := os.ReadFile(p); string(b) != content+appended {
		t.Fatal("concurrent write was lost or log rewritten")
	}
	if entries, _ := os.ReadDir(f.logDir); len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
	if backed, _ := Backed(f.belayPath); len(backed) != 0 {
		t.Fatalf("aborted scrub recorded a backup: %v", backed)
	}
}

func TestRun_NeedsEncryptedStore(t *testing.T) {
	f := newFixture(t)
	legacy, err := store.NewStore(filepath.Join(t.TempDir(), "objects"), true)
	if err != nil {
		t.Fatal(err)
	}
	f.scrubber.Store = legacy
	if _, err := f.scrubber.Run(RunOptions{}); !errors.Is(err, ErrNeedsEncrypt) {
		t.Fatalf("err = %v", err)
	}
}

func TestGarbageCollect_KeepsScrubBackups(t *testing.T) {
	f := newFixture(t)
	f.writeLog("s1.jsonl", transcript(t), time.Hour)
	if _, err := f.scrubber.Run(RunOptions{}); err != nil {
		t.Fatal(err)
	}
	backups, _ := ReadBackups(f.belayPath)
	idx, err := index.Open(filepath.Join(f.belayPath, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	old := store.GCGracePeriod
	store.GCGracePeriod = -time.Hour
	defer func() { store.GCGracePeriod = old }()
	if _, err := store.GarbageCollect(idx, f.store, false); err != nil {
		t.Fatal(err)
	}
	if !f.store.Has(backups[0].OriginalID) {
		t.Fatal("GC removed the scrub backup")
	}
}

func TestCountFindings_UsesCache(t *testing.T) {
	f := newFixture(t)
	f.writeLog("s1.jsonl", transcript(t), time.Hour)
	f.writeLog("s2.jsonl", jsonLine(t, map[string]string{"t": "clean"}), time.Hour)

	n := CountFindings(f.belayPath, f.scrubber.Sources, f.scrubber.Scanner, time.Second)
	if !n.Complete || n.Findings != 2 || n.Logs != 1 {
		t.Fatalf("notice = %+v", n)
	}
	cache := LoadCache(f.belayPath)
	if len(cache.Entries) != 2 {
		t.Fatalf("cache entries = %d", len(cache.Entries))
	}
	n = CountFindings(f.belayPath, f.scrubber.Sources, f.scrubber.Scanner, 0)
	if !n.Complete || n.Findings != 2 {
		t.Fatalf("cached notice = %+v", n)
	}
	f.writeLog("s3.jsonl", transcript(t), time.Hour)
	n = CountFindings(f.belayPath, f.scrubber.Sources, f.scrubber.Scanner, 0)
	if n.Complete {
		t.Fatalf("uncached log with no budget must leave the count incomplete: %+v", n)
	}
	if b, _ := os.ReadFile(cachePath(f.belayPath)); bytes.Contains(b, []byte(fakeGitHubToken())) {
		t.Fatal("cache holds a secret")
	}
}

func TestTick_AutoRedactsAndRespectsUndo(t *testing.T) {
	f := newFixture(t)
	content := transcript(t)
	p := f.writeLog("s1.jsonl", content, time.Hour)

	rep, err := f.scrubber.Tick(config.ScrubNotice, nil)
	if err != nil || rep != nil {
		t.Fatalf("notice tick = %v %v", rep, err)
	}
	if b, _ := os.ReadFile(p); string(b) != content {
		t.Fatal("notice mode modified a log")
	}
	if len(LoadCache(f.belayPath).Entries) != 1 {
		t.Fatal("notice tick did not warm the cache")
	}

	rep, err = f.scrubber.Tick(config.ScrubAuto, nil)
	if err != nil || rep.Redacted != 1 {
		t.Fatalf("auto tick = %+v %v", rep, err)
	}
	if LoadCache(f.belayPath).LastAutoAt.IsZero() {
		t.Fatal("auto tick not recorded")
	}
	if _, err := f.scrubber.Undo(p); err != nil {
		t.Fatal(err)
	}
	rep, err = f.scrubber.Tick(config.ScrubAuto, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != content {
		t.Fatal("auto tick redid an undone scrub")
	}
	if rep.Files[0].Status != StatusUndone {
		t.Fatalf("status = %s", rep.Files[0].Status)
	}

	if rep, _ := f.scrubber.Tick(config.ScrubOff, nil); rep != nil {
		t.Fatal("off mode did work")
	}
}

func TestTick_AutoRecordsError(t *testing.T) {
	f := newFixture(t)
	f.writeLog("s1.jsonl", transcript(t), time.Hour)
	legacy, _ := store.NewStore(filepath.Join(t.TempDir(), "objects"), true)
	f.scrubber.Store = legacy
	if _, err := f.scrubber.Tick(config.ScrubAuto, nil); err == nil {
		t.Fatal("expected error")
	}
	if LoadCache(f.belayPath).LastAutoError == "" {
		t.Fatal("error not recorded for status")
	}
}

func TestTick_BoundedByBytes(t *testing.T) {
	f := newFixture(t)
	f.writeLog("a.jsonl", transcript(t), time.Hour)
	f.writeLog("b.jsonl", transcript(t), time.Hour)
	rep, err := f.scrubber.Run(RunOptions{MaxBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Redacted != 1 || rep.Files[1].Status != StatusDeferred {
		t.Fatalf("report = %+v", rep)
	}
}

func TestClaudeCodeSource_AllLogs(t *testing.T) {
	f := newFixture(t)
	f.writeLog("s1.jsonl", "{}\n", time.Hour)
	other := filepath.Join(filepath.Dir(f.logDir), "-other-project")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "x.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := f.scrubber.Sources[0]
	proj, _ := src.ProjectLogs()
	all, _ := src.AllLogs()
	if len(proj) != 1 || len(all) != 2 {
		t.Fatalf("project=%v all=%v", proj, all)
	}
}
