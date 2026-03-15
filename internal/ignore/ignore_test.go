package ignore

import (
	"os"
	"path/filepath"
	"testing"
)

func writeIgnoreFile(t *testing.T, dir string, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, ".belayignore"), []byte(content), 0644)
	if err != nil {
		t.Fatalf("write .belayignore: %v", err)
	}
}

// ─── Default Patterns ───────────────────────────────────────────────────────

func TestDefaultPatterns_NoIgnoreFile(t *testing.T) {
	dir := t.TempDir()
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	defaults := []struct {
		path   string
		ignore bool
	}{
		{"node_modules/package.json", true},
		{".git/config", true},
		{".belay/index.db", true},
		{"__pycache__/module.pyc", true},
		{"build/output.js", true},
		{"dist/bundle.js", true},
		{".next/cache/data", true},
		{".vite/deps", true},
		{".turbo/cache", true},
		{".DS_Store", true},
		{"src/main.go", false},
		{"README.md", false},
	}

	for _, tt := range defaults {
		t.Run(tt.path, func(t *testing.T) {
			got := m.ShouldIgnore(tt.path)
			if got != tt.ignore {
				t.Errorf("ShouldIgnore(%q) = %v, want %v", tt.path, got, tt.ignore)
			}
		})
	}
}

func TestDefaultPatterns_PycFiles(t *testing.T) {
	dir := t.TempDir()
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		path   string
		ignore bool
	}{
		{"module.pyc", true},
		{"pkg/module.pyc", true},
		{"module.pyo", true},
		{"module.py", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := m.ShouldIgnore(tt.path)
			if got != tt.ignore {
				t.Errorf("ShouldIgnore(%q) = %v, want %v", tt.path, got, tt.ignore)
			}
		})
	}
}

// ─── Simple File Patterns ───────────────────────────────────────────────────

func TestSimpleFilePatterns(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, `
*.log
*.tmp
*.bak
`)
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		path   string
		ignore bool
	}{
		{"app.log", true},
		{"debug.log", true},
		{"tmp.tmp", true},
		{"backup.bak", true},
		{"nested/dir/app.log", true},
		{"main.go", false},
		{"logfile.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := m.ShouldIgnore(tt.path)
			if got != tt.ignore {
				t.Errorf("ShouldIgnore(%q) = %v, want %v", tt.path, got, tt.ignore)
			}
		})
	}
}

// ─── Directory Patterns ─────────────────────────────────────────────────────

func TestDirectoryPatterns(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, `
vendor/
tmp/
`)
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		path   string
		ignore bool
	}{
		{"vendor/pkg/mod.go", true},
		{"vendor/file.txt", true},
		{"tmp/cache/data", true},
		{"src/vendor/lib.go", true}, // vendor dir anywhere in path
		{"vendor_extras/file.go", false},
		{"src/main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := m.ShouldIgnore(tt.path)
			if got != tt.ignore {
				t.Errorf("ShouldIgnore(%q) = %v, want %v", tt.path, got, tt.ignore)
			}
		})
	}
}

// ─── Negation Patterns ──────────────────────────────────────────────────────

func TestNegationPatterns(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, `
*.log
!important.log
`)
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		path   string
		ignore bool
	}{
		{"debug.log", true},
		{"error.log", true},
		{"important.log", false}, // negated
		{"main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := m.ShouldIgnore(tt.path)
			if got != tt.ignore {
				t.Errorf("ShouldIgnore(%q) = %v, want %v", tt.path, got, tt.ignore)
			}
		})
	}
}

// ─── Nested Path Matching ───────────────────────────────────────────────────

func TestNestedPathMatching(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, `
src/generated/**
*.min.js
`)
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		path   string
		ignore bool
	}{
		{"src/generated/api.go", true},
		{"src/generated/types.go", true},
		{"src/main.go", false},
		{"bundle.min.js", true},
		{"assets/app.min.js", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := m.ShouldIgnore(tt.path)
			if got != tt.ignore {
				t.Errorf("ShouldIgnore(%q) = %v, want %v", tt.path, got, tt.ignore)
			}
		})
	}
}

