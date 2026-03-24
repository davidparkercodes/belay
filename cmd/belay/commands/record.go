package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/eventlog"
	"github.com/davidparkercodes/belay/internal/schema"
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

If the daemon API is reachable, the event is sent via POST /api/record.
If the daemon is not running, the event is written directly to the
event log as a fallback.`,
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
	filePath := args[0]
	operation, _ := cmd.Flags().GetString("operation")
	sessionID, _ := cmd.Flags().GetString("session")
	toolName, _ := cmd.Flags().GetString("tool")

	projectRoot, err := config.FindProjectRoot()
	if err != nil {
		return fmt.Errorf("not a belay project: %w", err)
	}

	cfg, err := config.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if filepath.IsAbs(filePath) {
		rel, err := filepath.Rel(projectRoot, filePath)
		if err != nil {
			return fmt.Errorf("cannot make path relative: %w", err)
		}
		filePath = rel
	}

	port := cfg.API.Port
	if port == 0 {
		port = 33412
	}

	req := recordRequest{
		FilePath:  filePath,
		Operation: operation,
		SessionID: sessionID,
		ToolName:  toolName,
	}

	if err := recordViaAPI(port, &req); err == nil {
		return nil
	}

	fmt.Fprintf(os.Stderr, "belay: daemon not reachable, writing directly to event log (event will not be indexed until daemon restarts)\n")
	return recordDirect(cfg, filePath, operation, sessionID, toolName)
}

func recordViaAPI(port int, req *recordRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/api/record", port)
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, errResp["error"])
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

	logWriter, err := eventlog.NewWriter(cfg.EventsDir(), cfg.Storage.SegmentMaxBytes)
	if err != nil {
		return fmt.Errorf("open event log: %w", err)
	}
	defer logWriter.Close()

	if err := logWriter.Append(event); err != nil {
		return fmt.Errorf("write event: %w", err)
	}

	return nil
}
