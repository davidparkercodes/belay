package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/spool"
)

func TestDrainSpoolIndexesQueuedEvents(t *testing.T) {
	d, cfg := e2eSubsystems(t)

	content := []byte("queued while daemon was down\n")
	if err := os.WriteFile(filepath.Join(cfg.ProjectRoot, "routes.ts"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	hash, size, err := d.objStore.Put(content)
	if err != nil {
		t.Fatal(err)
	}

	event := &schema.Event{
		EventID:               schema.NewEventID(),
		Version:               schema.SchemaVersion,
		FilePath:              "routes.ts",
		Op:                    schema.OpModify,
		ContentHash:           hash,
		ContentSize:           size,
		SessionID:             "agent-session",
		Attribution:           schema.AttrHook,
		AttributionConfidence: 1.0,
		Metadata:              map[string]string{"source": "hook", "tool_name": "claude-code"},
	}
	event.SetTimestamp(time.Now())
	if err := spool.Write(cfg.SpoolDir(), event); err != nil {
		t.Fatal(err)
	}

	d.drainSpool()

	got, err := d.idx.GetEvent(event.EventID)
	if err != nil || got == nil {
		t.Fatalf("spooled event not indexed: %v", err)
	}
	if got.SessionID != "agent-session" || got.ContentHash != hash {
		t.Errorf("indexed event = %+v", got)
	}
	if pending, _ := spool.Pending(cfg.SpoolDir()); len(pending) != 0 {
		t.Errorf("spool not emptied: %v", pending)
	}
}
