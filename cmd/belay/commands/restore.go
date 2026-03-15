package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"

	"github.com/spf13/cobra"
)

func newRestoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore [file]",
		Short: "Restore files from Belay history",
		Long: `Recover lost or overwritten files from Belay's event history.

Always creates a backup of the current state before restoring.`,
		RunE: runRestore,
		Args: cobra.MinimumNArgs(1),
	}

	cmd.Flags().String("at", "", "Restore to state at this time")
	cmd.Flags().String("event", "", "Restore to state after this event")
	cmd.Flags().String("session", "", "Restore to state before this session touched the file")
	cmd.Flags().Bool("dry-run", false, "Show what would change without doing it")
	cmd.Flags().Bool("execute", false, "Actually perform the restore (requires safety.allow_writes in config)")

	return cmd
}

func runRestore(cmd *cobra.Command, args []string) error {
	atTime, _ := cmd.Flags().GetString("at")
	eventID, _ := cmd.Flags().GetString("event")
	sessionID, _ := cmd.Flags().GetString("session")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	execute, _ := cmd.Flags().GetBool("execute")

	filePath := args[0]

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

	var targetEvent *schema.Event

	if eventID != "" {
		targetEvent, err = idx.GetEvent(eventID)
		if err != nil {
			return fmt.Errorf("event not found: %s", eventID)
		}
	} else if atTime != "" {
		t, err := parseRelativeTime(atTime)
		if err != nil {
			return fmt.Errorf("invalid --at: %w", err)
		}
		events, err := idx.QueryEvents(&index.Query{
			FilePaths: []string{filePath},
			Until:     t.UnixNano(),
			OrderDesc: true,
			Limit:     1,
		})
		if err != nil || len(events) == 0 {
			return fmt.Errorf("no history found for %s before %s", filePath, t.Format("2006-01-02 15:04:05"))
		}
		targetEvent = events[0]
	} else if sessionID != "" {
		events, err := idx.QueryEvents(&index.Query{
			FilePaths: []string{filePath},
			Sessions:  []string{sessionID},
			OrderDesc: false,
			Limit:     1,
		})
		if err != nil || len(events) == 0 {
			return fmt.Errorf("session %s never touched %s", sessionID, filePath)
		}
		beforeEvents, err := idx.QueryEvents(&index.Query{
			FilePaths: []string{filePath},
			Until:     events[0].TimestampNano - 1,
			OrderDesc: true,
			Limit:     1,
		})
		if err != nil || len(beforeEvents) == 0 {
			return fmt.Errorf("no history found for %s before session %s", filePath, sessionID)
		}
		targetEvent = beforeEvents[0]
	} else {
		targetEvent, err = idx.LatestEvent(filePath)
		if err != nil {
			return fmt.Errorf("no history for %s", filePath)
		}
	}

	if targetEvent.Op == schema.OpDelete {
		return fmt.Errorf("target state is a deletion -- file did not exist at that point")
	}

	if targetEvent.ContentHash == "" {
		return fmt.Errorf("no content available for this event")
	}

	content, err := objStore.Get(targetEvent.ContentHash)
	if err != nil {
		return fmt.Errorf("retrieve content: %w", err)
	}

	absPath := filepath.Join(projectRoot, filepath.Clean(filePath))
	// Resolve symlinks before checking prefix to prevent symlink escapes
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// file may not exist yet, fall back to cleaned path
		realPath = absPath
	}
	realRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		realRoot = projectRoot
	}
	if !strings.HasPrefix(realPath, realRoot+string(filepath.Separator)) && realPath != realRoot {
		return fmt.Errorf("file path escapes project root: %s", filePath)
	}

	fmt.Printf("Restore %s to state from %s\n", filePath, targetEvent.Timestamp().Format("2006-01-02 15:04:05"))
	fmt.Printf("  Event:   %s\n", targetEvent.EventID)
	fmt.Printf("  Op:      %s\n", targetEvent.Op.String())
	fmt.Printf("  Size:    %s\n", humanBytes(int64(len(content))))

	if targetEvent.SessionID != "" {
		fmt.Printf("  Session: %s\n", targetEvent.SessionID)
	}

	currentData, readErr := os.ReadFile(absPath)
	if readErr == nil {
		currentHash := schema.ContentHashForBytes(currentData)
		if currentHash == targetEvent.ContentHash {
			fmt.Println("\nFile is already at the target state. Nothing to restore.")
			return nil
		}
		fmt.Printf("  Current: %s (%s)\n", humanBytes(int64(len(currentData))), truncateStr(currentHash, 12))
	} else {
		fmt.Println("  Current: (file does not exist)")
	}

	if dryRun {
		fmt.Println("\n[dry-run] Would restore file to the above state.")
		return nil
	}

	if err := checkSafetyGate(cfg, execute, "restore", "--at <time> <file>"); err != nil {
		fmt.Printf("\n%s\n", err)
		return nil
	}

	if readErr == nil {
		backupHash, _, storeErr := objStore.Put(currentData)
		if storeErr == nil {
			fmt.Printf("  Backup:  stored as %s\n", truncateStr(backupHash, 12))
		}
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(absPath, content, 0644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	fmt.Printf("\nRestored %s successfully.\n", filePath)
	fmt.Printf("  Restored from: %s\n", targetEvent.Timestamp().Format("2006-01-02 15:04:05"))
	fmt.Printf("  Content hash:  %s\n", truncateStr(targetEvent.ContentHash, 12))

	return nil
}
