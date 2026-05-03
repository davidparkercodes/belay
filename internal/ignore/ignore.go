// Package ignore implements .belayignore pattern matching for excluding files from tracking.
package ignore

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Matcher evaluates file paths against .belayignore patterns to determine which files
// should be excluded from Belay tracking.
type Matcher struct {
	projectRoot string
	patterns    []pattern
	mu          sync.RWMutex
}

type pattern struct {
	raw      string
	negated  bool
	dirOnly  bool
	glob     string
	isAbsDir bool
}

// NewMatcher creates a Matcher by loading patterns from .belayignore in the project root.
func NewMatcher(projectRoot string) (*Matcher, error) {
	m := &Matcher{projectRoot: projectRoot}
	if err := m.Reload(); err != nil {
		return nil, err
	}
	return m, nil
}

// Reload re-reads the .belayignore file and updates the patterns.
func (m *Matcher) Reload() error {
	ignorePath := filepath.Join(m.projectRoot, ".belayignore")
	patterns, err := parseIgnoreFile(ignorePath)
	if err != nil {
		if os.IsNotExist(err) {
			patterns = defaultPatterns()
		} else {
			return err
		}
	}

	m.mu.Lock()
	m.patterns = patterns
	m.mu.Unlock()
	return nil
}

// ShouldIgnore reports whether the given relative path matches any ignore pattern.
func (m *Matcher) ShouldIgnore(relPath string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	relPath = filepath.ToSlash(relPath)

	ignored := false
	for _, p := range m.patterns {
		if p.matches(relPath) {
			ignored = !p.negated
		}
	}
	return ignored
}

// Patterns returns the raw pattern strings currently loaded.
func (m *Matcher) Patterns() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]string, len(m.patterns))
	for i, p := range m.patterns {
		result[i] = p.raw
	}
	return result
}

func parseIgnoreFile(path string) ([]pattern, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var patterns []pattern
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if p, ok := parseLine(line); ok {
			patterns = append(patterns, p)
		}
	}
	return patterns, scanner.Err()
}

func parseLine(line string) (pattern, bool) {
	line = strings.TrimSpace(line)

	if line == "" || strings.HasPrefix(line, "#") {
		return pattern{}, false
	}

	p := pattern{raw: line}

	if strings.HasPrefix(line, "!") {
		p.negated = true
		line = line[1:]
	}

	if strings.HasSuffix(line, "/") {
		p.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}

	if strings.HasPrefix(line, "/") {
		p.isAbsDir = true
		line = line[1:]
	}

	p.glob = line
	return p, true
}

func (p *pattern) matches(relPath string) bool {
	if strings.Contains(p.glob, "/") || p.isAbsDir {
		if matchGlob(p.glob, relPath) {
			return true
		}
		if p.dirOnly && strings.HasPrefix(filepath.ToSlash(relPath), p.glob+"/") {
			return true
		}
		return false
	}

	parts := strings.Split(relPath, "/")

	if p.dirOnly {
		for _, part := range parts[:len(parts)-1] {
			if matchGlob(p.glob, part) {
				return true
			}
		}
		return matchGlob(p.glob, relPath) || matchGlob(p.glob+"/", relPath+"/")
	}

	filename := parts[len(parts)-1]
	if matchGlob(p.glob, filename) {
		return true
	}

	return matchGlob(p.glob, relPath)
}

func matchGlob(pattern, name string) bool {
	if strings.Contains(pattern, "**") {
		return matchDoublestar(pattern, name)
	}

	matched, err := filepath.Match(pattern, name)
	if err != nil {
		return false
	}
	return matched
}

func matchDoublestar(pattern, name string) bool {
	parts := strings.SplitN(pattern, "**", 2)
	if len(parts) != 2 {
		return false
	}

	prefix := parts[0]
	suffix := strings.TrimPrefix(parts[1], "/")

	if prefix != "" {
		if !strings.HasPrefix(name, prefix) {
			return false
		}
		name = name[len(prefix):]
	}

	if suffix == "" {
		return true
	}

	pathParts := strings.Split(name, "/")
	for i := 0; i <= len(pathParts); i++ {
		remaining := strings.Join(pathParts[i:], "/")
		if matched, _ := filepath.Match(suffix, remaining); matched {
			return true
		}
		if i < len(pathParts) {
			if matched, _ := filepath.Match(suffix, pathParts[i]); matched {
				return true
			}
		}
	}

	return false
}

func defaultPatterns() []pattern {
	defaults := []string{
		"node_modules/",
		".git/",
		".belay/",
		"__pycache__/",
		"*.pyc",
		"*.pyo",
		".DS_Store",
		"build/",
		"dist/",
		"target/",
		".next/",
		".vite/",
		".turbo/",
	}

	var patterns []pattern
	for _, d := range defaults {
		if p, ok := parseLine(d); ok {
			patterns = append(patterns, p)
		}
	}
	return patterns
}
