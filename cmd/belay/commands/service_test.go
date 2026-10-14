package commands

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type serviceFakeRunner struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]int
}

func (f *serviceFakeRunner) run(name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call)
	for prefix, n := range f.fail {
		if n > 0 && strings.HasPrefix(call, prefix) {
			f.fail[prefix] = n - 1
			return []byte("simulated failure"), errors.New("exit status 1")
		}
	}
	return nil, nil
}

func serviceTestEnv(t *testing.T, goos string) (*serviceFakeRunner, string) {
	t.Helper()
	home := t.TempDir()
	fake := &serviceFakeRunner{fail: map[string]int{}}

	origRun, origHome, origGOOS, origUID := serviceRunCommand, serviceUserHomeDir, serviceGOOS, serviceUID
	origExe, origLook, origEnv, origStop := serviceExecutable, serviceLookPath, serviceGetenv, serviceStopRunningDaemon
	t.Cleanup(func() {
		serviceRunCommand, serviceUserHomeDir, serviceGOOS, serviceUID = origRun, origHome, origGOOS, origUID
		serviceExecutable, serviceLookPath, serviceGetenv, serviceStopRunningDaemon = origExe, origLook, origEnv, origStop
	})

	serviceRunCommand = fake.run
	serviceUserHomeDir = func() (string, error) { return home, nil }
	serviceGOOS = goos
	serviceUID = func() int { return 501 }
	serviceExecutable = func() (string, error) { return "/usr/local/bin/belay", nil }
	serviceLookPath = func(string) (string, error) { return "", errors.New("not found") }
	serviceGetenv = func(string) string { return "" }
	serviceStopRunningDaemon = func(string) (int, error) { return 0, nil }
	return fake, home
}

