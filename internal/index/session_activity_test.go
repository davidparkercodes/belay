package index

import (
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

func TestSessionLastActivity_FallsBackToStartedAt(t *testing.T) {
	idx := openTestIndex(t)
	started := time.Now().Add(-2 * time.Hour).Truncate(time.Millisecond)
	if err := idx.UpsertSession(&schema.Session{SessionID: "s-idle", ToolName: "t", Status: schema.SessionActive, StartedAt: started}); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	got, err := idx.SessionLastActivity("s-idle")
	if err != nil {
		t.Fatalf("SessionLastActivity: %v", err)
	}
	if !got.Equal(started) {
		t.Errorf("last activity = %v, want started_at %v", got, started)
	}
}

func TestSessionLastActivity_UsesNewestEvent(t *testing.T) {
	idx := openTestIndex(t)
	started := time.Now().Add(-2 * time.Hour)
	if err := idx.UpsertSession(&schema.Session{SessionID: "s-busy", ToolName: "t", Status: schema.SessionActive, StartedAt: started}); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	older := time.Now().Add(-90 * time.Minute)
	newest := time.Now().Add(-5 * time.Minute)
	for i, ts := range []time.Time{older, newest} {
		e := makeEvent(string(rune('a'+i)), "f.go", schema.OpModify, "s-busy", ts)
		if err := idx.IndexEvent(e, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	got, err := idx.SessionLastActivity("s-busy")
	if err != nil {
		t.Fatalf("SessionLastActivity: %v", err)
	}
	if got.UnixNano() != newest.UnixNano() {
		t.Errorf("last activity = %v, want newest event %v", got, newest)
	}
}

func TestSessionLastActivity_UnknownSession(t *testing.T) {
	idx := openTestIndex(t)
	if _, err := idx.SessionLastActivity("nope"); err == nil {
		t.Error("expected error for unknown session")
	}
}

func TestMarkSessionEnded(t *testing.T) {
	idx := openTestIndex(t)
	if err := idx.UpsertSession(&schema.Session{SessionID: "s-end", ToolName: "t", Status: schema.SessionActive, StartedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	endedAt := time.Now()
	if err := idx.MarkSessionEnded("s-end", endedAt); err != nil {
		t.Fatalf("MarkSessionEnded: %v", err)
	}
	s, err := idx.GetSession("s-end")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if s.Status != schema.SessionEnded {
		t.Errorf("status = %s, want ended", s.Status)
	}
	if s.EndedAt.UnixNano() != endedAt.UnixNano() {
		t.Errorf("ended_at = %v, want %v", s.EndedAt, endedAt)
	}

	if err := idx.MarkSessionEnded("s-end", endedAt.Add(time.Hour)); err != nil {
		t.Fatalf("second MarkSessionEnded: %v", err)
	}
	s, _ = idx.GetSession("s-end")
	if s.EndedAt.UnixNano() != endedAt.UnixNano() {
		t.Error("MarkSessionEnded must only touch active sessions")
	}
}
