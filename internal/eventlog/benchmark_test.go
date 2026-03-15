package eventlog

import (
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

func benchEvent(filePath string, op schema.Operation) *schema.Event {
	return &schema.Event{
		EventID:       schema.NewEventID(),
		Version:       schema.SchemaVersion,
		TimestampNano: time.Now().UnixNano(),
		FilePath:      filePath,
		Op:            op,
		ContentHash:   schema.ContentHashForBytes([]byte(filePath)),
		PreviousHash:  "prev-hash-placeholder-64char-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ContentSize:   int64(len(filePath)),
		SessionID:     "bench-session-1",
		Attribution:   schema.AttrPID,
		AttributionConfidence: 0.95,
	}
}

func newBenchWriter(b *testing.B, maxSegmentBytes int64) (*Writer, string) {
	b.Helper()
	dir := b.TempDir()
	w, err := NewWriter(dir, maxSegmentBytes)
	if err != nil {
		b.Fatalf("NewWriter: %v", err)
	}
	b.Cleanup(func() { w.Close() })
	return w, dir
}

// ─── BenchmarkAppend ────────────────────────────────────────────────────────

func BenchmarkAppend(b *testing.B) {
	w, _ := newBenchWriter(b, 100*1024*1024) // 100MB segments to avoid rotation

	ev := benchEvent("src/main.go", schema.OpModify)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ev.EventID = schema.NewEventID()
		ev.TimestampNano = time.Now().UnixNano()
		if err := w.Append(ev); err != nil {
			b.Fatalf("Append: %v", err)
		}
	}
}

// ─── BenchmarkReadSegment ───────────────────────────────────────────────────

func BenchmarkReadSegment(b *testing.B) {
	// Pre-populate a segment with events
	const numEvents = 1000
	w, dir := newBenchWriter(b, 100*1024*1024)

	for i := 0; i < numEvents; i++ {
		ev := benchEvent("src/main.go", schema.OpModify)
		if err := w.Append(ev); err != nil {
			b.Fatalf("Append: %v", err)
		}
	}

	segmentName := w.CurrentSegment()
	w.Close()

	r, err := NewReader(dir)
	if err != nil {
		b.Fatalf("NewReader: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		events, err := r.ReadSegment(segmentName)
		if err != nil {
			b.Fatalf("ReadSegment: %v", err)
		}
		if len(events) != numEvents {
			b.Fatalf("expected %d events, got %d", numEvents, len(events))
		}
	}
}

// ─── BenchmarkAppendConcurrent ──────────────────────────────────────────────

func BenchmarkAppendConcurrent(b *testing.B) {
	w, _ := newBenchWriter(b, 100*1024*1024)

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ev := benchEvent("src/concurrent.go", schema.OpModify)
			if err := w.Append(ev); err != nil {
				b.Errorf("Append: %v", err)
			}
		}
	})
}
