package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
)

// newTestCompactorEnv creates a temporary index, object store, and retention config for testing.
func newTestCompactorEnv(t *testing.T) (*index.Index, *Store, *config.RetentionConfig) {
	t.Helper()
	dir := t.TempDir()

	idx, err := index.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { idx.Close() })

	objStore, err := NewStore(filepath.Join(dir, "objects"), false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { objStore.Close() })

	retention := &config.RetentionConfig{
		HotHours:     24,
		WarmDays:     7,
		ColdDays:     30,
		ArchiveDays:  365,
		MaxStorageGB: 10,
	}

	return idx, objStore, retention
}

// insertEvent is a test helper that inserts an event into the index.
func insertEvent(t *testing.T, idx *index.Index, id string, ts time.Time, filePath, sessionID string, op schema.Operation, contentHash, previousHash string) {
	t.Helper()
	e := &schema.Event{
		EventID:       id,
		TimestampNano: ts.UnixNano(),
		FilePath:      filePath,
		Op:            op,
		ContentHash:   contentHash,
		PreviousHash:  previousHash,
		SessionID:     sessionID,
		ContentSize:   100,
	}
	if err := idx.IndexEvent(e, "seg.log", 0); err != nil {
		t.Fatalf("IndexEvent(%s): %v", id, err)
	}
}

func TestCompactor_Purge(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()

	// Insert events: 2 recent, 1 beyond archive_days
	retention.ArchiveDays = 90
	insertEvent(t, idx, "recent-1", now.Add(-1*time.Hour), "file.go", "s1", schema.OpModify, "h1", "")
	insertEvent(t, idx, "recent-2", now.Add(-10*24*time.Hour), "file.go", "s1", schema.OpModify, "h2", "h1")
	insertEvent(t, idx, "expired-1", now.Add(-100*24*time.Hour), "file.go", "s1", schema.OpModify, "h3", "h2")

	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now

	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	if result.EventsRemoved < 1 {
		t.Errorf("expected at least 1 event removed (purged), got %d", result.EventsRemoved)
	}

	// The expired event should be gone
	_, err = idx.GetEvent("expired-1")
	if err == nil {
		t.Error("expired event should have been purged")
	}

	// Recent events should remain
	_, err = idx.GetEvent("recent-1")
	if err != nil {
		t.Error("recent event should not have been purged")
	}
}

func TestCompactor_Purge_SkippedWhenZero(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	retention.ArchiveDays = 0 // retain forever

	insertEvent(t, idx, "old-1", now.Add(-1000*24*time.Hour), "file.go", "s1", schema.OpModify, "h1", "")

	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now

	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	// Should not purge anything since archive_days=0
	_, err = idx.GetEvent("old-1")
	if err != nil {
		t.Errorf("event should be retained when archive_days=0: %v", err)
	}
	_ = result
}

func TestCompactor_ArchiveCompaction(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	retention.ColdDays = 7
	retention.ArchiveDays = 365

	// Insert 5 events for the same file on the same day, all in archive tier (60 days ago)
	baseTime := now.Add(-60 * 24 * time.Hour)
	for i := 0; i < 5; i++ {
		ts := baseTime.Add(time.Duration(i) * time.Minute)
		insertEvent(t, idx, fmt.Sprintf("arch-%d", i), ts, "file.go", "s1",
			schema.OpModify, fmt.Sprintf("h%d", i), fmt.Sprintf("h%d", i-1))
	}

	// Insert 2 events on a different day (61 days ago)
	otherDay := now.Add(-61 * 24 * time.Hour)
	insertEvent(t, idx, "arch-other-0", otherDay, "file.go", "s1", schema.OpModify, "hx0", "")
	insertEvent(t, idx, "arch-other-1", otherDay.Add(5*time.Minute), "file.go", "s1", schema.OpModify, "hx1", "hx0")

	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now

	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	// 5 events on day 1 -> keep 1 (last), remove 4
	// 2 events on day 2 -> keep 1 (last), remove 1
	// Total removed from archive: 5
	if result.TierBreakdown["archive_compacted"] != 5 {
		t.Errorf("archive_compacted = %d, want 5", result.TierBreakdown["archive_compacted"])
	}

	// The last event of each day should survive
	_, err = idx.GetEvent("arch-4")
	if err != nil {
		t.Error("last event of day should be kept")
	}
	_, err = idx.GetEvent("arch-other-1")
	if err != nil {
		t.Error("last event of other day should be kept")
	}

	// An intermediate event should be gone
	_, err = idx.GetEvent("arch-2")
	if err == nil {
		t.Error("intermediate archive event should have been removed")
	}
}

