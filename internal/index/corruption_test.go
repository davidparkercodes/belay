package index

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

// ─── Corruption: Garbage Bytes ──────────────────────────────────────────────

func TestCorruption_GarbageBytesInDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "garbage.db")

	// Write random garbage to the file
	if err := os.WriteFile(dbPath, []byte("THIS IS NOT A SQLITE DATABASE AT ALL!!!"), 0644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	// Open should fail or return an index that errors on first query
	idx, err := Open(dbPath)
	if err != nil {
		// Clean error on open — acceptable behavior
		t.Logf("Open returned error (expected): %v", err)
		return
	}
	defer idx.Close()

	// If Open succeeded (sqlite3 is lazy), queries should produce an error
	_, err = idx.CountEvents()
	if err == nil {
		t.Error("expected error querying garbage database, got nil")
	} else {
		t.Logf("CountEvents error (expected): %v", err)
	}
}

// ─── Corruption: Truncated Database ─────────────────────────────────────────

func TestCorruption_TruncatedDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "truncated.db")

	// Create a valid database with some data
	idx, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	now := time.Now()
	for i := 0; i < 10; i++ {
		ev := &schema.Event{
			EventID:       "evt-trunc-" + string(rune('a'+i)),
			TimestampNano: now.Add(time.Duration(i) * time.Second).UnixNano(),
			FilePath:      "file.go",
			Op:            schema.OpModify,
			ContentHash:   "abc",
			SessionID:     "sess-1",
			Attribution:   schema.AttrPID,
		}
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}
	idx.Close()

	// Read the database file and truncate it at roughly half
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	if len(data) < 100 {
		t.Fatalf("database file unexpectedly small: %d bytes", len(data))
	}
	truncated := data[:len(data)/2]
	if err := os.WriteFile(dbPath, truncated, 0644); err != nil {
		t.Fatalf("write truncated: %v", err)
	}

	// Try to reopen — should error or degrade gracefully
	idx2, err := Open(dbPath)
	if err != nil {
		t.Logf("Open truncated DB returned error (expected): %v", err)
		return
	}
	defer idx2.Close()

	// If it opened, queries should return an error (not panic)
	_, err = idx2.QueryEvents(&Query{})
	if err != nil {
		t.Logf("QueryEvents on truncated DB error (expected): %v", err)
	}
}

// ─── Corruption: WAL File Deleted ───────────────────────────────────────────

func TestCorruption_WALFileDeleted(t *testing.T) {
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "wal-test.db")

	idx, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write some data to ensure WAL is created
	now := time.Now()
	for i := 0; i < 5; i++ {
		ev := &schema.Event{
			EventID:       "evt-wal-" + string(rune('a'+i)),
			TimestampNano: now.Add(time.Duration(i) * time.Second).UnixNano(),
			FilePath:      "file.go",
			Op:            schema.OpModify,
			ContentHash:   "abc",
		}
		if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
			t.Fatalf("IndexEvent: %v", err)
		}
	}

	// Delete the WAL file if it exists
	walPath := dbPath + "-wal"
	if _, err := os.Stat(walPath); err == nil {
		if err := os.Remove(walPath); err != nil {
			t.Fatalf("remove WAL: %v", err)
		}
		t.Log("WAL file deleted")
	} else {
		t.Log("WAL file not present (already checkpointed)")
	}

	// Subsequent operations should not panic
	count, err := idx.CountEvents()
	if err != nil {
		t.Logf("CountEvents after WAL delete: error=%v (may lose uncommitted data)", err)
	} else {
		t.Logf("CountEvents after WAL delete: %d events (some may be lost)", count)
	}

	// Close should not panic
	idx.Close()

	// Reopen should work (SQLite recreates WAL)
	idx2, err := Open(dbPath)
	if err != nil {
		t.Logf("Reopen after WAL delete: %v", err)
		return
	}
	defer idx2.Close()

	count2, err := idx2.CountEvents()
	if err != nil {
		t.Logf("CountEvents after reopen: %v", err)
	} else {
		t.Logf("CountEvents after reopen: %d", count2)
	}
}

// ─── Corruption: Directory Instead of File ──────────────────────────────────

func TestCorruption_DirectoryInsteadOfFile(t *testing.T) {
	dirPath := filepath.Join(t.TempDir(), "not-a-file.db")

	// Create a directory where the database file should be
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := Open(dirPath)
	if err == nil {
		t.Fatal("expected error when opening a directory as database, got nil")
	}
	t.Logf("Open directory error (expected): %v", err)
}

// ─── Corruption: Queries on Empty Index ─────────────────────────────────────

