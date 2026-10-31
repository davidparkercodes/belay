package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClaudeProjectsDir returns the directory where Claude Code keeps per-project
// session transcripts, honoring CLAUDE_CONFIG_DIR.
func ClaudeProjectsDir() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// ClaudeProjectDirNames returns the transcript directory names Claude Code may use
// for projectRoot, most likely first.
func ClaudeProjectDirNames(projectRoot string) []string {
	primary := encodeProjectPath(projectRoot)
	var b strings.Builder
	for _, r := range projectRoot {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	names := []string{primary}
	if alt := b.String(); alt != primary {
		names = append(names, alt)
	}
	return names
}
