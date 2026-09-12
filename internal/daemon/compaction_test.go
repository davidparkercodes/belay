package daemon

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"
)

func testDaemonForCompaction(t *testing.T) *Daemon {
	t.Helper()
	cfg := testConfig(t)
	if err := os.MkdirAll(cfg.EventsDir(), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	idx, err := index.Open(cfg.IndexPath())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	objStore, err := store.NewStore(cfg.ObjectsDir(), false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { objStore.Close() })
	return &Daemon{
		cfg:      cfg,
		idx:      idx,
		objStore: objStore,
		logger:   log.New(io.Discard, "", 0),
	}
}

func TestCompaction_DueOnFirstRunAndPersistsState(t *testing.T) {
	d := testDaemonForCompaction(t)
	d.cfg.Retention.MaxVersionsPerFile = 1
	d.cfg.Retention.WarmDays = 0
	d.cfg.Retention.ColdDays = 0

	now := time.Now()
	for i := 0; i < 3; i++ {
		e := &schema.Event{EventID: fmt.Sprintf("e%d", i), FilePath: "a.go", Op: schema.OpModify, ContentHash: fmt.Sprintf("h%d", i), SessionID: fmt.Sprintf("s%d", i)}
		e.SetTimestamp(now.Add(-time.Duration(10-i) * 24 * time.Hour))
		if err := d.idx.IndexEvent(e, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	d.loadCompactionState()
	d.compactIfDue()

	status := d.CompactionStatus()
	if _, ok := status["last_run_at"]; !ok {
		t.Fatal("compaction should have run on first check (never run before)")
	}
	if d.lastCompaction == nil || d.lastCompaction.EventsRemoved != 2 {
		t.Errorf("expected 2 events removed by version cap, got %+v", d.lastCompaction)
	}

	persisted, _ := d.idx.GetMeta(index.MetaLastCompactionAt)
	if persisted == "" {
		t.Error("last_compaction_at should be persisted in the index")
	}

	fresh := &Daemon{cfg: d.cfg, idx: d.idx, objStore: d.objStore, logger: d.logger}
	fresh.loadCompactionState()
	if fresh.lastCompactionAt.IsZero() {
		t.Error("restarted daemon should recover last_compaction_at from the index")
	}
	if fresh.lastCompaction == nil || fresh.lastCompaction.EventsRemoved != 2 {
		t.Error("restarted daemon should recover the last result from the index")
	}
}

func TestCompaction_NotDueWithinInterval(t *testing.T) {
	d := testDaemonForCompaction(t)
	d.cfg.Retention.CompactionIntervalMin = 60
	d.lastCompactionAt = time.Now().Add(-10 * time.Minute)

	d.compactIfDue()

	if v, _ := d.idx.GetMeta(index.MetaLastCompactionAt); v != "" {
		t.Error("compaction must not run when the last run is inside the interval")
	}
}

func TestCompaction_DueAfterWallClockGap(t *testing.T) {
	d := testDaemonForCompaction(t)
	d.cfg.Retention.CompactionIntervalMin = 60
	stale := time.Now().Add(-3 * time.Hour)
	if err := d.idx.SetMeta(index.MetaLastCompactionAt, stale.Format(time.RFC3339)); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	d.loadCompactionState()
	d.compactIfDue()

	if !d.lastCompactionAt.After(stale.Add(time.Hour)) {
		t.Error("a run older than the interval (e.g. across sleep/restart) must trigger compaction")
	}
}

func TestCompaction_DryRunDoesNotTouchSchedule(t *testing.T) {
	d := testDaemonForCompaction(t)
	if _, err := d.RunCompactionNow(true); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !d.lastCompactionAt.IsZero() {
		t.Error("dry run must not update the schedule")
	}
	if v, _ := d.idx.GetMeta(index.MetaLastCompactionAt); v != "" {
		t.Error("dry run must not persist state")
	}
}

func TestCompaction_RejectsConcurrentRun(t *testing.T) {
	d := testDaemonForCompaction(t)
	d.compactionRunning = true
	if _, err := d.RunCompactionNow(false); err != errCompactionInProgress {
		t.Errorf("expected errCompactionInProgress, got %v", err)
	}
}

func TestNew_WritesDaemonLogFile(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.logger.Println("hello from test")
	if d.logFile != nil {
		d.logFile.Close()
	}
	data, err := os.ReadFile(cfg.LogPath())
	if err != nil {
		t.Fatalf("daemon.log not written: %v", err)
	}
	if !strings.Contains(string(data), "hello from test") {
		t.Errorf("daemon.log missing log line: %q", data)
	}
}

func TestRotatingFile_RotatesAndCapsFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")
	rf, err := openRotatingFile(path, 32, 2)
	if err != nil {
		t.Fatalf("openRotatingFile: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := rf.Write([]byte("0123456789abcdef\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	rf.Close()

	if _, err := os.Stat(path); err != nil {
		t.Error("current log file missing")
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Error("rotated .1 missing")
	}
	if _, err := os.Stat(path + ".2"); err != nil {
		t.Error("rotated .2 missing")
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Error("more rotated files than log_max_files")
	}
}
