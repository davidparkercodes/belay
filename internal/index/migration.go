package index

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const (
	MetaStoreMigrationPhase   = "store_migration_phase"
	MetaStoreMigrationTotal   = "store_migration_total"
	MetaStoreMigrationCorrupt = "store_migration_corrupt"

	MigrationStatusCopied    = "copied"
	MigrationStatusCorrupt   = "corrupt"
	MigrationStatusTombstone = "tombstone"

	migrationTable = "store_migration"
)

func (idx *Index) MigrationTableExists() bool {
	var n int
	err := idx.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", migrationTable).Scan(&n)
	return err == nil && n > 0
}

func (idx *Index) migrationProtectedClause() string {
	if !idx.MigrationTableExists() {
		return ""
	}
	return "\n\t\tUNION\n\t\tSELECT DISTINCT new_id FROM " + migrationTable + " WHERE new_id != ''" +
		"\n\t\tUNION\n\t\tSELECT DISTINCT old_id FROM " + migrationTable + " WHERE status = '" + MigrationStatusCopied + "'"
}

func (idx *Index) DeleteMigration(oldID string) error {
	if !idx.MigrationTableExists() {
		return nil
	}
	if _, err := idx.db.Exec("DELETE FROM "+migrationTable+" WHERE old_id = ?", oldID); err != nil {
		return fmt.Errorf("delete migration entry %s: %w", oldID, err)
	}
	return nil
}

