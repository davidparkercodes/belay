package commands

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/davidparkercodes/belay/internal/githooks"
	"github.com/spf13/cobra"
)

func newGitHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git-hooks",
		Short: "Manage git hooks for tracking git operations",
		Long: `Install, remove, or check status of git hooks that report
checkout, merge, and rewrite operations to the Belay API.

These hooks run in the background and never block git operations.`,
	}

	cmd.AddCommand(
		newGitHooksInstallCmd(),
		newGitHooksRemoveCmd(),
		newGitHooksStatusCmd(),
	)

	return cmd
}

func newGitHooksInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install Belay git hooks into the current repository",
		RunE:  runGitHooksInstall,
	}
}

func newGitHooksRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Remove Belay git hooks from the current repository",
		RunE:  runGitHooksRemove,
	}
}

func newGitHooksStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which Belay git hooks are installed",
		RunE:  runGitHooksStatus,
	}
}

func runGitHooksInstall(cmd *cobra.Command, args []string) error {
	installed, err := githooks.Install()
	if err != nil {
		return fmt.Errorf("install git hooks: %w", err)
	}

	if len(installed) == 0 {
		fmt.Println("No hooks to install.")
		return nil
	}

	successStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#50FA7B"))

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#8BE9FD"))

	fmt.Println(successStyle.Render("Belay git hooks installed:"))
	for _, name := range installed {
		fmt.Printf("  %s %s\n", valueStyle.Render("+"), name)
	}

	return nil
}

func runGitHooksRemove(cmd *cobra.Command, args []string) error {
	removed, err := githooks.Remove()
	if err != nil {
		return fmt.Errorf("remove git hooks: %w", err)
	}

	if len(removed) == 0 {
		fmt.Println("No Belay git hooks found to remove.")
		return nil
	}

	successStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#50FA7B"))

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF5555"))

	fmt.Println(successStyle.Render("Belay git hooks removed:"))
	for _, name := range removed {
		fmt.Printf("  %s %s\n", valueStyle.Render("-"), name)
	}

	return nil
}

func runGitHooksStatus(cmd *cobra.Command, args []string) error {
	statuses, err := githooks.Status()
	if err != nil {
		return fmt.Errorf("check git hook status: %w", err)
	}

	installedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#50FA7B"))

	notInstalledStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#A1A1AA"))

	for _, s := range statuses {
		if s.Installed {
			fmt.Printf("  %s %s\n", installedStyle.Render("[installed]"), s.Name)
		} else {
			fmt.Printf("  %s %s\n", notInstalledStyle.Render("[  absent ]"), s.Name)
		}
	}

	return nil
}