func TestServiceCmd_Subcommands(t *testing.T) {
	cmd := newServiceCmd()
	if cmd.Use != "service" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	want := map[string]bool{"install": false, "uninstall": false, "status": false}
	for _, c := range cmd.Commands() {
		want[c.Name()] = true
		if c.Name() == "install" && c.Flags().Lookup("dry-run") == nil {
			t.Error("install missing --dry-run flag")
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func TestServiceNames_StableSanitizedDistinct(t *testing.T) {
	a := serviceLaunchdLabel("/home/user/My Project!")
	if a != serviceLaunchdLabel("/home/user/My Project!") {
		t.Error("label not stable")
	}
	if !strings.HasPrefix(a, "dev.belay.daemon.my-project-") {
		t.Errorf("label = %q", a)
	}
	hash := a[strings.LastIndex(a, "-")+1:]
	if len(hash) != 8 || strings.Trim(hash, "0123456789abcdef") != "" {
		t.Errorf("hash suffix = %q", hash)
	}

	b := serviceLaunchdLabel("/srv/other/My Project!")
	if a == b {
		t.Error("different roots produced the same label")
	}

	unit := serviceSystemdUnitName("/home/user/My Project!")
	if !strings.HasPrefix(unit, "belay-my-project-") || !strings.HasSuffix(unit, ".service") {
		t.Errorf("unit = %q", unit)
	}
	for _, r := range strings.TrimSuffix(unit, ".service") {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
			t.Errorf("unit name has invalid char %q", r)
		}
	}

	cases := map[string]string{
		"/home/user/project":                    "project",
		"/home/user/__x__y__":                   "x-y",
		"/home/user/日本":                         "project",
		"/home/user/a.b.c":                      "a-b-c",
		"/home/user/project/":                   "project",
		"/home/user/" + strings.Repeat("z", 80): strings.Repeat("z", 40),
	}
	for in, want := range cases {
		if got := serviceSlug(in); got != want {
			t.Errorf("serviceSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServiceRenderPlist_ValidXMLAndEscaped(t *testing.T) {
	root := "/home/user/a&b <c>"
	bin := "/opt/tools & more/<bin>/belay"
	out := serviceRenderPlist("dev.belay.daemon.x-12345678", bin, root)

	dec := xml.NewDecoder(strings.NewReader(out))
	dec.Strict = true
	var strs []string
	var inString bool
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid XML: %v\n%s", err, out)
		}
		switch tt := tok.(type) {
		case xml.StartElement:
			inString = tt.Name.Local == "string"
		case xml.EndElement:
			inString = false
		case xml.CharData:
			if inString {
				strs = append(strs, string(tt))
			}
		}
	}

	joined := strings.Join(strs, "\n")
	for _, want := range []string{root, bin, "daemon", "start", "--foreground", "/dev/null", "Background"} {
		if !strings.Contains(joined, want) {
			t.Errorf("decoded strings missing %q", want)
		}
	}
	if strings.Contains(out, "a&b") || strings.Contains(out, "<c>") {
		t.Error("raw & or < not escaped")
	}
	if !strings.Contains(out, "<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>") {
		t.Errorf("KeepAlive SuccessfulExit false missing:\n%s", out)
	}
	if !strings.Contains(out, "<key>RunAtLoad</key>\n\t<true/>") {
		t.Error("RunAtLoad true missing")
	}
}

func TestServiceRenderSystemdUnit_PathsWithSpaces(t *testing.T) {
	out := serviceRenderSystemdUnit("/home/user/my tools/belay", "/home/user/my project 100%")
	for _, want := range []string{
		`ExecStart="/home/user/my tools/belay" daemon start --foreground`,
		"WorkingDirectory=/home/user/my project 100%%",
		"Restart=on-failure",
		"RestartSec=5",
		"[Install]\nWantedBy=default.target",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("unit missing %q:\n%s", want, out)
		}
	}

	q := serviceSystemdQuote(`/a "b"\c$d`)
	if q != `"/a \"b\"\\c$$d"` {
		t.Errorf("quote = %s", q)
	}
}

func TestServiceSpec_UnsupportedOS(t *testing.T) {
	serviceTestEnv(t, "plan9")
	_, err := serviceSpecFor("/home/user/project", "/usr/local/bin/belay")
	if err == nil || !strings.Contains(err.Error(), "not supported on plan9") {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceResolveBinary(t *testing.T) {
	serviceTestEnv(t, "linux")
	dir := t.TempDir()
	real := filepath.Join(dir, "real-belay")
	if err := os.WriteFile(real, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "belay")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	serviceExecutable = func() (string, error) { return real, nil }
	serviceLookPath = func(string) (string, error) { return link, nil }
	if got, _ := serviceResolveBinary(); got != link {
		t.Errorf("same file on PATH: got %q, want symlink %q", got, link)
	}

	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte("y"), 0o755); err != nil {
		t.Fatal(err)
	}
	serviceLookPath = func(string) (string, error) { return other, nil }
	if got, _ := serviceResolveBinary(); got != real {
		t.Errorf("different file on PATH: got %q, want %q", got, real)
	}

	serviceGetenv = func(k string) string {
		if k == "BELAY_BIN" {
			return "/opt/custom/belay"
		}
		return ""
	}
	if got, _ := serviceResolveBinary(); got != "/opt/custom/belay" {
		t.Errorf("BELAY_BIN override: got %q", got)
	}
}

func TestServiceInstall_DryRunWritesNothing(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			fake, home := serviceTestEnv(t, goos)
			stopped := false
			serviceStopRunningDaemon = func(string) (int, error) { stopped = true; return 0, nil }

			var out bytes.Buffer
			if err := runServiceInstall(&out, "/home/user/project", true); err != nil {
				t.Fatal(err)
			}
			if len(fake.calls) != 0 {
				t.Errorf("dry run invoked commands: %v", fake.calls)
			}
			if stopped {
				t.Error("dry run stopped the daemon")
			}
			entries, _ := os.ReadDir(home)
			if len(entries) != 0 {
				t.Errorf("dry run wrote files under HOME: %v", entries)
			}
			if !strings.Contains(out.String(), "Would write "+home) || !strings.Contains(out.String(), "daemon") {
				t.Errorf("dry run output:\n%s", out.String())
			}
		})
	}
}

