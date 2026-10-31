package daemon

import (
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
)

func hookEvent(sessionID, file, hash string, at time.Time) *schema.Event {
	ev := &schema.Event{
		EventID:               schema.NewEventID(),
		Version:               schema.SchemaVersion,
		FilePath:              file,
		Op:                    schema.OpModify,
		ContentHash:           hash,
		ContentSize:           int64(len(hash)),
		SessionID:             sessionID,
		Attribution:           schema.AttrHook,
		AttributionConfidence: 1.0,
		Metadata:              map[string]string{"source": "hook"},
	}
	ev.SetTimestamp(at)
	return ev
}

func countFileEvents(t *testing.T, d *Daemon, file string) int {
	t.Helper()
	events, err := d.idx.QueryEvents(&index.Query{FilePaths: []string{file}})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	return len(events)
}

func TestProcessEvent_DropsHookDuplicateOfWatcherWrite(t *testing.T) {
	d := testDaemonWithSubsystems(t)
	now := time.Now()

	watcher := hookEvent("uuid-1", "a.go", "hash-1", now)
	watcher.Attribution = schema.AttrPID
	watcher.Metadata = nil
	d.processEvent(watcher)
	hook := hookEvent("uuid-1", "a.go", "hash-1", now.Add(300*time.Millisecond))
	hook.Op = schema.OpCreate
	d.processEvent(hook)

	if n := countFileEvents(t, d, "a.go"); n != 1 {
		t.Fatalf("same content, same session, moments apart should index once, got %d", n)
	}
	s, _ := d.idx.GetSession("uuid-1")
	if s == nil || s.EventCount != 1 {
		t.Fatalf("session event count should stay at 1, got %+v", s)
	}
}

func TestProcessEvent_KeepsDistinctSessionsAndContent(t *testing.T) {
	d := testDaemonWithSubsystems(t)
	now := time.Now()

	d.processEvent(hookEvent("uuid-1", "b.go", "hash-1", now))
	d.processEvent(hookEvent("uuid-2", "b.go", "hash-1", now.Add(time.Second)))
	d.processEvent(hookEvent("uuid-2", "b.go", "hash-2", now.Add(2*time.Second)))
	d.processEvent(hookEvent("uuid-2", "b.go", "hash-2", now.Add(time.Minute)))

	if n := countFileEvents(t, d, "b.go"); n != 4 {
		t.Fatalf("different session, different content, or outside the window must all be kept, got %d", n)
	}
}

func TestProcessEvent_SessionCountsAccumulateAcrossHookEvents(t *testing.T) {
	d := testDaemonWithSubsystems(t)
	now := time.Now()

	d.processEvent(hookEvent("uuid-3", "c.go", "h1", now))
	d.processEvent(hookEvent("uuid-3", "d.go", "h2", now.Add(time.Second)))
	d.processEvent(hookEvent("uuid-3", "c.go", "h3", now.Add(2*time.Second)))

	s, _ := d.idx.GetSession("uuid-3")
	if s == nil {
		t.Fatal("session not indexed")
	}
	if s.EventCount != 3 || s.FilesChanged != 2 {
		t.Fatalf("event_count=%d files_changed=%d, want 3/2", s.EventCount, s.FilesChanged)
	}
}