func TestCompactor_ColdCompaction(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	retention.WarmDays = 7
	retention.ColdDays = 30

	// Insert 5 events for the same file+session, in cold tier (15 days ago)
	baseTime := now.Add(-15 * 24 * time.Hour)
	for i := 0; i < 5; i++ {
		ts := baseTime.Add(time.Duration(i) * time.Hour)
		insertEvent(t, idx, fmt.Sprintf("cold-%d", i), ts, "file.go", "session-1",
			schema.OpModify, fmt.Sprintf("c%d", i), fmt.Sprintf("c%d", i-1))
	}

	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now

	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	// 5 events -> keep first and last (2), remove 3
	if result.TierBreakdown["cold_compacted"] != 3 {
		t.Errorf("cold_compacted = %d, want 3", result.TierBreakdown["cold_compacted"])
	}

	// First event should survive
	_, err = idx.GetEvent("cold-0")
	if err != nil {
		t.Error("first event should be kept (session boundary)")
	}

	// Last event should survive
	_, err = idx.GetEvent("cold-4")
	if err != nil {
		t.Error("last event should be kept (session boundary)")
	}

	// Middle events should be gone
	_, err = idx.GetEvent("cold-2")
	if err == nil {
		t.Error("intermediate cold event should have been removed")
	}
}

func TestCompactor_ColdCompaction_TwoEventsKept(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	retention.WarmDays = 7
	retention.ColdDays = 30

	// Insert exactly 2 events -- both should be kept
	baseTime := now.Add(-15 * 24 * time.Hour)
	insertEvent(t, idx, "cold-first", baseTime, "file.go", "s1", schema.OpModify, "c1", "")
	insertEvent(t, idx, "cold-last", baseTime.Add(time.Hour), "file.go", "s1", schema.OpModify, "c2", "c1")

	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now

	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	if result.TierBreakdown["cold_compacted"] != 0 {
		t.Errorf("cold_compacted = %d, want 0 (only 2 events)", result.TierBreakdown["cold_compacted"])
	}
}

func TestCompactor_WarmCompaction_RapidEdits(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	retention.HotHours = 24
	retention.WarmDays = 7

	// Insert a burst of rapid modify events (3 days ago, within warm tier)
	baseTime := now.Add(-3 * 24 * time.Hour)

	// Burst of 5 events, each 10 seconds apart
	for i := 0; i < 5; i++ {
		ts := baseTime.Add(time.Duration(i) * 10 * time.Second)
		insertEvent(t, idx, fmt.Sprintf("warm-%d", i), ts, "file.go", "s1",
			schema.OpModify, fmt.Sprintf("w%d", i), fmt.Sprintf("w%d", i-1))
	}

	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now

	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	if result.TierBreakdown["warm_compacted"] != 4 {
		t.Errorf("warm_compacted = %d, want 4 (one modify per hour survives)", result.TierBreakdown["warm_compacted"])
	}

	_, err = idx.GetEvent("warm-4")
	if err != nil {
		t.Error("last event in burst should be kept")
	}

	_, err = idx.GetEvent("warm-2")
	if err == nil {
		t.Error("intermediate modify within the hour should have been removed")
	}
}

func TestCompactor_WarmCompaction_SpreadAcrossHours(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	retention.HotHours = 24
	retention.WarmDays = 7

	baseTime := now.Add(-3 * 24 * time.Hour).Truncate(time.Hour)
	for i := 0; i < 3; i++ {
		ts := baseTime.Add(time.Duration(i) * 2 * time.Hour)
		insertEvent(t, idx, fmt.Sprintf("warm-spread-%d", i), ts, "file.go", "s1",
			schema.OpModify, fmt.Sprintf("ws%d", i), fmt.Sprintf("ws%d", i-1))
	}

	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now

	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	if result.TierBreakdown["warm_compacted"] != 0 {
		t.Errorf("warm_compacted = %d, want 0 (one modify per hour already)", result.TierBreakdown["warm_compacted"])
	}
}

