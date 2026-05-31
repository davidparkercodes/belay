package commands

import (
	"fmt"

	"github.com/davidparkercodes/belay/internal/config"
	gitbridge "github.com/davidparkercodes/belay/internal/git"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/store"

	"github.com/spf13/cobra"
)

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Project a session's net changes onto a git ref via plumbing (no working-tree mutation)",
		Long: `Build a single git commit from a session's net file changes and append it to a
target ref (default refs/heads/belay-history) using git plumbing only.

Unlike 'belay commit', this never touches the working tree, the index, or HEAD, so
it is safe to run while other AI sessions are editing the same checkout. It is the
mechanism behind the serialized Belay->git projector: many concurrent sessions, one
linear projection branch, no collisions on HEAD.`,
		RunE: runProject,
	}
	cmd.Flags().StringP("session", "s", "", "Session ID to project (required)")
	cmd.Flags().StringP("message", "m", "", "Commit message (default: generated summary)")
	cmd.Flags().String("to-ref", "refs/heads/belay-history", "Target ref to append the projection commit to")
	cmd.Flags().String("base-ref", "HEAD", "Ref to bootstrap the full base tree from when --to-ref does not yet exist")
	cmd.Flags().Bool("no-metadata", false, "Skip Belay trailers in commit message")
	cmd.Flags().Bool("dry-run", false, "Show what would be projected without writing")
	cmd.Flags().String("push", "", "After projecting, push the ref to this remote (e.g. origin)")
	_ = cmd.MarkFlagRequired("session")
	return cmd
}

func runProject(cmd *cobra.Command, args []string) error {
	sessionID, _ := cmd.Flags().GetString("session")
	message, _ := cmd.Flags().GetString("message")
	toRef, _ := cmd.Flags().GetString("to-ref")
	baseRef, _ := cmd.Flags().GetString("base-ref")
	noMetadata, _ := cmd.Flags().GetBool("no-metadata")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	pushRemote, _ := cmd.Flags().GetString("push")

	projectRoot, err := config.FindProjectRoot()
	if err != nil {
		return fmt.Errorf("not a belay project: %w", err)
	}
	cfg, err := config.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	idx, err := index.Open(cfg.IndexPath())
	if err != nil {
		return fmt.Errorf("open index: %w", err)
	}
	defer idx.Close()
	objStore, err := store.NewStore(cfg.ObjectsDir(), cfg.Storage.CompressionEnabled)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer objStore.Close()

	res, err := gitbridge.ProjectSession(idx, objStore, projectRoot, gitbridge.ProjectOptions{
		SessionID:  sessionID,
		TargetRef:  toRef,
		BaseRef:    baseRef,
		Message:    message,
		NoMetadata: noMetadata,
		DryRun:     dryRun,
	})
	if err != nil {
		return err
	}

	if res.Skipped {
		fmt.Printf("No net changes for session %s; nothing projected.", sessionID)
		if res.FilesSkipped > 0 {
			fmt.Printf(" (%d submodule path(s) skipped)", res.FilesSkipped)
		}
		fmt.Println()
		return nil
	}
	if dryRun {
		fmt.Printf("[dry-run] would project session %s onto %s\n  +%d ~%d -%d, base %s\n",
			sessionID, res.TargetRef, res.FilesAdded, res.FilesModified, res.FilesDeleted, shortSHA(res.Base))
		return nil
	}

	fmt.Printf("Projected session %s onto %s\n  commit %s (tree %s, base %s)\n  +%d ~%d -%d (skipped %d submodule)\n",
		sessionID, res.TargetRef, shortSHA(res.Hash), shortSHA(res.Tree), shortSHA(res.Base),
		res.FilesAdded, res.FilesModified, res.FilesDeleted, res.FilesSkipped)

	if pushRemote != "" {
		refspec := res.TargetRef + ":" + res.TargetRef
		if out, err := gitbridge.PushRef(projectRoot, pushRemote, refspec); err != nil {
			return fmt.Errorf("push %s to %s: %w", res.TargetRef, pushRemote, err)
		} else if out != "" {
			fmt.Println(out)
		}
		fmt.Printf("Pushed %s to %s\n", res.TargetRef, pushRemote)
	}
	return nil
}

func shortSHA(h string) string {
	if h == "" {
		return "(none)"
	}
	if len(h) >= 12 {
		return h[:12]
	}
	return h
}
