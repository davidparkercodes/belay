package commands

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/store"

	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Belay system status",
		Long: `Display the current state of Belay including daemon status,
active AI sessions, storage usage, and recent activity.`,
		RunE: runStatus,
	}

	cmd.Flags().Bool("json", false, "Output as JSON")
	cmd.Flags().Bool("sessions", false, "Show only session info")
	cmd.Flags().Bool("storage", false, "Show only storage info")
	cmd.Flags().StringSlice("scan", nil, "Check every Belay project under these directories (exits 1 if any is unhealthy)")

	return cmd
}

func runStatus(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")

	if dirs, _ := cmd.Flags().GetStringSlice("scan"); len(dirs) > 0 {
		return runStatusScan(dirs, jsonOutput)
	}

	projectRoot, err := config.FindProjectRoot()
	if err != nil {
		return fmt.Errorf("not a belay project: %w", err)
	}

	cfg, err := config.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	health := checkProject(cfg)
	daemonRunning, daemonPID := health.DaemonRunning, health.DaemonPID

	if jsonOutput {
		status := map[string]interface{}{
			"project_root":    projectRoot,
			"belay_path":      cfg.BelayPath,
			"daemon_running":  daemonRunning,
			"daemon_pid":      daemonPID,
			"api_ok":          health.APIOK,
			"api_port":        health.APIPort,
			"api_error":       health.APIError,
			"spooled_events":  health.Spooled,
			"spool_set_aside": health.SpoolBad,
		}
		if line := scrubNoticeLine(cfg); line != "" {
			status["agent_logs"] = line
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(status)
	}

	fmt.Println("Belay Status")
	fmt.Println("=============")
	fmt.Printf("  Project: %s\n", projectRoot)
	fmt.Printf("  Data:    %s\n", cfg.BelayPath)
	fmt.Println()

	if daemonRunning {
		fmt.Printf("  Daemon:  \033[32mrunning\033[0m (PID %d)\n", daemonPID)
		if health.APIOK {
			fmt.Printf("  API:     \033[32mok\033[0m http://127.0.0.1:%d\n", health.APIPort)
		} else {
			fmt.Printf("  API:     \033[31munavailable\033[0m on port %d: %s\n", health.APIPort, health.APIError)
			fmt.Println("           Hooks cannot reach this daemon. Run 'belay daemon restart'")
		}
	} else {
		fmt.Println("  Daemon:  \033[31mstopped\033[0m")
		fmt.Println("           Run 'belay daemon start' to begin watching files")
	}
	if health.Spooled > 0 {
		fmt.Printf("  Queued:  %d event(s) waiting in %s\n", health.Spooled, cfg.SpoolDir())
	}
	if health.SpoolBad > 0 {
		fmt.Printf("  Set aside: %d queued event(s) could not be read (*.json.bad in %s); the daemon retries sealed ones when it starts\n", health.SpoolBad, cfg.SpoolDir())
	}
	fmt.Println()

	eventsSize, _ := dirSize(cfg.EventsDir())
	objectsSize, _ := dirSize(cfg.ObjectsDir())
	indexSize := fileSize(cfg.IndexPath())

	fmt.Println("  Storage:")
	fmt.Printf("    Events:  %s\n", humanBytes(eventsSize))
	fmt.Printf("    Objects: %s\n", humanBytes(objectsSize))
	fmt.Printf("    Index:   %s\n", humanBytes(indexSize))
	fmt.Printf("    Total:   %s\n", humanBytes(eventsSize+objectsSize+indexSize))
	printStoreStatus(cfg)
	fmt.Println()

	printCompactionStatus(cfg)

	if line := scrubNoticeLine(cfg); line != "" {
		fmt.Println("  " + line)
		fmt.Println()
	}

	return nil
}

func printCompactionStatus(cfg *config.Config) {
	idx, err := index.Open(cfg.IndexPath())
	if err != nil {
		return
	}
	defer idx.Close()

	last, _ := idx.GetMeta(index.MetaLastCompactionAt)
	fmt.Println("  Compaction:")
	fmt.Printf("    Interval: every %s\n", cfg.Retention.CompactionInterval())
	if last == "" {
		fmt.Println("    Last run: never")
		fmt.Println()
		return
	}
	if t, err := time.Parse(time.RFC3339, last); err == nil {
		fmt.Printf("    Last run: %s ago\n", time.Since(t).Round(time.Minute))
	} else {
		fmt.Printf("    Last run: %s\n", last)
	}
	if raw, _ := idx.GetMeta(index.MetaLastCompactionResult); raw != "" {
		var r store.CompactionResult
		if json.Unmarshal([]byte(raw), &r) == nil {
			fmt.Printf("    Result:   removed %d events, freed %s\n", r.EventsRemoved, humanBytes(r.BytesFreed))
		}
	}
	fmt.Println()
}

func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if p == path {
				return walkErr
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		size += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return size, nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func printStoreStatus(cfg *config.Config) {
	var idx *index.Index
	if _, err := os.Stat(cfg.IndexPath()); err == nil {
		if opened, err := index.Open(cfg.IndexPath()); err == nil {
			idx = opened
			defer idx.Close()
		}
	}
	st := store.ReadMigrationStatus(idx, cfg.StoreManifestPath())
	line := storeFormatLine(st)
	if st.Format == store.FormatEncrypted && st.Migration == store.MigrationPending {
		line += ", migration " + migrationLine(st)
	}
	fmt.Printf("    Store:   %s\n", line)
}
