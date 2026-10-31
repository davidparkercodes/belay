package scrub

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	recordScrub = "scrub"
	recordUndo  = "undo"
)

// Backup is one line of .belay/scrub/backups.jsonl. A scrub record names the
// encrypted store object that holds the log's bytes from before the rewrite; an
// undo record marks that backup as put back.
type Backup struct {
	Kind          string    `json:"kind"`
	Log           string    `json:"log"`
	Agent         string    `json:"agent,omitempty"`
	OriginalID    string    `json:"original_id"`
	OriginalSize  int64     `json:"original_size"`
	OriginalMode  uint32    `json:"original_mode"`
	OriginalMtime int64     `json:"original_mtime_ns"`
	ScrubbedID    string    `json:"scrubbed_id,omitempty"`
	ScrubbedSize  int64     `json:"scrubbed_size,omitempty"`
	Redactions    Counts    `json:"redactions,omitempty"`
	At            time.Time `json:"at"`
}

func backupsPath(belayPath string) string {
	return filepath.Join(Dir(belayPath), "backups.jsonl")
}

// Dir is where scrub keeps its backup records, cache and lock.
func Dir(belayPath string) string {
	return filepath.Join(belayPath, "scrub")
}

func appendBackup(belayPath string, b Backup) error {
	if err := os.MkdirAll(Dir(belayPath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(backupsPath(belayPath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	data, err := json.Marshal(b)
	if err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ReadBackups returns every backup record in the order written.
func ReadBackups(belayPath string) ([]Backup, error) {
	f, err := os.Open(backupsPath(belayPath))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Backup
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var b Backup
		if json.Unmarshal(sc.Bytes(), &b) == nil && b.Log != "" {
			out = append(out, b)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", backupsPath(belayPath), err)
	}
	return out, nil
}

// pendingBackups returns, per log, the scrub records not yet undone, oldest first.
func pendingBackups(belayPath string) (map[string][]Backup, error) {
	all, err := ReadBackups(belayPath)
	if err != nil {
		return nil, err
	}
	out := map[string][]Backup{}
	for _, b := range all {
		switch b.Kind {
		case recordScrub:
			out[b.Log] = append(out[b.Log], b)
		case recordUndo:
			list := out[b.Log]
			for i := len(list) - 1; i >= 0; i-- {
				if list[i].OriginalID == b.OriginalID {
					out[b.Log] = append(list[:i:i], list[i+1:]...)
					break
				}
			}
		}
	}
	return out, nil
}

func undoneLogs(belayPath string) (map[string]bool, error) {
	all, err := ReadBackups(belayPath)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, b := range all {
		if b.Kind == recordUndo {
			out[b.Log] = true
		}
	}
	return out, nil
}
