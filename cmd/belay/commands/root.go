package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewRootCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "belay",
		Short: "Local version control for AI agentic coding",
		Long: `Belay is an event-sourced filesystem journal that automatically
captures every file change with AI session attribution.

Built for concurrent multi-agent development workflows where
traditional git fails. Never lose work, replay any session,
attribute every change.`,
		Version: version,
		SilenceUsage: true,
	}

	cmd.SetVersionTemplate(fmt.Sprintf("belay %s\n", version))

	cmd.AddCommand(
		newInitCmd(),
		newRemoveCmd(),
		newStatusCmd(),
		newDaemonCmd(version),
		newLogCmd(),
		newGrepCmd(),
		newDiffCmd(),
		newSessionsCmd(),
		newRestoreCmd(),
		newGCCmd(),
		newReplayCmd(),
		newConflictsCmd(),
		newCommitCmd(),
		newProjectCmd(),
		newSnapshotCmd(),
		newCheckpointCmd(),
		newCheckpointsCmd(),
		newRecordCmd(),
		newRebuildIndexCmd(),
		newAIInstructionsCmd(),
		newHookCmd(),
		newGitHooksCmd(),
	)

	return cmd
}
