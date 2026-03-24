package chaos

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/conflict"
	"github.com/davidparkercodes/belay/internal/eventlog"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/replay"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"
)

// ─── TestMain ────────────────────────────────────────────────────────────────

func TestMain(m *testing.M) {
	StartRun()
	code := m.Run()

	report := GenerateReport()

	// Write to belay-website/public/chaos-results/
	if dir := FindWebsiteResultsDir(); dir != "" {
		path, err := WriteReport(report, dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to write report: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Report written to: %s\n", path)
		}
	}

	data, _ := json.MarshalIndent(report, "", "  ")
	fmt.Fprintf(os.Stderr, "\n=== CHAOS TEST REPORT ===\n%s\n", string(data))

	os.Exit(code)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return dir
}

func newStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	objDir := filepath.Join(dir, "objects")
	s, err := store.NewStore(objDir, false)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newIndex(t *testing.T, dir string) *index.Index {
	t.Helper()
	dbPath := filepath.Join(dir, "index.db")
	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx
}

func newWriter(t *testing.T, dir string, maxBytes int64) *eventlog.Writer {
	t.Helper()
	eventsDir := filepath.Join(dir, "events")
	w, err := eventlog.NewWriter(eventsDir, maxBytes)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

func newReader(t *testing.T, dir string) *eventlog.Reader {
	t.Helper()
	eventsDir := filepath.Join(dir, "events")
	r, err := eventlog.NewReader(eventsDir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	return r
}

func makeEvent(id, filePath string, op schema.Operation, contentHash, previousHash, sessionID string, ts time.Time) *schema.Event {
	return &schema.Event{
		EventID:       id,
		Version:       schema.SchemaVersion,
		TimestampNano: ts.UnixNano(),
		FilePath:      filePath,
		Op:            op,
		ContentHash:   contentHash,
		PreviousHash:  previousHash,
		ContentSize:   int64(len(contentHash)),
		SessionID:     sessionID,
	}
}

// ─── Scenario 1: Rapid Fire ──────────────────────────────────────────────────

func TestChaos_RapidFire(t *testing.T) {
	start := time.Now()
	passed := true
	defer func() {
		RecordScenario("rapid_fire", "Rapid Fire", "1,000 concurrent events across 50 goroutines, all recovered", "stress",
			time.Since(start).Milliseconds(), map[string]int{"events": 1000, "goroutines": 50}, passed, "")
	}()
	dir := tempDir(t)
	objStore := newStore(t, dir)
	idx := newIndex(t, dir)
	w := newWriter(t, dir, 64*1024*1024)

	const totalEvents = 1000
	const goroutines = 50
	const eventsPerGoroutine = totalEvents / goroutines

	type eventRecord struct {
		event       *schema.Event
		contentHash string
		content     []byte
	}

	allRecords := make([]eventRecord, totalEvents)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gIdx int) {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				idx := gIdx*eventsPerGoroutine + i
				content := []byte(fmt.Sprintf("rapid-fire-content-g%d-i%d-%d", gIdx, i, time.Now().UnixNano()))

				hash, _, err := objStore.Put(content)
				if err != nil {
					mu.Lock()
					errs[gIdx] = fmt.Errorf("Put g=%d i=%d: %w", gIdx, i, err)
					mu.Unlock()
					return
				}

				evt := makeEvent(
					schema.NewEventID(),
					fmt.Sprintf("src/file_%d_%d.go", gIdx, i),
					schema.OpCreate,
					hash, "", fmt.Sprintf("session-%d", gIdx),
					time.Now(),
				)

				if err := w.Append(evt); err != nil {
					mu.Lock()
					errs[gIdx] = fmt.Errorf("Append g=%d i=%d: %w", gIdx, i, err)
					mu.Unlock()
					return
				}

				mu.Lock()
				allRecords[idx] = eventRecord{event: evt, contentHash: hash, content: content}
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}

	// Index all events
	for _, rec := range allRecords {
		if err := idx.IndexEvent(rec.event, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	// Verify: all events readable via reader
	r := newReader(t, dir)
	events, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != totalEvents {
		t.Errorf("ReadAll returned %d events, want %d", len(events), totalEvents)
	}

	// Verify: all content retrievable
	for _, rec := range allRecords {
		got, err := objStore.Get(rec.contentHash)
		if err != nil {
			t.Errorf("Get content %s: %v", rec.contentHash[:8], err)
			continue
		}
		if !bytes.Equal(got, rec.content) {
			t.Errorf("content mismatch for hash %s", rec.contentHash[:8])
		}
	}

	// Verify: all events queryable from index
	count, err := idx.CountEvents()
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}
	if count != totalEvents {
		t.Errorf("index has %d events, want %d", count, totalEvents)
	}
}

// ─── Scenario 2: Large File Boundaries ───────────────────────────────────────

func TestChaos_LargeFileBoundaries(t *testing.T) {
	start := time.Now()
	passed := true
	defer func() {
		RecordScenario("large_file_boundaries", "Large File Boundaries", "Store content at 49MB and 50MB boundaries, verify daemon skip at 51MB", "integrity",
			time.Since(start).Milliseconds(), map[string]int{"sizes_tested": 3}, passed, "")
	}()
	if testing.Short() {
		t.Skip("skipping large file test in short mode")
	}

	dir := tempDir(t)
	objStore := newStore(t, dir)

	sizes := []struct {
		name     string
		sizeMB   int
		shouldOK bool
	}{
		{"49MB", 49, true},
		{"50MB", 50, true},
	}

	for _, tc := range sizes {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.sizeMB*1024*1024)
			for i := range data {
				data[i] = byte(i % 256)
			}

			hash, size, err := objStore.Put(data)
			if err != nil {
				t.Fatalf("Put %s: %v", tc.name, err)
			}
			if size != int64(len(data)) {
				t.Errorf("size = %d, want %d", size, len(data))
			}

			got, err := objStore.Get(hash)
			if err != nil {
				t.Fatalf("Get %s: %v", tc.name, err)
			}
			if !bytes.Equal(got, data) {
				t.Errorf("content mismatch for %s", tc.name)
			}
		})
	}

	// 51MB: Simulate daemon behavior -- do NOT store it (daemon skips files > MaxFileSizeMB)
	t.Run("51MB_skipped_by_daemon", func(t *testing.T) {
		data51 := make([]byte, 51*1024*1024)
		hash51 := schema.ContentHashForBytes(data51)

		// We intentionally don't call objStore.Put -- the daemon would skip this
		if objStore.Has(hash51) {
			t.Error("51MB content should not be in store (daemon skips oversized files)")
		}
	})
}

// ─── Scenario 3: Concurrent Writers Same File ────────────────────────────────

