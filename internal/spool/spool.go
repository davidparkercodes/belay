package spool

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/davidparkercodes/belay/internal/schema"
)

const suffix = ".json"

// Entry is one queued event. Sealed, when set, is the file content encrypted to the
// daemon's recipient key; the daemon stores it and fills in the content id.
type Entry struct {
	Event  *schema.Event
	Sealed []byte
}

type fileEntry struct {
	*schema.Event
	SealedContent []byte `json:"sealed_content,omitempty"`
}

func Write(dir string, event *schema.Event) error {
	return WriteEntry(dir, &Entry{Event: event})
}

func WriteEntry(dir string, entry *Entry) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create spool dir: %w", err)
	}
	data, err := json.Marshal(fileEntry{Event: entry.Event, SealedContent: entry.Sealed})
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	event := entry.Event
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create spool file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("write spool file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("sync spool file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("close spool file: %w", err)
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, event.EventID+suffix))
}

func Pending(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, suffix) {
			continue
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	sort.Strings(paths)
	return paths, nil
}

func Drain(dir string, apply func(*schema.Event)) (int, error) {
	return DrainEntries(dir, func(e *Entry) error {
		apply(e.Event)
		return nil
	})
}

// MetaDeferredContent marks an event queued without its content because the store
// could not be opened; the daemon reads the file when it ingests the event.
const MetaDeferredContent = "deferred_content"

const badSuffix = ".bad"

func DeferContent(e *schema.Event) {
	if e.Metadata == nil {
		e.Metadata = map[string]string{}
	}
	e.Metadata[MetaDeferredContent] = "1"
}

func IsDeferred(e *schema.Event) bool {
	return e.Metadata != nil && e.Metadata[MetaDeferredContent] == "1"
}

// Quarantine wraps an error that retrying cannot fix, such as content sealed to a key
// this store does not hold. Only such entries are set aside as *.bad.
type Quarantine struct{ Err error }

func (q *Quarantine) Error() string { return q.Err.Error() }
func (q *Quarantine) Unwrap() error { return q.Err }

// DrainEntries hands each queued entry to apply, in order, and removes it once applied.
// An entry that cannot be decoded, or that apply rejects with a Quarantine error, is
// renamed to *.bad and kept. Any other apply error stops the drain and leaves the entry
// in place for the next one, so later events never overtake it.
func DrainEntries(dir string, apply func(*Entry) error) (int, error) {
	paths, err := Pending(dir)
	if err != nil {
		return 0, err
	}
	drained := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fe := fileEntry{Event: &schema.Event{}}
		if err := json.Unmarshal(data, &fe); err != nil || fe.EventID == "" {
			_ = os.Rename(path, path+badSuffix)
			continue
		}
		if err := apply(&Entry{Event: fe.Event, Sealed: fe.SealedContent}); err != nil {
			var q *Quarantine
			if errors.As(err, &q) {
				_ = os.Rename(path, path+badSuffix)
				continue
			}
			return drained, fmt.Errorf("spooled event %s: %w", fe.EventID, err)
		}
		_ = os.Remove(path)
		drained++
	}
	return drained, nil
}

// Bad returns the entries set aside as *.bad.
func Bad(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix+badSuffix) {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// Requeue puts *.bad entries holding sealed content that canOpen now accepts back in
// the queue, for example after the matching store key was installed.
func Requeue(dir string, canOpen func(sealed []byte) bool) (int, error) {
	bad, err := Bad(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, path := range bad {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fe := fileEntry{Event: &schema.Event{}}
		if json.Unmarshal(data, &fe) != nil || fe.EventID == "" || fe.SealedContent == nil {
			continue
		}
		if !canOpen(fe.SealedContent) {
			continue
		}
		if err := os.Rename(path, strings.TrimSuffix(path, badSuffix)); err == nil {
			n++
		}
	}
	return n, nil
}
