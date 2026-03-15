package commands

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// claudeCodePrompt is the prompt users pass to `claude -p` to add Belay
// awareness to their CLAUDE.md. Belay never writes to CLAUDE.md directly —
// Claude Code does, with the user's review and approval.
const claudeCodePrompt = `Add a Belay Integration section to my CLAUDE.md. ` +
	`This project uses Belay (.belay/ directory) for file change tracking ` +
	`with AI session attribution. Claude should prefer 'belay log', ` +
	`'belay diff', 'belay sessions', and 'belay restore' over git log/diff ` +
	`when investigating file changes.`

func newClaudeCodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claude-code",
		Short: "Claude Code integration commands",
		Long: `Commands for integrating Belay with Claude Code.

Configure Claude Code to be aware of Belay so it can use the CLI
for file recovery and session inspection when needed.`,
	}

	cmd.AddCommand(newClaudeCodeSetupCmd())

	return cmd
}

func newClaudeCodeSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Configure Claude Code to work with Belay",
		Long: `Prints a claude command you can run to add Belay awareness
to your CLAUDE.md. Review the command, edit if you like, and press Enter.

Belay never modifies your CLAUDE.md directly — Claude Code does,
with your review and approval.`,
		Run: runClaudeCodeSetup,
	}

	return cmd
}

func runClaudeCodeSetup(cmd *cobra.Command, args []string) {
	printClaudeCodeInstructions()
}

// printClaudeCodeInstructions prints the claude -p command for the user to run.
func printClaudeCodeInstructions() {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#BD93F9"))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#A1A1AA"))

	cmdStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#50FA7B"))

	fmt.Println()
	fmt.Println(titleStyle.Render("  Claude Code Integration"))
	fmt.Println()
	fmt.Println(dimStyle.Render("  Run this command to add Belay awareness to your CLAUDE.md."))
	fmt.Println(dimStyle.Render("  Feel free to edit the prompt before pressing Enter."))
	fmt.Println()
	fmt.Println(cmdStyle.Render(fmt.Sprintf(`  claude -p "%s"`, claudeCodePrompt)))
	fmt.Println()
}
