package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/davidparkercodes/belay/internal/apiclient"
	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/spool"
	"github.com/davidparkercodes/belay/internal/store"

	"github.com/spf13/cobra"
)

func newRecordCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "record <file-path>",
		Short: "Record a file change event (used by hooks)",
		Long: `Push a file-write notification to the Belay daemon.

This command is designed to be called from CI/CD hooks or AI tool hooks
(e.g. Claude Code PostToolUse). It notifies Belay that a file was
written so the change can be captured with hook-based attribution.

The path may be absolute or relative to the current directory; the event
is recorded in the Belay project that contains the file.

If the daemon API is reachable, the event is sent via POST /api/record.
Otherwise the event is queued in .belay/spool/, the daemon is started
(set BELAY_NO_AUTOSTART=1 to disable), and the daemon indexes the queue.`,
		Args: cobra.ExactArgs(1),
		RunE: runRecord,
	}

	cmd.Flags().StringP("operation", "o", "modify", "Operation type: create, modify, delete")
	cmd.Flags().StringP("session", "s", "", "Session ID to attribute to")
	cmd.Flags().String("tool", "", "Tool name (e.g. claude-code)")

	return cmd
}

type recordRequest struct {
	FilePath  string `json:"file_path"`
	Operation string `json:"operation"`
	SessionID string `json:"session_id,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
}

func runRecord(cmd *cobra.Command, args []string) error {
	operation, _ := cmd.Flags().GetString("operation")
	sessionID, _ := cmd.Flags().GetString("session")
	toolName, _ := cmd.Flags().GetString("tool")

	absPath, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	projectRoot, err := config.FindProjectRootFrom(filepath.Dir(absPath))
	if err != nil {
		return fmt.Errorf("%s is not inside a belay project: %w", absPath, err)
	}

	cfg, err := config.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	filePath, err := filepath.Rel(projectRoot, absPath)
	if err != nil {
		return fmt.Errorf("cannot make path relative: %w", err)
	}

	req := recordRequest{
		FilePath:  filePath,
		Operation: operation,
		SessionID: sessionID,
		ToolName:  toolName,
	}

	client := apiclient.New(cfg, 2*time.Second)
	err = client.PostJSON("/api/record", &req, nil)
	if err == nil {
		return nil
	}

	var mismatch *apiclient.MismatchError
	if !errors.As(err, &mismatch) && !apiclient.IsUnreachable(err) {
		return err
	}

	if err := recordDirect(cfg, filePath, operation, sessionID, toolName); err != nil {
		return err
	}

	reason := "daemon not reachable"
	if mismatch != nil {
		reason = mismatch.Error()
	}
	if autoStartDaemon(cfg) {
		fmt.Fprintf(os.Stderr, "belay: %s; event queued and daemon started for %s\n", reason, projectRoot)
	} else {
		fmt.Fprintf(os.Stderr, "belay: %s; event queued in %s until the daemon runs\n", reason, cfg.SpoolDir())
	}
	return nil
}

func recordDirect(cfg *config.Config, filePath, operation, sessionID, toolName string) error {
	op, err := schema.ParseOperation(operation)
	if err != nil {
		return err
	}

	absPath := filepath.Join(cfg.ProjectRoot, filePath)

	event := &schema.Event{
		EventID:               schema.NewEventID(),
		Version:               schema.SchemaVersion,
		FilePath:              filePath,
		Op:                    op,
		Attribution:           schema.AttrHook,
		AttributionConfidence: 1.0,
		Metadata: map[string]string{
			"source": "hook",
		},
	}
	if sessionID != "" {
		event.SessionID = sessionID
	}
	if toolName != "" {
		event.Metadata["tool_name"] = toolName
	}
	event.SetTimestamp(time.Now())

	if op != schema.OpDelete {
		data, err := os.ReadFile(absPath)
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}

		objStore, err := store.NewStore(cfg.ObjectsDir(), cfg.Storage.CompressionEnabled)
		if err != nil {
			return fmt.Errorf("open object store: %w", err)
		}
		defer objStore.Close()

		hash, size, err := objStore.Put(data)
		if err != nil {
			return fmt.Errorf("store content: %w", err)
		}
		event.ContentHash = hash
		event.ContentSize = size
	}

	if err := spool.Write(cfg.SpoolDir(), event); err != nil {
		return fmt.Errorf("queue event: %w", err)
	}

	return nil
}