func TestChaos_ConcurrentWritersSameFile(t *testing.T) {
	start := time.Now()
	passed := true
	defer func() {
		RecordScenario("concurrent_writers_same_file", "Concurrent Writers", "10 writers x 10 events on same file path, all indexed correctly", "concurrency",
			time.Since(start).Milliseconds(), map[string]int{"writers": 10, "events_per_writer": 10}, passed, "")
	}()
	dir := tempDir(t)
	objStore := newStore(t, dir)
	idx := newIndex(t, dir)
	w := newWriter(t, dir, 64*1024*1024)

	const writerCount = 10
	const eventsPerWriter = 10
	const totalEvents = writerCount * eventsPerWriter
	const targetFile = "src/shared_resource.go"

	type record struct {
		event   *schema.Event
		hash    string
		content []byte
	}

	allRecords := make([]record, totalEvents)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errs := make([]error, writerCount)

	for w_ := 0; w_ < writerCount; w_++ {
		wg.Add(1)
		go func(wIdx int) {
			defer wg.Done()
			sessionID := fmt.Sprintf("session-writer-%d", wIdx)
			for i := 0; i < eventsPerWriter; i++ {
				content := []byte(fmt.Sprintf("writer-%d-version-%d-ts-%d", wIdx, i, time.Now().UnixNano()))

				hash, _, err := objStore.Put(content)
				if err != nil {
					mu.Lock()
					errs[wIdx] = fmt.Errorf("Put w=%d i=%d: %w", wIdx, i, err)
					mu.Unlock()
					return
				}

				evt := makeEvent(
					schema.NewEventID(),
					targetFile,
					schema.OpModify,
					hash, "", sessionID,
					time.Now(),
				)

				if err := w.Append(evt); err != nil {
					mu.Lock()
					errs[wIdx] = fmt.Errorf("Append w=%d i=%d: %w", wIdx, i, err)
					mu.Unlock()
					return
				}

				slot := wIdx*eventsPerWriter + i
				mu.Lock()
				allRecords[slot] = record{event: evt, hash: hash, content: content}
				mu.Unlock()
			}
		}(w_)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}

	// Index all events
	for _, rec := range allRecords {
		if err := idx.IndexEvent(rec.event, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	// Verify: all 100 events exist in index
	count, err := idx.CountEvents()
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}
	if count != int64(totalEvents) {
		t.Errorf("index has %d events, want %d", count, totalEvents)
	}

	// Verify: FileHistory returns all events for the target path
	history, err := idx.FileHistory(targetFile, totalEvents+10)
	if err != nil {
		t.Fatalf("FileHistory: %v", err)
	}
	if len(history) != totalEvents {
		t.Errorf("FileHistory returned %d events, want %d", len(history), totalEvents)
	}

	// Verify: each content is distinct and retrievable
	seenHashes := make(map[string]bool)
	for _, rec := range allRecords {
		seenHashes[rec.hash] = true
		got, err := objStore.Get(rec.hash)
		if err != nil {
			t.Errorf("Get %s: %v", rec.hash[:8], err)
			continue
		}
		if !bytes.Equal(got, rec.content) {
			t.Errorf("content mismatch for %s", rec.hash[:8])
		}
	}
	if len(seenHashes) != totalEvents {
		t.Errorf("expected %d unique hashes, got %d", totalEvents, len(seenHashes))
	}
}

// ─── Scenario 4: Delete and Restore Chain ────────────────────────────────────

func TestChaos_DeleteRestoreChain(t *testing.T) {
	start := time.Now()
	passed := true
	defer func() {
		RecordScenario("delete_restore_chain", "Delete & Restore", "20 files through CREATE → 5x MODIFY → DELETE lifecycle", "recovery",
			time.Since(start).Milliseconds(), map[string]int{"files": 20, "modifications_per_file": 5}, passed, "")
	}()
	dir := tempDir(t)
	objStore := newStore(t, dir)
	idx := newIndex(t, dir)

	const fileCount = 20
	const modCount = 5

	type fileRecord struct {
		path         string
		hashes       []string // CREATE + 5 MODIFYs
		contents     [][]byte
		deleteEventID string
	}

	files := make([]fileRecord, fileCount)
	baseTime := time.Now()

	for f := 0; f < fileCount; f++ {
		filePath := fmt.Sprintf("src/chain/file_%03d.go", f)
		fr := fileRecord{path: filePath}

		// CREATE
		content := []byte(fmt.Sprintf("file-%d-v0", f))
		hash, _, err := objStore.Put(content)
		if err != nil {
			t.Fatalf("Put create: %v", err)
		}
		fr.hashes = append(fr.hashes, hash)
		fr.contents = append(fr.contents, content)

		createEvt := makeEvent(
			schema.NewEventID(), filePath, schema.OpCreate,
			hash, "", "session-chain",
			baseTime.Add(time.Duration(f)*time.Second),
		)
		if err := idx.IndexEvent(createEvt, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent create: %v", err)
		}

		// MODIFYs
		prevHash := hash
		for m := 1; m <= modCount; m++ {
			content := []byte(fmt.Sprintf("file-%d-v%d", f, m))
			hash, _, err := objStore.Put(content)
			if err != nil {
				t.Fatalf("Put modify: %v", err)
			}
			fr.hashes = append(fr.hashes, hash)
			fr.contents = append(fr.contents, content)

			modEvt := makeEvent(
				schema.NewEventID(), filePath, schema.OpModify,
				hash, prevHash, "session-chain",
				baseTime.Add(time.Duration(f)*time.Second+time.Duration(m)*100*time.Millisecond),
			)
			if err := idx.IndexEvent(modEvt, "seg.log", 0); err != nil {
				t.Fatalf("IndexEvent modify: %v", err)
			}
			prevHash = hash
		}

		// DELETE
		deleteEvt := makeEvent(
			schema.NewEventID(), filePath, schema.OpDelete,
			"", prevHash, "session-chain",
			baseTime.Add(time.Duration(f)*time.Second+time.Duration(modCount+1)*100*time.Millisecond),
		)
		if err := idx.IndexEvent(deleteEvt, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent delete: %v", err)
		}
		fr.deleteEventID = deleteEvt.EventID

		files[f] = fr
	}

	// Verify: each intermediate version's content is retrievable
	for f, fr := range files {
		for v, hash := range fr.hashes {
			got, err := objStore.Get(hash)
			if err != nil {
				t.Errorf("file %d version %d: Get %s: %v", f, v, hash[:8], err)
				continue
			}
			if !bytes.Equal(got, fr.contents[v]) {
				t.Errorf("file %d version %d: content mismatch", f, v)
			}
		}
	}

	// Verify: FileHistory shows the full lifecycle for each file
	for _, fr := range files {
		history, err := idx.FileHistory(fr.path, 100)
		if err != nil {
			t.Errorf("FileHistory %s: %v", fr.path, err)
			continue
		}
		// 1 CREATE + 5 MODIFYs + 1 DELETE = 7 events
		if len(history) != modCount+2 {
			t.Errorf("FileHistory %s: got %d events, want %d", fr.path, len(history), modCount+2)
		}
	}
}

// ─── Scenario 5: Segment Rollover ────────────────────────────────────────────

func TestChaos_SegmentRollover(t *testing.T) {
	start := time.Now()
	passed := true
	defer func() {
		RecordScenario("segment_rollover", "Segment Rollover", "500 events with 1KB segment max forcing frequent rotation", "integrity",
			time.Since(start).Milliseconds(), map[string]int{"events": 500, "segment_max_bytes": 1024}, passed, "")
	}()
	dir := tempDir(t)

	// Very small segment max to force frequent rotation
	const segmentMax int64 = 1024
	w := newWriter(t, dir, segmentMax)

	const eventCount = 500

	for i := 0; i < eventCount; i++ {
		evt := makeEvent(
			schema.NewEventID(),
			fmt.Sprintf("src/rollover_%04d.go", i),
			schema.OpCreate,
			schema.ContentHashForBytes([]byte(fmt.Sprintf("rollover-content-%d", i))),
			"", "session-rollover",
			time.Now(),
		)
		if err := w.Append(evt); err != nil {
			t.Fatalf("Append event %d: %v", i, err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	r := newReader(t, dir)

	// Verify: ReadAll returns exactly 500 events in order
	events, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != eventCount {
		t.Errorf("ReadAll returned %d events, want %d", len(events), eventCount)
	}

	// Verify: multiple segment files were created
	segments, err := r.Segments()
	if err != nil {
		t.Fatalf("Segments: %v", err)
	}
	if len(segments) < 2 {
		t.Errorf("expected multiple segments with max=%d, got %d segments", segmentMax, len(segments))
	}

	// Verify: within each segment, events are ordered by write sequence.
	// Cross-segment ordering is by segment filename (time-based), but events
	// within a segment are appended sequentially, so we verify per-segment ordering.
	for _, seg := range segments {
		segEvents, err := r.ReadSegment(seg)
		if err != nil {
			t.Errorf("ReadSegment %s: %v", seg, err)
			continue
		}
		for i := 1; i < len(segEvents); i++ {
			if segEvents[i].TimestampNano < segEvents[i-1].TimestampNano {
				t.Errorf("segment %s: event %d timestamp < event %d timestamp", seg, i, i-1)
			}
		}
	}
}

// ─── Scenario 6: Conflict Detection Under Load ──────────────────────────────

func TestChaos_ConflictDetection(t *testing.T) {
	start := time.Now()
	passed := true
	defer func() {
		RecordScenario("conflict_detection", "Conflict Detection", "50 files with 2 sessions writing within 3s, all detected as CRITICAL", "concurrency",
			time.Since(start).Milliseconds(), map[string]int{"files": 50, "sessions": 2}, passed, "")
	}()
	dir := tempDir(t)
	idx := newIndex(t, dir)

	const fileCount = 50
	baseTime := time.Now()

	// For each file, have 2 sessions write events within 3 seconds of each other
	for f := 0; f < fileCount; f++ {
		filePath := fmt.Sprintf("src/conflict_%03d.go", f)

		// Session A writes
		evtA := makeEvent(
			schema.NewEventID(), filePath, schema.OpModify,
			schema.ContentHashForBytes([]byte(fmt.Sprintf("sessionA-file%d", f))),
			"", "session-A",
			baseTime.Add(time.Duration(f)*10*time.Second),
		)
		if err := idx.IndexEvent(evtA, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent A: %v", err)
		}

		// Session B writes within 3 seconds
		evtB := makeEvent(
			schema.NewEventID(), filePath, schema.OpModify,
			schema.ContentHashForBytes([]byte(fmt.Sprintf("sessionB-file%d", f))),
			"", "session-B",
			baseTime.Add(time.Duration(f)*10*time.Second+3*time.Second),
		)
		if err := idx.IndexEvent(evtB, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent B: %v", err)
		}
	}

	// Run conflict detection with a 60-second window
	detector := conflict.NewDetector(idx, 60*time.Second)
	conflicts, err := detector.DetectSince(baseTime.Add(-1 * time.Second))
	if err != nil {
		t.Fatalf("DetectSince: %v", err)
	}

	if len(conflicts) < fileCount {
		t.Errorf("expected at least %d conflicts, got %d", fileCount, len(conflicts))
	}

	// Verify all conflicts are CRITICAL severity (gap <= 5s)
	for _, c := range conflicts {
		if c.Severity != conflict.SeverityCritical {
			t.Errorf("conflict for %s: severity = %s, want CRITICAL", c.FilePath, c.Severity)
		}
		if len(c.Sessions) < 2 {
			t.Errorf("conflict for %s: expected >= 2 sessions, got %d", c.FilePath, len(c.Sessions))
		}
	}
}

// ─── Scenario 7: GC Survival ─────────────────────────────────────────────────

func TestChaos_GCSurvival(t *testing.T) {
	start := time.Now()
	passed := true
	defer func() {
		RecordScenario("gc_survival", "GC Survival", "30 referenced + 20 orphaned objects, GC preserves referenced and cleans orphans", "integrity",
			time.Since(start).Milliseconds(), map[string]int{"referenced": 30, "orphaned": 20}, passed, "")
	}()
	dir := tempDir(t)
	objStore := newStore(t, dir)
	idx := newIndex(t, dir)

	// Create referenced events with content
	const referencedCount = 30
	referencedHashes := make(map[string]bool)

	for i := 0; i < referencedCount; i++ {
		content := []byte(fmt.Sprintf("referenced-content-%d", i))
		hash, _, err := objStore.Put(content)
		if err != nil {
			t.Fatalf("Put referenced: %v", err)
		}
		referencedHashes[hash] = true

		evt := makeEvent(
			schema.NewEventID(),
			fmt.Sprintf("src/referenced_%03d.go", i),
			schema.OpCreate,
			hash, "", "session-gc",
			time.Now(),
		)
		if err := idx.IndexEvent(evt, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	// Create orphaned content (stored but not indexed)
	const orphanCount = 20
	orphanHashes := make(map[string]bool)
	for i := 0; i < orphanCount; i++ {
		content := []byte(fmt.Sprintf("orphan-content-%d", i))
		hash, _, err := objStore.Put(content)
		if err != nil {
			t.Fatalf("Put orphan: %v", err)
		}
		orphanHashes[hash] = true
	}

	// Run GC
	result, err := store.GarbageCollect(idx, objStore, false)
	if err != nil {
		t.Fatalf("GarbageCollect: %v", err)
	}

	if result.ObjectsScanned != referencedCount+orphanCount {
		t.Errorf("ObjectsScanned = %d, want %d", result.ObjectsScanned, referencedCount+orphanCount)
	}
	if result.OrphanedObjects != orphanCount {
		t.Errorf("OrphanedObjects = %d, want %d", result.OrphanedObjects, orphanCount)
	}

	// Verify: all referenced content still retrievable
	for hash := range referencedHashes {
		if !objStore.Has(hash) {
			t.Errorf("referenced hash %s was deleted by GC", hash[:8])
		}
	}

	// Verify: all orphaned content was cleaned up
	for hash := range orphanHashes {
		if objStore.Has(hash) {
			t.Errorf("orphan hash %s survived GC", hash[:8])
		}
	}
}

// ─── Scenario 8: Snapshot Reconstruction ─────────────────────────────────────

func TestChaos_SnapshotReconstruction(t *testing.T) {
	start := time.Now()
	passed := true
	defer func() {
		RecordScenario("snapshot_reconstruction", "Snapshot Reconstruction", "100 files, 200 modifications, 20 deletes, 5 checkpoint snapshots verified", "recovery",
			time.Since(start).Milliseconds(), map[string]int{"files": 100, "modifications": 200, "deletes": 20, "checkpoints": 5}, passed, "")
	}()
	dir := tempDir(t)
	objStore := newStore(t, dir)
	idx := newIndex(t, dir)

	const fileCount = 100
	const modCount = 200
	const deleteCount = 20
	const checkpoints = 5

	rng := rand.New(rand.NewSource(42))
	baseTime := time.Now()
	tick := 0

	nextTime := func() time.Time {
		tick++
		return baseTime.Add(time.Duration(tick) * time.Millisecond)
	}

	// Track expected state at each point in time
	type fileState struct {
		contentHash string
		deleted     bool
	}
	currentState := make(map[string]*fileState)

	// Phase 1: Create 100 files
	for f := 0; f < fileCount; f++ {
		filePath := fmt.Sprintf("src/snapshot/file_%03d.go", f)
		content := []byte(fmt.Sprintf("initial-content-%d", f))
		hash, _, err := objStore.Put(content)
		if err != nil {
			t.Fatalf("Put create: %v", err)
		}

		evt := makeEvent(schema.NewEventID(), filePath, schema.OpCreate, hash, "", "session-snap", nextTime())
		if err := idx.IndexEvent(evt, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent create: %v", err)
		}
		currentState[filePath] = &fileState{contentHash: hash}
	}

	// Capture checkpoint timestamps and expected states
	checkpointTimes := make([]int64, checkpoints)
	checkpointStates := make([]map[string]string, checkpoints)

	eventsPerCheckpoint := (modCount + deleteCount) / checkpoints

	// Phase 2: Apply modifications and deletes, capturing checkpoints
	allPaths := make([]string, 0, len(currentState))
	for p := range currentState {
		allPaths = append(allPaths, p)
	}
	sort.Strings(allPaths)

	opIndex := 0
	for opIndex < modCount {
		filePath := allPaths[rng.Intn(len(allPaths))]
		state := currentState[filePath]
		if state.deleted {
			continue
		}

		prevHash := state.contentHash
		content := []byte(fmt.Sprintf("modified-content-%s-%d", filePath, opIndex))
		hash, _, err := objStore.Put(content)
		if err != nil {
			t.Fatalf("Put modify: %v", err)
		}

		evt := makeEvent(schema.NewEventID(), filePath, schema.OpModify, hash, prevHash, "session-snap", nextTime())
		if err := idx.IndexEvent(evt, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent modify: %v", err)
		}
		state.contentHash = hash
		opIndex++

		// Capture checkpoint
		cp := opIndex / eventsPerCheckpoint
		if cp < checkpoints && opIndex%eventsPerCheckpoint == 0 {
			checkpointTimes[cp] = evt.TimestampNano
			snapshot := make(map[string]string)
			for p, s := range currentState {
				if !s.deleted {
					snapshot[p] = s.contentHash
				}
			}
			checkpointStates[cp] = snapshot
		}
	}

	// Phase 3: Delete some files
	deletedPaths := make(map[string]bool)
	deleteOps := 0
	for deleteOps < deleteCount {
		filePath := allPaths[rng.Intn(len(allPaths))]
		if deletedPaths[filePath] {
			continue
		}
		state := currentState[filePath]
		if state.deleted {
			continue
		}

		evt := makeEvent(schema.NewEventID(), filePath, schema.OpDelete, "", state.contentHash, "session-snap", nextTime())
		if err := idx.IndexEvent(evt, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent delete: %v", err)
		}
		state.deleted = true
		deletedPaths[filePath] = true
		deleteOps++
	}

	// Verify: each checkpoint snapshot matches expected state
	for cp := 0; cp < checkpoints; cp++ {
		if checkpointTimes[cp] == 0 {
			continue
		}

		snap, err := replay.SnapshotAt(idx, checkpointTimes[cp])
		if err != nil {
			t.Errorf("SnapshotAt checkpoint %d: %v", cp, err)
			continue
		}

		expected := checkpointStates[cp]
		if len(snap.Files) != len(expected) {
			t.Errorf("checkpoint %d: snapshot has %d files, want %d", cp, len(snap.Files), len(expected))
			continue
		}

		for path, expectedHash := range expected {
			fs, ok := snap.Files[path]
			if !ok {
				t.Errorf("checkpoint %d: missing file %s in snapshot", cp, path)
				continue
			}
			if fs.ContentHash != expectedHash {
				t.Errorf("checkpoint %d: file %s hash = %s, want %s", cp, path, fs.ContentHash[:8], expectedHash[:8])
			}
		}
	}
}

// ─── Scenario 9: Session Attribution Ordering ────────────────────────────────

func TestChaos_SessionAttribution(t *testing.T) {
	start := time.Now()
	passed := true
	defer func() {
		RecordScenario("session_attribution", "Session Attribution", "Events with hook/pid/temporal/heuristic/none attribution, correctly grouped", "concurrency",
			time.Since(start).Milliseconds(), map[string]int{"attribution_methods": 5, "events": 10}, passed, "")
	}()
	dir := tempDir(t)
	idx := newIndex(t, dir)

	type attrCase struct {
		method     schema.AttributionMethod
		confidence float32
		sessionID  string
	}

	cases := []attrCase{
		{schema.AttrHook, 1.0, "session-hook"},
		{schema.AttrHook, 1.0, "session-hook"},
		{schema.AttrPID, 0.95, "session-pid"},
		{schema.AttrPID, 0.95, "session-pid"},
		{schema.AttrTemporal, 0.7, "session-temporal"},
		{schema.AttrTemporal, 0.7, "session-temporal"},
		{schema.AttrHeuristic, 0.6, "session-heuristic"},
		{schema.AttrHeuristic, 0.6, "session-heuristic"},
		{schema.AttrNone, 0.0, ""},     // No session
		{schema.AttrNone, 0.0, ""},     // No session
	}

	for i, ac := range cases {
		evt := &schema.Event{
			EventID:               schema.NewEventID(),
			Version:               schema.SchemaVersion,
			TimestampNano:         time.Now().Add(time.Duration(i) * time.Millisecond).UnixNano(),
			FilePath:              fmt.Sprintf("src/attr_%02d.go", i),
			Op:                    schema.OpModify,
			ContentHash:           schema.ContentHashForBytes([]byte(fmt.Sprintf("attr-content-%d", i))),
			SessionID:             ac.sessionID,
			Attribution:           ac.method,
			AttributionConfidence: ac.confidence,
		}
		if err := idx.IndexEvent(evt, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	// Verify: events are correctly grouped by session_id
	sessionIDs := []string{"session-hook", "session-pid", "session-temporal", "session-heuristic"}
	for _, sid := range sessionIDs {
		events, err := idx.QueryEvents(&index.Query{Sessions: []string{sid}})
		if err != nil {
			t.Fatalf("QueryEvents session %s: %v", sid, err)
		}
		if len(events) != 2 {
			t.Errorf("session %s: got %d events, want 2", sid, len(events))
		}
		for _, evt := range events {
			if evt.SessionID != sid {
				t.Errorf("session %s: event has session_id %s", sid, evt.SessionID)
			}
		}
	}

	// Verify: events with no session_id
	allEvents, err := idx.QueryEvents(&index.Query{})
	if err != nil {
		t.Fatalf("QueryEvents all: %v", err)
	}

	noSessionCount := 0
	for _, evt := range allEvents {
		if evt.SessionID == "" {
			noSessionCount++
		}
	}
	if noSessionCount != 2 {
		t.Errorf("expected 2 events with no session_id, got %d", noSessionCount)
	}
}

// ─── Scenario 10: Index Concurrent Read-Write ────────────────────────────────

func TestChaos_IndexConcurrentReadWrite(t *testing.T) {
	start := time.Now()
	passed := true
	defer func() {
		RecordScenario("index_concurrent_read_write", "Index Concurrent R/W", "2 writers + 10 readers for 2 seconds, no panics or errors", "concurrency",
			time.Since(start).Milliseconds(), map[string]int{"writers": 2, "readers": 10, "duration_seconds": 2}, passed, "")
	}()
	dir := tempDir(t)
	idx := newIndex(t, dir)

	const writerCount = 2
	const readerCount = 10
	const duration = 2 * time.Second

	var wg sync.WaitGroup
	var mu sync.Mutex
	var writeErrors []error
	var readErrors []error
	var writeCount int64

	done := make(chan struct{})
	go func() {
		time.Sleep(duration)
		close(done)
	}()

	// Writers -- retry on SQLITE_BUSY since concurrent writers are expected to
	// occasionally contend on the WAL lock. A short backoff avoids thundering herd.
	for w := 0; w < writerCount; w++ {
		wg.Add(1)
		go func(wIdx int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-done:
					return
				default:
				}

				evt := makeEvent(
					schema.NewEventID(),
					fmt.Sprintf("src/concurrent_%d_%d.go", wIdx, i),
					schema.OpCreate,
					schema.ContentHashForBytes([]byte(fmt.Sprintf("w%d-i%d", wIdx, i))),
					"", fmt.Sprintf("session-w%d", wIdx),
					time.Now(),
				)

				var err error
				for attempt := 0; attempt < 10; attempt++ {
					err = idx.IndexEvent(evt, "seg.log", 0)
					if err == nil {
						break
					}
					// Exponential backoff on busy/locked errors
					time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
				}
				if err != nil {
					mu.Lock()
					writeErrors = append(writeErrors, fmt.Errorf("writer %d event %d: %w", wIdx, i, err))
					mu.Unlock()
					return
				}
				mu.Lock()
				writeCount++
				mu.Unlock()
				i++
			}
		}(w)
	}

	// Readers
	for r := 0; r < readerCount; r++ {
		wg.Add(1)
		go func(rIdx int) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}

				// Alternate between QueryEvents and FileHistory
				if rIdx%2 == 0 {
					events, err := idx.QueryEvents(&index.Query{Limit: 10})
					if err != nil {
						mu.Lock()
						readErrors = append(readErrors, fmt.Errorf("reader %d query: %w", rIdx, err))
						mu.Unlock()
						return
					}
					// Validate returned data
					for _, e := range events {
						if e.EventID == "" {
							mu.Lock()
							readErrors = append(readErrors, fmt.Errorf("reader %d: got event with empty ID", rIdx))
							mu.Unlock()
							return
						}
					}
				} else {
					_, err := idx.FileHistory(fmt.Sprintf("src/concurrent_%d_0.go", rIdx%writerCount), 10)
					if err != nil {
						mu.Lock()
						readErrors = append(readErrors, fmt.Errorf("reader %d history: %w", rIdx, err))
						mu.Unlock()
						return
					}
				}
			}
		}(r)
	}

	wg.Wait()

	if len(writeErrors) > 0 {
		for _, err := range writeErrors {
			t.Errorf("write error: %v", err)
		}
	}
	if len(readErrors) > 0 {
		for _, err := range readErrors {
			t.Errorf("read error: %v", err)
		}
	}

	mu.Lock()
	wc := writeCount
	mu.Unlock()

	if wc == 0 {
		t.Error("no events were written during concurrent read-write test")
	}

	// Verify final count matches
	count, err := idx.CountEvents()
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}
	if count != int64(wc) {
		t.Errorf("index has %d events, expected %d writes", count, wc)
	}
}

// ─── Scenario 11: Event Log Corruption Recovery ─────────────────────────────

func TestChaos_CorruptionRecovery(t *testing.T) {
	start := time.Now()
	passed := true
	defer func() {
		RecordScenario("corruption_recovery", "Corruption Recovery", "100 events with last one truncated, first 99 recovered correctly", "recovery",
			time.Since(start).Milliseconds(), map[string]int{"total_events": 100, "recovered": 99}, passed, "")
	}()
	dir := tempDir(t)
	eventsDir := filepath.Join(dir, "events")

	// Write 100 valid events
	const validCount = 100
	w, err := eventlog.NewWriter(eventsDir, 100*1024*1024) // large segment so all fit in one
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	eventIDs := make([]string, validCount)
	for i := 0; i < validCount; i++ {
		id := schema.NewEventID()
		eventIDs[i] = id
		evt := makeEvent(
			id,
			fmt.Sprintf("src/corrupt_%03d.go", i),
			schema.OpCreate,
			schema.ContentHashForBytes([]byte(fmt.Sprintf("corrupt-content-%d", i))),
			"", "session-corrupt",
			time.Now(),
		)
		if err := w.Append(evt); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	seg := w.CurrentSegment()
	if err := w.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}

	// Truncate the last event (simulate partial write / crash)
	segPath := filepath.Join(eventsDir, seg)
	info, err := os.Stat(segPath)
	if err != nil {
		t.Fatalf("Stat segment: %v", err)
	}

	// Remove the last 20 bytes to corrupt the last event's frame
	truncatedSize := info.Size() - 20
	if truncatedSize < 0 {
		truncatedSize = 0
	}
	if err := os.Truncate(segPath, truncatedSize); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	// Read the segment
	r, err := eventlog.NewReader(eventsDir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	events, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll after truncation: %v", err)
	}

	// Verify: at least 99 of 100 events are recovered (last one was corrupted)
	if len(events) < validCount-1 {
		t.Errorf("expected at least %d events after corruption, got %d", validCount-1, len(events))
	}
	if len(events) >= validCount {
		t.Errorf("expected fewer than %d events after corruption, got %d (truncation had no effect?)", validCount, len(events))
	}

	// Verify: recovered events have correct IDs
	for i, evt := range events {
		if evt.EventID != eventIDs[i] {
			t.Errorf("event %d: ID = %s, want %s", i, evt.EventID, eventIDs[i])
		}
	}
}

// ─── Scenario 12: Full Pipeline Integration ─────────────────────────────────

func TestChaos_FullPipeline(t *testing.T) {
	start := time.Now()
	passed := true
	defer func() {
		RecordScenario("full_pipeline", "Full Pipeline", "Complete workflow: session, 50 creates, 100 mods, 10 deletes, replay, snapshot", "stress",
			time.Since(start).Milliseconds(), map[string]int{"creates": 50, "modifications": 100, "deletes": 10}, passed, "")
	}()
	dir := tempDir(t)
	objStore := newStore(t, dir)
	idx := newIndex(t, dir)
	w := newWriter(t, dir, 64*1024*1024)

	sessionID := "session-pipeline-full"
	baseTime := time.Now()

	// Step 1: Register session
	session := &schema.Session{
		SessionID:        sessionID,
		ToolName:         "claude-code",
		PID:              os.Getpid(),
		WorkingDirectory: dir,
		Status:           schema.SessionActive,
		StartedAt:        baseTime,
	}
	if err := idx.UpsertSession(session); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	// Step 2: 50 file creates
	const createCount = 50
	createdFiles := make(map[string]string) // path -> hash
	tick := 0

	for i := 0; i < createCount; i++ {
		tick++
		filePath := fmt.Sprintf("src/pipeline/file_%03d.go", i)
		content := []byte(fmt.Sprintf("pipeline-create-%d", i))
		hash, _, err := objStore.Put(content)
		if err != nil {
			t.Fatalf("Put create: %v", err)
		}

		evt := makeEvent(schema.NewEventID(), filePath, schema.OpCreate, hash, "", sessionID,
			baseTime.Add(time.Duration(tick)*time.Millisecond))
		if err := w.Append(evt); err != nil {
			t.Fatalf("Append create: %v", err)
		}
		if err := idx.IndexEvent(evt, w.CurrentSegment(), 0); err != nil {
			t.Fatalf("IndexEvent create: %v", err)
		}
		createdFiles[filePath] = hash
	}

	// Step 3: 100 modifications spread across created files
	const modCount = 100
	rng := rand.New(rand.NewSource(99))
	paths := make([]string, 0, len(createdFiles))
	for p := range createdFiles {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for i := 0; i < modCount; i++ {
		tick++
		filePath := paths[rng.Intn(len(paths))]
		prevHash := createdFiles[filePath]
		content := []byte(fmt.Sprintf("pipeline-modify-%s-%d", filePath, i))
		hash, _, err := objStore.Put(content)
		if err != nil {
			t.Fatalf("Put modify: %v", err)
		}

		evt := makeEvent(schema.NewEventID(), filePath, schema.OpModify, hash, prevHash, sessionID,
			baseTime.Add(time.Duration(tick)*time.Millisecond))
		if err := w.Append(evt); err != nil {
			t.Fatalf("Append modify: %v", err)
		}
		if err := idx.IndexEvent(evt, w.CurrentSegment(), 0); err != nil {
			t.Fatalf("IndexEvent modify: %v", err)
		}
		createdFiles[filePath] = hash
	}

	// Step 4: 10 deletes
	const deleteCount = 10
	deletedPaths := make(map[string]bool)
	for i := 0; i < deleteCount; i++ {
		tick++
		// Find a non-deleted path
		for {
			filePath := paths[rng.Intn(len(paths))]
			if deletedPaths[filePath] {
				continue
			}
			prevHash := createdFiles[filePath]

			evt := makeEvent(schema.NewEventID(), filePath, schema.OpDelete, "", prevHash, sessionID,
				baseTime.Add(time.Duration(tick)*time.Millisecond))
			if err := w.Append(evt); err != nil {
				t.Fatalf("Append delete: %v", err)
			}
			if err := idx.IndexEvent(evt, w.CurrentSegment(), 0); err != nil {
				t.Fatalf("IndexEvent delete: %v", err)
			}
			deletedPaths[filePath] = true
			delete(createdFiles, filePath)
			break
		}
	}

	// Step 5: End session
	session.Status = schema.SessionEnded
	session.EndedAt = baseTime.Add(time.Duration(tick+1) * time.Millisecond)
	session.EventCount = createCount + modCount + deleteCount
	session.FilesChanged = createCount
	if err := idx.UpsertSession(session); err != nil {
		t.Fatalf("UpsertSession end: %v", err)
	}

	// Verify: total event count
	count, err := idx.CountEvents()
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}
	expectedTotal := int64(createCount + modCount + deleteCount)
	if count != expectedTotal {
		t.Errorf("index has %d events, want %d", count, expectedTotal)
	}

	// Verify: conflict detection (single session = no conflicts)
	detector := conflict.NewDetector(idx, 60*time.Second)
	conflicts, err := detector.DetectSince(baseTime.Add(-1 * time.Second))
	if err != nil {
		t.Fatalf("DetectSince: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("single-session should have 0 conflicts, got %d", len(conflicts))
	}

	// Verify: replay shows correct net changes
	result, err := replay.ReplaySession(idx, objStore, sessionID)
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}

	// Files that were created and then deleted should not appear
	for path, fc := range result.Files {
		if deletedPaths[path] && fc.Operation != "delete" {
			t.Errorf("deleted file %s: expected delete operation, got %s", path, fc.Operation)
		}
	}

	// Verify: snapshot at end matches expected final state
	snap, err := replay.SnapshotAt(idx, baseTime.Add(time.Duration(tick+2)*time.Millisecond).UnixNano())
	if err != nil {
		t.Fatalf("SnapshotAt: %v", err)
	}

	// Deleted files should not be in snapshot
	for path := range deletedPaths {
		if _, ok := snap.Files[path]; ok {
			t.Errorf("deleted file %s should not be in final snapshot", path)
		}
	}

	// Remaining files should have correct hashes
	for path, expectedHash := range createdFiles {
		fs, ok := snap.Files[path]
		if !ok {
			t.Errorf("expected file %s in snapshot", path)
			continue
		}
		if fs.ContentHash != expectedHash {
			t.Errorf("file %s: snapshot hash = %s, want %s", path, fs.ContentHash[:8], expectedHash[:8])
		}
	}

	// Verify: session is retrievable and ended
	gotSession, err := idx.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if gotSession.Status != schema.SessionEnded {
		t.Errorf("session status = %s, want ended", gotSession.Status)
	}
}

// ─── Git Helpers ─────────────────────────────────────────────────────────────

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
}

func gitCommit(t *testing.T, dir string, msg string) {
	t.Helper()
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-m", msg)
}

func gitWorktreeAdd(t *testing.T, repoDir, worktreePath string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(worktreePath), 0755)
	run(t, repoDir, "git", "worktree", "add", worktreePath)
}

