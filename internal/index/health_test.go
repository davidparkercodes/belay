package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

// ─── CheckIntegrity ─────────────────────────────────────────────────────────

func TestHealthCheckIntegrity_HealthyDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "healthy.db")

	// Open and set up a valid index, then close it
	idx, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := idx.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	idx.Close()

	if err := CheckIntegrity(dbPath); err != nil {
		t.Fatalf("CheckIntegrity on healthy database should pass, got: %v", err)
	}
}

func TestHealthCheckIntegrity_WithData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "withdata.db")
	idx, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	now := time.Now()
	for i := 0; i < 10; i++ {
		ev := makeEvent("evt-health-"+string(rune('a'+i)), "src/file.go", schema.OpModify, "sess-1", now.Add(time.Duration(i)*time.Second))
		if err := idx.IndexEvent(ev, "seg.log", int64(i*100)); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}
	if err := idx.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	idx.Close()

	if err := CheckIntegrity(dbPath); err != nil {
		t.Fatalf("CheckIntegrity on database with data should pass, got: %v", err)
	}
}

func TestHealthCheckIntegrity_EmptyDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "empty.db")
	idx, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	idx.Close()

	if err := CheckIntegrity(dbPath); err != nil {
		t.Fatalf("CheckIntegrity on empty database should pass, got: %v", err)
	}
}

func TestHealthCheckIntegrity_NonexistentFile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "nonexistent", "missing.db")
	err := CheckIntegrity(dbPath)
	if err == nil {
		t.Fatal("expected error for nonexistent database file")
	}
}

func TestHealthCheckIntegrity_CorruptedFile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "corrupt.db")

	// Write garbage data to simulate a corrupted database
	if err := os.WriteFile(dbPath, []byte("this is not a valid sqlite database at all"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := CheckIntegrity(dbPath)
	if err == nil {
		t.Fatal("expected error for corrupted database file")
	}
}

func TestHealthCheckIntegrity_EmptyFile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "empty_file.db")

	if err := os.WriteFile(dbPath, []byte{}, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// SQLite may treat an empty file as a valid (empty) database, so this
	// may or may not return an error depending on the driver. The primary
	// purpose is verifying CheckIntegrity doesn't panic on an empty file.
	_ = CheckIntegrity(dbPath)
}

func TestHealthCheckIntegrity_TruncatedDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "truncated.db")

	idx, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Now()
	for i := 0; i < 5; i++ {
		ev := makeEvent("evt-trunc-"+string(rune('a'+i)), "file.go", schema.OpModify, "", now.Add(time.Duration(i)*time.Second))
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}
	if err := idx.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	idx.Close()

	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) < 200 {
		t.Skip("database file too small to meaningfully truncate")
	}
	// Truncate to half to corrupt the file
	if err := os.WriteFile(dbPath, data[:len(data)/2], 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// The primary purpose is verifying CheckIntegrity doesn't panic on truncated files.
	// Whether it returns an error depends on which SQLite pages were truncated.
	_ = CheckIntegrity(dbPath)
}

func TestHealthCheckIntegrity_DirectoryPath(t *testing.T) {
	dirPath := t.TempDir()

	err := CheckIntegrity(dirPath)
	if err == nil {
		t.Fatal("expected error when path is a directory, not a file")
	}
}
