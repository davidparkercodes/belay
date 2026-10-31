package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/daemon"
	"github.com/davidparkercodes/belay/internal/selfupdate"
	"github.com/davidparkercodes/belay/internal/selfupdate/releasetest"
)

func isolateUserDirs(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv(selfupdate.DisableEnv, "")
	return home
}

type updateFixture struct {
	srv    *releasetest.Server
	target string
}

func setupUpdate(t *testing.T, latest string) *updateFixture {
	t.Helper()
	home := isolateUserDirs(t)
	srv := releasetest.New(t, latest, []byte("belay "+latest), [2]string{"linux", "amd64"})
	target := filepath.Join(home, ".local", "bin", "belay")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("belay old"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldClient, oldExe, oldEnv := updateNewClient, updateExecutable, updateEnv
	oldOS, oldArch, oldTTY, oldIn := updateGOOS, updateGOARCH, updateIsTerminal, updateStdin
	t.Cleanup(func() {
		updateNewClient, updateExecutable, updateEnv = oldClient, oldExe, oldEnv
		updateGOOS, updateGOARCH, updateIsTerminal, updateStdin = oldOS, oldArch, oldTTY, oldIn
	})
	updateNewClient = func(string) *selfupdate.Client { return srv.Client() }
	updateExecutable = func() (string, error) { return target, nil }
	updateEnv = func() selfupdate.Env {
		return selfupdate.Env{Getenv: func(string) string { return "" }, HomeDir: home}
	}
	updateGOOS, updateGOARCH = "linux", "amd64"
	return &updateFixture{srv: srv, target: target}
}

func runUpdateCmd(t *testing.T, version string, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd(version)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"update"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestUpdateReleaseBinaryEndToEnd(t *testing.T) {
	f := setupUpdate(t, "v1.10.0")
	out, err := runUpdateCmd(t, "v1.9.0", "--yes")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if got := readFile(t, f.target); got != "belay v1.10.0" {
		t.Fatalf("binary = %q", got)
	}
	if got := readFile(t, selfupdate.PrevPath(f.target)); got != "belay old" {
		t.Fatalf("prev = %q", got)
	}
	if !strings.Contains(out, "Updated Belay v1.9.0 -> v1.10.0") {
		t.Fatalf("output = %s", out)
	}
}

func TestUpdateRefusesChecksumMismatch(t *testing.T) {
	f := setupUpdate(t, "v1.10.0")
	f.srv.Corrupt("v1.10.0", "linux", "amd64")
	out, err := runUpdateCmd(t, "v1.9.0", "--yes")
	if !errors.Is(err, selfupdate.ErrChecksumMismatch) {
		t.Fatalf("err = %v, want checksum mismatch\n%s", err, out)
	}
	if got := readFile(t, f.target); got != "belay old" {
		t.Fatalf("binary replaced despite mismatch: %q", got)
	}
	if _, err := os.Stat(selfupdate.PrevPath(f.target)); !os.IsNotExist(err) {
		t.Fatal("belay.prev written for a refused update")
	}
}

func TestUpdateRefusesMissingChecksum(t *testing.T) {
	f := setupUpdate(t, "v1.10.0")
	f.srv.AddArchive(t, "v1.10.0", "darwin", "arm64", []byte("x"))
	f.srv.DropChecksum("v1.10.0", "linux", "amd64")
	if _, err := runUpdateCmd(t, "v1.9.0", "--yes"); !errors.Is(err, selfupdate.ErrChecksumMissing) {
		t.Fatalf("err = %v, want missing checksum", err)
	}
	if got := readFile(t, f.target); got != "belay old" {
		t.Fatalf("binary replaced: %q", got)
	}
}

func TestUpdateCheckJSON(t *testing.T) {
	f := setupUpdate(t, "v1.10.0")
	out, err := runUpdateCmd(t, "v1.9.0", "--check", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rep map[string]interface{}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if rep["update_available"] != true || rep["latest"] != "v1.10.0" || rep["method"] != "release" {
		t.Fatalf("report = %v", rep)
	}
	if got := readFile(t, f.target); got != "belay old" {
		t.Fatal("--check replaced the binary")
	}
	if line := selfupdate.StatusNotice("v1.9.0", os.Getenv); line != "Belay v1.10.0 is available (you have v1.9.0). Run belay update." {
		t.Fatalf("status notice after --check = %q", line)
	}
}

func TestUpdateAlreadyCurrent(t *testing.T) {
	f := setupUpdate(t, "v1.9.0")
	out, err := runUpdateCmd(t, "v1.9.0", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "up to date") {
		t.Fatalf("output = %s", out)
	}
	if f.srv.DownloadRequests.Load() != 0 {
		t.Fatal("downloaded although already current")
	}
}

func TestUpdatePinnedVersion(t *testing.T) {
	f := setupUpdate(t, "v1.10.0")
	f.srv.AddArchive(t, "v1.9.5", "linux", "amd64", []byte("belay v1.9.5"))
	if _, err := runUpdateCmd(t, "v1.9.0", "--version", "1.9.5", "--yes"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, f.target); got != "belay v1.9.5" {
		t.Fatalf("binary = %q", got)
	}
	if f.srv.APIRequests.Load() != 0 {
		t.Fatal("queried latest although a version was pinned")
	}
	if _, err := runUpdateCmd(t, "v1.9.0", "--version", "nope"); err == nil {
		t.Fatal("invalid --version accepted")
	}
}

func TestUpdateDevBuildRefused(t *testing.T) {
	f := setupUpdate(t, "v1.10.0")
	_, err := runUpdateCmd(t, "dev", "--yes")
	if !errors.Is(err, selfupdate.ErrDevBuild) {
		t.Fatalf("err = %v, want dev refusal", err)
	}
	if got := readFile(t, f.target); got != "belay old" {
		t.Fatal("dev build replaced")
	}
}

func TestUpdateConfirmDeclined(t *testing.T) {
	f := setupUpdate(t, "v1.10.0")
	updateIsTerminal = func() bool { return true }
	updateStdin = strings.NewReader("n\n")
	out, err := runUpdateCmd(t, "v1.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[y/N]") || !strings.Contains(out, "cancelled") {
		t.Fatalf("output = %s", out)
	}
	if got := readFile(t, f.target); got != "belay old" {
		t.Fatal("binary replaced after the user said no")
	}

	updateStdin = strings.NewReader("y\n")
	if _, err := runUpdateCmd(t, "v1.9.0"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, f.target); got != "belay v1.10.0" {
		t.Fatalf("binary = %q", got)
	}
}

func TestUpdateNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	f := setupUpdate(t, "v1.10.0")
	dir := filepath.Dir(f.target)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_, err := runUpdateCmd(t, "v1.9.0", "--yes")
	if err == nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("err = %v, want a message naming %s", err, dir)
	}
	if f.srv.DownloadRequests.Load() != 0 {
		t.Fatal("downloaded before checking the directory is writable")
	}
}

func TestUpdateModeFlags(t *testing.T) {
	isolateUserDirs(t)
	path, err := config.UserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--auto", "on"}, config.UpdateAuto},
		{[]string{"--auto", "off"}, config.UpdateNotify},
		{[]string{"--mode", "off"}, config.UpdateOff},
		{[]string{"--mode", "notify"}, config.UpdateNotify},
	} {
		if out, err := runUpdateCmd(t, "v1.9.0", tc.args...); err != nil {
			t.Fatalf("%v: %v\n%s", tc.args, err, out)
		}
		cfg, err := config.LoadUserConfigFrom(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Update.Mode != tc.want {
			t.Fatalf("%v: mode = %q, want %q", tc.args, cfg.Update.Mode, tc.want)
		}
	}
	for _, bad := range [][]string{{"--auto", "maybe"}, {"--mode", "sometimes"}, {"--auto", "on", "--mode", "off"}} {
		if _, err := runUpdateCmd(t, "v1.9.0", bad...); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}

func TestStatusNoticeReadsCacheOnly(t *testing.T) {
	isolateUserDirs(t)
	dir, err := selfupdate.CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := selfupdate.WriteState(selfupdate.CachePath(dir), selfupdate.State{CheckedAt: time.Now(), Latest: "v2.0.0"}); err != nil {
		t.Fatal(err)
	}
	want := "Belay v2.0.0 is available (you have v1.9.0). Run belay update."
	if got := updateNoticeLine("v1.9.0"); got != want {
		t.Fatalf("notice = %q", got)
	}
	if got := updateNoticeLine("v2.0.0"); got != "" {
		t.Fatalf("notice when current = %q", got)
	}
	if got := updateNoticeLine("dev"); got != "" {
		t.Fatalf("notice for dev = %q", got)
	}
	t.Setenv(selfupdate.DisableEnv, "1")
	if got := updateNoticeLine("v1.9.0"); got != "" {
		t.Fatalf("notice with opt-out = %q", got)
	}
	t.Setenv(selfupdate.DisableEnv, "")
	path, _ := config.UserConfigPath()
	if err := config.SetUserUpdateMode(path, config.UpdateOff); err != nil {
		t.Fatal(err)
	}
	if got := updateNoticeLine("v1.9.0"); got != "" {
		t.Fatalf("notice with mode off = %q", got)
	}
}

func TestOtherDaemonRunningIgnoresOwnPID(t *testing.T) {
	cfg := config.DefaultConfig(t.TempDir())
	if err := os.MkdirAll(cfg.BelayPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.PIDPath(), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	if running, _ := otherDaemonRunning(cfg); running {
		t.Fatal("own PID treated as another daemon")
	}
	if running, _ := daemon.IsRunning(cfg); !running {
		t.Fatal("IsRunning should still report the live PID")
	}
}
