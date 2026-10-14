package spool

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

func newEvent(path, session string) *schema.Event {
	e := &schema.Event{
		EventID:   schema.NewEventID(),
		Version:   schema.SchemaVersion,
		FilePath:  path,
		Op:        schema.OpModify,
		SessionID: session,
		Metadata:  map[string]string{"source": "hook"},
	}
	e.SetTimestamp(time.Now())
	return e
}

func TestWriteDrainRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	first := newEvent("a.txt", "s1")
	time.Sleep(time.Millisecond)
	second := newEvent("b.txt", "s2")
	for _, e := range []*schema.Event{second, first} {
		if err := Write(dir, e); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	var got []*schema.Event
	n, err := Drain(dir, func(e *schema.Event) { got = append(got, e) })
	if err != nil || n != 2 {
		t.Fatalf("Drain = %d, %v", n, err)
	}
	if got[0].EventID != first.EventID || got[1].EventID != second.EventID {
		t.Errorf("events not drained in time order")
	}
	if got[0].SessionID != "s1" || got[0].Op != schema.OpModify || got[0].Metadata["source"] != "hook" {
		t.Errorf("event fields lost in round trip: %+v", got[0])
	}
	if pending, _ := Pending(dir); len(pending) != 0 {
		t.Errorf("spool not empty after drain: %v", pending)
	}
}

func TestDrainQuarantinesBadFiles(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := Drain(dir, func(*schema.Event) { t.Error("bad file applied") })
	if err != nil || n != 0 {
		t.Fatalf("Drain = %d, %v", n, err)
	}
	if _, err := os.Stat(bad + ".bad"); err != nil {
		t.Errorf("bad file not quarantined: %v", err)
	}
}

func TestDrainMissingDir(t *testing.T) {
	n, err := Drain(filepath.Join(t.TempDir(), "absent"), func(*schema.Event) {})
	if err != nil || n != 0 {
		t.Fatalf("Drain = %d, %v", n, err)
	}
}
