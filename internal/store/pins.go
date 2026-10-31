package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// PinsDir holds one file per object that garbage collection must keep even though
// no event references it, such as originals saved by the agent log scrub.
func (s *Store) PinsDir() string {
	return filepath.Join(filepath.Dir(s.dir), "pins")
}

func (s *Store) Pin(id, reason string) error {
	if !isContentID(id) {
		return fmt.Errorf("pin: invalid content id %q", id)
	}
	if err := os.MkdirAll(s.PinsDir(), 0o700); err != nil {
		return fmt.Errorf("create pins dir: %w", err)
	}
	return os.WriteFile(filepath.Join(s.PinsDir(), id), []byte(reason+"\n"), 0o600)
}

func (s *Store) Unpin(id string) error {
	if !isContentID(id) {
		return fmt.Errorf("unpin: invalid content id %q", id)
	}
	err := os.Remove(filepath.Join(s.PinsDir(), id))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) Pinned(id string) bool {
	if !isContentID(id) {
		return false
	}
	_, err := os.Stat(filepath.Join(s.PinsDir(), id))
	return err == nil
}
