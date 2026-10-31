package session

import (
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

type aliasDetector struct {
	sessions []*DetectedSession
}

func (d *aliasDetector) Name() string                        { return "alias-test" }
func (d *aliasDetector) Detect() ([]*DetectedSession, error) { return d.sessions, nil }
func (d *aliasDetector) Identify(pid int) (*DetectedSession, error) {
	return nil, nil
}
func (d *aliasDetector) Attribute(event *FileWriteEvent, active []*DetectedSession) (string, float32, schema.AttributionMethod) {
	for _, s := range active {
		if s.PID == event.WriterPID {
			return s.SessionID, 0.95, schema.AttrPID
		}
	}
	return "", 0, schema.AttrNone
}

func TestLinkAlias_RewritesAttributionForTrackedProcessSession(t *testing.T) {
	det := &aliasDetector{sessions: []*DetectedSession{{
		SessionID: "agent-pid-42", ToolName: "agent", PID: 42, WorkingDirectory: "/p", StartedAt: time.Now(),
	}}}
	r := NewRegistry(det)
	r.poll()

	before, _, _ := r.Attribute(&FileWriteEvent{FilePath: "a.go", WriterPID: 42})
	if before != "agent-pid-42" {
		t.Fatalf("before link: got %q", before)
	}

	links := r.LinkAlias(42, "uuid-1", "agent")
	if len(links) != 1 || links[0].ProcessSession != "agent-pid-42" || links[0].SessionID != "uuid-1" {
		t.Fatalf("unexpected links: %+v", links)
	}

	after, conf, method := r.Attribute(&FileWriteEvent{FilePath: "a.go", WriterPID: 42})
	if after != "uuid-1" {
		t.Fatalf("after link: got %q, want uuid-1", after)
	}
	if method != schema.AttrPID || conf < 0.9 {
		t.Fatalf("link must keep the underlying method/confidence, got %v/%v", method, conf)
	}
	if got := r.GetSession("agent-pid-42").Metadata[LinkedSessionKey]; got != "uuid-1" {
		t.Fatalf("process session metadata not updated: %q", got)
	}
	if again := r.LinkAlias(42, "uuid-1", "agent"); len(again) != 0 {
		t.Fatalf("relinking the same pair should be a no-op, got %+v", again)
	}
}

func TestLinkAlias_AppliesToSessionsDetectedLater(t *testing.T) {
	det := &aliasDetector{}
	r := NewRegistry(det)

	if links := r.LinkAlias(7, "uuid-7", "agent"); len(links) != 0 {
		t.Fatalf("nothing tracked yet, got %+v", links)
	}

	det.sessions = []*DetectedSession{{SessionID: "agent-pid-7", ToolName: "agent", PID: 7, StartedAt: time.Now()}}
	r.poll()

	got, _, _ := r.Attribute(&FileWriteEvent{FilePath: "b.go", WriterPID: 7})
	if got != "uuid-7" {
		t.Fatalf("session detected after the link should resolve to uuid-7, got %q", got)
	}
}

func TestSeedAliases_RestoresLinksFromPersistedMetadata(t *testing.T) {
	det := &aliasDetector{sessions: []*DetectedSession{{SessionID: "agent-pid-9", ToolName: "agent", PID: 9, StartedAt: time.Now()}}}
	r := NewRegistry(det)
	r.SeedAliases([]*schema.Session{{
		SessionID: "agent-pid-9", PID: 9, Metadata: map[string]string{LinkedSessionKey: "uuid-9"},
	}})
	r.poll()

	if got := r.ResolveAlias("agent-pid-9"); got != "uuid-9" {
		t.Fatalf("ResolveAlias = %q", got)
	}
	got, _, _ := r.Attribute(&FileWriteEvent{FilePath: "c.go", WriterPID: 9})
	if got != "uuid-9" {
		t.Fatalf("seeded alias not applied to attribution, got %q", got)
	}
}

func TestResolveAlias_UnknownPassesThrough(t *testing.T) {
	r := NewRegistry()
	if got := r.ResolveAlias("whatever"); got != "whatever" {
		t.Fatalf("got %q", got)
	}
	if _, ok := r.AliasFor("whatever"); ok {
		t.Fatal("no alias expected")
	}
}
