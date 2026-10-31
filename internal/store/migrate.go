package store

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/davidparkercodes/belay/internal/index"
)

const (
	PhaseCopy     = "copy"
	PhaseRemap    = "remap"
	PhaseSegments = "segments"
	PhaseCleanup  = "cleanup"
	PhaseDone     = "done"
)

var (
	ErrMigrationBusy    = errors.New("store migration already running")
	ErrMigrationStopped = errors.New("store migration stopped")
)

// Migrator converts legacy objects to the encrypted format in place. Every step is
// idempotent and progress lives in the index, so a crash at any point resumes cleanly.
type Migrator struct {
	Store     *Store
	Index     *index.Index
	EventsDir string
	LockPath  string
	Logf      func(format string, args ...interface{})

	// Exclusive runs fn while no event can be written. A live daemon also rotates its
	// active segment inside it so every older segment is sealed.
	Exclusive func(fn func() error) error

	// ActiveSegment names the segment a live writer is appending to; it is never rewritten.
	ActiveSegment func() string

	Stop <-chan struct{}

	// Fail is a test hook called at each crash point; a non-nil error aborts the run there.
	Fail func(point string) error
}

type MigrationResult struct {
	Copied        int                       `json:"copied"`
	Corrupt       int                       `json:"corrupt"`
	RowsRemapped  int64                     `json:"rows_remapped"`
	Segments      *index.SegmentRemapResult `json:"segments,omitempty"`
	LegacyDeleted int                       `json:"legacy_deleted"`
}

type MigrationStatus struct {
	Format     int      `json:"format"`
	KeyID      string   `json:"key_id,omitempty"`
	Migration  string   `json:"migration,omitempty"`
	Phase      string   `json:"phase,omitempty"`
	Total      int      `json:"total"`
	Copied     int      `json:"copied"`
	Corrupt    int      `json:"corrupt"`
	CorruptIDs []string `json:"corrupt_ids,omitempty"`
}

// Done reports whether no migration work remains.
func (st MigrationStatus) Done() bool {
	return st.Format == FormatEncrypted && st.Migration != MigrationPending
}

func (m *Migrator) logf(format string, args ...interface{}) {
	if m.Logf != nil {
		m.Logf(format, args...)
	}
}

func (m *Migrator) fail(point string) error {
	if m.Fail == nil {
		return nil
	}
	return m.Fail(point)
}

func (m *Migrator) exclusive(fn func() error) error {
	if m.Exclusive == nil {
		return fn()
	}
	return m.Exclusive(fn)
}

func (m *Migrator) stopped() bool {
	if m.Stop == nil {
		return false
	}
	select {
	case <-m.Stop:
		return true
	default:
		return false
	}
}

func (m *Migrator) phase() string {
	p, _ := m.Index.GetMeta(index.MetaStoreMigrationPhase)
	return p
}

