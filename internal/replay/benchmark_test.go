package replay

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"
)

// benchFixture holds a temp index and object store for benchmark setup.
type benchFixture struct {
	b        *testing.B
	idx      *index.Index
	objStore *store.Store
	eventSeq int
	timeBase int64
}

func newBenchFixture(b *testing.B) *benchFixture {
	b.Helper()
	tmpDir := b.TempDir()

	dbPath := filepath.Join(tmpDir, "bench-index.db")
	idx, err := index.Open(dbPath)
	if err != nil {
		b.Fatalf("open index: %v", err)
	}
	b.Cleanup(func() { idx.Close() })

	objDir := filepath.Join(tmpDir, "objects")
	objStore, err := store.NewStore(objDir, false)
	if err != nil {
		b.Fatalf("create store: %v", err)
	}
	b.Cleanup(func() { objStore.Close() })

	return &benchFixture{
		b:        b,
		idx:      idx,
		objStore: objStore,
		timeBase: 1700000000000000000,
	}
}

func (f *benchFixture) putContent(content string) string {
	f.b.Helper()
	hash, _, err := f.objStore.Put([]byte(content))
	if err != nil {
		f.b.Fatalf("put content: %v", err)
	}
	return hash
}

func (f *benchFixture) addEvent(sessionID, filePath string, op schema.Operation, contentHash, previousHash string, contentSize int64) {
	f.b.Helper()
	f.eventSeq++
	ev := &schema.Event{
		EventID:       fmt.Sprintf("bench-evt-%d", f.eventSeq),
		TimestampNano: f.timeBase + int64(f.eventSeq)*1_000_000_000,
		FilePath:      filePath,
		Op:            op,
		ContentHash:   contentHash,
		PreviousHash:  previousHash,
		ContentSize:   contentSize,
		SessionID:     sessionID,
		Attribution:   schema.AttrPID,
		AttributionConfidence: 0.9,
	}
	if err := f.idx.IndexEvent(ev, "seg.log", int64(f.eventSeq*256)); err != nil {
		f.b.Fatalf("IndexEvent: %v", err)
	}
}

// ─── BenchmarkSnapshot ──────────────────────────────────────────────────────

func BenchmarkSnapshot(b *testing.B) {
	f := newBenchFixture(b)

	// Seed: create 200 files, then modify each a few times
	const numFiles = 200
	const modifiesPerFile = 5

	for i := 0; i < numFiles; i++ {
		filePath := fmt.Sprintf("src/pkg%d/file%d.go", i/20, i)
		content := fmt.Sprintf("initial content of file %d", i)
		hash := f.putContent(content)
		f.addEvent("sess-1", filePath, schema.OpCreate, hash, "", int64(len(content)))

		for j := 0; j < modifiesPerFile; j++ {
			prevHash := hash
			content = fmt.Sprintf("modified content of file %d version %d", i, j)
			hash = f.putContent(content)
			f.addEvent("sess-1", filePath, schema.OpModify, hash, prevHash, int64(len(content)))
		}
	}

	snapshotTime := f.timeBase + int64(f.eventSeq+1)*1_000_000_000

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := SnapshotAt(f.idx, snapshotTime)
		if err != nil {
			b.Fatalf("SnapshotAt: %v", err)
		}
	}
}

// ─── BenchmarkUnifiedDiff ───────────────────────────────────────────────────

func BenchmarkUnifiedDiff(b *testing.B) {
	// Generate two versions of a file with realistic diffs
	makeLines := func(n int, prefix string) string {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&sb, "%s line %d: some code content here\n", prefix, i)
		}
		return sb.String()
	}

	sizes := []struct {
		name     string
		numLines int
	}{
		{"50_lines", 50},
		{"200_lines", 200},
		{"1000_lines", 1000},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			oldText := makeLines(sz.numLines, "old")
			// Modify ~10% of lines
			lines := strings.Split(oldText, "\n")
			for i := 0; i < len(lines); i += 10 {
				if i < len(lines) {
					lines[i] = fmt.Sprintf("new line %d: changed content here", i)
				}
			}
			newText := strings.Join(lines, "\n")

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_ = unifiedDiff("a/file.go", "b/file.go", oldText, newText)
			}
		})
	}
}
