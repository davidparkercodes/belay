package commands

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

const aiInstructionsBlock = `## Belay Integration

This project uses Belay (.belay/ directory) for local file change tracking
with AI session attribution. Prefer belay over git log/diff when investigating
file changes.

` + "```" + `bash
belay log                             # Recent file changes
belay sessions                        # List AI sessions
belay diff --session <id>             # What a session changed
belay restore <file> --session <id>   # Restore a file to a previous state
` + "```"

func newAIInstructionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ai-instructions",
		Short: "Show instructions to paste into your AI agent's config",
		Long: `Prints a markdown block you can paste into your AI agent's
instructions file (CLAUDE.md, .cursorrules, .windsurfrules, etc.)
so the agent knows how to use Belay.`,
		Run: runAIInstructions,
	}

	return cmd
}

func runAIInstructions(cmd *cobra.Command, args []string) {
	printAIInstructions()
}

func printAIInstructions() {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(orange)

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#A1A1AA"))

	blockStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F0E6D3"))

	fmt.Println()
	fmt.Println(titleStyle.Render("  AI Agent Instructions"))
	fmt.Println()
	fmt.Println(dimStyle.Render("  Paste this into your AI agent's instructions file:"))
	fmt.Println(dimStyle.Render("  (CLAUDE.md, .cursorrules, .windsurfrules, etc.)"))
	fmt.Println()
	fmt.Println(blockStyle.Render(aiInstructionsBlock))
	fmt.Println()
}
