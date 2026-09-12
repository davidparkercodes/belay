package index

import (
	"database/sql"
	"errors"
	"fmt"
)

// MetaLastCompactionAt is the meta key holding the RFC3339 timestamp of the last compaction run.
const MetaLastCompactionAt = "last_compaction_at"

// MetaLastCompactionResult is the meta key holding the JSON summary of the last compaction run.
const MetaLastCompactionResult = "last_compaction_result"

// GetMeta returns the value stored under key, or "" when the key is absent.
func (idx *Index) GetMeta(key string) (string, error) {
	var value string
	err := idx.db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get meta %s: %w", key, err)
	}
	return value, nil
}

// SetMeta upserts a key/value pair in the meta table.
func (idx *Index) SetMeta(key, value string) error {
	_, err := idx.db.Exec(
		"INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value)
	if err != nil {
		return fmt.Errorf("set meta %s: %w", key, err)
	}
	return nil
}

// EventIDsBySegment returns the set of event IDs whose segment_file matches segmentFile.
func (idx *Index) EventIDsBySegment(segmentFile string) (map[string]bool, error) {
	rows, err := idx.db.Query("SELECT event_id FROM events WHERE segment_file = ?", segmentFile)
	if err != nil {
		return nil, fmt.Errorf("query events by segment: %w", err)
	}
	defer rows.Close()

	ids := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan event id: %w", err)
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

// CountEventsBySegment returns how many indexed events point at segmentFile.
func (idx *Index) CountEventsBySegment(segmentFile string) (int64, error) {
	var n int64
	if err := idx.db.QueryRow("SELECT COUNT(*) FROM events WHERE segment_file = ?", segmentFile).Scan(&n); err != nil {
		return 0, fmt.Errorf("count events by segment: %w", err)
	}
	return n, nil
}

// DeleteMeta removes a key from the meta table.
func (idx *Index) DeleteMeta(key string) error {
	if _, err := idx.db.Exec("DELETE FROM meta WHERE key = ?", key); err != nil {
		return fmt.Errorf("delete meta %s: %w", key, err)
	}
	return nil
}

// SegmentFiles returns the distinct segment_file values referenced by indexed events.
func (idx *Index) SegmentFiles() ([]string, error) {
	rows, err := idx.db.Query("SELECT DISTINCT segment_file FROM events WHERE segment_file != ''")
	if err != nil {
		return nil, fmt.Errorf("query segment files: %w", err)
	}
	defer rows.Close()

	var files []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, fmt.Errorf("scan segment file: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// UpdateSegmentOffsets rewrites segment_file/segment_offset for the given events in one transaction.
func (idx *Index) UpdateSegmentOffsets(segmentFile string, offsets map[string]int64) error {
	if len(offsets) == 0 {
		return nil
	}

	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare("UPDATE events SET segment_file = ?, segment_offset = ? WHERE event_id = ?")
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for id, off := range offsets {
		if _, err := stmt.Exec(segmentFile, off, id); err != nil {
			return fmt.Errorf("update offset %s: %w", id, err)
		}
	}

	return tx.Commit()
}

// FreeSpace reports the number of free pages and total pages in the database file.
func (idx *Index) FreeSpace() (freePages, totalPages int64, err error) {
	if err = idx.db.QueryRow("PRAGMA freelist_count").Scan(&freePages); err != nil {
		return 0, 0, fmt.Errorf("freelist_count: %w", err)
	}
	if err = idx.db.QueryRow("PRAGMA page_count").Scan(&totalPages); err != nil {
		return 0, 0, fmt.Errorf("page_count: %w", err)
	}
	return freePages, totalPages, nil
}

// vacuumMinFreePages is the free-page floor (~10 MB at 4 KB pages) below which VACUUM is skipped.
const vacuumMinFreePages = 2500

// MaybeVacuum runs VACUUM when at least a quarter of the database file is free space
// (and the free space is large enough to be worth rewriting the file). Returns true if it ran.
func (idx *Index) MaybeVacuum() (bool, error) {
	free, total, err := idx.FreeSpace()
	if err != nil {
		return false, err
	}
	if total == 0 || free < vacuumMinFreePages || free*4 < total {
		return false, nil
	}
	if _, err := idx.db.Exec("VACUUM"); err != nil {
		return false, fmt.Errorf("vacuum: %w", err)
	}
	return true, nil
}