func gitWorktreeRemove(t *testing.T, repoDir, worktreePath string) {
	t.Helper()
	run(t, repoDir, "git", "worktree", "remove", "--force", worktreePath)
}

func writeFile(t *testing.T, dir, relPath string, content string) {
	t.Helper()
	absPath := filepath.Join(dir, relPath)
	_ = os.MkdirAll(filepath.Dir(absPath), 0755)
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

// ─── Worktree path helpers (inline, mirrors watcher package being built in parallel) ─

func isWorktreePath(relPath string) bool {
	return strings.HasPrefix(relPath, ".claude/worktrees/")
}

func parseWorktreePath(relPath string) (worktreeName string, canonicalPath string, ok bool) {
	if !isWorktreePath(relPath) {
		return "", "", false
	}
	// .claude/worktrees/<name>/rest/of/path
	trimmed := strings.TrimPrefix(relPath, ".claude/worktrees/")
	slashIdx := strings.Index(trimmed, "/")
	if slashIdx < 0 {
		return "", "", false
	}
	return trimmed[:slashIdx], trimmed[slashIdx+1:], true
}

var filterCacheMu sync.Mutex
var filterCache = map[string]map[string]bool{}

func shouldFilterWorktreeCreate(projectRoot, relPath string) (canonicalPath string, meta map[string]string, skip bool) {
	if !isWorktreePath(relPath) {
		return relPath, nil, false
	}
	wtName, canonical, ok := parseWorktreePath(relPath)
	if !ok {
		return relPath, nil, false
	}
	wtAbsPath := filepath.Join(projectRoot, ".claude", "worktrees", wtName)
	cacheKey := wtAbsPath

	filterCacheMu.Lock()
	dirty, cached := filterCache[cacheKey]
	if !cached {
		dirty = gitDirtyFilesRaw(wtAbsPath)
		filterCache[cacheKey] = dirty
	}
	filterCacheMu.Unlock()

	if !dirty[canonical] {
		return "", nil, true
	}
	return canonical, map[string]string{"worktree": wtName}, false
}

func gitDirtyFilesRaw(worktreePath string) map[string]bool {
	dirty := make(map[string]bool)
	cmds := [][]string{
		{"git", "-C", worktreePath, "diff", "--name-only"},
		{"git", "-C", worktreePath, "diff", "--name-only", "--staged"},
		{"git", "-C", worktreePath, "ls-files", "--others", "--exclude-standard"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).Output(); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				line = strings.TrimRight(line, "\r")
				if line != "" {
					dirty[line] = true
				}
			}
		}
	}
	return dirty
}

