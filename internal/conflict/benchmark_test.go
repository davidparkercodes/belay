package conflict

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
)

func openBenchIndex(b *testing.B) *index.Index {
	b.Helper()
	dbPath := filepath.Join(b.TempDir(), "bench.db")
	idx, err := index.Open(dbPath)
	if err != nil {
		b.Fatalf("Open index: %v", err)
	}
	b.Cleanup(func() { idx.Close() })
	return idx
}

func seedConflictEvents(b *testing.B, idx *index.Index, numEvents int, numFiles int, numSessions int, base time.Time) {
	b.Helper()

	batch := make([]struct {
		Event         *schema.Event
		SegmentFile   string
		SegmentOffset int64
	}, numEvents)

	for i := 0; i < numEvents; i++ {
		batch[i] = struct {
			Event         *schema.Event
			SegmentFile   string
			SegmentOffset int64
		}{
			Event: &schema.Event{
				EventID:               fmt.Sprintf("evt-cf-%d", i),
				TimestampNano:         base.Add(time.Duration(i) * time.Second).UnixNano(),
				FilePath:              fmt.Sprintf("src/file%d.go", i%numFiles),
				Op:                    schema.OpModify,
				ContentHash:           fmt.Sprintf("hash-%d", i),
				SessionID:             fmt.Sprintf("sess-%d", i%numSessions),
				Attribution:           schema.AttrPID,
				AttributionConfidence: 0.9,
			},
			SegmentFile:   "seg.log",
			SegmentOffset: int64(i * 256),
		}
	}

	if err := idx.IndexEventBatch(batch); err != nil {
		b.Fatalf("seed IndexEventBatch: %v", err)
	}
}

// ─── BenchmarkDetectSince ───────────────────────────────────────────────────

func BenchmarkDetectSince(b *testing.B) {
	for _, numEvents := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("events_%d", numEvents), func(b *testing.B) {
			idx := openBenchIndex(b)
			base := time.Now().Add(-time.Duration(numEvents) * time.Second)

			// Create events across 20 files and 5 sessions to generate realistic conflict patterns
			seedConflictEvents(b, idx, numEvents, 20, 5, base)

			d := NewDetector(idx, 60*time.Second)

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_, err := d.DetectSince(base.Add(-time.Second))
				if err != nil {
					b.Fatalf("DetectSince: %v", err)
				}
			}
		})
	}
}

// ─── BenchmarkDetectForFile ─────────────────────────────────────────────────

func BenchmarkDetectForFile(b *testing.B) {
	idx := openBenchIndex(b)
	base := time.Now().Add(-5000 * time.Second)

	// Seed 5000 events across 20 files and 5 sessions
	seedConflictEvents(b, idx, 5000, 20, 5, base)

	d := NewDetector(idx, 60*time.Second)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := d.DetectForFile("src/file0.go", base.Add(-time.Second))
		if err != nil {
			b.Fatalf("DetectForFile: %v", err)
		}
	}
}
