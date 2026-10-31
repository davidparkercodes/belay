package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/apiclient"
	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/session"
	"github.com/davidparkercodes/belay/internal/spool"

	"github.com/spf13/cobra"
)

func newCheckpointCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checkpoint",
		Short: "Mark a labeled, restorable point in time",
		Long: `Write a CHECKPOINT marker into the Belay event log.

Checkpoints are restorable references with a human-readable label. Use them
before risky operations (rm, git reset, build scripts) so you can roll back
with: belay restore --to-checkpoint <label-or-id> --all --execute

Belay's continuous file watcher captures changes; the checkpoint pins the
moment-in-time you can name later.`,
		RunE: runCheckpoint,
	}

	cmd.Flags().StringP("label", "l", "", "Human-readable label for this checkpoint (default: timestamp)")
	cmd.Flags().StringP("reason", "r", "", "Why this checkpoint was created (free-form)")
	cmd.Flags().StringP("session", "s", "", "Session ID to attribute this checkpoint to")
	cmd.Flags().String("tool", "", "Tool name that triggered this checkpoint (e.g. claude-code)")
	cmd.Flags().Bool("quiet", false, "Print only the event ID")

	return cmd
}

type checkpointRequest struct {
	Label     string            `json:"label"`
	Reason    string            `json:"reason,omitempty"`
	SessionID string            `json:"session_id,omitempty"`
	ToolName  string            `json:"tool_name,omitempty"`
	PID       int               `json:"pid,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type checkpointResponse struct {
	Status        string `json:"status"`
	EventID       string `json:"event_id"`
	Label         string `json:"label"`
	TimestampNano int64  `json:"timestamp_nano"`
}

func runCheckpoint(cmd *cobra.Command, args []string) error {
	label, _ := cmd.Flags().GetString("label")
	reason, _ := cmd.Flags().GetString("reason")
	sessionID, _ := cmd.Flags().GetString("session")
	toolName, _ := cmd.Flags().GetString("tool")
	quiet, _ := cmd.Flags().GetBool("quiet")

	projectRoot, err := config.FindProjectRoot()
	if err != nil {
		return fmt.Errorf("not a belay project: %w", err)
	}

	cfg, err := config.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	req := checkpointRequest{
		Label:     strings.TrimSpace(label),
		Reason:    strings.TrimSpace(reason),
		SessionID: sessionID,
		ToolName:  toolName,
	}
	if sessionID != "" {
		req.PID = session.CallerAgentPID()
	}

	var out checkpointResponse
	err = apiclient.New(cfg, 2*time.Second).PostJSON("/api/checkpoint", &req, &out)
	if err != nil {
		var mismatch *apiclient.MismatchError
		if !errors.As(err, &mismatch) && !apiclient.IsUnreachable(err) {
			return err
		}
		out, err = spoolCheckpoint(cfg, &req)
		if err != nil {
			return err
		}
		reason := "daemon not reachable"
		if mismatch != nil {
			reason = mismatch.Error()
		}
		if autoStartDaemon(cfg) {
			fmt.Fprintf(os.Stderr, "belay: %s; checkpoint queued and daemon started for %s\n", reason, projectRoot)
		} else {
			fmt.Fprintf(os.Stderr, "belay: %s; checkpoint queued in %s until the daemon runs\n", reason, cfg.SpoolDir())
		}
	}

	if quiet {
		fmt.Println(out.EventID)
		return nil
	}

	t := time.Unix(0, out.TimestampNano)
	fmt.Fprintf(os.Stdout, "Checkpoint %s\n", out.EventID)
	fmt.Fprintf(os.Stdout, "  Label: %s\n", out.Label)
	fmt.Fprintf(os.Stdout, "  Time:  %s\n", t.Format("2006-01-02 15:04:05"))
	if reason != "" {
		fmt.Fprintf(os.Stdout, "  Reason: %s\n", reason)
	}
	fmt.Fprintf(os.Stdout, "\nRestore with: belay restore --to-checkpoint %s --all --execute\n", out.EventID)
	return nil
}

// spoolCheckpoint mirrors the API's checkpoint event so a hook never waits on a daemon start.
func spoolCheckpoint(cfg *config.Config, req *checkpointRequest) (checkpointResponse, error) {
	label := req.Label
	if label == "" {
		label = "checkpoint at " + time.Now().Format("2006-01-02 15:04:05")
	}
	if len(label) > 256 {
		label = label[:256]
	}
	metadata := map[string]string{
		"source": "checkpoint",
		"label":  label,
	}
	if req.Reason != "" {
		metadata["reason"] = req.Reason
	}
	if req.ToolName != "" {
		metadata["tool_name"] = req.ToolName
	}
	for k, v := range req.Metadata {
		if _, exists := metadata[k]; !exists {
			metadata[k] = v
		}
	}
	event := &schema.Event{
		EventID:               schema.NewEventID(),
		Version:               schema.SchemaVersion,
		Op:                    schema.OpCheckpoint,
		SessionID:             req.SessionID,
		Attribution:           schema.AttrManual,
		AttributionConfidence: 1.0,
		Metadata:              metadata,
	}
	event.SetTimestamp(time.Now())
	if err := spool.Write(cfg.SpoolDir(), event); err != nil {
		return checkpointResponse{}, fmt.Errorf("queue checkpoint: %w", err)
	}
	return checkpointResponse{Status: "queued", EventID: event.EventID, Label: label, TimestampNano: event.TimestampNano}, nil
}