// MigrationRemapTable returns old id -> replacement for every row the remap applies:
// verified copies and tombstones for legacy ids whose object is gone.
func (idx *Index) MigrationRemapTable() (map[string]string, error) {
	out := make(map[string]string)
	if !idx.MigrationTableExists() {
		return out, nil
	}
	rows, err := idx.db.Query("SELECT old_id, new_id FROM "+migrationTable+" WHERE status IN (?, ?) AND new_id != ''",
		MigrationStatusCopied, MigrationStatusTombstone)
	if err != nil {
		return nil, fmt.Errorf("query migration remap: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var o, n string
		if err := rows.Scan(&o, &n); err != nil {
			return nil, err
		}
		out[o] = n
	}
	return out, rows.Err()
}

// MigrationKnownIDs returns every id the migration table mentions, old or new.
func (idx *Index) MigrationKnownIDs() (map[string]bool, error) {
	out := make(map[string]bool)
	if !idx.MigrationTableExists() {
		return out, nil
	}
	rows, err := idx.db.Query("SELECT old_id, new_id FROM " + migrationTable)
	if err != nil {
		return nil, fmt.Errorf("query migration ids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var o, n string
		if err := rows.Scan(&o, &n); err != nil {
			return nil, err
		}
		out[o] = true
		if n != "" {
			out[n] = true
		}
	}
	return out, rows.Err()
}

// EventContentIDs returns every distinct content id the events table references.
func (idx *Index) EventContentIDs() (map[string]bool, error) {
	rows, err := idx.db.Query(`SELECT content_hash FROM events WHERE content_hash != ''
		UNION SELECT previous_hash FROM events WHERE previous_hash != ''`)
	if err != nil {
		return nil, fmt.Errorf("query event content ids: %w", err)
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out[h] = true
	}
	return out, rows.Err()
}

// SegmentContentIDs returns every distinct content id in the event-log segments outside skip.
func SegmentContentIDs(eventsDir string, skip map[string]bool) (map[string]bool, error) {
	segments, err := listSegmentFiles(eventsDir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool)
	for _, seg := range segments {
		if skip[seg] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(eventsDir, seg))
		if err != nil {
			return nil, fmt.Errorf("read segment %s: %w", seg, err)
		}
		frames, _ := scanFrames(data)
		for _, f := range frames {
			if f.Event.ContentHash != "" {
				out[f.Event.ContentHash] = true
			}
			if f.Event.PreviousHash != "" {
				out[f.Event.PreviousHash] = true
			}
		}
	}
	return out, nil
}

func (idx *Index) EnsureMigrationTable() error {
	_, err := idx.db.Exec(`CREATE TABLE IF NOT EXISTS ` + migrationTable + ` (
		old_id TEXT PRIMARY KEY,
		new_id TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	return nil
}

func (idx *Index) RecordMigration(oldID, newID, status string) error {
	_, err := idx.db.Exec(
		"INSERT INTO "+migrationTable+" (old_id, new_id, status) VALUES (?, ?, ?) ON CONFLICT(old_id) DO UPDATE SET new_id = excluded.new_id, status = excluded.status",
		oldID, newID, status)
	if err != nil {
		return fmt.Errorf("record migration %s: %w", oldID, err)
	}
	return nil
}

func (idx *Index) MigrationEntry(oldID string) (newID, status string, ok bool, err error) {
	if !idx.MigrationTableExists() {
		return "", "", false, nil
	}
	err = idx.db.QueryRow("SELECT new_id, status FROM "+migrationTable+" WHERE old_id = ?", oldID).Scan(&newID, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("migration entry %s: %w", oldID, err)
	}
	return newID, status, true, nil
}

// MigrationMapping returns old id -> new id for every verified copy.
func (idx *Index) MigrationMapping() (map[string]string, error) {
	out := make(map[string]string)
	if !idx.MigrationTableExists() {
		return out, nil
	}
	rows, err := idx.db.Query("SELECT old_id, new_id FROM "+migrationTable+" WHERE status = ?", MigrationStatusCopied)
	if err != nil {
		return nil, fmt.Errorf("query migration mapping: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var o, n string
		if err := rows.Scan(&o, &n); err != nil {
			return nil, err
		}
		out[o] = n
	}
	return out, rows.Err()
}

func (idx *Index) MigrationIDs(status string) ([]string, error) {
	if !idx.MigrationTableExists() {
		return nil, nil
	}
	rows, err := idx.db.Query("SELECT old_id FROM "+migrationTable+" WHERE status = ? ORDER BY old_id", status)
	if err != nil {
		return nil, fmt.Errorf("query migration ids: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (idx *Index) MigrationCounts() (copied, corrupt int, err error) {
	if !idx.MigrationTableExists() {
		return 0, 0, nil
	}
	err = idx.db.QueryRow(
		"SELECT COALESCE(SUM(status = ?), 0), COALESCE(SUM(status = ?), 0) FROM "+migrationTable,
		MigrationStatusCopied, MigrationStatusCorrupt).Scan(&copied, &corrupt)
	if err != nil {
		return 0, 0, fmt.Errorf("count migration rows: %w", err)
	}
	return copied, corrupt, nil
}

// ApplyMigrationMapping rewrites every content id column through the mapping and advances
// the phase in the same transaction.
func (idx *Index) ApplyMigrationMapping(nextPhase string) (int64, error) {
	tx, err := idx.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var total int64
	for _, col := range []string{"content_hash", "previous_hash"} {
		res, err := tx.Exec(fmt.Sprintf(
			`UPDATE events SET %[1]s = (SELECT m.new_id FROM %[2]s m WHERE m.old_id = events.%[1]s AND m.status IN (?, ?) AND m.new_id != '')
			 WHERE %[1]s IN (SELECT old_id FROM %[2]s WHERE status IN (?, ?) AND new_id != '')`, col, migrationTable),
			MigrationStatusCopied, MigrationStatusTombstone, MigrationStatusCopied, MigrationStatusTombstone)
		if err != nil {
			return 0, fmt.Errorf("remap %s: %w", col, err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	if _, err := tx.Exec(
		"INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		MetaStoreMigrationPhase, nextPhase); err != nil {
		return 0, fmt.Errorf("set migration phase: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit remap: %w", err)
	}
	return total, nil
}

// FinishMigration drops the mapping table (it holds plaintext SHA-256 ids) and records the
// final phase and the corrupt-object summary atomically.
func (idx *Index) FinishMigration(phase, corruptJSON string) error {
	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	upsert := "INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value"
	if _, err := tx.Exec(upsert, MetaStoreMigrationCorrupt, corruptJSON); err != nil {
		return fmt.Errorf("record corrupt objects: %w", err)
	}
	if _, err := tx.Exec(upsert, MetaStoreMigrationPhase, phase); err != nil {
		return fmt.Errorf("set migration phase: %w", err)
	}
	if _, err := tx.Exec("DROP TABLE IF EXISTS " + migrationTable); err != nil {
		return fmt.Errorf("drop migration table: %w", err)
	}
	return tx.Commit()
}

// ScrubFreePages rewrites the database so remapped and dropped legacy ids do not
// linger in free pages or the WAL.
func (idx *Index) ScrubFreePages() error {
	if _, err := idx.db.Exec("VACUUM"); err != nil {
		return fmt.Errorf("vacuum after migration: %w", err)
	}
	return idx.Checkpoint()
}

// LockSegments takes the same exclusive lock segment compaction uses.
func LockSegments(eventsDir string) (func(), error) {
	return acquireSegmentLock(filepath.Join(eventsDir, ".compact.lock"))
}

type SegmentRemapResult struct {
	SegmentsScanned   int `json:"segments_scanned"`
	SegmentsRewritten int `json:"segments_rewritten"`
	FramesRemapped    int `json:"frames_remapped"`
}

// RemapSegments rewrites content ids inside event-log segments through mapping. Each changed
// segment is written beside the original, re-read and verified, then renamed over it. Bytes
// that do not decode as frames are carried over unchanged. Segments in skip are left alone.
// The caller must hold LockSegments.
func RemapSegments(idx *Index, eventsDir string, mapping map[string]string, skip map[string]bool, beforeRename func(seg string) error) (*SegmentRemapResult, error) {
	result := &SegmentRemapResult{}
	segments, err := listSegmentFiles(eventsDir)
	if err != nil {
		return nil, err
	}
	sort.Strings(segments)
	for _, seg := range segments {
		if skip[seg] {
			continue
		}
		result.SegmentsScanned++
		n, err := remapOneSegment(idx, eventsDir, seg, mapping, beforeRename)
		if err != nil {
			return result, fmt.Errorf("segment %s: %w", seg, err)
		}
		if n > 0 {
			result.SegmentsRewritten++
			result.FramesRemapped += n
		}
	}
	return result, nil
}

type remapExpect struct {
	eventID     string
	contentHash string
	prevHash    string
}

func remapOneSegment(idx *Index, eventsDir, seg string, mapping map[string]string, beforeRename func(string) error) (int, error) {
	segPath := filepath.Join(eventsDir, seg)
	data, err := os.ReadFile(segPath)
	if err != nil {
		return 0, fmt.Errorf("read: %w", err)
	}
	frames, _ := scanFrames(data)

	var out bytes.Buffer
	out.Grow(len(data))
	pos := 0
	changed := 0
	expects := make([]remapExpect, 0, len(frames))
	newOffsets := make(map[string]int64, len(frames))
	for _, f := range frames {
		start := int(f.Offset)
		if start > pos {
			out.Write(data[pos:start])
		}
		ev := f.Event
		newContent, okC := mapping[ev.ContentHash]
		newPrev, okP := mapping[ev.PreviousHash]
		newOffsets[ev.EventID] = int64(out.Len())
		if okC || okP {
			cp := *ev
			if okC {
				cp.ContentHash = newContent
			}
			if okP {
				cp.PreviousHash = newPrev
			}
			frame, err := cp.MarshalBinary()
			if err != nil {
				return 0, fmt.Errorf("marshal remapped frame: %w", err)
			}
			out.Write(frame)
			expects = append(expects, remapExpect{cp.EventID, cp.ContentHash, cp.PreviousHash})
			changed++
		} else {
			out.Write(f.Raw)
			expects = append(expects, remapExpect{ev.EventID, ev.ContentHash, ev.PreviousHash})
		}
		pos = start + len(f.Raw)
	}
	if pos < len(data) {
		out.Write(data[pos:])
	}
	if changed == 0 {
		return 0, nil
	}

	tmpPath := segPath + ".remap.tmp"
	if err := writeSynced(tmpPath, out.Bytes()); err != nil {
		return 0, err
	}
	written, err := os.ReadFile(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return 0, fmt.Errorf("read back: %w", err)
	}
	if err := verifyRemapped(written, expects, mapping); err != nil {
		os.Remove(tmpPath)
		return 0, err
	}
	if beforeRename != nil {
		if err := beforeRename(seg); err != nil {
			return 0, err
		}
	}
	if err := os.Rename(tmpPath, segPath); err != nil {
		os.Remove(tmpPath)
		return 0, fmt.Errorf("rename: %w", err)
	}
	syncDir(eventsDir)

	if len(written) != len(data) {
		live, err := idx.EventIDsBySegment(seg)
		if err != nil {
			return changed, err
		}
		offsets := make(map[string]int64)
		for id, off := range newOffsets {
			if live[id] {
				offsets[id] = off
			}
		}
		if err := idx.UpdateSegmentOffsets(seg, offsets); err != nil {
			return changed, err
		}
	}
	_ = idx.DeleteMeta(segmentCleanKey(seg))
	return changed, nil
}

func verifyRemapped(written []byte, expects []remapExpect, mapping map[string]string) error {
	frames, _ := scanFrames(written)
	if len(frames) != len(expects) {
		return fmt.Errorf("verify rewritten segment: %d frames, want %d", len(frames), len(expects))
	}
	for i, f := range frames {
		e := expects[i]
		if f.Event.EventID != e.eventID || f.Event.ContentHash != e.contentHash || f.Event.PreviousHash != e.prevHash {
			return fmt.Errorf("verify rewritten segment: frame %d does not match (event %s)", i, e.eventID)
		}
		if _, stale := mapping[f.Event.ContentHash]; stale {
			return fmt.Errorf("verify rewritten segment: frame %d still carries an unmapped id", i)
		}
		if _, stale := mapping[f.Event.PreviousHash]; stale {
			return fmt.Errorf("verify rewritten segment: frame %d still carries an unmapped id", i)
		}
	}
	return nil
}

func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return fmt.Errorf("close temp: %w", err)
	}
	return nil
}

// LockFile takes an exclusive, non-blocking lock on path.
func LockFile(path string) (func(), error) {
	return acquireSegmentLock(path)
}