func TestServiceInstallUninstall_Darwin(t *testing.T) {
	fake, home := serviceTestEnv(t, "darwin")
	root := "/home/user/project"
	label := serviceLaunchdLabel(root)
	plist := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	stoppedPID := 0
	serviceStopRunningDaemon = func(string) (int, error) { stoppedPID = 4242; return 4242, nil }

	var out bytes.Buffer
	if err := runServiceInstall(&out, root, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(plist)
	if err != nil {
		t.Fatalf("plist not written: %v", err)
	}
	if !strings.Contains(string(data), "<string>/usr/local/bin/belay</string>") {
		t.Errorf("plist missing binary:\n%s", data)
	}
	if stoppedPID == 0 {
		t.Error("running daemon was not stopped")
	}
	wantCalls := []string{"launchctl bootstrap gui/501 " + plist}
	if strings.Join(fake.calls, "|") != strings.Join(wantCalls, "|") {
		t.Errorf("calls = %v, want %v", fake.calls, wantCalls)
	}
	if !strings.Contains(out.String(), label) || !strings.Contains(out.String(), plist) {
		t.Errorf("output missing label/path:\n%s", out.String())
	}

	fake.calls = nil
	if err := runServiceUninstall(io.Discard, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Error("plist not removed")
	}
	wantCalls = []string{"launchctl print gui/501/" + label, "launchctl bootout gui/501/" + label}
	if strings.Join(fake.calls, "|") != strings.Join(wantCalls, "|") {
		t.Errorf("uninstall calls = %v, want %v", fake.calls, wantCalls)
	}

	fake.fail["launchctl print"] = 1
	fake.calls = nil
	out.Reset()
	if err := runServiceUninstall(&out, root); err != nil {
		t.Fatalf("second uninstall should be a no-op: %v", err)
	}
	if !strings.Contains(out.String(), "not installed") {
		t.Errorf("output = %q", out.String())
	}
}

func TestServiceInstall_DarwinAlreadyLoadedRetries(t *testing.T) {
	fake, home := serviceTestEnv(t, "darwin")
	root := "/home/user/project"
	label := serviceLaunchdLabel(root)
	plist := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	fake.fail["launchctl bootstrap"] = 1

	if err := runServiceInstall(io.Discard, root, false); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"launchctl bootstrap gui/501 " + plist,
		"launchctl print gui/501/" + label,
		"launchctl bootout gui/501/" + label,
		"launchctl bootstrap gui/501 " + plist,
	}
	if strings.Join(fake.calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v, want %v", fake.calls, want)
	}
}

func TestServiceInstall_DarwinBootstrapFailsNotLoaded(t *testing.T) {
	fake, _ := serviceTestEnv(t, "darwin")
	fake.fail["launchctl bootstrap"] = 1
	fake.fail["launchctl print"] = 1
	err := runServiceInstall(io.Discard, "/home/user/project", false)
	if err == nil || !strings.Contains(err.Error(), "launchctl bootstrap") {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceInstallUninstall_Linux(t *testing.T) {
	fake, home := serviceTestEnv(t, "linux")
	root := "/home/user/my project"
	unit := serviceSystemdUnitName(root)
	unitPath := filepath.Join(home, ".config", "systemd", "user", unit)

	var out bytes.Buffer
	if err := runServiceInstall(&out, root, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("unit not written: %v", err)
	}
	if !strings.Contains(string(data), "WorkingDirectory=/home/user/my project\n") {
		t.Errorf("unit content:\n%s", data)
	}
	want := []string{"systemctl --user daemon-reload", "systemctl --user enable --now " + unit}
	if strings.Join(fake.calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v, want %v", fake.calls, want)
	}
	if !strings.Contains(out.String(), "loginctl enable-linger") {
		t.Error("missing linger hint")
	}

	fake.calls = nil
	out.Reset()
	if err := runServiceStatus(&out, root); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Installed: yes") {
		t.Errorf("status output:\n%s", out.String())
	}
	if len(fake.calls) != 1 || fake.calls[0] != "systemctl --user is-active "+unit {
		t.Errorf("status calls = %v", fake.calls)
	}

	fake.calls = nil
	if err := runServiceUninstall(io.Discard, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Error("unit not removed")
	}
	want = []string{"systemctl --user disable --now " + unit, "systemctl --user daemon-reload"}
	if strings.Join(fake.calls, "|") != strings.Join(want, "|") {
		t.Errorf("uninstall calls = %v, want %v", fake.calls, want)
	}

	fake.fail["systemctl --user disable"] = 1
	out.Reset()
	if err := runServiceUninstall(&out, root); err != nil {
		t.Fatalf("uninstall when not installed should succeed: %v", err)
	}
	if !strings.Contains(out.String(), "not installed") {
		t.Errorf("output = %q", out.String())
	}
}

func TestServiceStatus_NotInstalledDarwin(t *testing.T) {
	fake, _ := serviceTestEnv(t, "darwin")
	fake.fail["launchctl print"] = 1
	var out bytes.Buffer
	if err := runServiceStatus(&out, "/home/user/project"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Installed: no") || !strings.Contains(out.String(), "not loaded") {
		t.Errorf("status output:\n%s", out.String())
	}
}