// Run migrates every legacy object, resuming from the recorded phase.
func (m *Migrator) Run() (*MigrationResult, error) {
	if !m.Store.Encrypting() {
		return nil, errors.New("store encryption is off ([store] encryption = false)")
	}
	if m.LockPath != "" {
		unlock, err := index.LockFile(m.LockPath)
		if err != nil {
			if errors.Is(err, index.ErrSegmentCompactionBusy) {
				return nil, ErrMigrationBusy
			}
			return nil, err
		}
		defer unlock()
	}

	result := &MigrationResult{}
	phase := m.phase()
	if phase == PhaseDone || phase == "" {
		if !m.Store.NeedsMigration() {
			return result, nil
		}
		phase = PhaseCopy
		if err := m.Index.SetMeta(index.MetaStoreMigrationPhase, PhaseCopy); err != nil {
			return nil, err
		}
	}

	if phase == PhaseCopy {
		if err := m.copyPhase(result); err != nil {
			return result, err
		}
		if err := m.Index.SetMeta(index.MetaStoreMigrationPhase, PhaseRemap); err != nil {
			return result, err
		}
		if err := m.fail("after-copy"); err != nil {
			return result, err
		}
		phase = PhaseRemap
	}

	if phase == PhaseRemap || phase == PhaseSegments {
		if err := m.verifyCopies(); err != nil {
			return result, err
		}
		if err := m.remap(PhaseSegments, result); err != nil {
			return result, err
		}
		if err := m.fail("after-remap"); err != nil {
			return result, err
		}
		if err := m.segmentsPhase(result); err != nil {
			return result, err
		}
		if err := m.Index.SetMeta(index.MetaStoreMigrationPhase, PhaseCleanup); err != nil {
			return result, err
		}
		if err := m.fail("after-segments"); err != nil {
			return result, err
		}
		phase = PhaseCleanup
	}

	if phase == PhaseCleanup {
		if err := m.cleanupPhase(result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func isContentID(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func (m *Migrator) copyPhase(result *MigrationResult) error {
	if err := m.Index.EnsureMigrationTable(); err != nil {
		return err
	}
	hashes, err := m.Store.ListHashes()
	if err != nil {
		return err
	}

	var pending []string
	recorded := 0
	for _, h := range hashes {
		if !isContentID(h) {
			continue
		}
		if _, _, ok, err := m.Index.MigrationEntry(h); err != nil {
			return err
		} else if ok {
			recorded++
			continue
		}
		enc, err := m.Store.IsEncrypted(h)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if !enc {
			pending = append(pending, h)
		}
	}
	_ = m.Index.SetMeta(index.MetaStoreMigrationTotal, strconv.Itoa(recorded+len(pending)))
	m.logf("store migration: %d legacy objects to encrypt (%d already done)", len(pending), recorded)

	for _, oldID := range pending {
		if m.stopped() {
			return ErrMigrationStopped
		}
		if err := m.copyOne(oldID, result); err != nil {
			return err
		}
	}
	return nil
}

func (m *Migrator) copyOne(oldID string, result *MigrationResult) error {
	raw, err := os.ReadFile(m.Store.objectPath(oldID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read legacy object %s: %w", oldID, err)
	}
	data, err := legacyDecode(oldID, raw)
	if err != nil {
		m.logf("store migration: legacy object %s failed verification, keeping it untouched: %v", oldID, err)
		result.Corrupt++
		return m.Index.RecordMigration(oldID, "", index.MigrationStatusCorrupt)
	}

	newID := m.Store.keyedID(data)
	content, err := m.Store.encode(newID, data)
	if err != nil {
		return err
	}
	if err := m.Store.writeObject(newID, content); err != nil {
		return err
	}
	if err := m.fail("copy-written:" + oldID); err != nil {
		return err
	}
	back, err := m.Store.Get(newID)
	if err != nil {
		return fmt.Errorf("verify encrypted copy of %s: %w", oldID, err)
	}
	if !bytes.Equal(back, data) {
		return fmt.Errorf("verify encrypted copy of %s: content differs after decrypt", oldID)
	}
	if err := m.Index.RecordMigration(oldID, newID, index.MigrationStatusCopied); err != nil {
		return err
	}
	result.Copied++
	return m.fail("copy-recorded:" + oldID)
}

var errCopyLost = errors.New("encrypted copy does not verify and the legacy object is gone")

// verifyCopies re-reads every encrypted copy right before ids are remapped to it. A copy
// that no longer verifies is written again from its legacy object.
func (m *Migrator) verifyCopies() error {
	mapping, err := m.Index.MigrationMapping()
	if err != nil {
		return err
	}
	for oldID, newID := range mapping {
		if m.stopped() {
			return ErrMigrationStopped
		}
		err := m.ensureCopy(oldID, newID)
		if errors.Is(err, errCopyLost) {
			m.logf("store migration: %s: %v; its id will be tombstoned", oldID, err)
			if err := m.Index.DeleteMigration(oldID); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ensureCopy makes sure newID decrypts to the content of the legacy object oldID,
// re-copying from the legacy object when it does not.
func (m *Migrator) ensureCopy(oldID, newID string) error {
	back, getErr := m.Store.Get(newID)
	raw, err := os.ReadFile(m.Store.objectPath(oldID))
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read legacy object %s: %w", oldID, err)
		}
		if getErr == nil {
			return nil
		}
		return errCopyLost
	}
	legacy, err := legacyDecode(oldID, raw)
	if err != nil {
		if getErr == nil {
			return nil
		}
		return errCopyLost
	}
	if getErr == nil && bytes.Equal(back, legacy) {
		return nil
	}
	if m.Store.keyedID(legacy) != newID {
		return fmt.Errorf("re-copy %s: content id mismatch", oldID)
	}
	content, err := m.Store.encode(newID, legacy)
	if err != nil {
		return err
	}
	if err := m.Store.writeObject(newID, content); err != nil {
		return fmt.Errorf("re-copy %s: %w", oldID, err)
	}
	again, err := m.Store.Get(newID)
	if err != nil || !bytes.Equal(again, legacy) {
		return fmt.Errorf("re-copy %s: encrypted copy %s still does not verify: %v", oldID, newID, err)
	}
	m.logf("store migration: re-copied %s, its encrypted copy did not verify", oldID)
	return nil
}

// tombstone records a replacement for every id in ids that is neither known to the
// migration nor backed by an object, so no legacy SHA-256 survives the remap.
func (m *Migrator) tombstone(ids map[string]bool) error {
	known, err := m.Index.MigrationKnownIDs()
	if err != nil {
		return err
	}
	for id := range ids {
		if known[id] || !isContentID(id) {
			continue
		}
		exists, err := m.Store.ObjectExists(id)
		if err != nil {
			return fmt.Errorf("check object %s: %w", id, err)
		}
		if exists {
			continue
		}
		if err := m.Index.RecordMigration(id, m.Store.TombstoneID(id), index.MigrationStatusTombstone); err != nil {
			return err
		}
	}
	return nil
}

func (m *Migrator) remap(next string, result *MigrationResult) error {
	return m.exclusive(func() error {
		ids, err := m.Index.EventContentIDs()
		if err != nil {
			return err
		}
		if err := m.tombstone(ids); err != nil {
			return err
		}
		n, err := m.Index.ApplyMigrationMapping(next)
		if err != nil {
			return err
		}
		result.RowsRemapped += n
		return nil
	})
}

func (m *Migrator) segmentsPhase(result *MigrationResult) error {
	if m.EventsDir == "" {
		return nil
	}
	if _, err := os.Stat(m.EventsDir); os.IsNotExist(err) {
		return nil
	}
	unlock, err := index.LockSegments(m.EventsDir)
	if err != nil {
		if errors.Is(err, index.ErrSegmentCompactionBusy) {
			return fmt.Errorf("segment compaction is running, migration will resume later: %w", err)
		}
		return err
	}
	defer unlock()

	skip := map[string]bool{}
	if m.ActiveSegment != nil {
		if err := m.exclusive(func() error {
			if seg := m.ActiveSegment(); seg != "" {
				skip[seg] = true
			}
			return nil
		}); err != nil {
			return err
		}
	}

	ids, err := index.SegmentContentIDs(m.EventsDir, skip)
	if err != nil {
		return err
	}
	if err := m.tombstone(ids); err != nil {
		return err
	}
	mapping, err := m.Index.MigrationRemapTable()
	if err != nil {
		return err
	}
	if len(mapping) == 0 {
		return nil
	}

	res, err := index.RemapSegments(m.Index, m.EventsDir, mapping, skip, func(seg string) error {
		return m.fail("segment-before-rename:" + seg)
	})
	result.Segments = res
	return err
}

func (m *Migrator) cleanupPhase(result *MigrationResult) error {
	if err := m.remap(PhaseCleanup, result); err != nil {
		return err
	}
	mapping, err := m.Index.MigrationMapping()
	if err != nil {
		return err
	}
	failed := 0
	for oldID, newID := range mapping {
		if m.stopped() {
			return ErrMigrationStopped
		}
		if err := m.ensureCopy(oldID, newID); err != nil {
			if errors.Is(err, errCopyLost) {
				m.logf("store migration: %s: %v", oldID, err)
				continue
			}
			m.logf("store migration: keeping legacy object %s: %v", oldID, err)
			failed++
			continue
		}
		raw, err := os.ReadFile(m.Store.objectPath(oldID))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		back, err := m.Store.Get(newID)
		legacy, lerr := legacyDecode(oldID, raw)
		if err != nil || lerr != nil || !bytes.Equal(legacy, back) {
			m.logf("store migration: legacy object %s no longer matches its copy, keeping it", oldID)
			continue
		}
		if m.Store.Pinned(oldID) {
			m.logf("store migration: legacy object %s is pinned, keeping it", oldID)
			continue
		}
		if err := m.Store.Delete(oldID); err != nil {
			return err
		}
		result.LegacyDeleted++
		if err := m.fail("cleanup-deleted:" + oldID); err != nil {
			return err
		}
	}

	if failed > 0 {
		return fmt.Errorf("store migration: %d encrypted copies do not verify; their legacy objects are kept and cleanup will retry", failed)
	}

	corrupt, err := m.Index.MigrationIDs(index.MigrationStatusCorrupt)
	if err != nil {
		return err
	}
	prev := readCorruptIDs(m.Index)
	corrupt = mergeIDs(prev, corrupt)
	corruptJSON, _ := json.Marshal(corrupt)
	if err := m.fail("before-finish"); err != nil {
		return err
	}
	if err := m.Index.FinishMigration(PhaseDone, string(corruptJSON)); err != nil {
		return err
	}
	if err := m.Index.ScrubFreePages(); err != nil {
		m.logf("store migration: %v", err)
	}
	if err := m.fail("before-manifest"); err != nil {
		return err
	}
	return m.Store.SetMigration(MigrationComplete)
}

func readCorruptIDs(idx *index.Index) []string {
	v, _ := idx.GetMeta(index.MetaStoreMigrationCorrupt)
	var ids []string
	if v != "" {
		_ = json.Unmarshal([]byte(v), &ids)
	}
	return ids
}

func mergeIDs(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{a, b} {
		for _, id := range list {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// ReadMigrationStatus summarizes the store format and migration progress without taking locks.
func ReadMigrationStatus(idx *index.Index, manifestPath string) MigrationStatus {
	st := MigrationStatus{}
	if m, ok, err := ReadManifest(manifestPath); err == nil && ok {
		st.Format, st.KeyID, st.Migration = m.Format, m.KeyID, m.Migration
	} else {
		st.Format = FormatLegacy
	}
	if idx == nil {
		return st
	}
	st.Phase, _ = idx.GetMeta(index.MetaStoreMigrationPhase)
	if v, _ := idx.GetMeta(index.MetaStoreMigrationTotal); v != "" {
		st.Total, _ = strconv.Atoi(v)
	}
	st.Copied, st.Corrupt, _ = idx.MigrationCounts()
	if st.Phase == PhaseDone {
		st.CorruptIDs = readCorruptIDs(idx)
		st.Corrupt = len(st.CorruptIDs)
		st.Copied = st.Total - st.Corrupt
	} else if ids, err := idx.MigrationIDs(index.MigrationStatusCorrupt); err == nil {
		st.CorruptIDs = ids
	}
	return st
}

// MigrationLockPath is where a store migration takes its exclusive lock.
func MigrationLockPath(belayDir string) string {
	return filepath.Join(belayDir, "migrate.lock")
}

// ReopenMigrationIfLegacy sets a finished store back to pending when a legacy object
// shows up again, for example after an older .belay was copied over this one. Objects
// kept on purpose (corrupt ones and pinned ones) do not count.
func ReopenMigrationIfLegacy(s *Store, idx *index.Index) (string, bool) {
	if s == nil || !s.Encrypting() || s.Manifest().Migration == MigrationPending {
		return "", false
	}
	keep := map[string]bool{}
	if idx != nil {
		for _, id := range readCorruptIDs(idx) {
			keep[id] = true
		}
		ids, _ := idx.MigrationIDs(index.MigrationStatusCorrupt)
		for _, id := range ids {
			keep[id] = true
		}
	}
	found, ok, err := s.FindLegacyObject(func(id string) bool { return keep[id] || s.Pinned(id) })
	if err != nil || !ok {
		return "", false
	}
	if err := s.SetMigration(MigrationPending); err != nil {
		return "", false
	}
	return found, true
}
