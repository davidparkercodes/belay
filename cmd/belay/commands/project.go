package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/apiclient"
	"github.com/davidparkercodes/belay/internal/config"
	gitbridge "github.com/davidparkercodes/belay/internal/git"
	"github.com/davidparkercodes/belay/internal/index"

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
linear projection branch, no collisions on HEAD.

With --push, the local ref is first synced with the remote copy (fast-forwarded, or
local-only projections replayed on top when another machine projected onto the same
ref), then pushed; a rejected push re-syncs and retries. With --queue, the job is
handed to the running daemon, which runs projections one at a time in the
background, so a hook returns immediately. Use --status to see the daemon's queue.

Defaults for --to-ref, --base-ref, --push and --working-tree come from the
[projection] section of .belay/config.toml.

Files whose net content looks like it holds a credential are held back: a new
file is left out, a modified file stays at its previously projected version, and
deletions still project. The commit still happens. Allow paths or switch rules
off in .belayallow (gitignore syntax, plus rule:<name> lines), pass
--allow-secrets for one run, or set [projection] hold_back_secrets = false.`,
		RunE: runProject,
	}
	cmd.Flags().StringP("session", "s", "", "Session ID to project")
	cmd.Flags().StringP("message", "m", "", "Commit message (default: generated summary)")
	cmd.Flags().String("to-ref", "", "Target ref to append the projection commit to")
	cmd.Flags().String("base-ref", "", "Ref to bootstrap the full base tree from when --to-ref does not yet exist")
	cmd.Flags().Bool("no-metadata", false, "Skip Belay trailers in commit message")
	cmd.Flags().Bool("dry-run", false, "Show what would be projected without writing")
	cmd.Flags().String("push", "", "Sync with and push the ref to this remote (e.g. origin)")
	cmd.Flags().Bool("working-tree", false, "Also project a snapshot of the current working tree (reconcile; attribution-independent)")
	cmd.Flags().Bool("no-working-tree", false, "Skip the working-tree reconcile even if the config enables it")
	cmd.Flags().Bool("queue", false, "Hand the job to the running daemon's projector instead of running it here")
	cmd.Flags().Bool("status", false, "Show the daemon projector's queue and recent results")
	cmd.Flags().Bool("allow-secrets", false, "Project files with likely credentials instead of holding them back (this run only)")
	cmd.Flags().Bool("json", false, "Print the result as JSON")
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
	workingTree, _ := cmd.Flags().GetBool("working-tree")
	noWorkingTree, _ := cmd.Flags().GetBool("no-working-tree")
	queue, _ := cmd.Flags().GetBool("queue")
	showStatus, _ := cmd.Flags().GetBool("status")
	jsonOut, _ := cmd.Flags().GetBool("json")
	allowSecrets, _ := cmd.Flags().GetBool("allow-secrets")

	projectRoot, err := config.FindProjectRoot()
	if err != nil {
		return fmt.Errorf("not a belay project: %w", err)
	}
	cfg, err := config.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if showStatus {
		return printProjectorStatus(cfg, jsonOut)
	}

	if toRef == "" {
		toRef = cfg.Projection.Ref
	}
	if baseRef == "" {
		baseRef = cfg.Projection.BaseRef
	}
	if !cmd.Flags().Changed("push") {
		pushRemote = cfg.Projection.Remote
	}
	if !cmd.Flags().Changed("working-tree") && sessionID != "" {
		workingTree = cfg.Projection.WorkingTree
	}
	if noWorkingTree {
		workingTree = false
	}
	if sessionID == "" && !workingTree {
		return fmt.Errorf("give --session <id> and/or --working-tree")
	}

	job := gitbridge.ProjectionJob{
		SessionID:    sessionID,
		WorkingTree:  workingTree,
		Remote:       pushRemote,
		Ref:          toRef,
		BaseRef:      baseRef,
		Message:      message,
		NoMetadata:   noMetadata,
		AllowSecrets: allowSecrets || !cfg.Projection.HoldBackSecrets,
	}

	if dryRun {
		return runProjectDryRun(cfg, job)
	}

	if queue {
		if err := enqueueProjection(cfg, job, jsonOut); err == nil {
			return nil
		} else if !isQueueUnavailable(err) {
			return err
		} else {
			fmt.Fprintf(os.Stderr, "belay: daemon projector unavailable (%v); running the projection here\n", err)
		}
	}

	idx, err := index.Open(cfg.IndexPath())
	if err != nil {
		return fmt.Errorf("open index: %w", err)
	}
	defer idx.Close()
	objStore, err := openStore(cfg)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer objStore.Close()

	report, runErr := gitbridge.RunProjection(idx, objStore, projectRoot, job)
	if report != nil {
		printGuardNotices(report)
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if report != nil {
			_ = enc.Encode(report)
		}
		return runErr
	}
	if report != nil {
		printProjectionReport(report)
	}
	return runErr
}

func printGuardNotices(r *gitbridge.ProjectionReport) {
	for _, w := range r.Warnings {
		fmt.Fprintf(os.Stderr, "belay: %s\n", w)
	}
	if notice := gitbridge.HeldBackNotice(r.HeldBack); notice != "" {
		fmt.Fprintln(os.Stderr, notice)
	}
}

func runProjectDryRun(cfg *config.Config, job gitbridge.ProjectionJob) error {
	var guard *gitbridge.SecretGuard
	if !job.AllowSecrets {
		guard = gitbridge.LoadSecretGuard(cfg.ProjectRoot)
	}
	if job.SessionID != "" {
		idx, err := index.Open(cfg.IndexPath())
		if err != nil {
			return fmt.Errorf("open index: %w", err)
		}
		defer idx.Close()
		objStore, err := openStore(cfg)
		if err != nil {
			return fmt.Errorf("open store: %w", err)
		}
		defer objStore.Close()
		res, err := gitbridge.ProjectSession(idx, objStore, cfg.ProjectRoot, gitbridge.ProjectOptions{
			SessionID: job.SessionID, TargetRef: job.Ref, BaseRef: job.BaseRef,
			Message: job.Message, NoMetadata: job.NoMetadata, DryRun: true, Guard: guard,
		})
		if err != nil {
			return err
		}
		printDryRun("session "+job.SessionID, res)
	}
	if job.WorkingTree {
		res, err := gitbridge.ProjectWorkingTree(cfg.ProjectRoot, gitbridge.ProjectWorkingTreeOptions{
			TargetRef: job.Ref, BaseRef: job.BaseRef, DryRun: true, Guard: guard,
		})
		if err != nil {
			return err
		}
		printDryRun("working tree", res)
	}
	if job.Remote != "" {
		fmt.Printf("[dry-run] would sync %s with %s and push\n", job.Ref, job.Remote)
	}
	return nil
}

func printDryRun(subject string, res *gitbridge.ProjectResult) {
	if res.Skipped {
		fmt.Printf("[dry-run] no net changes for %s; nothing to project.\n", subject)
	} else {
		fmt.Printf("[dry-run] would project %s onto %s\n  +%d ~%d -%d, base %s\n",
			subject, res.TargetRef, res.FilesAdded, res.FilesModified, res.FilesDeleted, shortSHA(res.Base))
	}
	for _, h := range res.HeldBack {
		fmt.Printf("[dry-run] would hold back %s (%s)\n", h.Path, strings.Join(h.Rules, ", "))
	}
	for _, w := range res.GuardWarnings {
		fmt.Fprintf(os.Stderr, "belay: %s\n", w)
	}
}

func printProjectionReport(r *gitbridge.ProjectionReport) {
	if r.Session != nil {
		if r.Session.Skipped {
			fmt.Printf("No net changes for session %s; nothing projected.", r.Job.SessionID)
			if r.Session.FilesSkipped > 0 {
				fmt.Printf(" (%d submodule path(s) skipped)", r.Session.FilesSkipped)
			}
			fmt.Println()
		} else {
			fmt.Printf("Projected session %s onto %s\n  commit %s (tree %s, base %s)\n  +%d ~%d -%d (skipped %d submodule)\n",
				r.Job.SessionID, r.Session.TargetRef, shortSHA(r.Session.Hash), shortSHA(r.Session.Tree), shortSHA(r.Session.Base),
				r.Session.FilesAdded, r.Session.FilesModified, r.Session.FilesDeleted, r.Session.FilesSkipped)
		}
	} else if r.SessionError != "" {
		fmt.Printf("Session %s not projected: %s\n", r.Job.SessionID, r.SessionError)
	}
	if r.WorkingTree != nil {
		if r.WorkingTree.Skipped {
			fmt.Printf("Working tree already matches %s; nothing to reconcile.\n", r.WorkingTree.TargetRef)
		} else {
			fmt.Printf("Reconciled working tree onto %s\n  commit %s (tree %s)\n  +%d ~%d -%d\n",
				r.WorkingTree.TargetRef, shortSHA(r.WorkingTree.Hash), shortSHA(r.WorkingTree.Tree),
				r.WorkingTree.FilesAdded, r.WorkingTree.FilesModified, r.WorkingTree.FilesDeleted)
		}
	}
	if r.Sync != nil {
		switch r.Sync.Action {
		case gitbridge.SyncRebased:
			fmt.Printf("Synced with %s: replayed %d local projection(s) onto %s\n", r.Sync.Remote, r.Sync.Rebased, shortSHA(r.Sync.RemoteTip))
		case gitbridge.SyncFastForwarded, gitbridge.SyncCreated:
			fmt.Printf("Synced with %s: local ref now at %s\n", r.Sync.Remote, shortSHA(r.Sync.LocalAfter))
		}
	}
	if r.SyncError != "" {
		fmt.Printf("Sync with %s failed: %s\n", r.Job.Remote, r.SyncError)
	}
	if r.Job.Remote != "" {
		switch {
		case r.Pushed:
			fmt.Printf("Pushed %s to %s\n", r.Job.Ref, r.Job.Remote)
		case r.PushError != "":
			fmt.Printf("Push to %s failed after %d attempt(s): %s\n", r.Job.Remote, r.PushAttempts, r.PushError)
		default:
			fmt.Printf("%s already has %s\n", r.Job.Remote, r.Job.Ref)
		}
	}
}

type projectQueueResponse struct {
	Status  string `json:"status"`
	Added   bool   `json:"added"`
	Pending int    `json:"pending"`
	Job     string `json:"job"`
}

func enqueueProjection(cfg *config.Config, job gitbridge.ProjectionJob, jsonOut bool) error {
	var out projectQueueResponse
	if err := apiclient.New(cfg, 3*time.Second).PostJSON("/api/project", &job, &out); err != nil {
		return err
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if out.Added {
		fmt.Printf("Queued %s (%d pending)\n", out.Job, out.Pending)
	} else {
		fmt.Printf("Already queued: %s (%d pending)\n", out.Job, out.Pending)
	}
	return nil
}

func isQueueUnavailable(err error) bool {
	if apiclient.IsUnreachable(err) {
		return true
	}
	var mismatch *apiclient.MismatchError
	if errors.As(err, &mismatch) {
		return true
	}
	var status *apiclient.StatusError
	if errors.As(err, &status) {
		return status.Status == 404 || status.Status == 503 || status.Status == 405
	}
	return false
}

func printProjectorStatus(cfg *config.Config, jsonOut bool) error {
	var out map[string]interface{}
	if err := apiclient.New(cfg, 3*time.Second).Get("/api/project/status", &out); err != nil {
		return fmt.Errorf("projector status: %w", err)
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	fmt.Printf("Projector\n  Pending: %v\n", out["pending"])
	if running, ok := out["running"]; ok && running != nil {
		fmt.Printf("  Running: %v\n", running)
	}
	fmt.Printf("  Runs:    %v total, %v failed\n", out["total_runs"], out["failed_runs"])
	if recent, ok := out["recent"].([]interface{}); ok && len(recent) > 0 {
		fmt.Println("  Recent:")
		for _, r := range recent {
			rec, _ := r.(map[string]interface{})
			if rec == nil {
				continue
			}
			line := fmt.Sprintf("%v", rec["summary"])
			if e, _ := rec["error"].(string); e != "" {
				line = "ERROR " + e + " | " + line
			}
			if held, _ := rec["held_back"].([]interface{}); len(held) > 0 {
				var paths []string
				for _, h := range held {
					if m, _ := h.(map[string]interface{}); m != nil {
						paths = append(paths, fmt.Sprintf("%v", m["path"]))
					}
				}
				line += " | held back: " + strings.Join(paths, ", ")
			}
			fmt.Printf("    %v  %s\n", rec["finished_at"], line)
		}
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
