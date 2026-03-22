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

Use --session or --event for precise, deterministic restores (recommended for AI agents).
Use --roughly-around for approximate time-based restores (human convenience).
Use --all to restore all tracked files instead of a single file.

Always creates a backup of the current state before restoring.`,
		RunE: runRestore,
	}

	cmd.Flags().String("session", "", "Restore to state before this session touched the file(s)")
	cmd.Flags().String("event", "", "Restore to state after this event (single file only)")
	cmd.Flags().String("roughly-around", "", "Restore to state at roughly this time (human convenience, AI agents should use --session or --event)")
	cmd.Flags().Bool("all", false, "Restore all tracked files instead of a single file")
	cmd.Flags().Bool("dry-run", false, "Show what would change without doing it")
	cmd.Flags().Bool("execute", false, "Actually perform the restore (requires safety.allow_writes in config)")

	return cmd
}

func runRestore(cmd *cobra.Command, args []string) error {
	atTime, _ := cmd.Flags().GetString("roughly-around")
	eventID, _ := cmd.Flags().GetString("event")
	sessionID, _ := cmd.Flags().GetString("session")
	restoreAll, _ := cmd.Flags().GetBool("all")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	execute, _ := cmd.Flags().GetBool("execute")

	if restoreAll && eventID != "" {
		return fmt.Errorf("--all cannot be used with --event (event targets a single file)")
	}

	if !restoreAll && len(args) == 0 {
		return fmt.Errorf("specify a file path or use --all to restore all tracked files")
	}

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

	if restoreAll {
		return restoreAllFiles(cmd, idx, objStore, cfg, projectRoot, sessionID, atTime, dryRun, execute)
	}

	return restoreSingleFile(cmd, idx, objStore, cfg, projectRoot, args[0], sessionID, eventID, atTime, dryRun, execute)
}

func restoreSingleFile(_ *cobra.Command, idx *index.Index, objStore *store.Store, cfg *config.Config, projectRoot, filePath, sessionID, eventID, atTime string, dryRun, execute bool) error {
	var targetEvent *schema.Event
	var err error

	if eventID != "" {
		targetEvent, err = idx.GetEvent(eventID)
		if err != nil {
			return fmt.Errorf("event not found: %s", eventID)
		}
	} else if sessionID != "" {
		targetEvent, err = findPreSessionEvent(idx, filePath, sessionID)
		if err != nil {
			return err
		}
	} else if atTime != "" {
		t, err := parseRelativeTime(atTime)
		if err != nil {
			return fmt.Errorf("invalid --roughly-around: %w", err)
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
	} else {
		targetEvent, err = idx.LatestEvent(filePath)
		if err != nil {
			return fmt.Errorf("no history for %s", filePath)
		}
	}

	return writeRestore(objStore, cfg, projectRoot, filePath, targetEvent, dryRun, execute)
}

func restoreAllFiles(_ *cobra.Command, idx *index.Index, objStore *store.Store, cfg *config.Config, projectRoot, sessionID, atTime string, dryRun, execute bool) error {
	if sessionID == "" && atTime == "" {
		return fmt.Errorf("--all requires --session or --roughly-around to determine which state to restore to")
	}

	type fileTarget struct {
		path  string
		event *schema.Event
	}

	var targets []fileTarget

	if sessionID != "" {
		events, err := idx.QueryEvents(&index.Query{
			Sessions:  []string{sessionID},
			OrderDesc: false,
		})
		if err != nil {
			return fmt.Errorf("query session events: %w", err)
		}
		if len(events) == 0 {
			return fmt.Errorf("no events found for session %s", sessionID)
		}

		touchedFiles := make(map[string]bool)
		for _, e := range events {
			touchedFiles[e.FilePath] = true
		}

		for filePath := range touchedFiles {
			evt, err := findPreSessionEvent(idx, filePath, sessionID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", filePath, err)
				continue
			}
			targets = append(targets, fileTarget{path: filePath, event: evt})
		}
	} else {
		t, err := parseRelativeTime(atTime)
		if err != nil {
			return fmt.Errorf("invalid --roughly-around: %w", err)
		}
		targetNano := t.UnixNano()

		allEvents, err := idx.QueryEvents(&index.Query{
			Until:     targetNano,
			OrderDesc: false,
		})
		if err != nil {
			return fmt.Errorf("query events: %w", err)
		}

		latestByFile := make(map[string]*schema.Event)
		for _, e := range allEvents {
			latestByFile[e.FilePath] = e
		}

		for filePath, evt := range latestByFile {
			if evt.Op == schema.OpDelete {
				continue
			}
			targets = append(targets, fileTarget{path: filePath, event: evt})
		}
	}

	if len(targets) == 0 {
		fmt.Println("No files to restore.")
		return nil
	}

	fmt.Printf("Restoring %d files...\n\n", len(targets))

	restored := 0
	skipped := 0
	for _, t := range targets {
		err := writeRestore(objStore, cfg, projectRoot, t.path, t.event, dryRun, execute)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", t.path, err)
			skipped++
		} else {
			restored++
		}
	}

	fmt.Printf("\n%d files restored, %d skipped.\n", restored, skipped)
	return nil
}

func findPreSessionEvent(idx *index.Index, filePath, sessionID string) (*schema.Event, error) {
	events, err := idx.QueryEvents(&index.Query{
		FilePaths: []string{filePath},
		Sessions:  []string{sessionID},
		OrderDesc: false,
		Limit:     1,
	})
	if err != nil || len(events) == 0 {
		return nil, fmt.Errorf("session %s never touched %s", sessionID, filePath)
	}
	beforeEvents, err := idx.QueryEvents(&index.Query{
		FilePaths: []string{filePath},
		Until:     events[0].TimestampNano - 1,
		OrderDesc: true,
		Limit:     1,
	})
	if err != nil || len(beforeEvents) == 0 {
		return nil, fmt.Errorf("no history found for %s before session %s", filePath, sessionID)
	}
	return beforeEvents[0], nil
}

func writeRestore(objStore *store.Store, cfg *config.Config, projectRoot, filePath string, targetEvent *schema.Event, dryRun, execute bool) error {
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
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
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
			fmt.Println("  Already at target state. Skipped.")
			return nil
		}
		fmt.Printf("  Current: %s (%s)\n", humanBytes(int64(len(currentData))), truncateStr(currentHash, 12))
	} else {
		fmt.Println("  Current: (file does not exist)")
	}

	if dryRun {
		fmt.Println("  [dry-run] Would restore.")
		fmt.Println()
		return nil
	}

	if err := checkSafetyGate(cfg, execute, "restore", "--session <id> <file>"); err != nil {
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

	fmt.Printf("  Restored successfully.\n\n")
	return nil
}
