package index

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

func openAliasIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx
}

func TestSetSessionMetadata_AndLinkedSessions(t *testing.T) {
	idx := openAliasIndex(t)
	if err := idx.UpsertSession(&schema.Session{SessionID: "agent-pid-1", ToolName: "agent", PID: 1, Status: schema.SessionActive, StartedAt: time.Now()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := idx.SetSessionMetadata("agent-pid-1", "linked_session_id", "uuid-a"); err != nil {
		t.Fatalf("set metadata: %v", err)
	}
	if err := idx.SetSessionMetadata("agent-pid-2", "linked_session_id", "uuid-a"); err != nil {
		t.Fatalf("set metadata on missing session should insert: %v", err)
	}
	s, err := idx.GetSession("agent-pid-1")
	if err != nil || s == nil || s.Metadata["linked_session_id"] != "uuid-a" {
		t.Fatalf("metadata not persisted: %+v err=%v", s, err)
	}

	linked, err := idx.LinkedSessions("uuid-a")
	if err != nil {
		t.Fatalf("linked: %v", err)
	}
	if len(linked) != 2 {
		t.Fatalf("linked = %v, want 2 ids", linked)
	}
	if none, _ := idx.LinkedSessions("uuid-z"); len(none) != 0 {
		t.Fatalf("unexpected links: %v", none)
	}
}

func TestQueryEvents_ExpandsLinkedSessions(t *testing.T) {
	idx := openAliasIndex(t)
	base := time.Now().UnixNano()
	add := func(id, sid, file string) {
		base++
		ev := &schema.Event{EventID: id, TimestampNano: base, FilePath: file, Op: schema.OpModify, ContentHash: "h" + id, SessionID: sid}
		if err := idx.IndexEvent(ev, "seg", 0); err != nil {
			t.Fatalf("index: %v", err)
		}
	}
	add("e1", "agent-pid-5", "before-link.go")
	add("e2", "uuid-5", "hook.go")
	add("e3", "agent-pid-6", "other.go")

	if err := idx.SetSessionMetadata("agent-pid-5", "linked_session_id", "uuid-5"); err != nil {
		t.Fatalf("link: %v", err)
	}

	events, err := idx.QueryEvents(&Query{Sessions: []string{"uuid-5"}})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want the hook event plus the pre-link process event", len(events))
	}
	only, _ := idx.QueryEvents(&Query{Sessions: []string{"agent-pid-6"}})
	if len(only) != 1 {
		t.Fatalf("unlinked session should be unaffected, got %d", len(only))
	}
}
