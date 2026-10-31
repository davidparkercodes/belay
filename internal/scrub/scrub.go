// Package scrub redacts credentials from AI agent session logs. It only touches
// idle logs, keeps the original bytes in Belay's encrypted store before any
// rewrite, and can put them back byte-exact.
package scrub

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/secrets"
	"github.com/davidparkercodes/belay/internal/session"
	"github.com/davidparkercodes/belay/internal/store"
)

const (
	// DefaultIdleAfter is how long a log must go without a write before it may be rewritten.
	DefaultIdleAfter = 30 * time.Minute

	// MaxLogBytes is the largest log scrub reads; bigger logs are reported and left alone.
	MaxLogBytes = 256 << 20
)

const (
	StatusRedacted    = "redacted"
	StatusWouldRedact = "would-redact"
	StatusClean       = "clean"
	StatusActive      = "skipped-active"
	StatusRecent      = "skipped-recent"
	StatusChanged     = "skipped-changed"
	StatusTooLarge    = "skipped-large"
	StatusUndone      = "skipped-undone"
	StatusDeferred    = "deferred"
	StatusError       = "error"
)

var (
	ErrBusy          = errors.New("another scrub is running")
	ErrNeedsEncrypt  = errors.New("agent log scrub keeps originals in the encrypted store, but store encryption is off ([store] encryption = false)")
	ErrNoBackup      = errors.New("no scrub backup for this log")
	ErrChangedSince  = errors.New("the log changed in a way that is not an append since it was scrubbed")
	errChangedDuring = errors.New("log changed during scrub")
)

type Scrubber struct {
	BelayPath string
	Store     *store.Store
	Sources   []Source
	Scanner   *secrets.Scanner
	IdleAfter time.Duration
	// Active returns the session ids that must not be touched (active Belay sessions).
	Active func() map[string]bool
	Now    func() time.Time

	beforeRename func(path string) error
}

type RunOptions struct {
	DryRun      bool
	AllProjects bool
	MaxBytes    int64
	Deadline    time.Time
	Pause       time.Duration
	Stop        <-chan struct{}
	// SkipUndone leaves alone logs whose scrub someone undid, so background
	// scrubbing never redoes what a person reverted.
	SkipUndone bool
}