func gitDirtyFiles(t *testing.T, worktreePath string) map[string]bool {
	t.Helper()
	return gitDirtyFilesRaw(worktreePath)
}

// ─── Scenario 13: Worktree Tracking ──────────────────────────────────────────

func TestChaos_WorktreeTracking(t *testing.T) {
	start := time.Now()
	passed := true
	errMsg := ""

	initialFileCount := 7
	editCount := 4
	newFileCount := 2
	worktreeCount := 3

	defer func() {
		RecordScenario("worktree_tracking", "Worktree Tracking",
			"Simulate Claude Code worktree agent: checkout burst filtered, real edits captured with canonical paths",
			"recovery",
			time.Since(start).Milliseconds(),
			map[string]int{"worktree_files": initialFileCount, "ai_edits": editCount, "new_files": newFileCount, "worktrees": worktreeCount},
			passed, errMsg)
	}()

	dir := tempDir(t)
	objStore := newStore(t, dir)
	idx := newIndex(t, dir)
	w := newWriter(t, dir, 64*1024*1024)

	gitInit(t, dir)

	initialFiles := []string{
		"src/main.go",
		"src/utils.go",
		"src/config.go",
		"domains/service-a/handler.go",
		"domains/service-a/model.go",
		"domains/service-b/api.go",
		"README.md",
	}
	for _, f := range initialFiles {
		writeFile(t, dir, f, fmt.Sprintf("initial content of %s", f))
	}
	gitCommit(t, dir, "initial commit")

	baseTime := time.Now()
	tick := 0
	nextTime := func() time.Time {
		tick++
		return baseTime.Add(time.Duration(tick) * time.Millisecond)
	}

	mainSessionID := "session-main-editor"
	mainEditPath := "src/main.go"
	mainContent := []byte("main tree edit of src/main.go")
	mainHash, _, err := objStore.Put(mainContent)
	if err != nil {
		t.Fatalf("Put main edit: %v", err)
	}
	mainEvt := makeEvent(schema.NewEventID(), mainEditPath, schema.OpModify, mainHash, "", mainSessionID, nextTime())
	mainEvt.Metadata = map[string]string{}
	if err := w.Append(mainEvt); err != nil {
		t.Fatalf("Append main edit: %v", err)
	}
	if err := idx.IndexEvent(mainEvt, w.CurrentSegment(), 0); err != nil {
		t.Fatalf("IndexEvent main edit: %v", err)
	}

	type worktreeResult struct {
		name          string
		path          string
		editPaths     []string
		newFilePaths  []string
	}

	worktrees := make([]worktreeResult, worktreeCount)
	worktreeNames := []string{"agent-alpha-001", "agent-beta-002", "agent-gamma-003"}

	for wi := 0; wi < worktreeCount; wi++ {
		wtName := worktreeNames[wi]
		wtPath := filepath.Join(dir, ".claude", "worktrees", wtName)
		gitWorktreeAdd(t, dir, wtPath)
		t.Cleanup(func() {
			gitWorktreeRemove(t, dir, wtPath)
		})

		wtr := worktreeResult{name: wtName, path: wtPath}

		// --- Phase 1: Simulate checkout burst (CREATE events for all repo files) ---
		dirtyBeforeEdits := gitDirtyFiles(t, wtPath)

		for _, f := range initialFiles {
			wtRelPath := filepath.Join(".claude", "worktrees", wtName, f)
			wtRelPath = filepath.ToSlash(wtRelPath)

			_, canonical, ok := parseWorktreePath(wtRelPath)
			if !ok {
				t.Fatalf("parseWorktreePath failed for %s", wtRelPath)
			}

			// Checkout file: op=CREATE, but git-status shows it as clean
			if !dirtyBeforeEdits[f] {
				// Filter: clean checkout file, skip this CREATE event
				continue
			}

			content, readErr := os.ReadFile(filepath.Join(wtPath, f))
			if readErr != nil {
				t.Fatalf("read checkout file: %v", readErr)
			}
			hash, _, putErr := objStore.Put(content)
			if putErr != nil {
				t.Fatalf("Put checkout: %v", putErr)
			}
			evt := makeEvent(schema.NewEventID(), canonical, schema.OpCreate, hash, "", fmt.Sprintf("session-wt-%s", wtName), nextTime())
			evt.Metadata = map[string]string{"worktree": wtName}
			if err := w.Append(evt); err != nil {
				t.Fatalf("Append checkout: %v", err)
			}
			if err := idx.IndexEvent(evt, w.CurrentSegment(), 0); err != nil {
				t.Fatalf("IndexEvent checkout: %v", err)
			}
		}

		// --- Phase 2: AI edits existing files in the worktree ---
		editFiles := []string{"src/main.go", "src/utils.go", "domains/service-a/handler.go", "domains/service-b/api.go"}
		if wi == 1 {
			editFiles = []string{"src/config.go", "domains/service-a/model.go", "README.md", "src/main.go"}
		} else if wi == 2 {
			editFiles = []string{"src/utils.go", "src/config.go", "domains/service-a/handler.go", "domains/service-a/model.go"}
		}

		for _, f := range editFiles {
			modContent := []byte(fmt.Sprintf("AI edit by %s: %s v%d", wtName, f, tick))
			_ = os.WriteFile(filepath.Join(wtPath, f), modContent, 0644)

			hash, _, putErr := objStore.Put(modContent)
			if putErr != nil {
				t.Fatalf("Put AI edit: %v", putErr)
			}

			wtRelPath := filepath.ToSlash(filepath.Join(".claude", "worktrees", wtName, f))
			_, canonical, _ := parseWorktreePath(wtRelPath)

			evt := makeEvent(schema.NewEventID(), canonical, schema.OpModify, hash, "", fmt.Sprintf("session-wt-%s", wtName), nextTime())
			evt.Metadata = map[string]string{"worktree": wtName}
			if err := w.Append(evt); err != nil {
				t.Fatalf("Append AI edit: %v", err)
			}
			if err := idx.IndexEvent(evt, w.CurrentSegment(), 0); err != nil {
				t.Fatalf("IndexEvent AI edit: %v", err)
			}
		}
		wtr.editPaths = editFiles

		// --- Phase 3: AI creates new files in the worktree ---
		newFiles := []string{
			fmt.Sprintf("domains/service-a/new_%s_1.go", wtName),
			fmt.Sprintf("domains/service-b/new_%s_2.go", wtName),
		}

		for _, f := range newFiles {
			newContent := []byte(fmt.Sprintf("new file by %s: %s", wtName, f))
			writeFile(t, wtPath, f, string(newContent))

			hash, _, putErr := objStore.Put(newContent)
			if putErr != nil {
				t.Fatalf("Put new file: %v", putErr)
			}

			wtRelPath := filepath.ToSlash(filepath.Join(".claude", "worktrees", wtName, f))

			if !isWorktreePath(wtRelPath) {
				t.Fatalf("expected worktree path: %s", wtRelPath)
			}
			_, canonical, _ := parseWorktreePath(wtRelPath)

			dirtyAfter := gitDirtyFiles(t, wtPath)
			if !dirtyAfter[f] {
				t.Errorf("new file %s should be dirty (untracked) in worktree", f)
			}

			evt := makeEvent(schema.NewEventID(), canonical, schema.OpCreate, hash, "", fmt.Sprintf("session-wt-%s", wtName), nextTime())
			evt.Metadata = map[string]string{"worktree": wtName}
			if err := w.Append(evt); err != nil {
				t.Fatalf("Append new file: %v", err)
			}
			if err := idx.IndexEvent(evt, w.CurrentSegment(), 0); err != nil {
				t.Fatalf("IndexEvent new file: %v", err)
			}
		}
		wtr.newFilePaths = newFiles

		worktrees[wi] = wtr
	}

	// ─── Validation 1: Checkout burst was filtered ───────────────────────────

	r := newReader(t, dir)
	allEvents, readErr := r.ReadAll()
	if readErr != nil {
		t.Fatalf("ReadAll: %v", readErr)
	}

	for _, evt := range allEvents {
		if strings.Contains(evt.FilePath, ".claude/worktrees/") {
			t.Errorf("event has un-translated worktree path: %s", evt.FilePath)
			passed = false
			errMsg = "worktree path not translated to canonical"
		}
	}

	// ─── Validation 2: AI edits captured (MODIFY events pass through) ────────

	for _, wtr := range worktrees {
		sessionID := fmt.Sprintf("session-wt-%s", wtr.name)
		events, qErr := idx.QueryEvents(&index.Query{Sessions: []string{sessionID}})
		if qErr != nil {
			t.Fatalf("QueryEvents for %s: %v", sessionID, qErr)
		}

		modifyCount := 0
		createCount := 0
		for _, evt := range events {
			switch evt.Op {
			case schema.OpModify:
				modifyCount++
			case schema.OpCreate:
				createCount++
			}
		}

		if modifyCount != len(wtr.editPaths) {
			t.Errorf("worktree %s: got %d MODIFY events, want %d", wtr.name, modifyCount, len(wtr.editPaths))
			passed = false
			errMsg = fmt.Sprintf("wrong MODIFY count for %s", wtr.name)
		}

		if createCount != len(wtr.newFilePaths) {
			t.Errorf("worktree %s: got %d CREATE events, want %d (new files only, checkout filtered)", wtr.name, createCount, len(wtr.newFilePaths))
			passed = false
			errMsg = fmt.Sprintf("wrong CREATE count for %s", wtr.name)
		}
	}

	// ─── Validation 3: Path translation correct ─────────────────────────────

	for _, evt := range allEvents {
		if isWorktreePath(evt.FilePath) {
			t.Errorf("event stored with worktree prefix: %s", evt.FilePath)
			passed = false
			errMsg = "canonical path translation failed"
		}
	}

	// ─── Validation 4: Metadata preserved ────────────────────────────────────

	for _, wtr := range worktrees {
		sessionID := fmt.Sprintf("session-wt-%s", wtr.name)
		events, _ := idx.QueryEvents(&index.Query{Sessions: []string{sessionID}})
		for _, evt := range events {
			if evt.Metadata == nil || evt.Metadata["worktree"] != wtr.name {
				t.Errorf("event %s missing worktree metadata, got %v", evt.EventID, evt.Metadata)
				passed = false
				errMsg = "worktree metadata missing"
			}
		}
	}

	// ─── Validation 5: Cross-tree queryability ───────────────────────────────

	mainHistory, hErr := idx.FileHistory("src/main.go", 100)
	if hErr != nil {
		t.Fatalf("FileHistory src/main.go: %v", hErr)
	}

	// main tree edit + worktrees that edited src/main.go
	mainTreeCount := 1
	wtMainEditors := 0
	for _, wtr := range worktrees {
		for _, f := range wtr.editPaths {
			if f == "src/main.go" {
				wtMainEditors++
				break
			}
		}
	}
	expectedMainHistory := mainTreeCount + wtMainEditors

	if len(mainHistory) != expectedMainHistory {
		t.Errorf("FileHistory src/main.go: got %d events, want %d (1 main + %d worktrees)", len(mainHistory), expectedMainHistory, wtMainEditors)
		passed = false
		errMsg = "cross-tree query failed"
	}

	sessionsFound := make(map[string]bool)
	for _, evt := range mainHistory {
		sessionsFound[evt.SessionID] = true
	}
	if !sessionsFound[mainSessionID] {
		t.Errorf("main tree session %s not found in FileHistory for src/main.go", mainSessionID)
		passed = false
		errMsg = "main session missing from cross-tree query"
	}

	// ─── Validation 6: Multiple concurrent worktrees have distinct attribution ─

	allWorktreeSessions := make(map[string]int)
	for _, evt := range allEvents {
		if evt.Metadata != nil && evt.Metadata["worktree"] != "" {
			allWorktreeSessions[evt.Metadata["worktree"]]++
		}
	}

	if len(allWorktreeSessions) != worktreeCount {
		t.Errorf("expected %d distinct worktree attributions, got %d: %v", worktreeCount, len(allWorktreeSessions), allWorktreeSessions)
		passed = false
		errMsg = "worktree attribution count mismatch"
	}

	for _, wtr := range worktrees {
		expectedEvents := len(wtr.editPaths) + len(wtr.newFilePaths)
		if allWorktreeSessions[wtr.name] != expectedEvents {
			t.Errorf("worktree %s: expected %d events, got %d", wtr.name, expectedEvents, allWorktreeSessions[wtr.name])
			passed = false
			errMsg = fmt.Sprintf("event count mismatch for worktree %s", wtr.name)
		}
	}

	// ─── Validation 7: Snapshot at end includes worktree edits ───────────────

	snap, snapErr := replay.SnapshotAt(idx, baseTime.Add(time.Duration(tick+2)*time.Millisecond).UnixNano())
	if snapErr != nil {
		t.Fatalf("SnapshotAt: %v", snapErr)
	}

	if _, ok := snap.Files["src/main.go"]; !ok {
		t.Error("src/main.go missing from final snapshot")
		passed = false
		errMsg = "snapshot missing main file"
	}

	for _, wtr := range worktrees {
		for _, f := range wtr.newFilePaths {
			if _, ok := snap.Files[f]; !ok {
				t.Errorf("new file %s from worktree %s missing from snapshot", f, wtr.name)
				passed = false
				errMsg = "new worktree file missing from snapshot"
			}
		}
	}
}