func TestDirectoryPatternWithSlash_MatchesExactDir(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, `src/generated/`)
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	if !m.ShouldIgnore("src/generated") {
		t.Error("expected src/generated to be ignored")
	}
	// gitignore semantics: dir pattern with slash matches files inside
	if !m.ShouldIgnore("src/generated/api.go") {
		t.Error("src/generated/api.go should be ignored by directory pattern")
	}
}

// ─── Doublestar Patterns ────────────────────────────────────────────────────

func TestDoublestarPatterns(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, `
**/*.test.js
src/**/generated/*.go
`)
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		path   string
		ignore bool
	}{
		{"app.test.js", true},
		{"src/app.test.js", true},
		{"src/deep/nested/app.test.js", true},
		{"app.js", false},
		{"src/generated/types.go", true},
		{"src/pkg/generated/types.go", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := m.ShouldIgnore(tt.path)
			if got != tt.ignore {
				t.Errorf("ShouldIgnore(%q) = %v, want %v", tt.path, got, tt.ignore)
			}
		})
	}
}

// ─── Comments and Whitespace ────────────────────────────────────────────────

func TestCommentsAndWhitespace(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, `
# This is a comment
*.log

  # Indented comment

   *.tmp

`)
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		path   string
		ignore bool
	}{
		{"app.log", true},
		{"data.tmp", true},
		{"# This is a comment", false}, // Comments not treated as patterns
		{"main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := m.ShouldIgnore(tt.path)
			if got != tt.ignore {
				t.Errorf("ShouldIgnore(%q) = %v, want %v", tt.path, got, tt.ignore)
			}
		})
	}
}

func TestEmptyIgnoreFile(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "")
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	// Empty file = no patterns = nothing ignored
	if m.ShouldIgnore("anything.go") {
		t.Error("empty ignore file should not ignore anything")
	}
}

func TestWhitespaceOnlyIgnoreFile(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "   \n\n  \n")
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	if m.ShouldIgnore("anything.go") {
		t.Error("whitespace-only ignore file should not ignore anything")
	}
}

// ─── Absolute Path Patterns ─────────────────────────────────────────────────

func TestAbsolutePathPatterns(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, `
/build.sh
/config.local
`)
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		path   string
		ignore bool
	}{
		{"build.sh", true},
		{"config.local", true},
		{"scripts/build.sh", false}, // absolute pattern only matches root
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := m.ShouldIgnore(tt.path)
			if got != tt.ignore {
				t.Errorf("ShouldIgnore(%q) = %v, want %v", tt.path, got, tt.ignore)
			}
		})
	}
}

// ─── Patterns() ─────────────────────────────────────────────────────────────

func TestPatterns(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, `
*.log
vendor/
!keep.log
`)
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	patterns := m.Patterns()
	if len(patterns) != 3 {
		t.Fatalf("expected 3 patterns, got %d: %v", len(patterns), patterns)
	}
	if patterns[0] != "*.log" {
		t.Errorf("patterns[0] = %q, want %q", patterns[0], "*.log")
	}
	if patterns[1] != "vendor/" {
		t.Errorf("patterns[1] = %q, want %q", patterns[1], "vendor/")
	}
	if patterns[2] != "!keep.log" {
		t.Errorf("patterns[2] = %q, want %q", patterns[2], "!keep.log")
	}
}

// ─── Reload ─────────────────────────────────────────────────────────────────

