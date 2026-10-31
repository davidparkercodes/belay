package selfupdate_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/selfupdate"
	"github.com/davidparkercodes/belay/internal/selfupdate/releasetest"
)

var linuxAMD64 = [2]string{"linux", "amd64"}

func TestLatestTag(t *testing.T) {
	srv := releasetest.New(t, "v1.10.0", []byte("bin"), linuxAMD64)
	got, err := srv.Client().LatestTag(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "v1.10.0" {
		t.Fatalf("LatestTag = %q", got)
	}
}

func TestLatestTagRejectsOddTag(t *testing.T) {
	srv := releasetest.New(t, "nightly", []byte("bin"))
	if _, err := srv.Client().LatestTag(context.Background()); err == nil {
		t.Fatal("expected an error for a non-semver tag")
	}
}

func TestLatestTagTimeout(t *testing.T) {
	srv := releasetest.New(t, "v1.0.0", nil)
	c := srv.Client()
	c.APITimeout = time.Nanosecond
	if _, err := c.LatestTag(context.Background()); err == nil {
		t.Fatal("expected a timeout")
	}
}

func TestDownloadBinaryVerifies(t *testing.T) {
	want := []byte("#!/bin/sh\necho belay v1.10.0\n")
	srv := releasetest.New(t, "v1.10.0", want, linuxAMD64, [2]string{"windows", "amd64"})

	got, err := srv.Client().DownloadBinary(context.Background(), "v1.10.0", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("binary = %q", got)
	}
	got, err = srv.Client().DownloadBinary(context.Background(), "1.10.0", "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("windows binary = %q", got)
	}
}

func TestDownloadBinaryRefusesMismatch(t *testing.T) {
	srv := releasetest.New(t, "v1.10.0", []byte("good"), linuxAMD64)
	srv.Corrupt("v1.10.0", "linux", "amd64")
	_, err := srv.Client().DownloadBinary(context.Background(), "v1.10.0", "linux", "amd64")
	if !errors.Is(err, selfupdate.ErrChecksumMismatch) {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
}

func TestDownloadBinaryRefusesMissingChecksum(t *testing.T) {
	srv := releasetest.New(t, "v1.10.0", []byte("good"), linuxAMD64, [2]string{"darwin", "arm64"})
	srv.DropChecksum("v1.10.0", "linux", "amd64")
	_, err := srv.Client().DownloadBinary(context.Background(), "v1.10.0", "linux", "amd64")
	if !errors.Is(err, selfupdate.ErrChecksumMissing) {
		t.Fatalf("err = %v, want missing checksum", err)
	}
}

func writeBinary(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestReplaceBinaryKeepsPrevious(t *testing.T) {
	target := filepath.Join(t.TempDir(), "belay")
	writeBinary(t, target, "old")
	before, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}

	if err := selfupdate.ReplaceBinary(target, []byte("new"), runtime.GOOS); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Fatalf("target = %q", got)
	}
	if got, _ := os.ReadFile(selfupdate.PrevPath(target)); string(got) != "old" {
		t.Fatalf("prev = %q", got)
	}
	after, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("target kept its inode; expected an atomic rename of a new file")
	}
	if runtime.GOOS != "windows" && after.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", after.Mode().Perm())
	}

	if err := selfupdate.ReplaceBinary(target, []byte("newer"), runtime.GOOS); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(selfupdate.PrevPath(target)); string(got) != "new" {
		t.Fatalf("prev after second update = %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(target))
	if len(entries) != 2 {
		t.Fatalf("leftover temp files: %v", entries)
	}
}

func TestReplaceBinaryNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a non-root unix user")
	}
	dir := filepath.Join(t.TempDir(), "ro")
	target := filepath.Join(dir, "belay")
	writeBinary(t, target, "old")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := selfupdate.ReplaceBinary(target, []byte("new"), runtime.GOOS)
	var nw *selfupdate.NotWritableError
	if !errors.As(err, &nw) || nw.Dir != dir || !strings.Contains(err.Error(), dir) {
		t.Fatalf("err = %v, want NotWritableError naming %s", err, dir)
	}
}

