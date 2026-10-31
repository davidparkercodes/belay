package selfupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
)

const (
	CheckInterval  = 24 * time.Hour
	DisableEnv     = "BELAY_NO_UPDATE_CHECK"
	cacheFileName  = "update.json"
	lockDirName    = "update.lock"
	lockStaleAfter = 15 * time.Minute
)

type State struct {
	CheckedAt   time.Time `json:"checked_at"`
	Latest      string    `json:"latest,omitempty"`
	CheckedBy   string    `json:"checked_by,omitempty"`
	Error       string    `json:"error,omitempty"`
	AutoTarget  string    `json:"auto_target,omitempty"`
	AutoAt      time.Time `json:"auto_at,omitempty"`
	AutoError   string    `json:"auto_error,omitempty"`
	AutoVersion string    `json:"auto_installed,omitempty"`
}

func (s State) Due(now time.Time) bool {
	return s.CheckedAt.IsZero() || now.Sub(s.CheckedAt) >= CheckInterval || now.Before(s.CheckedAt)
}

func Disabled(getenv func(string) string) bool {
	return getenv(DisableEnv) == "1"
}

func CacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find user cache dir: %w", err)
	}
	return filepath.Join(dir, "belay"), nil
}

func CachePath(dir string) string { return filepath.Join(dir, cacheFileName) }

func ReadState(path string) (State, error) {
	var st State
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, nil
	}
	return st, nil
}

func WriteState(path string, st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".update-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

func NoticeLine(latest, current string) string {
	if !IsNewer(latest, current) {
		return ""
	}
	return fmt.Sprintf("Belay %s is available (you have %s). Run belay update.", Tag(latest), Tag(current))
}

func StatusNotice(current string, getenv func(string) string) string {
	if Disabled(getenv) {
		return ""
	}
	if cfg, err := config.LoadUserConfig(); err == nil && cfg.Update.Mode == config.UpdateOff {
		return ""
	}
	dir, err := CacheDir()
	if err != nil {
		return ""
	}
	st, err := ReadState(CachePath(dir))
	if err != nil {
		return ""
	}
	return NoticeLine(st.Latest, current)
}

type dirLock struct{ path string }

func acquireLock(dir string, now time.Time) (*dirLock, bool) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false
	}
	path := filepath.Join(dir, lockDirName)
	if err := os.Mkdir(path, 0o755); err != nil {
		info, statErr := os.Stat(path)
		if statErr != nil || now.Sub(info.ModTime()) < lockStaleAfter {
			return nil, false
		}
		_ = os.Remove(path)
		if err := os.Mkdir(path, 0o755); err != nil {
			return nil, false
		}
	}
	return &dirLock{path: path}, true
}

func (l *dirLock) release() {
	if l != nil {
		_ = os.Remove(l.path)
	}
}
