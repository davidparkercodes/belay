package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/githooks"
	"github.com/spf13/cobra"
)

// projectType represents a detected project language/framework.
type projectType struct {
	Name     string
	Detected bool
	Patterns []string
}

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize Belay in the current directory",
		Long: `Sets up a new Belay-tracked project by creating the .belay/ directory
structure, generating a default configuration, and optionally creating an
initial snapshot of all existing files.

Runs an interactive TUI wizard by default. Use -y/--non-interactive to
skip the wizard and use defaults (for CI/scripts).

Safe to run multiple times (idempotent).`,
		RunE: runInit,
	}

	cmd.Flags().Bool("force", false, "Reinitialize even if .belay/ exists")
	cmd.Flags().BoolP("non-interactive", "y", false, "Skip wizard, use defaults (for CI/scripts)")

	return cmd
}

// detectProjectTypes checks which project types are present in the directory.
func detectProjectTypes(projectRoot string) []projectType {
	types := []projectType{
		{
			Name: "Node.js",
			Patterns: []string{
				"node_modules/", "dist/", ".next/", ".nuxt/", ".vite/",
				".turbo/", "out/", ".cache/", "coverage/",
			},
		},
		{
			Name: "Go",
			Patterns: []string{
				"vendor/", "bin/",
			},
		},
		{
			Name: "Python",
			Patterns: []string{
				"__pycache__/", ".venv/", "*.pyc", "*.pyo",
				".mypy_cache/", ".pytest_cache/", "*.egg-info/",
			},
		},
		{
			Name: "Rust",
			Patterns: []string{
				"target/",
			},
		},
	}

	detectors := map[string][]string{
		"Node.js": {"package.json"},
		"Go":      {"go.mod"},
		"Python":  {"requirements.txt", "pyproject.toml", "setup.py", "Pipfile"},
		"Rust":    {"Cargo.toml"},
	}

	for i, pt := range types {
		for _, marker := range detectors[pt.Name] {
			if _, err := os.Stat(filepath.Join(projectRoot, marker)); err == nil {
				types[i].Detected = true
				break
			}
		}
	}

	return types
}

// buildIgnoreContent constructs the .belayignore content from detected project types
// and the user's selection.
func buildIgnoreContent(types []projectType, useRecommended bool) string {
	var b strings.Builder

	b.WriteString("# Belay ignore patterns\n")
	b.WriteString("# Syntax is identical to .gitignore\n\n")

	// Always-included defaults
	b.WriteString("# Version control\n")
	b.WriteString(".git/\n")
	b.WriteString(".belay/\n\n")

	// IDE / Editor
	b.WriteString("# IDE / Editor\n")
	b.WriteString(".idea/\n")
	b.WriteString(".vscode/\n")
	b.WriteString("*.swp\n")
	b.WriteString("*.swo\n")
	b.WriteString("*~\n\n")

	// OS files
	b.WriteString("# OS files\n")
	b.WriteString(".DS_Store\n")
	b.WriteString("Thumbs.db\n\n")

	// Logs
	b.WriteString("# Logs\n")
	b.WriteString("*.log\n\n")

	// Environment files
	b.WriteString("# Environment files (may contain secrets)\n")
	b.WriteString(".env\n")
	b.WriteString(".env.local\n")
	b.WriteString(".env.*.local\n\n")

	// Common build artifacts (always included)
	b.WriteString("# Build artifacts\n")
	b.WriteString("build/\n")
	b.WriteString("*.o\n")
	b.WriteString("*.so\n")
	b.WriteString("*.dylib\n\n")

	if useRecommended {
		// Add project-type-specific patterns for detected types
		for _, pt := range types {
			if !pt.Detected {
				continue
			}
			b.WriteString(fmt.Sprintf("# %s\n", pt.Name))
			for _, pattern := range pt.Patterns {
				b.WriteString(pattern + "\n")
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

// detectedTypeNames returns the names of all detected project types.
func detectedTypeNames(types []projectType) []string {
	var names []string
	for _, pt := range types {
		if pt.Detected {
			names = append(names, pt.Name)
		}
	}
	return names
}

// countIgnorePatterns counts non-comment, non-empty lines.
func countIgnorePatterns(content string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			count++
		}
	}
	return count
}

// currentShell returns the user's current shell name.
func currentShell() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		if runtime.GOOS == "windows" {
			return "powershell"
		}
		return "bash"
	}
	return filepath.Base(shell)
}

