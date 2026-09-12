package index

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/eventlog"
	"github.com/davidparkercodes/belay/internal/schema"
)

func segEvent(id, path string, op schema.Operation, ts time.Time) *schema.Event {
	e := &schema.Event{
		EventID:     id,
		Version:     schema.SchemaVersion,
		FilePath:    path,
		Op:          op,
		ContentHash: "hash-" + id,
		SessionID:   "s1",
	}
	e.SetTimestamp(ts)
	return e
}

// touchSegment creates a fresh empty segment so the next NewWriter seals the previous one.
func touchSegment(t *testing.T, eventsDir string) {
	t.Helper()
	time.Sleep(1100 * time.Millisecond)
	name := time.Now().Format("20060102-150405") + ".log"
	f, err := os.OpenFile(filepath.Join(eventsDir, name), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
	if err != nil {
		t.Fatalf("create segment: %v", err)
	}
	f.Close()
}

// writeSegments writes perSeg events into each of segCount segments and indexes them all.
func writeSegments(t *testing.T, dir string, idx *Index, segCount, perSeg int) []string {
	t.Helper()
	eventsDir := filepath.Join(dir, "events")
	var ids []string
	base := time.Now().Add(-time.Hour)
	for s := 0; s < segCount; s++ {
		if s > 0 {
			touchSegment(t, eventsDir)
		}
		w, err := eventlog.NewWriter(eventsDir, 1<<30)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		for i := 0; i < perSeg; i++ {
			id := fmt.Sprintf("seg%d-ev%d", s, i)
			e := segEvent(id, fmt.Sprintf("file%d.go", i), schema.OpModify, base.Add(time.Duration(s*perSeg+i)*time.Second))
			if err := w.Append(e); err != nil {
				t.Fatalf("Append: %v", err)
			}
			if err := idx.IndexEvent(e, w.CurrentSegment(), w.CurrentOffset()); err != nil {
				t.Fatalf("IndexEvent: %v", err)
			}
			ids = append(ids, id)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	return ids
}

func TestCompactSegments_RewritesAndDeletesSealed(t *testing.T) {
	dir := t.TempDir()
	idx, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	eventsDir := filepath.Join(dir, "events")
	writeSegments(t, dir, idx, 3, 4)

	segs, _ := listSegmentFiles(eventsDir)
	if len(segs) != 3 {
		t.Fatalf("expected 3 segments, got %d: %v", len(segs), segs)
	}

	// Segment 0: delete every event -> file should be removed.
	// Segment 1: delete half -> file rewritten.
	// Segment 2 (active): delete half -> must be untouched.
	var del []string
	for i := 0; i < 4; i++ {
		del = append(del, fmt.Sprintf("seg0-ev%d", i))
	}
	del = append(del, "seg1-ev0", "seg1-ev1", "seg2-ev0", "seg2-ev1")
	if _, err := idx.DeleteEventsBatch(del); err != nil {
		t.Fatalf("DeleteEventsBatch: %v", err)
	}

	activeBefore, _ := os.ReadFile(filepath.Join(eventsDir, segs[2]))

	res, err := CompactSegments(idx, eventsDir, false, nil)
	if err != nil {
		t.Fatalf("CompactSegments: %v", err)
	}
	if res.SegmentsDeleted != 1 || res.SegmentsRewritten != 1 {
		t.Errorf("deleted=%d rewritten=%d, want 1/1", res.SegmentsDeleted, res.SegmentsRewritten)
	}
	if res.FramesDropped != 6 || res.FramesKept != 2 {
		t.Errorf("dropped=%d kept=%d, want 6/2", res.FramesDropped, res.FramesKept)
	}
	if res.BytesFreed <= 0 {
		t.Error("expected bytes freed")
	}

	if _, err := os.Stat(filepath.Join(eventsDir, segs[0])); !os.IsNotExist(err) {
		t.Error("fully-purged sealed segment should be deleted")
	}

	activeAfter, _ := os.ReadFile(filepath.Join(eventsDir, segs[2]))
	if string(activeBefore) != string(activeAfter) {
		t.Error("active segment must never be rewritten")
	}

	r, _ := eventlog.NewReader(eventsDir)
	kept, _ := r.ReadSegment(segs[1])
	if len(kept) != 2 || kept[0].EventID != "seg1-ev2" || kept[1].EventID != "seg1-ev3" {
		t.Errorf("rewritten segment content wrong: %+v", kept)
	}

	withOffsets, _ := r.ReadSegmentWithOffsets(segs[1])
	for _, eo := range withOffsets {
		var off int64
		var file string
		err := idx.db.QueryRow("SELECT segment_file, segment_offset FROM events WHERE event_id = ?", eo.Event.EventID).Scan(&file, &off)
		if err != nil {
			t.Fatalf("query offset: %v", err)
		}
		if file != segs[1] || off != eo.Offset {
			t.Errorf("%s offset = %s:%d, want %s:%d", eo.Event.EventID, file, off, segs[1], eo.Offset)
		}
	}
}

func TestCompactSegments_PurgeSurvivesRebuild(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.db")
	idx, err := Open(indexPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	eventsDir := filepath.Join(dir, "events")
	ids := writeSegments(t, dir, idx, 2, 5)

	if _, err := idx.DeleteEventsBatch([]string{"seg0-ev1", "seg0-ev2", "seg0-ev3"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := CompactSegments(idx, eventsDir, false, nil); err != nil {
		t.Fatalf("CompactSegments: %v", err)
	}
	idx.Close()

	logger := log.New(io.Discard, "", 0)
	res, err := Rebuild(indexPath, eventsDir, logger)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if res.EventsIndexed != len(ids)-3 {
		t.Errorf("rebuild indexed %d events, want %d (purged events must not resurrect)", res.EventsIndexed, len(ids)-3)
	}

	idx2, err := Open(indexPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer idx2.Close()
	if _, err := idx2.GetEvent("seg0-ev2"); err == nil {
		t.Error("purged event resurrected by rebuild")
	}
	if _, err := idx2.GetEvent("seg0-ev0"); err != nil {
		t.Error("kept event lost after rebuild")
	}
}

func TestCompactSegments_KeepsSessionMetaEvents(t *testing.T) {
	dir := t.TempDir()
	idx, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()
	eventsDir := filepath.Join(dir, "events")

	w, err := eventlog.NewWriter(eventsDir, 1<<30)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	meta := segEvent("meta-1", ".belay/sessions", schema.OpCreate, time.Now())
	meta.Metadata = map[string]string{"event_type": "session_start", "tool_name": "claude-code"}
	if err := w.Append(meta); err != nil {
		t.Fatalf("Append meta: %v", err)
	}
	e := segEvent("real-1", "a.go", schema.OpModify, time.Now())
	_ = w.Append(e)
	_ = idx.IndexEvent(e, w.CurrentSegment(), w.CurrentOffset())
	w.Close()
	touchSegment(t, eventsDir)

	if _, err := idx.DeleteEventsBatch([]string{"real-1"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	res, err := CompactSegments(idx, eventsDir, false, nil)
	if err != nil {
		t.Fatalf("CompactSegments: %v", err)
	}
	if res.SegmentsDeleted != 0 || res.FramesKept != 1 {
		t.Errorf("deleted=%d kept=%d, want 0/1 (session meta-event must survive)", res.SegmentsDeleted, res.FramesKept)
	}
}

func TestCompactSegments_DryRunTouchesNothing(t *testing.T) {
	dir := t.TempDir()
	idx, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()
	eventsDir := filepath.Join(dir, "events")
	writeSegments(t, dir, idx, 2, 3)
	if _, err := idx.DeleteEventsBatch([]string{"seg0-ev0", "seg0-ev1", "seg0-ev2"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	segs, _ := listSegmentFiles(eventsDir)
	before, _ := os.ReadFile(filepath.Join(eventsDir, segs[0]))

	res, err := CompactSegments(idx, eventsDir, true, nil)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.FramesDropped != 3 || res.BytesFreed != int64(len(before)) {
		t.Errorf("dry-run report: dropped=%d freed=%d, want 3/%d", res.FramesDropped, res.BytesFreed, len(before))
	}
	after, err := os.ReadFile(filepath.Join(eventsDir, segs[0]))
	if err != nil || string(before) != string(after) {
		t.Error("dry run must not modify or delete segments")
	}
}

func TestCompactSegments_SingleSegmentIsNoop(t *testing.T) {
	dir := t.TempDir()
	idx, _ := Open(filepath.Join(dir, "index.db"))
	defer idx.Close()
	eventsDir := filepath.Join(dir, "events")
	writeSegments(t, dir, idx, 1, 3)
	res, err := CompactSegments(idx, eventsDir, false, nil)
	if err != nil {
		t.Fatalf("CompactSegments: %v", err)
	}
	if res.SegmentsScanned != 0 {
		t.Errorf("scanned %d, want 0 (only the active segment exists)", res.SegmentsScanned)
	}
}

func TestMeta_RoundTripAndVacuum(t *testing.T) {
	dir := t.TempDir()
	idx, _ := Open(filepath.Join(dir, "index.db"))
	defer idx.Close()

	if v, _ := idx.GetMeta("missing"); v != "" {
		t.Errorf("missing key = %q, want empty", v)
	}
	if err := idx.SetMeta(MetaLastCompactionAt, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if err := idx.SetMeta(MetaLastCompactionAt, "2026-02-01T00:00:00Z"); err != nil {
		t.Fatalf("SetMeta upsert: %v", err)
	}
	if v, _ := idx.GetMeta(MetaLastCompactionAt); v != "2026-02-01T00:00:00Z" {
		t.Errorf("GetMeta = %q", v)
	}
	ran, err := idx.MaybeVacuum()
	if err != nil {
		t.Fatalf("MaybeVacuum: %v", err)
	}
	if ran {
		t.Error("vacuum should be skipped on a tiny database")
	}
}

func TestCompactSegments_SkipsCleanSegmentsOnSecondPass(t *testing.T) {
	dir := t.TempDir()
	idx, _ := Open(filepath.Join(dir, "index.db"))
	defer idx.Close()
	eventsDir := filepath.Join(dir, "events")
	writeSegments(t, dir, idx, 2, 3)
	if _, err := idx.DeleteEventsBatch([]string{"seg0-ev0"}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	first, err := CompactSegments(idx, eventsDir, false, nil)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if first.SegmentsRewritten != 1 {
		t.Fatalf("first pass rewritten=%d, want 1", first.SegmentsRewritten)
	}

	second, err := CompactSegments(idx, eventsDir, false, nil)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if second.SegmentsScanned != 0 {
		t.Errorf("second pass scanned %d segments, want 0 (fingerprint unchanged)", second.SegmentsScanned)
	}

	if _, err := idx.DeleteEventsBatch([]string{"seg0-ev1"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	third, err := CompactSegments(idx, eventsDir, false, nil)
	if err != nil {
		t.Fatalf("third pass: %v", err)
	}
	if third.SegmentsRewritten != 1 || third.FramesDropped != 1 {
		t.Errorf("third pass rewritten=%d dropped=%d, want 1/1 after a new deletion", third.SegmentsRewritten, third.FramesDropped)
	}
}
