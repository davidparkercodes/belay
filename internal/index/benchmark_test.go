package index

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

func openBenchIndex(b *testing.B) *Index {
	b.Helper()
	dbPath := filepath.Join(b.TempDir(), "bench.db")
	idx, err := Open(dbPath)
	if err != nil {
		b.Fatalf("Open index: %v", err)
	}
	b.Cleanup(func() { idx.Close() })
	return idx
}

func benchEvent(id, filePath string, op schema.Operation, sessionID string, ts time.Time) *schema.Event {
	return &schema.Event{
		EventID:               id,
		TimestampNano:         ts.UnixNano(),
		FilePath:              filePath,
		Op:                    op,
		ContentHash:           "abc123def456abc123def456abc123def456abc123def456abc123def456abcd",
		PreviousHash:          "def456abc123def456abc123def456abc123def456abc123def456abc123defg",
		ContentSize:           1024,
		SessionID:             sessionID,
		Attribution:           schema.AttrPID,
		AttributionConfidence: 0.95,
	}
}

// ─── BenchmarkIndexEvent ────────────────────────────────────────────────────

func BenchmarkIndexEvent(b *testing.B) {
	idx := openBenchIndex(b)
	now := time.Now()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ev := benchEvent(
			fmt.Sprintf("evt-bench-%d", i),
			"src/main.go",
			schema.OpModify,
			"sess-1",
			now.Add(time.Duration(i)*time.Microsecond),
		)
		if err := idx.IndexEvent(ev, "seg001.log", int64(i*256)); err != nil {
			b.Fatalf("IndexEvent: %v", err)
		}
	}
}

// ─── BenchmarkIndexEventBatch ───────────────────────────────────────────────

func BenchmarkIndexEventBatch(b *testing.B) {
	for _, batchSize := range []int{100, 1000} {
		b.Run(fmt.Sprintf("batch_%d", batchSize), func(b *testing.B) {
			idx := openBenchIndex(b)
			now := time.Now()

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				batch := make([]struct {
					Event         *schema.Event
					SegmentFile   string
					SegmentOffset int64
				}, batchSize)

				for j := 0; j < batchSize; j++ {
					seqNum := i*batchSize + j
					batch[j] = struct {
						Event         *schema.Event
						SegmentFile   string
						SegmentOffset int64
					}{
						Event: benchEvent(
							fmt.Sprintf("evt-batch-%d", seqNum),
							fmt.Sprintf("src/file%d.go", j%50),
							schema.OpModify,
							fmt.Sprintf("sess-%d", j%5),
							now.Add(time.Duration(seqNum)*time.Microsecond),
						),
						SegmentFile:   "seg001.log",
						SegmentOffset: int64(seqNum * 256),
					}
				}

				if err := idx.IndexEventBatch(batch); err != nil {
					b.Fatalf("IndexEventBatch: %v", err)
				}
			}
		})
	}
}

// ─── BenchmarkQueryEvents ───────────────────────────────────────────────────

func BenchmarkQueryEvents(b *testing.B) {
	idx := openBenchIndex(b)
	now := time.Now()

	// Seed index with events across multiple files and sessions
	const numEvents = 5000
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
			Event: benchEvent(
				fmt.Sprintf("evt-q-%d", i),
				fmt.Sprintf("src/pkg%d/file%d.go", i%10, i%100),
				schema.OpModify,
				fmt.Sprintf("sess-%d", i%10),
				now.Add(time.Duration(i)*time.Second),
			),
			SegmentFile:   "seg001.log",
			SegmentOffset: int64(i * 256),
		}
	}
	if err := idx.IndexEventBatch(batch); err != nil {
		b.Fatalf("seed IndexEventBatch: %v", err)
	}

	b.Run("by_file", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := idx.QueryEvents(&Query{
				FilePaths: []string{"src/pkg0/file0.go"},
			})
			if err != nil {
				b.Fatalf("QueryEvents: %v", err)
			}
		}
	})

	b.Run("by_session", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := idx.QueryEvents(&Query{
				Sessions: []string{"sess-0"},
			})
			if err != nil {
				b.Fatalf("QueryEvents: %v", err)
			}
		}
	})

	b.Run("by_time_range", func(b *testing.B) {
		since := now.Add(1000 * time.Second).UnixNano()
		until := now.Add(2000 * time.Second).UnixNano()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := idx.QueryEvents(&Query{
				Since: since,
				Until: until,
			})
			if err != nil {
				b.Fatalf("QueryEvents: %v", err)
			}
		}
	})

	b.Run("combined_filters", func(b *testing.B) {
		since := now.Add(500 * time.Second).UnixNano()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := idx.QueryEvents(&Query{
				Since:      since,
				Sessions:   []string{"sess-0", "sess-1"},
				Operations: []string{"MODIFY"},
				Limit:      100,
			})
			if err != nil {
				b.Fatalf("QueryEvents: %v", err)
			}
		}
	})
}

// ─── BenchmarkFileHistory ───────────────────────────────────────────────────

func BenchmarkFileHistory(b *testing.B) {
	idx := openBenchIndex(b)
	now := time.Now()

	// Seed a single file with many events
	const numEvents = 2000
	for i := 0; i < numEvents; i++ {
		ev := benchEvent(
			fmt.Sprintf("evt-fh-%d", i),
			"src/main.go",
			schema.OpModify,
			"sess-1",
			now.Add(time.Duration(i)*time.Second),
		)
		if err := idx.IndexEvent(ev, "seg.log", int64(i*256)); err != nil {
			b.Fatalf("IndexEvent: %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := idx.FileHistory("src/main.go", 100)
		if err != nil {
			b.Fatalf("FileHistory: %v", err)
		}
	}
}

// ─── BenchmarkCountEvents ───────────────────────────────────────────────────

func BenchmarkCountEvents(b *testing.B) {
	idx := openBenchIndex(b)
	now := time.Now()

	// Seed with events
	const numEvents = 5000
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
			Event: benchEvent(
				fmt.Sprintf("evt-cnt-%d", i),
				fmt.Sprintf("src/file%d.go", i%100),
				schema.OpModify,
				fmt.Sprintf("sess-%d", i%10),
				now.Add(time.Duration(i)*time.Second),
			),
			SegmentFile:   "seg.log",
			SegmentOffset: int64(i * 256),
		}
	}
	if err := idx.IndexEventBatch(batch); err != nil {
		b.Fatalf("seed IndexEventBatch: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := idx.CountEvents()
		if err != nil {
			b.Fatalf("CountEvents: %v", err)
		}
	}
}
