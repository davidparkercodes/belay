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

const belayASCII = `    ██████  ███████ ██       █████  ██    ██
    ██   ██ ██      ██      ██   ██  ██  ██
    ██████  █████   ██      ███████   ████
    ██   ██ ██      ██      ██   ██    ██
    ██████  ███████ ███████ ██   ██    ██`

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

func buildIgnoreContent(types []projectType, useRecommended bool) string {
	var b strings.Builder

	b.WriteString("# Belay ignore patterns\n")
	b.WriteString("# Syntax is identical to .gitignore\n\n")

	b.WriteString("# Version control\n")
	b.WriteString(".git/\n")
	b.WriteString(".belay/\n\n")

	b.WriteString("# IDE / Editor\n")
	b.WriteString(".idea/\n")
	b.WriteString(".vscode/\n")
	b.WriteString("*.swp\n")
	b.WriteString("*.swo\n")
	b.WriteString("*~\n\n")

	b.WriteString("# OS files\n")
	b.WriteString(".DS_Store\n")
	b.WriteString("Thumbs.db\n\n")

	b.WriteString("# Logs\n")
	b.WriteString("*.log\n\n")

	b.WriteString("# Environment files (may contain secrets)\n")
	b.WriteString(".env\n")
	b.WriteString(".env.local\n")
	b.WriteString(".env.*.local\n\n")

	b.WriteString("# Build artifacts\n")
	b.WriteString("build/\n")
	b.WriteString("*.o\n")
	b.WriteString("*.so\n")
	b.WriteString("*.dylib\n\n")

	if useRecommended {
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

func detectedTypeNames(types []projectType) []string {
	var names []string
	for _, pt := range types {
		if pt.Detected {
			names = append(names, pt.Name)
		}
	}
	return names
}

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

var (
	cyan    = lipgloss.Color("#00F0FF")
	green   = lipgloss.Color("#50FA7B")
	purple  = lipgloss.Color("#BD93F9")
	white   = lipgloss.Color("#F8F8F2")
	dimGray = lipgloss.Color("#A1A1AA")
	yellow  = lipgloss.Color("#F1FA8C")
)

func printBanner(version string) {
	bannerStyle := lipgloss.NewStyle().
		Foreground(cyan).
		Bold(true)

	versionStyle := lipgloss.NewStyle().
		Foreground(purple).
		Bold(true)

	taglineStyle := lipgloss.NewStyle().
		Foreground(dimGray)

	fmt.Println()
	fmt.Println(bannerStyle.Render(belayASCII))
	fmt.Println()
	fmt.Printf("    %s  %s\n",
		versionStyle.Render(version),
		taglineStyle.Render("AI-aware local version control"))
	fmt.Println()
}

func printEnvironment(projectRoot string, detected []string, hasGit bool) {
	headerStyle := lipgloss.NewStyle().
		Foreground(white).
		Bold(true)

	valueStyle := lipgloss.NewStyle().
		Foreground(cyan)

	dimStyle := lipgloss.NewStyle().
		Foreground(dimGray)

	fmt.Println(headerStyle.Render("    Environment"))
	fmt.Println()

	dirName := filepath.Base(projectRoot)
	fmt.Printf("    %s %s\n",
		dimStyle.Render("Directory"),
		valueStyle.Render(dirName+"/"))

	if hasGit {
		fmt.Printf("    %s     %s\n",
			dimStyle.Render("Git"),
			valueStyle.Render("detected"))
	}

	if len(detected) > 0 {
		fmt.Printf("    %s %s\n",
			dimStyle.Render("Projects"),
			valueStyle.Render(strings.Join(detected, ", ")))
	} else {
		fmt.Printf("    %s %s\n",
			dimStyle.Render("Projects"),
			dimStyle.Render("none detected"))
	}

	fmt.Println()

	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#3E3E50"))
	fmt.Println(separator.Render("    " + strings.Repeat("─", 44)))
	fmt.Println()
}

func printStep(label string, success bool) {
	checkStyle := lipgloss.NewStyle().
		Foreground(green).
		Bold(true)

	failStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF5555")).
		Bold(true)

	labelStyle := lipgloss.NewStyle().
		Foreground(white)

	if success {
		fmt.Printf("    %s %s\n", checkStyle.Render("+"), labelStyle.Render(label))
	} else {
		fmt.Printf("    %s %s\n", failStyle.Render("x"), labelStyle.Render(label))
	}
}

func printStepDetail(label, detail string) {
	checkStyle := lipgloss.NewStyle().
		Foreground(green).
		Bold(true)

	labelStyle := lipgloss.NewStyle().
		Foreground(white)

	detailStyle := lipgloss.NewStyle().
		Foreground(dimGray)

	fmt.Printf("    %s %s %s\n", checkStyle.Render("+"), labelStyle.Render(label), detailStyle.Render(detail))
}

func printTip(text string) {
	tipLabel := lipgloss.NewStyle().
		Foreground(yellow).
		Bold(true)

	tipText := lipgloss.NewStyle().
		Foreground(dimGray)

	fmt.Printf("    %s %s\n", tipLabel.Render("TIP"), tipText.Render(text))
}

func printCommand(cmd string) {
	cmdStyle := lipgloss.NewStyle().
		Foreground(cyan)

	fmt.Printf("         %s\n", cmdStyle.Render(cmd))
}

func runInit(cmd *cobra.Command, args []string) error {
	force, _ := cmd.Flags().GetBool("force")
	nonInteractive, _ := cmd.Flags().GetBool("non-interactive")

	projectRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	belayPath := filepath.Join(projectRoot, config.BelayDir)

	version := cmd.Root().Version

	if info, err := os.Stat(belayPath); err == nil && info.IsDir() && !force {
		printBanner(version)

		alreadyStyle := lipgloss.NewStyle().
			Foreground(yellow).
			Bold(true)

		dimStyle := lipgloss.NewStyle().
			Foreground(dimGray)

		fmt.Println(alreadyStyle.Render("    Belay is already initialized in this directory."))
		fmt.Println(dimStyle.Render("    Use --force to reinitialize."))
		fmt.Println()
		return nil
	}

	detectedTypes := detectProjectTypes(projectRoot)
	detected := detectedTypeNames(detectedTypes)

	gitDir := filepath.Join(projectRoot, ".git")
	hasGit := false
	if info, statErr := os.Stat(gitDir); statErr == nil && info.IsDir() {
		hasGit = true
	}

	printBanner(version)
	printEnvironment(projectRoot, detected, hasGit)

	useRecommendedIgnore := true
	autoStartDaemon := true
	claudeCodeIntegration := true
	installGitHooks := hasGit

	if !nonInteractive {
		var detectionLabel string
		if len(detected) > 0 {
			detectionLabel = fmt.Sprintf("Use recommended ignore patterns for %s?", strings.Join(detected, " + "))
		} else {
			detectionLabel = "Use default ignore patterns?"
		}

		fields := []huh.Field{
			huh.NewConfirm().
				Title(detectionLabel).
				Value(&useRecommendedIgnore).
				Affirmative("Yes").
				Negative("No"),

			huh.NewConfirm().
				Title("Auto-start daemon when entering this project?").
				Value(&autoStartDaemon).
				Affirmative("Yes").
				Negative("No"),

			huh.NewConfirm().
				Title("Set up Claude Code integration?").
				Value(&claudeCodeIntegration).
				Affirmative("Yes").
				Negative("No"),
		}

		if hasGit {
			fields = append(fields,
				huh.NewConfirm().
					Title("Install git hooks to track git operations?").
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
			return fmt.Errorf("wizard cancelled: %w", err)
		}
	}

	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#3E3E50"))
	fmt.Println(separator.Render("    " + strings.Repeat("─", 44)))
	fmt.Println()

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
	printStep("Created .belay/ directory", true)

	cfg := config.DefaultConfig(projectRoot)
	configContent := cfg.ToTOML()
	if err := os.WriteFile(cfg.ConfigPath(), []byte(configContent), 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	printStep("Wrote config.toml", true)

	ignoreContent := buildIgnoreContent(detectedTypes, useRecommendedIgnore)
	ignorePath := filepath.Join(projectRoot, ".belayignore")
	patternCount := countIgnorePatterns(ignoreContent)
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) {
		if err := os.WriteFile(ignorePath, []byte(ignoreContent), 0644); err != nil {
			return fmt.Errorf("write .belayignore: %w", err)
		}
		printStepDetail(
			fmt.Sprintf("Created .belayignore (%d patterns)", patternCount),
			"")
	} else {
		printStepDetail("Kept existing .belayignore", "(not overwritten)")
	}

	if hasGit {
		if err := ensureGitignoreEntry(projectRoot, ".belay/"); err != nil {
			printStep(fmt.Sprintf("Update .gitignore: %v", err), false)
		} else {
			printStep("Added .belay/ to .gitignore", true)
		}
	}

	if installGitHooks {
		installed, ghErr := githooks.Install()
		if ghErr != nil {
			printStep(fmt.Sprintf("Git hooks: %v", ghErr), false)
		} else if len(installed) > 0 {
			printStep(fmt.Sprintf("Installed git hooks (%s)", strings.Join(installed, ", ")), true)
		}
	}

	pid, startErr := startDaemonBackground(projectRoot)
	if startErr != nil {
		printStep(fmt.Sprintf("Start daemon: %v", startErr), false)
	} else {
		printStep(fmt.Sprintf("Daemon started (PID %d)", pid), true)
	}

	fmt.Println()

	successStyle := lipgloss.NewStyle().
		Foreground(green).
		Bold(true)

	fmt.Println(successStyle.Render("    Belay is watching your files."))
	fmt.Println()

	shell := currentShell()
	if autoStartDaemon {
		printTip("Auto-start daemon on cd:")
		printCommand(fmt.Sprintf(`eval "$(belay hook init %s)"`, shell))
		fmt.Println()
	}

	if claudeCodeIntegration {
		printTip("Add Belay to your Claude Code context:")
		printCommand(fmt.Sprintf(`claude -p "%s"`, claudeCodePrompt))
		fmt.Println()
	}

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