func TestCompactor_WarmCompaction_MixedOperations(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	retention.HotHours = 24
	retention.WarmDays = 7

	baseTime := now.Add(-3 * 24 * time.Hour).Truncate(time.Hour)

	insertEvent(t, idx, "wm-0", baseTime, "file.go", "s1", schema.OpModify, "a1", "")
	insertEvent(t, idx, "wm-1", baseTime.Add(10*time.Second), "file.go", "s1", schema.OpModify, "a2", "a1")
	insertEvent(t, idx, "wm-2", baseTime.Add(20*time.Second), "file.go", "s1", schema.OpCreate, "a3", "")
	insertEvent(t, idx, "wm-3", baseTime.Add(30*time.Second), "file.go", "s1", schema.OpModify, "a4", "a3")
	insertEvent(t, idx, "wm-4", baseTime.Add(40*time.Second), "file.go", "s1", schema.OpModify, "a5", "a4")

	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now

	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	if result.TierBreakdown["warm_compacted"] != 3 {
		t.Errorf("warm_compacted = %d, want 3 (last modify of the hour + the create survive)", result.TierBreakdown["warm_compacted"])
	}
	if _, err := idx.GetEvent("wm-2"); err != nil {
		t.Error("create event must never be collapsed")
	}
	if _, err := idx.GetEvent("wm-4"); err != nil {
		t.Error("last modify of the hour should be kept")
	}
}

func TestCompactor_MaxVersionsPerFile(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	retention.MaxVersionsPerFile = 3
	retention.WarmDays = 0
	retention.ColdDays = 0

	for i := 0; i < 10; i++ {
		ts := now.Add(-time.Duration(30-i) * 24 * time.Hour)
		insertEvent(t, idx, fmt.Sprintf("v-%d", i), ts, "churn.log", fmt.Sprintf("s%d", i),
			schema.OpModify, fmt.Sprintf("vh%d", i), "")
	}
	insertEvent(t, idx, "v-hot", now.Add(-time.Hour), "churn.log", "s-hot", schema.OpModify, "vh-hot", "")
	insertEvent(t, idx, "v-create", now.Add(-40*24*time.Hour), "churn.log", "s0", schema.OpCreate, "vh-c", "")

	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now

	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	if result.TierBreakdown["version_capped"] != 7 {
		t.Errorf("version_capped = %d, want 7", result.TierBreakdown["version_capped"])
	}
	for _, keep := range []string{"v-9", "v-8", "v-7", "v-hot", "v-create"} {
		if _, err := idx.GetEvent(keep); err != nil {
			t.Errorf("%s should be kept", keep)
		}
	}
	if _, err := idx.GetEvent("v-0"); err == nil {
		t.Error("oldest version beyond the cap should be removed")
	}
}

func TestCompactor_MaxVersionsPerFile_Disabled(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	retention.MaxVersionsPerFile = 0
	for i := 0; i < 5; i++ {
		insertEvent(t, idx, fmt.Sprintf("v-%d", i), now.Add(-time.Duration(10+i)*24*time.Hour), "f.go", fmt.Sprintf("s%d", i),
			schema.OpModify, fmt.Sprintf("h%d", i), "")
	}
	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now
	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}
	if result.TierBreakdown["version_capped"] != 0 {
		t.Errorf("version_capped = %d, want 0 when disabled", result.TierBreakdown["version_capped"])
	}
}

func TestCompactor_CheckpointsNeverCollapsed(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	baseTime := now.Add(-15 * 24 * time.Hour).Truncate(time.Hour)
	for i := 0; i < 5; i++ {
		insertEvent(t, idx, fmt.Sprintf("cp-%d", i), baseTime.Add(time.Duration(i)*time.Minute), "", "s1",
			schema.OpCheckpoint, "", "")
	}

	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now
	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}
	if result.EventsRemoved != 0 {
		t.Errorf("EventsRemoved = %d, want 0 (checkpoints are never compacted)", result.EventsRemoved)
	}
}

