package daemon

import (
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gitbridge "github.com/davidparkercodes/belay/internal/git"
)

func writeExe(t *testing.T, path, data string) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(data), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func stamp(t *testing.T, path string) (fileStamp, error) {
	t.Helper()
	return statStamp(path)
}

func TestBinaryWatchNeedsSettledReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "belay")
	writeExe(t, path, "v1")
	w := newBinaryWatch(path)

	if w.observe(stamp(t, path)) {
		t.Fatal("unchanged binary reported as replaced")
	}
	writeExe(t, path, "v2")
	if w.observe(stamp(t, path)) {
		t.Fatal("replacement accepted on its first sighting")
	}
	writeExe(t, path, "v2 still writing")
	if w.observe(stamp(t, path)) {
		t.Fatal("replacement accepted while it was still changing")
	}
	if !w.observe(stamp(t, path)) {
		t.Fatal("settled replacement not reported")
	}
}

func TestBinaryWatchIgnoresMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "belay")
	writeExe(t, path, "v1")
	w := newBinaryWatch(path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if w.observe(stamp(t, path)) {
			t.Fatal("missing binary reported as a replacement")
		}
	}
	writeExe(t, path, "v1")
	w2 := newBinaryWatch("")
	if w2.observe(stamp(t, path)) {
		t.Fatal("watch with no launch path fired")
	}
}

func TestBinaryWatchRevertToOriginalClearsPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "belay")
	writeExe(t, path, "v1")
	w := newBinaryWatch(path)
	orig := w.original
	writeExe(t, path, "v2")
	w.observe(stamp(t, path))
	if !w.pending.valid() {
		t.Fatal("replacement not recorded as pending")
	}
	if w.observe(orig, nil) || w.pending.valid() {
		t.Fatal("pending kept after the original came back")
	}
}

func updateTestDaemon(t *testing.T, path string) (*Daemon, *strings.Builder) {
	t.Helper()
	var logs strings.Builder
	d := &Daemon{
		cfg:       testConfig(t),
		version:   "v1.0.0",
		logger:    log.New(io.MultiWriter(&logs), "", 0),
		restartCh: make(chan string, 1),
	}
	d.binaryWatch = newBinaryWatch(path)
	return d, &logs
}

func stubProbe(t *testing.T, version string, err error) *[]string {
	t.Helper()
	var calls []string
	old := probeBinary
	probeBinary = func(p string) (string, error) {
		calls = append(calls, p)
		return version, err
	}
	t.Cleanup(func() { probeBinary = old })
	return &calls
}

func TestCheckBinaryRequestsRestartWhenIdle(t *testing.T) {
	if !reexecSupported() {
		t.Skip("re-exec is unix only")
	}
	path := filepath.Join(t.TempDir(), "belay")
	writeExe(t, path, "v1")
	d, logs := updateTestDaemon(t, path)
	calls := stubProbe(t, "v1.1.0", nil)

	if d.checkBinary() {
		t.Fatal("restart requested with no change")
	}
	writeExe(t, path, "v2")
	if d.checkBinary() {
		t.Fatal("restart requested on the first sighting")
	}
	if !d.checkBinary() {
		t.Fatal("no restart for a settled replacement")
	}
	select {
	case got := <-d.restartCh:
		if got != path {
			t.Fatalf("restart path = %s", got)
		}
	default:
		t.Fatal("nothing sent on restartCh")
	}
	if len(*calls) != 1 || (*calls)[0] != path {
		t.Fatalf("probe calls = %v", *calls)
	}
	if !strings.Contains(logs.String(), "restarting into it") {
		t.Fatalf("logs = %s", logs.String())
	}
}

func TestCheckBinaryWaitsForIdle(t *testing.T) {
	if !reexecSupported() {
		t.Skip("re-exec is unix only")
	}
	path := filepath.Join(t.TempDir(), "belay")
	writeExe(t, path, "v1")
	d, _ := updateTestDaemon(t, path)
	stubProbe(t, "v1.1.0", nil)
	writeExe(t, path, "v2")
	d.checkBinary()

	d.projector = newProjector(d)
	d.projector.Enqueue(gitbridge.ProjectionJob{SessionID: "s1"})
	if d.checkBinary() {
		t.Fatal("restarted with a queued projection")
	}
	d.projector.queue = nil

	d.migrating.Store(true)
	if d.checkBinary() {
		t.Fatal("restarted during a store migration")
	}
	d.migrating.Store(false)

	d.compactionRunning = true
	if d.checkBinary() {
		t.Fatal("restarted during compaction")
	}
	d.compactionRunning = false

	d.updating.Store(true)
	if d.checkBinary() {
		t.Fatal("restarted during an update")
	}
	d.updating.Store(false)

	if !d.checkBinary() {
		t.Fatal("did not restart once idle")
	}
}

