package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/store"

	"github.com/spf13/cobra"
)

// gcOutput combines compaction and garbage collection results for JSON output.
type gcOutput struct {
	Compaction *store.CompactionResult `json:"compaction,omitempty"`
	GC         *store.GCResult         `json:"gc,omitempty"`
}

func newGCCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Run garbage collection and compaction",
		Long: `Apply tiered retention compaction and remove orphaned objects to reclaim storage.

Compaction tiers:
  hot     Full fidelity (every event kept)
  warm    Rapid edits collapsed (consecutive modifies within 60s merged)
  cold    Session boundaries only (first + last event per file per session)
  archive Daily snapshots (one event per file per day)
  purge   Events older than archive_days are deleted entirely

After compaction, orphaned objects are garbage collected.

Use --dry-run to see what would be cleaned up without deleting anything.
Use --gc-only to skip compaction and only garbage collect orphaned objects.`,
		RunE: runGC,
	}

	cmd.Flags().Bool("dry-run", false, "Show what would be collected without doing it")
	cmd.Flags().Bool("json", false, "Output as JSON")
	cmd.Flags().Bool("gc-only", false, "Skip compaction, only garbage collect orphaned objects")

	return cmd
}

func runGC(cmd *cobra.Command, args []string) error {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	gcOnly, _ := cmd.Flags().GetBool("gc-only")

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

	output := &gcOutput{}

	if !gcOnly {
		// Run compaction first
		if dryRun {
			fmt.Println("Compaction + Garbage Collection (dry-run)")
			fmt.Println("==========================================")
		} else {
			fmt.Println("Running Compaction + Garbage Collection...")
		}

		compactor := store.NewCompactor(idx, objStore, &cfg.Retention, dryRun)
		compResult, compErr := compactor.RunCompaction()
		if compErr != nil {
			return fmt.Errorf("compaction: %w", compErr)
		}
		output.Compaction = compResult

		if !jsonOutput {
			fmt.Println("\n  Compaction Results")
			fmt.Println("  ------------------")
			fmt.Printf("  Events reviewed:  %d\n", compResult.EventsReviewed)
			fmt.Printf("  Events kept:      %d\n", compResult.EventsKept)
			fmt.Printf("  Events removed:   %d\n", compResult.EventsRemoved)
			if compResult.BytesFreed > 0 {
				fmt.Printf("  Storage freed:    %s\n", formatBytes(compResult.BytesFreed))
			}
			if len(compResult.TierBreakdown) > 0 {
				fmt.Println("\n  Tier Breakdown:")
				for tier, count := range compResult.TierBreakdown {
					if count > 0 {
						fmt.Printf("    %-20s %d events\n", tier+":", count)
					}
				}
			}
		}
	} else {
		// GC only mode
		if dryRun {
			fmt.Println("Garbage Collection (dry-run)")
			fmt.Println("============================")
		} else {
			fmt.Println("Running Garbage Collection...")
		}

		result, gcErr := store.GarbageCollect(idx, objStore, dryRun)
		if gcErr != nil {
			return fmt.Errorf("garbage collection: %w", gcErr)
		}
		output.GC = result

		if !jsonOutput {
			fmt.Printf("\n  Objects scanned:  %d\n", result.ObjectsScanned)
			fmt.Printf("  Orphaned objects: %d\n", result.OrphanedObjects)

			if dryRun {
				if result.OrphanedObjects > 0 {
					fmt.Printf("\n  Would remove %d orphaned objects (%s).\n", result.OrphanedObjects, formatBytes(result.BytesFreed))
					fmt.Println("  Run without --dry-run to clean up.")
				} else {
					fmt.Println("\n  No orphaned objects found. Store is clean.")
				}
			} else {
				if result.OrphanedObjects > 0 {
					fmt.Printf("\n  Removed %d orphaned objects (%s freed).\n", result.OrphanedObjects, formatBytes(result.BytesFreed))
				} else {
					fmt.Println("\n  No orphaned objects found.")
				}
			}
		}
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	}

	if dryRun {
		fmt.Println("\n  Run without --dry-run to apply changes.")
	} else {
		fmt.Println("\n  Done.")
	}

	return nil
}

// formatBytes returns a human-readable byte size string.
func formatBytes(b int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d bytes", b)
	}
}