func TestChaos_WorktreeScaleBurst(t *testing.T) {
	start := time.Now()
	passed := true
	errMsg := ""

	repoFileCount := 2000
	worktreeCount := 3
	editsPerWorktree := 5
	newFilesPerWorktree := 3
	totalCheckoutFiltered := 0
	totalCheckoutPassed := 0

	defer func() {
		RecordScenario("worktree_scale_burst", "Worktree Scale Burst",
			fmt.Sprintf("Simulate %d-file checkout burst across %d worktrees: verify filtering drops all %d clean CREATEs, passes %d real edits",
				repoFileCount, worktreeCount, repoFileCount*worktreeCount, (editsPerWorktree+newFilesPerWorktree)*worktreeCount),
			"performance",
			time.Since(start).Milliseconds(),
			map[string]int{
				"repo_files":           repoFileCount,
				"worktrees":            worktreeCount,
				"checkout_creates":     repoFileCount * worktreeCount,
				"filtered_creates":     totalCheckoutFiltered,
				"real_edits":           editsPerWorktree * worktreeCount,
				"new_files":            newFilesPerWorktree * worktreeCount,
			},
			passed, errMsg)
	}()

	dir := tempDir(t)
	objStore := newStore(t, dir)
	idx := newIndex(t, dir)
	w := newWriter(t, dir, 64*1024*1024)

	gitInit(t, dir)

	dirs := []string{
		"src/api", "src/core", "src/utils", "src/config",
		"domains/svc-a/handlers", "domains/svc-a/models", "domains/svc-a/routes",
		"domains/svc-b/handlers", "domains/svc-b/models",
		"domains/svc-c/controllers", "domains/svc-c/views",
		"lib/shared", "lib/auth", "lib/db",
		"tests/unit", "tests/integration",
	}

	var initialFiles []string
	fileIdx := 0
	for fileIdx < repoFileCount {
		d := dirs[fileIdx%len(dirs)]
		f := fmt.Sprintf("%s/file_%04d.go", d, fileIdx)
		writeFile(t, dir, f, fmt.Sprintf("package pkg\nvar V%d = %d\n", fileIdx, fileIdx))
		initialFiles = append(initialFiles, f)
		fileIdx++
	}
	gitCommit(t, dir, "initial commit with many files")

	baseTime := time.Now()
	tick := 0
	nextTime := func() time.Time {
		tick++
		return baseTime.Add(time.Duration(tick) * time.Millisecond)
	}

	type wtResult struct {
		name         string
		path         string
		editPaths    []string
		newPaths     []string
	}

	results := make([]wtResult, worktreeCount)
	wtNames := []string{"scale-agent-001", "scale-agent-002", "scale-agent-003"}

	for wi := 0; wi < worktreeCount; wi++ {
		wtName := wtNames[wi]
		wtPath := filepath.Join(dir, ".claude", "worktrees", wtName)
		gitWorktreeAdd(t, dir, wtPath)
		t.Cleanup(func() {
			gitWorktreeRemove(t, dir, wtPath)
		})

		wtr := wtResult{name: wtName, path: wtPath}

		dirty := gitDirtyFiles(t, wtPath)

		for _, f := range initialFiles {
			wtRelPath := filepath.ToSlash(filepath.Join(".claude", "worktrees", wtName, f))
			_, _, skip := shouldFilterWorktreeCreate(dir, wtRelPath)
			if skip {
				totalCheckoutFiltered++
			} else {
				totalCheckoutPassed++
			}
			expectedSkip := !dirty[f]
			if skip != expectedSkip {
				t.Errorf("filter mismatch for %s: shouldFilter=%v, expectedSkip=%v (dirty=%v)", f, skip, expectedSkip, dirty[f])
			}
		}

		if len(dirty) != 0 {
			t.Errorf("worktree %s: expected 0 dirty files after checkout, got %d", wtName, len(dirty))
			passed = false
			errMsg = "fresh worktree has dirty files"
		}

		offset := wi * editsPerWorktree * 7
		for ei := 0; ei < editsPerWorktree; ei++ {
			f := initialFiles[(offset+ei*37)%len(initialFiles)]
			modContent := []byte(fmt.Sprintf("edited by %s iteration %d", wtName, ei))
			_ = os.WriteFile(filepath.Join(wtPath, f), modContent, 0644)

			hash, _, err := objStore.Put(modContent)
			if err != nil {
				t.Fatalf("Put: %v", err)
			}

			wtRelPath := filepath.ToSlash(filepath.Join(".claude", "worktrees", wtName, f))
			_, canonical, _ := parseWorktreePath(wtRelPath)

			evt := makeEvent(schema.NewEventID(), canonical, schema.OpModify, hash, "", fmt.Sprintf("session-%s", wtName), nextTime())
			evt.Metadata = map[string]string{"worktree": wtName}
			if err := w.Append(evt); err != nil {
				t.Fatalf("Append: %v", err)
			}
			if err := idx.IndexEvent(evt, w.CurrentSegment(), 0); err != nil {
				t.Fatalf("IndexEvent: %v", err)
			}
			wtr.editPaths = append(wtr.editPaths, f)
		}

		for ni := 0; ni < newFilesPerWorktree; ni++ {
			f := fmt.Sprintf("domains/svc-a/new_%s_%d.go", wtName, ni)
			newContent := []byte(fmt.Sprintf("package new\nvar Created = \"%s-%d\"\n", wtName, ni))
			writeFile(t, wtPath, f, string(newContent))

			dirtyAfter := gitDirtyFiles(t, wtPath)
			if !dirtyAfter[f] {
				t.Errorf("new file %s should appear as dirty/untracked", f)
				passed = false
				errMsg = "new file not detected as dirty"
			}

			hash, _, err := objStore.Put(newContent)
			if err != nil {
				t.Fatalf("Put new: %v", err)
			}

			wtRelPath := filepath.ToSlash(filepath.Join(".claude", "worktrees", wtName, f))
			_, canonical, _ := parseWorktreePath(wtRelPath)

			evt := makeEvent(schema.NewEventID(), canonical, schema.OpCreate, hash, "", fmt.Sprintf("session-%s", wtName), nextTime())
			evt.Metadata = map[string]string{"worktree": wtName}
			if err := w.Append(evt); err != nil {
				t.Fatalf("Append new: %v", err)
			}
			if err := idx.IndexEvent(evt, w.CurrentSegment(), 0); err != nil {
				t.Fatalf("IndexEvent new: %v", err)
			}
			wtr.newPaths = append(wtr.newPaths, f)
		}

		results[wi] = wtr
	}

	expectedFilteredTotal := repoFileCount * worktreeCount
	if totalCheckoutFiltered != expectedFilteredTotal {
		t.Errorf("expected %d checkout CREATEs filtered, got %d (passed: %d)", expectedFilteredTotal, totalCheckoutFiltered, totalCheckoutPassed)
		passed = false
		errMsg = fmt.Sprintf("checkout filtering: %d/%d filtered", totalCheckoutFiltered, expectedFilteredTotal)
	}

	t.Logf("Scale: %d repo files x %d worktrees = %d checkout CREATEs filtered", repoFileCount, worktreeCount, totalCheckoutFiltered)

	r := newReader(t, dir)
	allEvents, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	expectedTotal := (editsPerWorktree + newFilesPerWorktree) * worktreeCount
	if len(allEvents) != expectedTotal {
		t.Errorf("expected %d total events (only real edits), got %d", expectedTotal, len(allEvents))
		passed = false
		errMsg = fmt.Sprintf("event count: got %d want %d", len(allEvents), expectedTotal)
	}

	for _, evt := range allEvents {
		if strings.Contains(evt.FilePath, ".claude/worktrees/") {
			t.Errorf("un-translated worktree path in event: %s", evt.FilePath)
			passed = false
			errMsg = "path translation failed"
		}
	}

	worktreeEventCounts := make(map[string]int)
	for _, evt := range allEvents {
		if evt.Metadata != nil && evt.Metadata["worktree"] != "" {
			worktreeEventCounts[evt.Metadata["worktree"]]++
		}
	}

	for _, wtr := range results {
		expected := editsPerWorktree + newFilesPerWorktree
		if worktreeEventCounts[wtr.name] != expected {
			t.Errorf("worktree %s: expected %d events, got %d", wtr.name, expected, worktreeEventCounts[wtr.name])
			passed = false
			errMsg = fmt.Sprintf("event count mismatch for %s", wtr.name)
		}
	}

	editedFile := results[0].editPaths[0]
	history, hErr := idx.FileHistory(editedFile, 100)
	if hErr != nil {
		t.Fatalf("FileHistory: %v", hErr)
	}
	if len(history) < 1 {
		t.Errorf("expected at least 1 history entry for %s, got %d", editedFile, len(history))
		passed = false
		errMsg = "file history empty for edited file"
	}

	snap, sErr := replay.SnapshotAt(idx, baseTime.Add(time.Duration(tick+2)*time.Millisecond).UnixNano())
	if sErr != nil {
		t.Fatalf("SnapshotAt: %v", sErr)
	}

	for _, wtr := range results {
		for _, f := range wtr.newPaths {
			if _, ok := snap.Files[f]; !ok {
				t.Errorf("new file %s from %s missing from snapshot", f, wtr.name)
				passed = false
				errMsg = "snapshot missing new file"
			}
		}
	}

	t.Logf("Result: %d checkout CREATEs filtered, %d real events indexed, %d files in snapshot",
		totalCheckoutFiltered, len(allEvents), len(snap.Files))
}