type FileResult struct {
	Path      string `json:"path"`
	Agent     string `json:"agent"`
	SessionID string `json:"session_id,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	Counts    Counts `json:"counts,omitempty"`
	Total     int    `json:"total"`
}

type Report struct {
	DryRun   bool         `json:"dry_run"`
	Files    []FileResult `json:"files"`
	Redacted int          `json:"files_redacted"`
	Findings int          `json:"findings"`
	Skipped  int          `json:"files_skipped"`
}

func (s *Scrubber) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scrubber) idleAfter() time.Duration {
	if s.IdleAfter > 0 {
		return s.IdleAfter
	}
	return DefaultIdleAfter
}

func (s *Scrubber) scanner() *secrets.Scanner {
	if s.Scanner != nil {
		return s.Scanner
	}
	return secrets.NewScanner(secrets.Options{})
}

func (s *Scrubber) lock() (func(), error) {
	if err := os.MkdirAll(Dir(s.BelayPath), 0o700); err != nil {
		return nil, err
	}
	unlock, err := index.LockFile(filepath.Join(Dir(s.BelayPath), "scrub.lock"))
	if err != nil {
		if errors.Is(err, index.ErrSegmentCompactionBusy) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return unlock, nil
}

func (s *Scrubber) logs(all bool) ([]Log, error) {
	var out []Log
	for _, src := range s.Sources {
		var logs []Log
		var err error
		if all {
			logs, err = src.AllLogs()
		} else {
			logs, err = src.ProjectLogs()
		}
		if err != nil {
			return nil, fmt.Errorf("%s logs: %w", src.Agent(), err)
		}
		out = append(out, logs...)
	}
	return out, nil
}

// Run scans the logs and redacts every idle one that has findings. It never
// returns secret values, only counts per rule.
func (s *Scrubber) Run(opts RunOptions) (*Report, error) {
	if !opts.DryRun && (s.Store == nil || !s.Store.Encrypting()) {
		return nil, ErrNeedsEncrypt
	}
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	logs, err := s.logs(opts.AllProjects)
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	if s.Active != nil {
		active = s.Active()
	}
	var undone map[string]bool
	if opts.SkipUndone {
		if undone, err = undoneLogs(s.BelayPath); err != nil {
			return nil, err
		}
	}
	cache := LoadCache(s.BelayPath)
	sc := s.scanner()
	rep := &Report{DryRun: opts.DryRun, Files: []FileResult{}}
	var spent int64
	stopped := false

	for _, l := range logs {
		r := FileResult{Path: l.Path, Agent: l.Agent, SessionID: l.SessionID}
		info, err := os.Stat(l.Path)
		switch {
		case err != nil:
			continue
		case undone[l.Path]:
			r.Status, r.Reason = StatusUndone, "scrub was undone for this log"
		case stopped || s.stopRequested(opts) || (!opts.Deadline.IsZero() && s.now().After(opts.Deadline)) ||
			(opts.MaxBytes > 0 && spent > 0 && spent+info.Size() > opts.MaxBytes):
			stopped = true
			r.Status = StatusDeferred
		default:
			s.processLog(&r, info, active, cache, sc, opts)
			if r.Status != StatusClean || r.Reason != "cached" {
				spent += info.Size()
			}
			if opts.Pause > 0 && r.Reason != "cached" {
				time.Sleep(opts.Pause)
			}
		}
		if r.Status == StatusClean {
			continue
		}
		r.Total = r.Counts.Total()
		switch r.Status {
		case StatusRedacted:
			rep.Redacted++
			rep.Findings += r.Total
		case StatusWouldRedact:
			rep.Findings += r.Total
		default:
			rep.Skipped++
			rep.Findings += r.Total
		}
		rep.Files = append(rep.Files, r)
	}
	_ = cache.Save(s.BelayPath)
	return rep, nil
}

func (s *Scrubber) stopRequested(opts RunOptions) bool {
	if opts.Stop == nil {
		return false
	}
	select {
	case <-opts.Stop:
		return true
	default:
		return false
	}
}

func (s *Scrubber) processLog(r *FileResult, info os.FileInfo, active map[string]bool, cache *Cache, sc *secrets.Scanner, opts RunOptions) {
	if counts, ok := cache.lookup(r.Path, info); ok {
		r.Counts = counts
		if counts.Total() == 0 {
			r.Status, r.Reason = StatusClean, "cached"
			return
		}
	}
	idle := s.now().Sub(info.ModTime()) >= s.idleAfter()
	if active[r.SessionID] {
		r.Status, r.Reason = StatusActive, "session is active in Belay"
		return
	}
	if !idle {
		r.Status, r.Reason = StatusRecent, fmt.Sprintf("written in the last %s", s.idleAfter())
		return
	}
	if info.Size() > MaxLogBytes {
		r.Status, r.Reason = StatusTooLarge, fmt.Sprintf("larger than %d MB", MaxLogBytes>>20)
		return
	}
	data, err := os.ReadFile(r.Path)
	if err != nil {
		r.Status, r.Reason = StatusError, err.Error()
		return
	}
	if after, err := os.Stat(r.Path); err != nil || !sameFile(info, after) {
		r.Status, r.Reason = StatusChanged, "log changed while reading"
		return
	}
	red, counts, err := RedactLog(data, sc, opts.Deadline)
	if err != nil {
		r.Status, r.Reason = StatusDeferred, err.Error()
		return
	}
	r.Counts = counts
	if counts.Total() == 0 {
		cache.put(r.Path, info, nil)
		r.Status = StatusClean
		return
	}
	cache.put(r.Path, info, counts)
	if opts.DryRun {
		r.Status = StatusWouldRedact
		return
	}
	if err := s.rewrite(r, info, data, red); err != nil {
		if errors.Is(err, errChangedDuring) {
			r.Status, r.Reason = StatusChanged, "log changed during scrub, left untouched"
			return
		}
		r.Status, r.Reason = StatusError, err.Error()
		return
	}
	if after, err := os.Stat(r.Path); err == nil {
		cache.put(r.Path, after, nil)
	}
	r.Status = StatusRedacted
}

func (s *Scrubber) rewrite(r *FileResult, info os.FileInfo, original, redacted []byte) error {
	id, _, err := s.Store.Put(original)
	if err != nil {
		return fmt.Errorf("save original: %w", err)
	}
	back, err := s.Store.Get(id)
	if err != nil || !bytes.Equal(back, original) {
		return fmt.Errorf("save original: stored copy does not read back")
	}
	tmp, err := writeTemp(r.Path, redacted, info.Mode().Perm(), info.ModTime())
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if s.beforeRename != nil {
		if err := s.beforeRename(r.Path); err != nil {
			return err
		}
	}
	if cur, err := os.Stat(r.Path); err != nil || !sameFile(info, cur) {
		return errChangedDuring
	}
	if err := s.Store.Pin(id, "scrub "+r.Path); err != nil {
		return fmt.Errorf("pin original: %w", err)
	}
	b := Backup{
		Kind:          recordScrub,
		Log:           r.Path,
		Agent:         r.Agent,
		OriginalID:    id,
		OriginalSize:  int64(len(original)),
		OriginalMode:  uint32(info.Mode().Perm()),
		OriginalMtime: info.ModTime().UnixNano(),
		ScrubbedID:    s.Store.ContentID(redacted),
		ScrubbedSize:  int64(len(redacted)),
		Redactions:    r.Counts,
		At:            s.now().UTC(),
	}
	if err := appendBackup(s.BelayPath, b); err != nil {
		return fmt.Errorf("record backup: %w", err)
	}
	if err := os.Rename(tmp, r.Path); err != nil {
		return fmt.Errorf("replace log: %w", err)
	}
	return nil
}

func writeTemp(path string, data []byte, mode os.FileMode, mtime time.Time) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".belay-scrub-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	name := f.Name()
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(name)
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Chmod(mode); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	if !mtime.IsZero() {
		_ = os.Chtimes(name, mtime, mtime)
	}
	return name, nil
}

type UndoResult struct {
	Log          string `json:"log"`
	Restored     bool   `json:"restored"`
	KeptAppended int64  `json:"kept_appended_bytes"`
	Remaining    int    `json:"older_backups"`
}

// Undo puts back the most recent scrubbed original of logPath byte-exact. Lines the
// agent appended after the scrub are kept after the original bytes.
func (s *Scrubber) Undo(logPath string) (*UndoResult, error) {
	if s.Store == nil {
		return nil, errors.New("store not open")
	}
	abs, err := filepath.Abs(logPath)
	if err != nil {
		return nil, err
	}
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	pending, err := pendingBackups(s.BelayPath)
	if err != nil {
		return nil, err
	}
	list := pending[abs]
	if len(list) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoBackup, abs)
	}
	b := list[len(list)-1]
	original, err := s.Store.Get(b.OriginalID)
	if err != nil {
		return nil, fmt.Errorf("read saved original: %w", err)
	}
	res := &UndoResult{Log: abs, Remaining: len(list) - 1}

	info, statErr := os.Stat(abs)
	current, readErr := os.ReadFile(abs)
	var restored []byte
	mtime := time.Unix(0, b.OriginalMtime)
	switch {
	case readErr != nil && os.IsNotExist(readErr):
		restored = original
	case readErr != nil:
		return nil, readErr
	case bytes.Equal(current, original):
	case s.Store.ContentID(current) == b.ScrubbedID:
		restored = original
	case int64(len(current)) > b.ScrubbedSize && s.Store.ContentID(current[:b.ScrubbedSize]) == b.ScrubbedID:
		suffix := current[b.ScrubbedSize:]
		restored = append(append(make([]byte, 0, len(original)+len(suffix)), original...), suffix...)
		res.KeptAppended = int64(len(suffix))
		if statErr == nil {
			mtime = info.ModTime()
		}
	default:
		return nil, fmt.Errorf("%w: %s", ErrChangedSince, abs)
	}

	if restored != nil {
		tmp, err := writeTemp(abs, restored, os.FileMode(b.OriginalMode), mtime)
		if err != nil {
			return nil, err
		}
		if statErr == nil {
			if cur, err := os.Stat(abs); err != nil || !sameFile(info, cur) {
				os.Remove(tmp)
				return nil, errChangedDuring
			}
		}
		if err := os.Rename(tmp, abs); err != nil {
			os.Remove(tmp)
			return nil, fmt.Errorf("restore log: %w", err)
		}
		res.Restored = true
	}

	if err := appendBackup(s.BelayPath, Backup{Kind: recordUndo, Log: abs, Agent: b.Agent, OriginalID: b.OriginalID, At: s.now().UTC()}); err != nil {
		return res, fmt.Errorf("record undo: %w", err)
	}
	stillUsed := false
	for _, logs := range pending {
		for _, o := range logs {
			if o.OriginalID == b.OriginalID && !(o.Log == abs && o.At.Equal(b.At)) {
				stillUsed = true
			}
		}
	}
	if !stillUsed {
		_ = s.Store.Unpin(b.OriginalID)
	}
	return res, nil
}

// Backed returns the logs that have a scrub backup that can be undone, sorted.
func Backed(belayPath string) ([]string, error) {
	pending, err := pendingBackups(belayPath)
	if err != nil {
		return nil, err
	}
	var out []string
	for p, list := range pending {
		if len(list) > 0 {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ActiveSessionIDs returns the ids of sessions Belay currently considers active,
// including the tool session ids they are linked to.
func ActiveSessionIDs(idx *index.Index) map[string]bool {
	out := map[string]bool{}
	if idx == nil {
		return out
	}
	sessions, err := idx.ListSessions(true, 0, 0)
	if err != nil {
		return out
	}
	for _, s := range sessions {
		out[s.SessionID] = true
		if linked := s.Metadata[session.LinkedSessionKey]; linked != "" {
			out[linked] = true
		}
	}
	return out
}
