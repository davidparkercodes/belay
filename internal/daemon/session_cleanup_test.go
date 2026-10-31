package daemon

import (
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

func upsertActiveSession(t *testing.T, d *Daemon, id string, pid int, startedAt time.Time) {
	t.Helper()
	if err := d.idx.UpsertSession(&schema.Session{
		SessionID: id,
		ToolName:  "test",
		PID:       pid,
		Status:    schema.SessionActive,
		StartedAt: startedAt,
	}); err != nil {
		t.Fatalf("UpsertSession(%s): %v", id, err)
	}
}

func indexSessionEvent(t *testing.T, d *Daemon, id, sessionID string, ts time.Time) {
	t.Helper()
	e := &schema.Event{
		EventID:       id,
		TimestampNano: ts.UnixNano(),
		FilePath:      "f.go",
		Op:            schema.OpModify,
		ContentHash:   "h",
		SessionID:     sessionID,
	}
	if err := d.idx.IndexEvent(e, "seg.log", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}
}

func sessionStatus(t *testing.T, d *Daemon, id string) schema.SessionStatus {
	t.Helper()
	s, err := d.idx.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession(%s): %v", id, err)
	}
	return s.Status
}

func TestCleanupStaleSessions_PIDZeroRecentlyActiveStaysActive(t *testing.T) {
	d := testDaemonWithSubsystems(t)
	upsertActiveSession(t, d, "hook-fresh", 0, time.Now().Add(-2*time.Hour))
	indexSessionEvent(t, d, "e1", "hook-fresh", time.Now().Add(-5*time.Minute))

	d.cleanupStaleSessions()

	if got := sessionStatus(t, d, "hook-fresh"); got != schema.SessionActive {
		t.Errorf("status = %s, want active (last event 5m ago)", got)
	}
}

func TestCleanupStaleSessions_PIDZeroNoEventsRecentStartStaysActive(t *testing.T) {
	d := testDaemonWithSubsystems(t)
	upsertActiveSession(t, d, "hook-new", 0, time.Now().Add(-1*time.Minute))

	d.cleanupStaleSessions()

	if got := sessionStatus(t, d, "hook-new"); got != schema.SessionActive {
		t.Errorf("status = %s, want active (started 1m ago, no events yet)", got)
	}
}

func TestCleanupStaleSessions_PIDZeroIdleIsEnded(t *testing.T) {
	d := testDaemonWithSubsystems(t)
	upsertActiveSession(t, d, "hook-idle", 0, time.Now().Add(-3*time.Hour))
	indexSessionEvent(t, d, "e1", "hook-idle", time.Now().Add(-hookSessionIdleTimeout-time.Minute))
	d.trackSessionFile("hook-idle", "f.go")

	d.cleanupStaleSessions()

	if got := sessionStatus(t, d, "hook-idle"); got != schema.SessionEnded {
		t.Errorf("status = %s, want ended (idle past timeout)", got)
	}
	if d.sessionHasFile("hook-idle", "f.go") {
		t.Error("ended session should be dropped from sessionFiles")
	}
}

func TestCleanupStaleSessions_DeadPIDIsEndedNotCrashed(t *testing.T) {
	d := testDaemonWithSubsystems(t)
	upsertActiveSession(t, d, "proc-gone", 4000000, time.Now().Add(-10*time.Minute))

	d.cleanupStaleSessions()

	if got := sessionStatus(t, d, "proc-gone"); got != schema.SessionEnded {
		t.Errorf("status = %s, want ended", got)
	}
}
