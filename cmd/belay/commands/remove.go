package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/daemon"
	"github.com/davidparkercodes/belay/internal/githooks"
	"github.com/spf13/cobra"
)

func newRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove Belay from the current directory",
		Long: `Removes Belay from the current project. This is the opposite of belay init.

Stops the daemon, removes the .belay/ directory, .belayignore file,
shell hook, and git hooks. Asks for confirmation before proceeding
unless --force is passed.`,
		RunE: runRemove,
	}

	cmd.Flags().Bool("force", false, "Skip confirmation prompt")

	return cmd
}

func runRemove(cmd *cobra.Command, args []string) error {
	force, _ := cmd.Flags().GetBool("force")

	projectRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	belayPath := filepath.Join(projectRoot, config.BelayDir)

	if _, err := os.Stat(belayPath); os.IsNotExist(err) {
		warnStyle := lipgloss.NewStyle().
			Foreground(yellow).
			Bold(true)
		fmt.Println()
		fmt.Println(warnStyle.Render("    Belay is not initialized in this directory."))
		fmt.Println()
		return nil
	}

	if !force {
		fmt.Println()

		headerStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5555")).
			Bold(true)
		fmt.Println(headerStyle.Render("    This will remove Belay and all tracked history from this project."))
		fmt.Println()

		var confirm bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("Remove Belay from this directory?").
					Description(fmt.Sprintf("Deletes %s and all Belay data. This cannot be undone.", belayPath)).
					Value(&confirm).
					WithButtonAlignment(lipgloss.Left).
					Affirmative("Yes, remove").
					Negative("Cancel"),
			),
		).WithTheme(belayTheme())

		if err := form.Run(); err != nil {
			return fmt.Errorf("cancelled: %w", err)
		}

		if !confirm {
			fmt.Println()
			fmt.Println("    Cancelled.")
			fmt.Println()
			return nil
		}
	}

	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#3E3E50"))
	fmt.Println()
	fmt.Println(separator.Render("    " + strings.Repeat("─", 44)))
	fmt.Println()

	cfg, cfgErr := config.Load(projectRoot)
	if cfgErr != nil {
		cfg = config.DefaultConfig(projectRoot)
	}

	if running, _ := daemon.IsRunning(cfg); running {
		if stopErr := daemon.Stop(cfg); stopErr != nil {
			printStep(fmt.Sprintf("Stop daemon: %v", stopErr), false)
		} else {
			printStep("Stopped daemon", true)
		}
	} else {
		printStep("Daemon not running", true)
	}

	removed, ghErr := githooks.Remove()
	if ghErr != nil {
		printStep(fmt.Sprintf("Remove git hooks: %v", ghErr), false)
	} else if len(removed) > 0 {
		printStep(fmt.Sprintf("Removed git hooks (%s)", strings.Join(removed, ", ")), true)
	} else {
		printStep("No git hooks to remove", true)
	}

	removeShellHook()

	if err := os.RemoveAll(belayPath); err != nil {
		printStep(fmt.Sprintf("Remove .belay/: %v", err), false)
	} else {
		printStep("Removed .belay/ directory", true)
	}

	ignorePath := filepath.Join(projectRoot, ".belayignore")
	if _, err := os.Stat(ignorePath); err == nil {
		if err := os.Remove(ignorePath); err != nil {
			printStep(fmt.Sprintf("Remove .belayignore: %v", err), false)
		} else {
			printStep("Removed .belayignore", true)
		}
	}

	fmt.Println()

	doneStyle := lipgloss.NewStyle().
		Foreground(green).
		Bold(true)
	fmt.Println(doneStyle.Render("    Belay has been removed from this project."))
	fmt.Println()

	return nil
}

func removeShellHook() {
	shells := []string{"zsh", "bash"}

	for _, shell := range shells {
		rcFile := shellRCFile(shell)
		content, err := os.ReadFile(rcFile)
		if err != nil {
			continue
		}

		lines := strings.Split(string(content), "\n")
		var filtered []string
		found := false

		for _, line := range lines {
			if strings.Contains(line, "belay hook init") {
				found = true
				continue
			}
			filtered = append(filtered, line)
		}

		if !found {
			continue
		}

		if err := os.WriteFile(rcFile, []byte(strings.Join(filtered, "\n")), 0644); err != nil {
			printStep(fmt.Sprintf("Remove shell hook from %s: %v", rcFile, err), false)
		} else {
			printStep(fmt.Sprintf("Removed shell hook from %s", rcFile), true)
		}
	}
}
