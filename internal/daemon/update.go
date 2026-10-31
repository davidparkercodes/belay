package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/selfupdate"
)

var (
	updateStartupSettle = 2 * time.Minute
	updatePoll          = time.Hour
	binaryPoll          = 30 * time.Second
	binaryProbeTimeout  = 15 * time.Second

	newUpdateClient = func(version string) *selfupdate.Client {
		return selfupdate.NewClient("belay/" + version)
	}

	probeBinary = func(path string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), binaryProbeTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, path, "--version").Output()
		if err != nil {
			return "", err
		}
		line := strings.TrimSpace(string(out))
		if !strings.HasPrefix(line, "belay ") {
			return "", fmt.Errorf("unexpected --version output %q", line)
		}
		return strings.TrimPrefix(line, "belay "), nil
	}

	reexecProcess = execSelf
)

func (d *Daemon) startUpdateLoops() {
	d.restartCh = make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	d.updateCancel = cancel
	d.updateDone = make(chan struct{})
	go d.runUpdateLoop(ctx)

	d.binaryWatch = newBinaryWatch(d.launchPath)
	d.stopBinaryWatch = make(chan struct{})
	d.binaryWatchDone = make(chan struct{})
	go d.runBinaryWatch()
}

func (d *Daemon) stopUpdateLoops() {
	if d.updateCancel != nil {
		d.updateCancel()
		<-d.updateDone
		d.updateCancel = nil
	}
	if d.stopBinaryWatch != nil {
		close(d.stopBinaryWatch)
		<-d.binaryWatchDone
		d.stopBinaryWatch = nil
	}
}

func (d *Daemon) newUpdateChecker() (*selfupdate.Checker, error) {
	dir, err := selfupdate.CacheDir()
	if err != nil {
		return nil, err
	}
	client := newUpdateClient(d.version)
	return &selfupdate.Checker{
		Client:   client,
		Updater:  &selfupdate.Updater{Client: client, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Current:  d.version,
		CacheDir: dir,
		Getenv:   os.Getenv,
		Mode: func() string {
			cfg, err := config.LoadUserConfig()
			if err != nil {
				return config.UpdateNotify
			}
			return cfg.Update.Mode
		},
		Binary: d.installedBinary,
		Logf:   d.logger.Printf,
	}, nil
}

func (d *Daemon) installedBinary() (selfupdate.Install, error) {
	path := d.launchPath
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			return selfupdate.Install{}, err
		}
		path = exe
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return selfupdate.Install{}, fmt.Errorf("resolve %s: %w", path, err)
	}
	return selfupdate.Detect(resolved, d.version, selfupdate.DefaultEnv()), nil
}

func (d *Daemon) runUpdateLoop(ctx context.Context) {
	defer close(d.updateDone)
	if selfupdate.Disabled(os.Getenv) {
		return
	}
	select {
	case <-time.After(updateStartupSettle):
	case <-ctx.Done():
		return
	}
	checker, err := d.newUpdateChecker()
	if err != nil {
		d.logger.Printf("update check disabled: %v", err)
		return
	}
	for {
		d.updating.Store(true)
		checker.Tick(ctx)
		d.updating.Store(false)
		select {
		case <-time.After(updatePoll):
		case <-ctx.Done():
			return
		}
	}
}

func (d *Daemon) idle() bool {
	if d.projector != nil && !d.projector.idle() {
		return false
	}
	if d.migrating.Load() || d.updating.Load() {
		return false
	}
	d.compactionMu.Lock()
	running := d.compactionRunning
	d.compactionMu.Unlock()
	return !running
}

type fileStamp struct {
	info os.FileInfo
}

func statStamp(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{info: info}, nil
}

func (s fileStamp) valid() bool { return s.info != nil }

func (s fileStamp) same(o fileStamp) bool {
	if !s.valid() || !o.valid() {
		return s.valid() == o.valid()
	}
	return os.SameFile(s.info, o.info) && s.info.ModTime().Equal(o.info.ModTime()) && s.info.Size() == o.info.Size()
}

type binaryWatch struct {
	path     string
	original fileStamp
	pending  fileStamp
	rejected fileStamp
}

func newBinaryWatch(path string) *binaryWatch {
	w := &binaryWatch{path: path}
	if path != "" {
		w.original, _ = statStamp(path)
	}
	return w
}

func (w *binaryWatch) observe(cur fileStamp, err error) bool {
	if w.path == "" || !w.original.valid() || err != nil || !cur.valid() {
		return false
	}
	if cur.same(w.original) {
		w.pending = fileStamp{}
		return false
	}
	if w.rejected.valid() && cur.same(w.rejected) {
		return false
	}
	if !w.pending.valid() || !cur.same(w.pending) {
		w.pending = cur
		return false
	}
	return true
}

func (w *binaryWatch) reject() {
	w.rejected = w.pending
	w.pending = fileStamp{}
}

func (w *binaryWatch) accept() {
	w.original = w.pending
	w.pending = fileStamp{}
}

func (d *Daemon) runBinaryWatch() {
	defer close(d.binaryWatchDone)
	if d.binaryWatch.path == "" {
		return
	}
	ticker := time.NewTicker(binaryPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if d.checkBinary() {
				return
			}
		case <-d.stopBinaryWatch:
			return
		}
	}
}

func (d *Daemon) checkBinary() bool {
	w := d.binaryWatch
	cur, err := statStamp(w.path)
	if !w.observe(cur, err) || !d.idle() {
		return false
	}
	if !reexecSupported() {
		d.logger.Printf("the belay binary at %s was replaced; restart this daemon to run the new version", w.path)
		w.accept()
		return false
	}
	version, err := probeBinary(w.path)
	if err != nil {
		d.logger.Printf("the belay binary at %s was replaced but does not run (%v); staying on %s", w.path, err, d.version)
		w.reject()
		return false
	}
	if selfupdate.IsNewer(d.version, version) {
		d.logger.Printf("the belay binary at %s was replaced by an older version (%s); restart this daemon to run it", w.path, version)
		w.accept()
		return false
	}
	d.logger.Printf("the belay binary at %s was replaced (%s), restarting into it", w.path, version)
	select {
	case d.restartCh <- w.path:
	default:
	}
	return true
}

func launchPathFor(arg0 string, lookPath func(string) (string, error)) string {
	if arg0 == "" {
		return ""
	}
	if strings.ContainsRune(arg0, '/') || strings.ContainsRune(arg0, filepath.Separator) {
		abs, err := filepath.Abs(arg0)
		if err != nil {
			return ""
		}
		return abs
	}
	p, err := lookPath(arg0)
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	return abs
}

func (d *Daemon) restart(path string) error {
	err := reexecProcess(path, os.Args, os.Environ())
	d.pidLock.Release()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[belay] restart into %s failed: %v\n", path, err)
		return fmt.Errorf("restart into %s: %w", path, err)
	}
	return nil
}
