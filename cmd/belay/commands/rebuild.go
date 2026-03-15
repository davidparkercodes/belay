package commands

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/daemon"
	"github.com/davidparkercodes/belay/internal/index"

	"github.com/spf13/cobra"
)

func newRebuildIndexCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rebuild-index",
		Short: "Rebuild the SQLite index from the event log",
		Long: `Rebuilds the SQLite event index by replaying all events from the
append-only event log segments. Use this when the index is corrupted
(e.g., "database disk image is malformed") or out of sync.

The event log is the source of truth. This command:
  1. Backs up the existing index.db (if present)
  2. Creates a fresh SQLite index
  3. Reads all events from .belay/events/ segment files
  4. Batch-inserts events into the new index
  5. Reconstructs session records from session meta-events

Uses a tolerant reader that skips corrupted frames, recovering as many
events as possible from partially damaged segment files.

The daemon must be stopped before running this command.`,
		RunE: runRebuildIndex,
	}

	cmd.Flags().Bool("no-backup", false, "Skip backing up the existing index.db")

	return cmd
}

func runRebuildIndex(cmd *cobra.Command, args []string) error {
	noBackup, _ := cmd.Flags().GetBool("no-backup")

	projectRoot, err := config.FindProjectRoot()
	if err != nil {
		return fmt.Errorf("not a belay project: %w", err)
	}

	cfg, err := config.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Check if daemon is running
	if running, pid := daemon.IsRunning(cfg); running {
		return fmt.Errorf("daemon is running (PID %d) -- stop it first with 'belay daemon stop'", pid)
	}

	indexPath := cfg.IndexPath()

	// If --no-backup, remove the existing index before calling Rebuild
	// (Rebuild always backs up if it finds an existing file)
	if noBackup {
		if _, err := os.Stat(indexPath); err == nil {
			fmt.Println("Removing corrupted index (backup skipped)...")
			os.Remove(indexPath)
			os.Remove(indexPath + "-wal")
			os.Remove(indexPath + "-shm")
		}
	}

	logger := log.New(os.Stdout, "", 0)

	fmt.Printf("Reading events from %s\n", cfg.EventsDir())

	result, err := index.Rebuild(indexPath, cfg.EventsDir(), logger)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Index Rebuild Complete")
	fmt.Println("======================")
	fmt.Printf("  Events indexed:     %d\n", result.EventsIndexed)
	fmt.Printf("  Verified in index:  %d\n", result.VerifiedCount)
	fmt.Printf("  Sessions rebuilt:   %d\n", result.SessionsRebuilt)
	if result.CorruptedSkipped > 0 {
		fmt.Printf("  Corrupted frames:   %d (skipped)\n", result.CorruptedSkipped)
	}
	fmt.Printf("  Time elapsed:       %s\n", result.Elapsed.Round(time.Millisecond))
	fmt.Println()
	fmt.Println("Restart the daemon to resume normal operation.")

	return nil
}
