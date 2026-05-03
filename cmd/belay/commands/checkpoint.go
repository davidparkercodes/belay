package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/config"

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

	port := cfg.API.Port
	if port == 0 {
		port = 33412
	}

	req := checkpointRequest{
		Label:     strings.TrimSpace(label),
		Reason:    strings.TrimSpace(reason),
		SessionID: sessionID,
		ToolName:  toolName,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/api/checkpoint", port)
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("daemon not reachable on port %d (start it with `belay daemon`): %w", port, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, errResp["error"])
	}

	var out checkpointResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("decode response: %w", err)
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