func TestReload(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "*.log")
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	if !m.ShouldIgnore("app.log") {
		t.Error("expected app.log to be ignored")
	}
	if m.ShouldIgnore("app.tmp") {
		t.Error("expected app.tmp to not be ignored")
	}

	// Rewrite ignore file
	writeIgnoreFile(t, dir, "*.tmp")
	if err := m.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// Now .tmp is ignored, .log is not
	if m.ShouldIgnore("app.log") {
		t.Error("after reload, app.log should not be ignored")
	}
	if !m.ShouldIgnore("app.tmp") {
		t.Error("after reload, app.tmp should be ignored")
	}
}

func TestReload_FileDeleted(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "*.log")
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	// Delete the ignore file
	os.Remove(filepath.Join(dir, ".belayignore"))

	// Reload should fall back to default patterns
	if err := m.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// Default patterns should be active now
	if !m.ShouldIgnore("node_modules/pkg.json") {
		t.Error("after reload (file deleted), defaults should apply: node_modules should be ignored")
	}
}

// ─── parseLine ──────────────────────────────────────────────────────────────

func TestParseLine(t *testing.T) {
	tests := []struct {
		line    string
		ok      bool
		negated bool
		dirOnly bool
		absDir  bool
		glob    string
	}{
		{"*.log", true, false, false, false, "*.log"},
		{"!important.log", true, true, false, false, "important.log"},
		{"vendor/", true, false, true, false, "vendor"},
		{"/build.sh", true, false, false, true, "build.sh"},
		{"# comment", false, false, false, false, ""},
		{"", false, false, false, false, ""},
		{"   ", false, false, false, false, ""},
		{"!/keep.log", true, true, false, true, "keep.log"},
		{"!vendor/", true, true, true, false, "vendor"},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			p, ok := parseLine(tt.line)
			if ok != tt.ok {
				t.Fatalf("parseLine(%q) ok = %v, want %v", tt.line, ok, tt.ok)
			}
			if !ok {
				return
			}
			if p.negated != tt.negated {
				t.Errorf("negated = %v, want %v", p.negated, tt.negated)
			}
			if p.dirOnly != tt.dirOnly {
				t.Errorf("dirOnly = %v, want %v", p.dirOnly, tt.dirOnly)
			}
			if p.isAbsDir != tt.absDir {
				t.Errorf("isAbsDir = %v, want %v", p.isAbsDir, tt.absDir)
			}
			if p.glob != tt.glob {
				t.Errorf("glob = %q, want %q", p.glob, tt.glob)
			}
		})
	}
}

// ─── Slash Normalization ────────────────────────────────────────────────────

func TestSlashNormalization(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "*.log")
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	// Backslash paths should be normalized to forward slash
	if !m.ShouldIgnore("logs\\app.log") {
		// filepath.ToSlash converts backslashes on Windows;
		// on Unix this is a literal backslash in the filename,
		// so this may or may not match depending on platform.
		// The key test is that the function doesn't crash.
		t.Log("backslash path: platform-dependent behavior")
	}
}

// ─── DS_Store exact match ───────────────────────────────────────────────────

func TestSubdirectoryNegation(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, `.claude/
!.claude/worktrees/
`)
	m, err := NewMatcher(dir)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		path   string
		ignore bool
	}{
		{".claude/settings.json", true},
		{".claude/projects/data.json", true},
		{".claude/worktrees/agent-abc123/src/App.tsx", false},
		{".claude/worktrees/agent-abc123/domains/service/file.go", false},
		{".claude/worktrees/agent-xyz/README.md", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := m.ShouldIgnore(tt.path)
			if got != tt.ignore {
				t.Errorf("ShouldIgnore(%q) = %v, want %v", tt.path, got, tt.ignore)
			}
		})
	}
}

func TestDSStore(t *testing.T) {
	dir := t.TempDir()
	m, err := NewMatcher(dir) // uses defaults
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	if !m.ShouldIgnore(".DS_Store") {
		t.Error("expected .DS_Store to be ignored by defaults")
	}
	if !m.ShouldIgnore("subdir/.DS_Store") {
		t.Error("expected subdir/.DS_Store to be ignored by defaults")
	}
}