type commandLog struct {
	mu    sync.Mutex
	calls [][]string
	envs  [][]string
	err   error
}

func (c *commandLog) run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, append([]string{name}, args...))
	c.envs = append(c.envs, env)
	return []byte("ok"), c.err
}

func stubCommands(t *testing.T, found map[string]string) *commandLog {
	t.Helper()
	log := &commandLog{}
	oldRun, oldLook := selfupdate.RunCommand, selfupdate.LookPath
	selfupdate.RunCommand = log.run
	selfupdate.LookPath = func(name string) (string, error) {
		if p, ok := found[name]; ok {
			return p, nil
		}
		return "", fmt.Errorf("%s not found", name)
	}
	t.Cleanup(func() { selfupdate.RunCommand, selfupdate.LookPath = oldRun, oldLook })
	return log
}

func TestApplyHomebrew(t *testing.T) {
	log := stubCommands(t, map[string]string{"brew": "/fake/brew"})
	u := &selfupdate.Updater{GOOS: "darwin", GOARCH: "arm64"}
	inst := selfupdate.Install{Method: selfupdate.MethodHomebrew, Path: "/fake/Caskroom/belay/1.0.0/belay"}

	res, err := u.Apply(context.Background(), inst, "v1.0.0", "v1.1.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ran || len(log.calls) != 1 || strings.Join(log.calls[0], " ") != "/fake/brew upgrade --cask "+selfupdate.CaskName {
		t.Fatalf("res = %+v, calls = %v", res, log.calls)
	}

	if _, err := u.Apply(context.Background(), inst, "v1.0.0", "v1.0.5", true); err == nil {
		t.Fatal("pinned version through Homebrew should be refused")
	}
}

func TestApplyHomebrewFindsBrewInPrefix(t *testing.T) {
	prefix := t.TempDir()
	writeBinary(t, filepath.Join(prefix, "bin", "brew"), "brew")
	log := stubCommands(t, nil)
	u := &selfupdate.Updater{}
	inst := selfupdate.Install{Method: selfupdate.MethodHomebrew, Prefix: prefix}
	if _, err := u.Apply(context.Background(), inst, "v1.0.0", "v1.1.0", false); err != nil {
		t.Fatal(err)
	}
	if log.calls[0][0] != filepath.Join(prefix, "bin", "brew") {
		t.Fatalf("calls = %v", log.calls)
	}
}

func TestApplyGoInstall(t *testing.T) {
	log := stubCommands(t, map[string]string{"go": "/fake/go"})
	u := &selfupdate.Updater{Environ: func() []string { return []string{"PATH=/x"} }}
	inst := selfupdate.Install{Method: selfupdate.MethodGoInstall, Path: filepath.Join("gobin", "belay")}
	res, err := u.Apply(context.Background(), inst, "v1.0.0", "1.1.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ran || strings.Join(log.calls[0], " ") != "/fake/go install "+selfupdate.ModulePath+"/cmd/belay@v1.1.0" {
		t.Fatalf("calls = %v", log.calls)
	}
	if env := strings.Join(log.envs[0], " "); !strings.Contains(env, "GOBIN=gobin") {
		t.Fatalf("env = %s", env)
	}
}

func TestApplyGoInstallWithoutGoPrints(t *testing.T) {
	log := stubCommands(t, nil)
	u := &selfupdate.Updater{}
	res, err := u.Apply(context.Background(), selfupdate.Install{Method: selfupdate.MethodGoInstall}, "v1.0.0", "v1.1.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ran || len(log.calls) != 0 || len(res.Command) == 0 {
		t.Fatalf("res = %+v calls = %v", res, log.calls)
	}
}

