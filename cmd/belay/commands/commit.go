package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"

	"github.com/spf13/cobra"
)

func newCommitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Generate a git commit from a session's changes",
		Long: `Create a clean git commit from the net changes of a specific AI session.
This bridges Belay's event store to git's commit model.`,
		RunE: runCommit,
	}

	cmd.Flags().StringP("session", "s", "", "Session ID to commit (required)")
	cmd.Flags().StringP("message", "m", "", "Commit message")
	cmd.Flags().Bool("dry-run", false, "Show what would be committed")
	cmd.Flags().Bool("execute", false, "Actually create the git commit (requires safety.allow_writes in config)")
	cmd.Flags().Bool("no-metadata", false, "Skip Belay trailers in commit message")
	cmd.MarkFlagRequired("session")

	return cmd
}

func runCommit(cmd *cobra.Command, args []string) error {
	sessionID, _ := cmd.Flags().GetString("session")
	message, _ := cmd.Flags().GetString("message")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	execute, _ := cmd.Flags().GetBool("execute")
	noMetadata, _ := cmd.Flags().GetBool("no-metadata")

	projectRoot, err := config.FindProjectRoot()
	if err != nil {
		return fmt.Errorf("not a belay project: %w", err)
	}

	cfg, err := config.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if _, err := exec.Command("git", "-C", projectRoot, "status").Output(); err != nil {
		return fmt.Errorf("not a git repository: %w", err)
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

	session, err := idx.GetSession(sessionID)
	if err != nil {
		fmt.Printf("Warning: session not found in index, proceeding with events\n")
	}

	events, err := idx.QueryEvents(&index.Query{
		Sessions:  []string{sessionID},
		OrderDesc: false,
	})
	if err != nil || len(events) == 0 {
		return fmt.Errorf("no events found for session %s", sessionID)
	}

	type netChange struct {
		path    string
		op      string
		oldHash string
		newHash string
		events  int
	}

	changes := make(map[string]*netChange)
	for _, e := range events {
		nc, exists := changes[e.FilePath]
		if !exists {
			nc = &netChange{
				path:    e.FilePath,
				oldHash: e.PreviousHash,
			}
			changes[e.FilePath] = nc
		}
		nc.newHash = e.ContentHash
		nc.events++

		switch {
		case e.Op == schema.OpDelete:
			nc.op = "delete"
		case nc.oldHash == "" && e.Op == schema.OpCreate:
			nc.op = "create"
		default:
			if nc.op != "create" {
				nc.op = "modify"
			}
		}
	}

	for path, nc := range changes {
		if nc.oldHash == nc.newHash && nc.op != "delete" {
			delete(changes, path)
		}
	}

	if len(changes) == 0 {
		fmt.Println("No net changes from this session.")
		return nil
	}

	var added, modified, deleted int
	for _, nc := range changes {
		switch nc.op {
		case "create":
			added++
		case "modify":
			modified++
		case "delete":
			deleted++
		}
	}

	fmt.Printf("Session %s: %d files (%d added, %d modified, %d deleted)\n\n",
		sessionID, len(changes), added, modified, deleted)

	for _, nc := range changes {
		switch nc.op {
		case "create":
			fmt.Printf("  \033[32m+ %s\033[0m\n", nc.path)
		case "modify":
			fmt.Printf("  \033[33m~ %s\033[0m\n", nc.path)
		case "delete":
			fmt.Printf("  \033[31m- %s\033[0m\n", nc.path)
		}
	}

	if dryRun {
		fmt.Println("\n[dry-run] Would create git commit with the above changes.")
		return nil
	}

	if err := checkSafetyGate(cfg, execute, "commit", "-s <session-id>"); err != nil {
		fmt.Printf("\n%s\n", err)
		return nil
	}

	for _, nc := range changes {
		absPath := filepath.Join(projectRoot, nc.path)

		switch nc.op {
		case "create", "modify":
			if nc.newHash == "" {
				continue
			}
			data, err := objStore.Get(nc.newHash)
			if err != nil {
				return fmt.Errorf("retrieve content for %s: %w", nc.path, err)
			}
			if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
				return fmt.Errorf("create dir for %s: %w", nc.path, err)
			}
			if err := os.WriteFile(absPath, data, 0644); err != nil {
				return fmt.Errorf("write %s: %w", nc.path, err)
			}
			if _, err := gitExec(projectRoot, "add", nc.path); err != nil {
				return fmt.Errorf("git add %s: %w", nc.path, err)
			}

		case "delete":
			if _, err := gitExec(projectRoot, "rm", "-f", nc.path); err != nil {
				os.Remove(absPath)
			}
		}
	}

	if message == "" {
		if session != nil && session.Label != "" {
			message = session.Label
		} else {
			message = fmt.Sprintf("belay: session %s (%d files)", sessionID[:12], len(changes))
		}
	}

	if !noMetadata {
		message += fmt.Sprintf("\n\nBelay-Session: %s", sessionID)
		if session != nil {
			message += fmt.Sprintf("\nBelay-Tool: %s", session.ToolName)
			message += fmt.Sprintf("\nBelay-Events: %d", len(events))
			message += fmt.Sprintf("\nBelay-Files: %d", len(changes))
		}
	}

	out, err := gitExec(projectRoot, "commit", "-m", message)
	if err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	fmt.Printf("\n%s\n", out)
	return nil
}

func gitExec(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}