func runInit(cmd *cobra.Command, args []string) error {
	force, _ := cmd.Flags().GetBool("force")
	nonInteractive, _ := cmd.Flags().GetBool("non-interactive")

	projectRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	belayPath := filepath.Join(projectRoot, config.BelayDir)

	if info, err := os.Stat(belayPath); err == nil && info.IsDir() && !force {
		fmt.Println("Belay is already initialized in this directory.")
		fmt.Println("Use --force to reinitialize.")
		return nil
	}

	// Detect project types
	detectedTypes := detectProjectTypes(projectRoot)
	detected := detectedTypeNames(detectedTypes)

	// Default wizard answers
	useRecommendedIgnore := true
	autoStartDaemon := true
	claudeCodeIntegration := true
	gitDir := filepath.Join(projectRoot, ".git")
	hasGit := false
	if info, statErr := os.Stat(gitDir); statErr == nil && info.IsDir() {
		hasGit = true
	}
	installGitHooks := hasGit

	if !nonInteractive {
		// Build the detection label
		var detectionLabel string
		if len(detected) > 0 {
			detectionLabel = fmt.Sprintf("Detected %s project. Use recommended ignore patterns?", strings.Join(detected, " + "))
		} else {
			detectionLabel = "No specific project type detected. Use default ignore patterns?"
		}

		fields := []huh.Field{
			huh.NewConfirm().
				Title(detectionLabel).
				Value(&useRecommendedIgnore).
				Affirmative("Yes").
				Negative("No"),

			huh.NewConfirm().
				Title("Start Belay daemon automatically when you enter this project?").
				Value(&autoStartDaemon).
				Affirmative("Yes").
				Negative("No"),

			huh.NewConfirm().
				Title("Add Belay integration to your Claude Code config?").
				Value(&claudeCodeIntegration).
				Affirmative("Yes").
				Negative("No"),
		}

		if hasGit {
			fields = append(fields,
				huh.NewConfirm().
					Title("Install git hooks for tracking git operations?").
					Value(&installGitHooks).
					Affirmative("Yes").
					Negative("No"),
			)
		}

		form := huh.NewForm(
			huh.NewGroup(fields...),
		).WithTheme(huh.ThemeDracula())

		err := form.Run()
		if err != nil {
			// User cancelled (ctrl+c) -- exit gracefully
			return fmt.Errorf("wizard cancelled: %w", err)
		}
	}

	// --- Execute init steps ---

	// 1. Create directory structure
	dirs := []string{
		belayPath,
		filepath.Join(belayPath, "events"),
		filepath.Join(belayPath, "objects"),
		filepath.Join(belayPath, "stashes"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	// 2. Write config
	cfg := config.DefaultConfig(projectRoot)
	configContent := cfg.ToTOML()
	if err := os.WriteFile(cfg.ConfigPath(), []byte(configContent), 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// 3. Write .belayignore
	ignoreContent := buildIgnoreContent(detectedTypes, useRecommendedIgnore)
	ignorePath := filepath.Join(projectRoot, ".belayignore")
	ignoreStatus := "created"
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) {
		if err := os.WriteFile(ignorePath, []byte(ignoreContent), 0644); err != nil {
			return fmt.Errorf("write .belayignore: %w", err)
		}
	} else {
		ignoreStatus = "already exists, skipped"
	}

	if hasGit {
		if err := ensureGitignoreEntry(projectRoot, ".belay/"); err != nil {
			fmt.Printf("  Warning: could not update .gitignore: %v\n", err)
		}
	}

	gitHooksStatus := ""
	if installGitHooks {
		installed, ghErr := githooks.Install()
		if ghErr != nil {
			fmt.Printf("  Warning: could not install git hooks: %v\n", ghErr)
			gitHooksStatus = "failed"
		} else if len(installed) > 0 {
			gitHooksStatus = fmt.Sprintf("installed (%s)", strings.Join(installed, ", "))
		}
	}

	autoStartStatus := "disabled"
	shell := currentShell()
	if autoStartDaemon {
		autoStartStatus = fmt.Sprintf("enabled (add shell hook to ~/.%src)", shell)
	}

	// --- Print summary ---
	patternCount := countIgnorePatterns(ignoreContent)

	// Use lipgloss for styled output
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#BD93F9"))

	successStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#50FA7B"))

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F8F8F2")).
		Width(14)

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#8BE9FD"))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#A1A1AA"))

	fmt.Println()
	fmt.Println(successStyle.Render("  Belay initialized!"))
	fmt.Println()
	fmt.Printf("  %s %s\n", labelStyle.Render("Project:"), valueStyle.Render(projectRoot))
	fmt.Printf("  %s %s\n", labelStyle.Render("Config:"), valueStyle.Render(".belay/config.toml"))

	ignoreDesc := fmt.Sprintf(".belayignore (%d patterns, %s)", patternCount, ignoreStatus)
	fmt.Printf("  %s %s\n", labelStyle.Render("Ignore:"), valueStyle.Render(ignoreDesc))

	fmt.Printf("  %s %s\n", labelStyle.Render("Auto-start:"), valueStyle.Render(autoStartStatus))

	if gitHooksStatus != "" {
		fmt.Printf("  %s %s\n", labelStyle.Render("Git hooks:"), valueStyle.Render(gitHooksStatus))
	}

	if len(detected) > 0 {
		fmt.Printf("  %s %s\n", labelStyle.Render("Detected:"), valueStyle.Render(strings.Join(detected, ", ")))
	}

	fmt.Println()

	if autoStartDaemon {
		fmt.Println(dimStyle.Render("  To enable auto-start on cd, add this to your shell config:"))
		fmt.Println()
		fmt.Printf("  %s\n", titleStyle.Render(fmt.Sprintf(`eval "$(belay hook init %s)"`, shell)))
		fmt.Println()
	}

	if claudeCodeIntegration {
		printClaudeCodeInstructions()
	}

	pid, startErr := startDaemonBackground(projectRoot)
	if startErr != nil {
		fmt.Printf("  %s %s\n", labelStyle.Render("Daemon:"), dimStyle.Render(fmt.Sprintf("failed to start: %v", startErr)))
		fmt.Printf("  %s\n", dimStyle.Render("Run 'belay daemon start' to start manually."))
	} else {
		fmt.Printf("  %s\n", successStyle.Render(fmt.Sprintf("  Daemon started (PID %d) -- watching for file changes.", pid)))
	}
	fmt.Println()

	return nil
}

func defaultBelayIgnore() string {
	return `# Belay ignore patterns
# Syntax is identical to .gitignore

# Version control
.git/
.belay/

# Dependencies
node_modules/
vendor/
.venv/
__pycache__/
*.pyc
*.pyo

# Build artifacts
build/
dist/
out/
bin/
*.o
*.so
*.dylib

# IDE / Editor
.idea/
.vscode/
*.swp
*.swo
*~

# OS files
.DS_Store
Thumbs.db

# Frontend build caches
.next/
.nuxt/
.vite/
.turbo/

# Logs
*.log

# Environment files (may contain secrets)
.env
.env.local
.env.*.local
`
}

func ensureGitignoreEntry(projectRoot, entry string) error {
	gitignorePath := filepath.Join(projectRoot, ".gitignore")

	content, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	lines := string(content)
	for _, line := range splitLines(lines) {
		if line == entry {
			return nil
		}
	}

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	if len(content) > 0 && content[len(content)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}

	_, err = f.WriteString(entry + "\n")
	return err
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