func TestCompactor_VersionCapOrphansObjects(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	retention.MaxVersionsPerFile = 1
	retention.WarmDays = 0
	retention.ColdDays = 0

	var hashes []string
	for i := 0; i < 4; i++ {
		h, _, err := objStore.Put([]byte(fmt.Sprintf("version %d", i)))
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		hashes = append(hashes, h)
		insertEvent(t, idx, fmt.Sprintf("ov-%d", i), now.Add(-time.Duration(10-i)*24*time.Hour), "big.bin", fmt.Sprintf("s%d", i),
			schema.OpModify, h, "")
	}

	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now
	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}
	if result.ObjectsFreed != 3 {
		t.Errorf("ObjectsFreed = %d, want 3", result.ObjectsFreed)
	}
	if !objStore.Has(hashes[3]) {
		t.Error("newest version blob must survive")
	}
	if objStore.Has(hashes[0]) {
		t.Error("capped version blob should have been garbage collected")
	}
}

func TestHourlyCollapse(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	events := []*schema.Event{
		{EventID: "a", TimestampNano: base.UnixNano(), Op: schema.OpModify},
		{EventID: "b", TimestampNano: base.Add(5 * time.Minute).UnixNano(), Op: schema.OpModify},
		{EventID: "c", TimestampNano: base.Add(50 * time.Minute).UnixNano(), Op: schema.OpDelete},
		{EventID: "d", TimestampNano: base.Add(55 * time.Minute).UnixNano(), Op: schema.OpModify},
		{EventID: "e", TimestampNano: base.Add(65 * time.Minute).UnixNano(), Op: schema.OpModify},
	}
	got := hourlyCollapse(events)
	want := map[string]bool{"a": true, "b": true}
	if len(got) != len(want) {
		t.Fatalf("hourlyCollapse = %v, want a,b", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected removal %s", id)
		}
	}
	if hourlyCollapse(nil) != nil || hourlyCollapse(events[:1]) != nil {
		t.Error("nil/single input must return nil")
	}
}

func TestCompactor_DryRun(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	retention.ArchiveDays = 90

	// Insert an expired event
	insertEvent(t, idx, "expired-1", now.Add(-100*24*time.Hour), "file.go", "s1", schema.OpModify, "h1", "")

	compactor := NewCompactor(idx, objStore, retention, true) // dry-run mode
	compactor.now = now

	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction (dry-run): %v", err)
	}

	// Should report removal but not actually delete
	if result.EventsRemoved == 0 {
		t.Error("dry-run should still report events that would be removed")
	}

	// Event should still exist
	_, err = idx.GetEvent("expired-1")
	if err != nil {
		t.Error("dry-run should not actually delete events")
	}
}

func TestCompactor_RunCompaction_NoEvents(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	compactor := NewCompactor(idx, objStore, retention, false)

	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	if result.EventsReviewed != 0 {
		t.Errorf("EventsReviewed = %d, want 0", result.EventsReviewed)
	}
	if result.EventsRemoved != 0 {
		t.Errorf("EventsRemoved = %d, want 0", result.EventsRemoved)
	}
}