func TestApplyDevRefused(t *testing.T) {
	stubCommands(t, nil)
	u := &selfupdate.Updater{}
	_, err := u.Apply(context.Background(), selfupdate.Install{Method: selfupdate.MethodDev, Reason: "development build"}, "dev", "v1.1.0", false)
	if !errors.Is(err, selfupdate.ErrDevBuild) {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyReleaseEndToEnd(t *testing.T) {
	stubCommands(t, nil)
	srv := releasetest.New(t, "v1.10.0", []byte("new build"), linuxAMD64)
	target := filepath.Join(t.TempDir(), "bin", "belay")
	writeBinary(t, target, "old build")
	u := &selfupdate.Updater{Client: srv.Client(), GOOS: "linux", GOARCH: "amd64"}
	inst := selfupdate.Install{Method: selfupdate.MethodRelease, Path: target}

	res, err := u.Apply(context.Background(), inst, "v1.9.0", "v1.10.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ran {
		t.Fatalf("res = %+v", res)
	}
	if got, _ := os.ReadFile(target); string(got) != "new build" {
		t.Fatalf("target = %q", got)
	}

	srv.AddArchive(t, "v1.11.0", "linux", "amd64", []byte("tampered"))
	srv.Corrupt("v1.11.0", "linux", "amd64")
	if _, err := u.Apply(context.Background(), inst, "v1.10.0", "v1.11.0", true); !errors.Is(err, selfupdate.ErrChecksumMismatch) {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new build" {
		t.Fatalf("target changed after a refused update: %q", got)
	}
}

type checkerEnv struct {
	srv    *releasetest.Server
	target string
	logs   *[]string
	now    time.Time
	mode   string
}

func newChecker(t *testing.T, current, latest string) (*selfupdate.Checker, *checkerEnv) {
	t.Helper()
	stubCommands(t, nil)
	srv := releasetest.New(t, latest, []byte("binary "+latest), linuxAMD64)
	target := filepath.Join(t.TempDir(), "belay")
	writeBinary(t, target, "binary "+current)
	var logs []string
	e := &checkerEnv{srv: srv, target: target, logs: &logs, now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), mode: config.UpdateNotify}
	c := &selfupdate.Checker{
		Client:   srv.Client(),
		Updater:  &selfupdate.Updater{Client: srv.Client(), GOOS: "linux", GOARCH: "amd64"},
		Current:  current,
		CacheDir: t.TempDir(),
		Now:      func() time.Time { return e.now },
		Getenv:   func(string) string { return "" },
		Mode:     func() string { return e.mode },
		Binary: func() (selfupdate.Install, error) {
			return selfupdate.Install{Method: selfupdate.MethodRelease, Path: target}, nil
		},
		Logf: func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) },
	}
	return c, e
}

func TestCheckerChecksOncePerInterval(t *testing.T) {
	c, e := newChecker(t, "v1.9.0", "v1.10.0")
	res := c.Tick(context.Background())
	if !res.Checked || res.Latest != "v1.10.0" || res.Attempted {
		t.Fatalf("first tick = %+v", res)
	}
	st, _ := selfupdate.ReadState(selfupdate.CachePath(c.CacheDir))
	if st.Latest != "v1.10.0" || !st.CheckedAt.Equal(e.now) {
		t.Fatalf("state = %+v", st)
	}
	if line := selfupdate.NoticeLine(st.Latest, "v1.9.0"); line != "Belay v1.10.0 is available (you have v1.9.0). Run belay update." {
		t.Fatalf("notice = %q", line)
	}

	e.now = e.now.Add(23 * time.Hour)
	if res := c.Tick(context.Background()); res.Checked {
		t.Fatal("checked again inside the interval")
	}
	e.now = e.now.Add(2 * time.Hour)
	if res := c.Tick(context.Background()); !res.Checked {
		t.Fatal("did not check after the interval")
	}
	if n := e.srv.APIRequests.Load(); n != 2 {
		t.Fatalf("api requests = %d, want 2", n)
	}
	if got, _ := os.ReadFile(e.target); string(got) != "binary v1.9.0" {
		t.Fatal("notify mode replaced the binary")
	}
}

