package commands

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
)

func openCheckpointIndex(t *testing.T) *index.Index {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx
}

func writeCheckpointEvent(t *testing.T, idx *index.Index, id, label string, ts time.Time) *schema.Event {
	t.Helper()
	e := &schema.Event{
		EventID:               id,
		TimestampNano:         ts.UnixNano(),
		FilePath:              "",
		Op:                    schema.OpCheckpoint,
		Attribution:           schema.AttrManual,
		AttributionConfidence: 1.0,
		Metadata:              map[string]string{"label": label, "source": "checkpoint"},
	}
	if err := idx.IndexEvent(e, "", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}
	return e
}

func TestResolveCheckpoint_ByID(t *testing.T) {
	idx := openCheckpointIndex(t)
	e := writeCheckpointEvent(t, idx, "01900000-0000-7000-8000-000000000001", "label-one", time.Now())

	got, err := resolveCheckpoint(idx, e.EventID)
	if err != nil {
		t.Fatalf("resolveCheckpoint by id: %v", err)
	}
	if got.EventID != e.EventID {
		t.Errorf("got event %s, want %s", got.EventID, e.EventID)
	}
}

func TestResolveCheckpoint_ByLabelLatestWins(t *testing.T) {
	idx := openCheckpointIndex(t)

	older := writeCheckpointEvent(t, idx,
		"01900000-0000-7000-8000-000000000001",
		"pre-bash: rm -rf foo",
		time.Now().Add(-1*time.Hour))

	newer := writeCheckpointEvent(t, idx,
		"01900000-0000-7000-8000-000000000002",
		"pre-bash: rm -rf foo",
		time.Now())

	got, err := resolveCheckpoint(idx, "pre-bash: rm -rf foo")
	if err != nil {
		t.Fatalf("resolveCheckpoint by label: %v", err)
	}
	if got.EventID != newer.EventID {
		t.Errorf("expected latest checkpoint %s, got %s (older was %s)", newer.EventID, got.EventID, older.EventID)
	}
}

func TestResolveCheckpoint_UnknownRef(t *testing.T) {
	idx := openCheckpointIndex(t)
	writeCheckpointEvent(t, idx, "01900000-0000-7000-8000-000000000001", "exists", time.Now())

	_, err := resolveCheckpoint(idx, "does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown ref")
	}
}

func TestResolveCheckpoint_IDMustBeCheckpoint(t *testing.T) {
	idx := openCheckpointIndex(t)

	fileEvent := &schema.Event{
		EventID:       "01900000-0000-7000-8000-000000000099",
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "foo.txt",
		Op:            schema.OpModify,
		ContentHash:   "abc",
	}
	if err := idx.IndexEvent(fileEvent, "", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}

	_, err := resolveCheckpoint(idx, fileEvent.EventID)
	if err == nil {
		t.Fatal("resolveCheckpoint should reject non-checkpoint event IDs")
	}
}