func TestCompactor_GCOrphanedObjects(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	retention.ArchiveDays = 90

	// Store an object and reference it from an event
	hash, _, err := objStore.Put([]byte("kept content"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	insertEvent(t, idx, "evt-1", now.Add(-1*time.Hour), "file.go", "s1", schema.OpModify, hash, "")

	// Store an orphaned object (not referenced by any event)
	orphanHash, _, err := objStore.Put([]byte("orphaned content"))
	if err != nil {
		t.Fatalf("Put orphan: %v", err)
	}

	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now

	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	if result.BytesFreed == 0 {
		t.Error("should have freed bytes from orphaned object")
	}

	// Referenced object should still exist
	if !objStore.Has(hash) {
		t.Error("referenced object should not be deleted")
	}

	// Orphaned object should be gone
	if objStore.Has(orphanHash) {
		t.Error("orphaned object should have been deleted")
	}
}

func TestCompactor_ColdCompaction_MultipleSessionsSameFile(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	retention.WarmDays = 7
	retention.ColdDays = 30

	baseTime := now.Add(-15 * 24 * time.Hour)

	// Session A: 4 events
	for i := 0; i < 4; i++ {
		ts := baseTime.Add(time.Duration(i) * time.Hour)
		insertEvent(t, idx, fmt.Sprintf("sa-%d", i), ts, "file.go", "session-A",
			schema.OpModify, fmt.Sprintf("sa-h%d", i), "")
	}

	// Session B: 3 events on the same file
	for i := 0; i < 3; i++ {
		ts := baseTime.Add(time.Duration(i)*time.Hour + 30*time.Minute)
		insertEvent(t, idx, fmt.Sprintf("sb-%d", i), ts, "file.go", "session-B",
			schema.OpModify, fmt.Sprintf("sb-h%d", i), "")
	}

	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now

	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	// Session A: 4 events -> keep first+last, remove 2
	// Session B: 3 events -> keep first+last, remove 1
	// Total: 3 removed
	if result.TierBreakdown["cold_compacted"] != 3 {
		t.Errorf("cold_compacted = %d, want 3", result.TierBreakdown["cold_compacted"])
	}

	// Verify session A boundaries
	_, err = idx.GetEvent("sa-0")
	if err != nil {
		t.Error("session A first event should be kept")
	}
	_, err = idx.GetEvent("sa-3")
	if err != nil {
		t.Error("session A last event should be kept")
	}

	// Verify session B boundaries
	_, err = idx.GetEvent("sb-0")
	if err != nil {
		t.Error("session B first event should be kept")
	}
	_, err = idx.GetEvent("sb-2")
	if err != nil {
		t.Error("session B last event should be kept")
	}
}

func TestCompactor_FullPipeline(t *testing.T) {
	idx, objStore, retention := newTestCompactorEnv(t)

	now := time.Now()
	retention.HotHours = 24
	retention.WarmDays = 7
	retention.ColdDays = 30
	retention.ArchiveDays = 90

	// Hot tier: 1 hour ago (should be untouched)
	insertEvent(t, idx, "hot-1", now.Add(-1*time.Hour), "file-hot.go", "s1", schema.OpModify, "hot1", "")

	// Warm tier: rapid edits 3 days ago (should collapse intermediates)
	warmBase := now.Add(-3 * 24 * time.Hour)
	for i := 0; i < 4; i++ {
		insertEvent(t, idx, fmt.Sprintf("warm-%d", i), warmBase.Add(time.Duration(i)*10*time.Second),
			"file-warm.go", "s1", schema.OpModify, fmt.Sprintf("w%d", i), fmt.Sprintf("w%d", i-1))
	}

	// Cold tier: 15 days ago, 4 events (should keep first+last)
	coldBase := now.Add(-15 * 24 * time.Hour)
	for i := 0; i < 4; i++ {
		insertEvent(t, idx, fmt.Sprintf("cold-%d", i), coldBase.Add(time.Duration(i)*time.Hour),
			"file-cold.go", "s1", schema.OpModify, fmt.Sprintf("c%d", i), "")
	}

	// Archive tier: 60 days ago, 3 events on same day (should keep last)
	archBase := now.Add(-60 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		insertEvent(t, idx, fmt.Sprintf("arch-%d", i), archBase.Add(time.Duration(i)*time.Minute),
			"file-arch.go", "s1", schema.OpModify, fmt.Sprintf("a%d", i), "")
	}

	// Expired: 100 days ago (should be purged)
	insertEvent(t, idx, "expired-1", now.Add(-100*24*time.Hour), "file-exp.go", "s1", schema.OpModify, "exp1", "")

	compactor := NewCompactor(idx, objStore, retention, false)
	compactor.now = now

	result, err := compactor.RunCompaction()
	if err != nil {
		t.Fatalf("RunCompaction: %v", err)
	}

	// Verify hot event is untouched
	_, err = idx.GetEvent("hot-1")
	if err != nil {
		t.Error("hot event should be untouched")
	}

	// Verify expired event is purged
	_, err = idx.GetEvent("expired-1")
	if err == nil {
		t.Error("expired event should be purged")
	}

	// Verify some events were removed overall
	if result.EventsRemoved == 0 {
		t.Error("expected some events to be removed")
	}

	// Verify result integrity
	if result.EventsKept+result.EventsRemoved != result.EventsReviewed {
		t.Errorf("kept (%d) + removed (%d) != reviewed (%d)",
			result.EventsKept, result.EventsRemoved, result.EventsReviewed)
	}
}
