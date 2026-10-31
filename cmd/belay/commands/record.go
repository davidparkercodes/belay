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
	"github.com/davidparkercodes/belay/internal/session"
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
(set BELAY_NO_AUTOSTART=1 to disable), and the daemon indexes the queue.
Queued content is sealed to the daemon's public key, so recording never
needs the store key once the daemon has run in this project.`,
		Args: cobra.ExactArgs(1),
		RunE: runRecord,
	}

	cmd.Flags().StringP("operation", "o", "modify", "Operation type: create, modify, delete")
	cmd.Flags().StringP("session", "s", "", "Session ID to attribute to")
	cmd.Flags().String("tool", "", "Tool name (e.g. claude-code)")
	cmd.Flags().Int("pid", 0, "Agent process id to link the session to (default: nearest agent ancestor of this process)")

	return cmd
}

type recordRequest struct {
	FilePath  string `json:"file_path"`
	Operation string `json:"operation"`
	SessionID string `json:"session_id,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	PID       int    `json:"pid,omitempty"`
}

func runRecord(cmd *cobra.Command, args []string) error {
	operation, _ := cmd.Flags().GetString("operation")
	sessionID, _ := cmd.Flags().GetString("session")
	toolName, _ := cmd.Flags().GetString("tool")
	pid, _ := cmd.Flags().GetInt("pid")
	if pid <= 0 && sessionID != "" {
		pid = session.CallerAgentPID()
	}

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
		PID:       pid,
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

	entry := &spool.Entry{Event: event}
	if op != schema.OpDelete {
		data, err := os.ReadFile(absPath)
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
		if err := queueContent(cfg, entry, data); err != nil {
			return err
		}
	}

	if err := spool.WriteEntry(cfg.SpoolDir(), entry); err != nil {
		return fmt.Errorf("queue event: %w", err)
	}

	return nil
}

// Only a store whose daemon has never published a recipient needs the store key here.
// When the store cannot be opened the event is still queued and the daemon reads the
// content itself, so a key problem never fails the hook.
func queueContent(cfg *config.Config, entry *spool.Entry, data []byte) error {
	m, haveManifest, _ := store.ReadManifest(cfg.StoreManifestPath())
	encrypted := cfg.Store.Encryption || (haveManifest && m.Format == store.FormatEncrypted)
	if !encrypted {
		objStore, err := store.OpenConfigWithKeys(cfg, nil)
		if err != nil {
			return fmt.Errorf("open object store: %w", err)
		}
		defer objStore.Close()
		return putContent(objStore, entry, data)
	}

	if r, ok, err := spool.LoadRecipient(cfg.SpoolDir()); err == nil && ok && haveManifest && recipientMatches(m, r) {
		sealed, err := store.Seal(r.PublicKey, data)
		if err != nil {
			return fmt.Errorf("seal content: %w", err)
		}
		entry.Sealed = sealed
		entry.Event.ContentSize = int64(len(data))
		return nil
	}

	objStore, err := openStore(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "belay: store unavailable (%v); event queued, the daemon will store the content\n", err)
		spool.DeferContent(entry.Event)
		return nil
	}
	defer objStore.Close()
	return putContent(objStore, entry, data)
}

// recipientMatches only trusts a published recipient whose key id and public key
// fingerprint both match the store manifest.
func recipientMatches(m store.Manifest, r spool.Recipient) bool {
	return m.KeyID != "" && m.KeyID == r.KeyID && m.SealKey != "" && m.SealKey == store.SealFingerprint(r.PublicKey)
}

func putContent(objStore *store.Store, entry *spool.Entry, data []byte) error {
	hash, size, err := objStore.Put(data)
	if err != nil {
		return fmt.Errorf("store content: %w", err)
	}
	entry.Event.ContentHash = hash
	entry.Event.ContentSize = size
	return nil
}
