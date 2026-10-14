package spool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/davidparkercodes/belay/internal/schema"
)

const suffix = ".json"

func Write(dir string, event *schema.Event) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create spool dir: %w", err)
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
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
		var event schema.Event
		if err := json.Unmarshal(data, &event); err != nil || event.EventID == "" {
			_ = os.Rename(path, path+".bad")
			continue
		}
		apply(&event)
		_ = os.Remove(path)
		drained++
	}
	return drained, nil
}