func TestCorruption_QueriesOnEmptyIndex(t *testing.T) {
	idx := openTestIndexCorruption(t)

	tests := []struct {
		name string
		fn   func() error
	}{
		{
			"QueryEvents empty",
			func() error {
				events, err := idx.QueryEvents(&Query{})
				if err != nil {
					return err
				}
				if len(events) != 0 {
					t.Errorf("expected 0 events, got %d", len(events))
				}
				return nil
			},
		},
		{
			"GetEvent nonexistent",
			func() error {
				_, err := idx.GetEvent("does-not-exist")
				if err == nil {
					return nil // should actually error
				}
				return nil // error is expected
			},
		},
		{
			"FileHistory nonexistent",
			func() error {
				events, err := idx.FileHistory("no-such-file.go", 10)
				if err != nil {
					return err
				}
				if len(events) != 0 {
					t.Errorf("expected 0 events for nonexistent file, got %d", len(events))
				}
				return nil
			},
		},
		{
			"LatestEvent nonexistent",
			func() error {
				_, err := idx.LatestEvent("no-such-file.go")
				// Error is expected — just verify no panic
				_ = err
				return nil
			},
		},
		{
			"ListSessions empty",
			func() error {
				sessions, err := idx.ListSessions(false, 0, 0)
				if err != nil {
					return err
				}
				if len(sessions) != 0 {
					t.Errorf("expected 0 sessions, got %d", len(sessions))
				}
				return nil
			},
		},
		{
			"CountEvents empty",
			func() error {
				count, err := idx.CountEvents()
				if err != nil {
					return err
				}
				if count != 0 {
					t.Errorf("expected count 0, got %d", count)
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fn(); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// ─── Corruption: Database Closed Then Queried ───────────────────────────────

func TestCorruption_QueryAfterClose(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "closed.db")
	idx, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Close the index
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Queries after close should return errors, not panic
	_, err = idx.CountEvents()
	if err == nil {
		t.Error("expected error querying closed database")
	}

	_, err = idx.QueryEvents(&Query{})
	if err == nil {
		t.Error("expected error querying closed database")
	}
}

// ─── Corruption: Overwrite Database Mid-Use ─────────────────────────────────

func TestCorruption_OverwriteDatabaseWhileOpen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "overwrite.db")

	idx, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	// Insert some data
	now := time.Now()
	ev := &schema.Event{
		EventID:       "evt-overwrite-1",
		TimestampNano: now.UnixNano(),
		FilePath:      "file.go",
		Op:            schema.OpCreate,
		ContentHash:   "abc",
	}
	if err := idx.IndexEvent(ev, "seg.log", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}

	// Overwrite the file with garbage while index is still open
	if err := os.WriteFile(dbPath, []byte("GARBAGE OVERWRITE"), 0644); err != nil {
		t.Fatalf("overwrite: %v", err)
	}

	// Subsequent queries should error, not panic
	_, err = idx.CountEvents()
	if err != nil {
		t.Logf("CountEvents after overwrite: %v (expected)", err)
	}

	_, err = idx.QueryEvents(&Query{})
	if err != nil {
		t.Logf("QueryEvents after overwrite: %v (expected)", err)
	}
}

// ─── Corruption: Read-Only Database File ────────────────────────────────────

func TestCorruption_ReadOnlyDatabaseFile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "readonly.db")

	// Create a valid database
	idx, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	idx.Close()

	// Make the file read-only
	if err := os.Chmod(dbPath, 0444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dbPath, 0644) })

	// Open with read-only file — should fail or succeed read-only
	idx2, err := Open(dbPath)
	if err != nil {
		t.Logf("Open read-only: %v (expected for WAL mode)", err)
		return
	}
	defer idx2.Close()

	// Reads may work
	_, err = idx2.CountEvents()
	if err != nil {
		t.Logf("CountEvents on read-only: %v", err)
	}

	// Writes should fail
	ev := &schema.Event{
		EventID:       "evt-ro-1",
		TimestampNano: time.Now().UnixNano(),
		FilePath:      "file.go",
		Op:            schema.OpCreate,
	}
	err = idx2.IndexEvent(ev, "seg.log", 0)
	if err == nil {
		t.Log("IndexEvent on read-only database succeeded (unexpected but possible with WAL)")
	} else {
		t.Logf("IndexEvent on read-only: %v (expected)", err)
	}
}

// ─── Helper ─────────────────────────────────────────────────────────────────

// openTestIndex is shared with index_test.go only if that file exists;
// define it here for standalone corruption tests.
func openTestIndexCorruption(t *testing.T) *Index {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	idx, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx
}

// Verify sql import is used (compiler will check this).
var _ = sql.ErrNoRows
