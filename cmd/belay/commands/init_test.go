package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidparkercodes/belay/internal/config"
)

func TestInitCmd_BasicProperties(t *testing.T) {
	cmd := newInitCmd()

	if cmd.Use != "init" {
		t.Errorf("Use = %q, want %q", cmd.Use, "init")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}

	force := cmd.Flags().Lookup("force")
	if force == nil {
		t.Fatal("--force flag not registered")
	}
	if force.DefValue != "false" {
		t.Errorf("--force default = %q, want %q", force.DefValue, "false")
	}

	nonInteractive := cmd.Flags().Lookup("non-interactive")
	if nonInteractive == nil {
		t.Fatal("--non-interactive flag not registered")
	}
	if nonInteractive.DefValue != "false" {
		t.Errorf("--non-interactive default = %q, want %q", nonInteractive.DefValue, "false")
	}
}

func TestInitCmd_CreatesDirectoryStructure(t *testing.T) {
	dir := t.TempDir()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	root := NewRootCmd("test")
	root.SetArgs([]string{"init", "-y"})
	root.SetOut(&strings.Builder{})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	expectedDirs := []string{
		filepath.Join(dir, config.BelayDir),
		filepath.Join(dir, config.BelayDir, "events"),
		filepath.Join(dir, config.BelayDir, "objects"),
		filepath.Join(dir, config.BelayDir, "stashes"),
	}

	for _, d := range expectedDirs {
		info, err := os.Stat(d)
		if err != nil {
			t.Errorf("expected directory %s to exist: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s should be a directory", d)
		}
	}
}

func TestInitCmd_CreatesConfigFile(t *testing.T) {
	dir := t.TempDir()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	root := NewRootCmd("test")
	root.SetArgs([]string{"init", "-y"})
	root.SetOut(&strings.Builder{})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	configPath := filepath.Join(dir, config.BelayDir, config.ConfigFile)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config file should exist: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[daemon]") {
		t.Error("config should contain [daemon] section")
	}
	if !strings.Contains(content, "[api]") {
		t.Error("config should contain [api] section")
	}
}

func TestInitCmd_CreatesBelayignore(t *testing.T) {
	dir := t.TempDir()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	root := NewRootCmd("test")
	root.SetArgs([]string{"init", "-y"})
	root.SetOut(&strings.Builder{})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	ignorePath := filepath.Join(dir, ".belayignore")
	data, err := os.ReadFile(ignorePath)
	if err != nil {
		t.Fatalf(".belayignore should exist: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, ".git/") {
		t.Error(".belayignore should contain .git/")
	}
	if !strings.Contains(content, ".belay/") {
		t.Error(".belayignore should contain .belay/")
	}
}

func TestInitCmd_Idempotent(t *testing.T) {
	dir := t.TempDir()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	// First init
	root := NewRootCmd("test")
	root.SetArgs([]string{"init", "-y"})
	root.SetOut(&strings.Builder{})
	if err := root.Execute(); err != nil {
		t.Fatalf("first init: %v", err)
	}

	// Second init without --force should not error
	root2 := NewRootCmd("test")
	root2.SetArgs([]string{"init", "-y"})
	root2.SetOut(&strings.Builder{})
	if err := root2.Execute(); err != nil {
		t.Fatalf("second init should succeed without error: %v", err)
	}
}

func TestInitCmd_ForceReinitializes(t *testing.T) {
	dir := t.TempDir()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	// First init
	root := NewRootCmd("test")
	root.SetArgs([]string{"init", "-y"})
	root.SetOut(&strings.Builder{})
	if err := root.Execute(); err != nil {
		t.Fatalf("first init: %v", err)
	}

	// Modify config
	configPath := filepath.Join(dir, config.BelayDir, config.ConfigFile)
	if err := os.WriteFile(configPath, []byte("[api]\nport = 9999\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Reinitialize with --force
	root2 := NewRootCmd("test")
	root2.SetArgs([]string{"init", "--force", "-y"})
	root2.SetOut(&strings.Builder{})
	if err := root2.Execute(); err != nil {
		t.Fatalf("init --force: %v", err)
	}

	// Config should be overwritten with defaults
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), "port = 9999") {
		t.Error("config should have been overwritten by --force")
	}
}

func TestInitCmd_AddsToGitignore(t *testing.T) {
	dir := t.TempDir()

	// Create a .git directory to trigger gitignore update
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	root := NewRootCmd("test")
	root.SetArgs([]string{"init", "-y"})
	root.SetOut(&strings.Builder{})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	gitignorePath := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf(".gitignore should exist: %v", err)
	}
	if !strings.Contains(string(data), ".belay/") {
		t.Error(".gitignore should contain .belay/")
	}
}