func TestCheckBinaryRejectsBrokenBinary(t *testing.T) {
	if !reexecSupported() {
		t.Skip("re-exec is unix only")
	}
	path := filepath.Join(t.TempDir(), "belay")
	writeExe(t, path, "v1")
	d, logs := updateTestDaemon(t, path)
	calls := stubProbe(t, "", errors.New("exec format error"))
	writeExe(t, path, "garbage")
	d.checkBinary()
	if d.checkBinary() {
		t.Fatal("restarted into a binary that failed its probe")
	}
	for i := 0; i < 3; i++ {
		d.checkBinary()
	}
	if len(*calls) != 1 {
		t.Fatalf("probed a rejected binary %d times", len(*calls))
	}
	if !strings.Contains(logs.String(), "does not run") {
		t.Fatalf("logs = %s", logs.String())
	}

	*calls = nil
	probeBinary = func(p string) (string, error) {
		*calls = append(*calls, p)
		return "v1.2.0", nil
	}
	writeExe(t, path, "fixed")
	d.checkBinary()
	if !d.checkBinary() {
		t.Fatal("no restart after a working binary replaced the broken one")
	}
}

func TestCheckBinarySkipsDowngrade(t *testing.T) {
	if !reexecSupported() {
		t.Skip("re-exec is unix only")
	}
	path := filepath.Join(t.TempDir(), "belay")
	writeExe(t, path, "v1")
	d, logs := updateTestDaemon(t, path)
	calls := stubProbe(t, "v0.9.0", nil)
	writeExe(t, path, "older")
	d.checkBinary()
	if d.checkBinary() {
		t.Fatal("re-exec'd into an older binary")
	}
	for i := 0; i < 3; i++ {
		d.checkBinary()
	}
	if len(*calls) != 1 || !strings.Contains(logs.String(), "older version") {
		t.Fatalf("calls = %v logs = %s", *calls, logs.String())
	}
}

func TestRestartUsesStubbedExec(t *testing.T) {
	cfg := testConfig(t)
	lock, err := acquirePIDLock(cfg.PIDPath())
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: cfg, pidLock: lock}
	var gotPath string
	var gotArgs []string
	old := reexecProcess
	reexecProcess = func(path string, args, env []string) error {
		gotPath, gotArgs = path, args
		return errors.New("stubbed")
	}
	t.Cleanup(func() { reexecProcess = old })

	if err := d.restart("/some/belay"); err == nil {
		t.Fatal("expected the stub's error")
	}
	if gotPath != "/some/belay" || len(gotArgs) != len(os.Args) {
		t.Fatalf("exec(%s, %v)", gotPath, gotArgs)
	}
	if _, err := os.Stat(cfg.PIDPath()); !os.IsNotExist(err) {
		t.Fatal("PID file left behind after a failed restart")
	}
}

func TestLaunchPathFor(t *testing.T) {
	dir := t.TempDir()
	look := func(name string) (string, error) {
		if name == "belay" {
			return filepath.Join(dir, "belay"), nil
		}
		return "", errors.New("not found")
	}
	if got := launchPathFor("belay", look); got != filepath.Join(dir, "belay") {
		t.Fatalf("bare name = %s", got)
	}
	abs := filepath.Join(dir, "link", "belay")
	if got := launchPathFor(abs, look); got != abs {
		t.Fatalf("absolute = %s", got)
	}
	if got := launchPathFor("missing", look); got != "" {
		t.Fatalf("missing = %s", got)
	}
	if got := launchPathFor("", look); got != "" {
		t.Fatalf("empty = %s", got)
	}
}

func TestLaunchPathKeepsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "Caskroom", "belay", "1.0.0", "belay")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	writeExe(t, target, "v1")
	link := filepath.Join(dir, "bin", "belay")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got := launchPathFor(link, nil); got != link {
		t.Fatalf("launch path = %s, want the link", got)
	}

	w := newBinaryWatch(link)
	next := filepath.Join(dir, "Caskroom", "belay", "1.1.0", "belay")
	if err := os.MkdirAll(filepath.Dir(next), 0o755); err != nil {
		t.Fatal(err)
	}
	writeExe(t, next, "v2")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(next, link); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(target)); err != nil {
		t.Fatal(err)
	}
	w.observe(stamp(t, link))
	if !w.observe(stamp(t, link)) {
		t.Fatal("upgrade through a relinked symlink not detected")
	}
}

func TestRunUpdateLoopRespectsOptOut(t *testing.T) {
	d, _ := updateTestDaemon(t, "")
	old := updateStartupSettle
	updateStartupSettle = time.Hour
	t.Cleanup(func() { updateStartupSettle = old })
	d.startUpdateLoops()
	done := make(chan struct{})
	go func() {
		d.stopUpdateLoops()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("update loops did not stop")
	}
}
