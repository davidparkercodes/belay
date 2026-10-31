package scrub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/davidparkercodes/belay/internal/secrets"
)

const cacheVersion = 1

type cacheEntry struct {
	Size    int64  `json:"size"`
	MtimeNs int64  `json:"mtime_ns"`
	Counts  Counts `json:"counts,omitempty"`
}

// Cache holds finding counts per log keyed by (path, size, mtime), so belay status
// never rescans an unchanged log. It also carries the last auto-scrub error.
type Cache struct {
	Version       int                   `json:"version"`
	Entries       map[string]cacheEntry `json:"entries"`
	LastAutoAt    time.Time             `json:"last_auto_at,omitempty"`
	LastAutoError string                `json:"last_auto_error,omitempty"`
}

func cachePath(belayPath string) string {
	return filepath.Join(Dir(belayPath), "cache.json")
}

func LoadCache(belayPath string) *Cache {
	c := &Cache{Version: cacheVersion, Entries: map[string]cacheEntry{}}
	data, err := os.ReadFile(cachePath(belayPath))
	if err != nil {
		return c
	}
	var loaded Cache
	if json.Unmarshal(data, &loaded) != nil || loaded.Version != cacheVersion {
		return c
	}
	if loaded.Entries == nil {
		loaded.Entries = map[string]cacheEntry{}
	}
	return &loaded
}

func (c *Cache) Save(belayPath string) error {
	if err := os.MkdirAll(Dir(belayPath), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(Dir(belayPath), ".cache-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), cachePath(belayPath)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func (c *Cache) lookup(path string, info os.FileInfo) (Counts, bool) {
	e, ok := c.Entries[path]
	if !ok || e.Size != info.Size() || e.MtimeNs != info.ModTime().UnixNano() {
		return nil, false
	}
	return e.Counts, true
}

func (c *Cache) put(path string, info os.FileInfo, counts Counts) {
	if len(counts) == 0 {
		counts = nil
	}
	c.Entries[path] = cacheEntry{Size: info.Size(), MtimeNs: info.ModTime().UnixNano(), Counts: counts}
}

func (c *Cache) prune(keep map[string]bool) {
	for p := range c.Entries {
		if !keep[p] {
			delete(c.Entries, p)
		}
	}
}

// Notice is what belay status needs: the number of findings in this project's logs
// and whether every log was counted within the time budget.
type Notice struct {
	Findings int  `json:"findings"`
	Logs     int  `json:"logs_with_findings"`
	Complete bool `json:"complete"`
}

// CountFindings totals findings across the project logs of sources, reading only
// logs the cache does not already know and stopping when budget runs out.
func CountFindings(belayPath string, sources []Source, sc *secrets.Scanner, budget time.Duration) Notice {
	deadline := time.Now().Add(budget)
	readLimit := int64(budget.Seconds() * float64(64<<20))
	readLimit = max(min(readLimit, MaxLogBytes), 1<<20)
	cache := LoadCache(belayPath)
	n := Notice{Complete: true}
	keep := map[string]bool{}
	dirty := false
	for _, src := range sources {
		logs, err := src.ProjectLogs()
		if err != nil {
			continue
		}
		for _, l := range logs {
			info, err := os.Stat(l.Path)
			if err != nil {
				continue
			}
			keep[l.Path] = true
			counts, ok := cache.lookup(l.Path, info)
			if !ok {
				if !time.Now().Before(deadline) || info.Size() > readLimit {
					n.Complete = false
					continue
				}
				data, err := os.ReadFile(l.Path)
				if err != nil {
					continue
				}
				_, c, err := RedactLog(data, sc, deadline)
				if err != nil {
					n.Complete = false
					continue
				}
				if after, err := os.Stat(l.Path); err == nil && sameFile(info, after) {
					cache.put(l.Path, info, c)
					dirty = true
				}
				counts = c
			}
			if t := counts.Total(); t > 0 {
				n.Findings += t
				n.Logs++
			}
		}
	}
	if n.Complete {
		before := len(cache.Entries)
		cache.prune(keep)
		dirty = dirty || len(cache.Entries) != before
	}
	if dirty {
		_ = cache.Save(belayPath)
	}
	return n
}

func sameFile(a, b os.FileInfo) bool {
	return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
