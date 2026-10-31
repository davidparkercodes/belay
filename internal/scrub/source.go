package scrub

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/davidparkercodes/belay/internal/session"
)

// Log is one agent session log on disk.
type Log struct {
	Path      string `json:"path"`
	Agent     string `json:"agent"`
	SessionID string `json:"session_id,omitempty"`
}

// Source finds the session logs one agent keeps. Adding support for another agent
// means implementing Source and adding it to DefaultSources.
type Source interface {
	Agent() string
	// ProjectLogs returns the logs for the Belay project the source was built for.
	ProjectLogs() ([]Log, error)
	// AllLogs returns every log the agent keeps, for every project.
	AllLogs() ([]Log, error)
}

// ClaudeCode reads Claude Code transcripts: <ProjectsDir>/<encoded project root>/*.jsonl,
// plus subagent transcripts nested under a session directory.
type ClaudeCode struct {
	ProjectsDir string
	ProjectRoot string
}

// DefaultSources returns the log sources for projectRoot on this machine.
func DefaultSources(projectRoot string) []Source {
	dir, err := session.ClaudeProjectsDir()
	if err != nil {
		return nil
	}
	return []Source{&ClaudeCode{ProjectsDir: dir, ProjectRoot: projectRoot}}
}

func (c *ClaudeCode) Agent() string { return "claude-code" }

func (c *ClaudeCode) ProjectLogs() ([]Log, error) {
	var out []Log
	for _, name := range session.ClaudeProjectDirNames(c.ProjectRoot) {
		logs, err := c.walk(filepath.Join(c.ProjectsDir, name))
		if err != nil {
			return nil, err
		}
		out = append(out, logs...)
	}
	return sortLogs(out), nil
}

func (c *ClaudeCode) AllLogs() ([]Log, error) {
	entries, err := os.ReadDir(c.ProjectsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Log
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		logs, err := c.walk(filepath.Join(c.ProjectsDir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, logs...)
	}
	return sortLogs(out), nil
}

func (c *ClaudeCode) walk(dir string) ([]Log, error) {
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var out []Log
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			return nil
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			if rel != "." && strings.Count(rel, string(filepath.Separator)) >= 2 {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || filepath.Ext(p) != ".jsonl" {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		sid := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		if parts := strings.Split(rel, string(filepath.Separator)); len(parts) > 1 {
			sid = parts[0]
		}
		out = append(out, Log{Path: p, Agent: c.Agent(), SessionID: sid})
		return nil
	})
	return out, err
}

func sortLogs(logs []Log) []Log {
	seen := map[string]bool{}
	out := logs[:0]
	for _, l := range logs {
		if seen[l.Path] {
			continue
		}
		seen[l.Path] = true
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