func TestInitCmd_PreservesExistingBelayignore(t *testing.T) {
	dir := t.TempDir()

	// Create a custom .belayignore before init
	ignorePath := filepath.Join(dir, ".belayignore")
	customContent := "# custom patterns\nmy-custom-dir/\n"
	if err := os.WriteFile(ignorePath, []byte(customContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	root := NewRootCmd("test")
	root.SetArgs([]string{"init", "-y"})
	root.SetOut(&strings.Builder{})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, err := os.ReadFile(ignorePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != customContent {
		t.Error("existing .belayignore should be preserved")
	}
}

// --- Project detection tests ---

func TestDetectProjectTypes_Go(t *testing.T) {
	dir := t.TempDir()

	// Create go.mod marker
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	types := detectProjectTypes(dir)
	found := false
	for _, pt := range types {
		if pt.Name == "Go" && pt.Detected {
			found = true
			break
		}
	}
	if !found {
		t.Error("should detect Go project when go.mod exists")
	}
}

func TestDetectProjectTypes_NodeJS(t *testing.T) {
	dir := t.TempDir()

	// Create package.json marker
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	types := detectProjectTypes(dir)
	found := false
	for _, pt := range types {
		if pt.Name == "Node.js" && pt.Detected {
			found = true
			break
		}
	}
	if !found {
		t.Error("should detect Node.js project when package.json exists")
	}
}

func TestDetectProjectTypes_Python(t *testing.T) {
	dir := t.TempDir()

	// Create pyproject.toml marker
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[tool]"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	types := detectProjectTypes(dir)
	found := false
	for _, pt := range types {
		if pt.Name == "Python" && pt.Detected {
			found = true
			break
		}
	}
	if !found {
		t.Error("should detect Python project when pyproject.toml exists")
	}
}

func TestDetectProjectTypes_Rust(t *testing.T) {
	dir := t.TempDir()

	// Create Cargo.toml marker
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	types := detectProjectTypes(dir)
	found := false
	for _, pt := range types {
		if pt.Name == "Rust" && pt.Detected {
			found = true
			break
		}
	}
	if !found {
		t.Error("should detect Rust project when Cargo.toml exists")
	}
}

func TestDetectProjectTypes_None(t *testing.T) {
	dir := t.TempDir()

	types := detectProjectTypes(dir)
	for _, pt := range types {
		if pt.Detected {
			t.Errorf("should not detect %s in empty directory", pt.Name)
		}
	}
}

func TestDetectProjectTypes_Multiple(t *testing.T) {
	dir := t.TempDir()

	// Create both Go and Node markers
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644)

	types := detectProjectTypes(dir)
	names := detectedTypeNames(types)

	if len(names) != 2 {
		t.Errorf("expected 2 detected types, got %d: %v", len(names), names)
	}
}

// --- Ignore content tests ---

func TestBuildIgnoreContent_WithRecommended(t *testing.T) {
	types := []projectType{
		{Name: "Go", Detected: true, Patterns: []string{"vendor/", "bin/"}},
		{Name: "Node.js", Detected: false, Patterns: []string{"node_modules/", "dist/"}},
	}

	content := buildIgnoreContent(types, true)

	if !strings.Contains(content, ".git/") {
		t.Error("should always include .git/")
	}
	if !strings.Contains(content, ".belay/") {
		t.Error("should always include .belay/")
	}
	if !strings.Contains(content, "vendor/") {
		t.Error("should include Go patterns when detected")
	}
	if strings.Contains(content, "# Node.js") {
		t.Error("should not include Node.js section when not detected")
	}
}

func TestBuildIgnoreContent_WithoutRecommended(t *testing.T) {
	types := []projectType{
		{Name: "Go", Detected: true, Patterns: []string{"vendor/", "bin/"}},
	}

	content := buildIgnoreContent(types, false)

	if !strings.Contains(content, ".git/") {
		t.Error("should always include .git/")
	}
	if strings.Contains(content, "vendor/") {
		t.Error("should not include project-specific patterns when not recommended")
	}
}

func TestCountIgnorePatterns(t *testing.T) {
	content := "# comment\n\n.git/\n.belay/\nnode_modules/\n"
	count := countIgnorePatterns(content)
	if count != 3 {
		t.Errorf("countIgnorePatterns = %d, want 3", count)
	}
}

// --- Helper function tests ---

func TestSplitLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"single line", "hello", []string{"hello"}},
		{"two lines", "a\nb", []string{"a", "b"}},
		{"trailing newline", "a\nb\n", []string{"a", "b"}},
		{"blank lines", "a\n\nb", []string{"a", "", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitLines(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("splitLines(%q) returned %d lines, want %d", tt.input, len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitLines(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestDefaultBelayIgnore(t *testing.T) {
	content := defaultBelayIgnore()

	requiredPatterns := []string{
		".git/",
		".belay/",
		"node_modules/",
		"__pycache__/",
		".DS_Store",
	}

	for _, pattern := range requiredPatterns {
		if !strings.Contains(content, pattern) {
			t.Errorf("defaultBelayIgnore() should contain %q", pattern)
		}
	}
}

func TestEnsureGitignoreEntry_CreatesNew(t *testing.T) {
	dir := t.TempDir()

	if err := ensureGitignoreEntry(dir, ".belay/"); err != nil {
		t.Fatalf("ensureGitignoreEntry: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), ".belay/") {
		t.Error("should contain .belay/ entry")
	}
}

func TestEnsureGitignoreEntry_SkipsDuplicate(t *testing.T) {
	dir := t.TempDir()

	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(".belay/\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := ensureGitignoreEntry(dir, ".belay/"); err != nil {
		t.Fatalf("ensureGitignoreEntry: %v", err)
	}

	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Count(string(data), ".belay/") != 1 {
		t.Error("should not duplicate .belay/ entry")
	}
}

func TestEnsureGitignoreEntry_AppendsNewline(t *testing.T) {
	dir := t.TempDir()

	// Write a file without trailing newline
	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("some-pattern"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := ensureGitignoreEntry(dir, ".belay/"); err != nil {
		t.Fatalf("ensureGitignoreEntry: %v", err)
	}

	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "some-pattern\n.belay/\n") {
		t.Errorf("should have newline before appended entry, got: %q", content)
	}
}

// --- Non-interactive / -y flag tests ---

func TestInitCmd_NonInteractiveFlag(t *testing.T) {
	dir := t.TempDir()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	root := NewRootCmd("test")
	root.SetArgs([]string{"init", "--non-interactive"})
	root.SetOut(&strings.Builder{})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Should create all expected files
	if _, err := os.Stat(filepath.Join(dir, config.BelayDir)); err != nil {
		t.Error(".belay/ directory should exist")
	}
	if _, err := os.Stat(filepath.Join(dir, config.BelayDir, config.ConfigFile)); err != nil {
		t.Error("config.toml should exist")
	}
	if _, err := os.Stat(filepath.Join(dir, ".belayignore")); err != nil {
		t.Error(".belayignore should exist")
	}
}

func TestInitCmd_DetectsGoProject(t *testing.T) {
	dir := t.TempDir()

	// Create go.mod in the temp dir
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	root := NewRootCmd("test")
	root.SetArgs([]string{"init", "-y"})
	root.SetOut(&strings.Builder{})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// .belayignore should contain Go-specific patterns
	data, err := os.ReadFile(filepath.Join(dir, ".belayignore"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "# Go") {
		t.Error(".belayignore should contain Go section")
	}
	if !strings.Contains(content, "vendor/") {
		t.Error(".belayignore should contain vendor/")
	}
}