func TestCheckerDisabled(t *testing.T) {
	c, e := newChecker(t, "v1.9.0", "v1.10.0")
	c.Getenv = func(k string) string {
		if k == selfupdate.DisableEnv {
			return "1"
		}
		return ""
	}
	if res := c.Tick(context.Background()); res.Checked {
		t.Fatal("checked with the env opt-out set")
	}
	c.Getenv = func(string) string { return "" }
	e.mode = config.UpdateOff
	if res := c.Tick(context.Background()); res.Checked {
		t.Fatal("checked with mode off")
	}
	if e.srv.APIRequests.Load() != 0 {
		t.Fatal("made a request while disabled")
	}
}

func TestCheckerSkipsWhileLocked(t *testing.T) {
	c, e := newChecker(t, "v1.9.0", "v1.10.0")
	if err := os.MkdirAll(filepath.Join(c.CacheDir, "update.lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if res := c.Tick(context.Background()); res.Checked {
		t.Fatal("checked while another process held the lock")
	}
	if e.srv.APIRequests.Load() != 0 {
		t.Fatal("made a request while locked")
	}
}

func TestCheckerAutoInstalls(t *testing.T) {
	c, e := newChecker(t, "v1.9.0", "v1.10.0")
	e.mode = config.UpdateAuto
	res := c.Tick(context.Background())
	if !res.Attempted || !res.Installed || res.Err != nil {
		t.Fatalf("tick = %+v", res)
	}
	if got, _ := os.ReadFile(e.target); string(got) != "binary v1.10.0" {
		t.Fatalf("target = %q", got)
	}
	if len(*e.logs) != 1 || !strings.Contains((*e.logs)[0], "auto-update: installed v1.10.0") {
		t.Fatalf("logs = %v", *e.logs)
	}

	e.now = e.now.Add(time.Hour)
	if res := c.Tick(context.Background()); res.Attempted {
		t.Fatal("old process retried an update that already succeeded")
	}
}

func TestCheckerAutoUsesCachedRelease(t *testing.T) {
	c, e := newChecker(t, "v1.9.0", "v1.10.0")
	c.Tick(context.Background())
	e.mode = config.UpdateAuto
	e.now = e.now.Add(time.Hour)
	res := c.Tick(context.Background())
	if res.Checked || !res.Installed {
		t.Fatalf("tick = %+v, want an install from the cached result without a new check", res)
	}
}

func TestCheckerAutoFailureRetriesNextCheck(t *testing.T) {
	c, e := newChecker(t, "v1.9.0", "v1.10.0")
	e.mode = config.UpdateAuto
	e.srv.Corrupt("v1.10.0", "linux", "amd64")

	res := c.Tick(context.Background())
	if !res.Attempted || !errors.Is(res.Err, selfupdate.ErrChecksumMismatch) {
		t.Fatalf("tick = %+v", res)
	}
	if got, _ := os.ReadFile(e.target); string(got) != "binary v1.9.0" {
		t.Fatal("binary replaced despite checksum mismatch")
	}
	if len(*e.logs) != 1 || !strings.Contains((*e.logs)[0], "will retry on the next check") {
		t.Fatalf("logs = %v", *e.logs)
	}

	e.now = e.now.Add(time.Hour)
	if res := c.Tick(context.Background()); res.Attempted {
		t.Fatal("retried before the next check")
	}

	e.srv.AddArchive(t, "v1.10.0", "linux", "amd64", []byte("binary v1.10.0"))
	e.now = e.now.Add(selfupdate.CheckInterval)
	res = c.Tick(context.Background())
	if !res.Installed {
		t.Fatalf("retry tick = %+v", res)
	}
}
