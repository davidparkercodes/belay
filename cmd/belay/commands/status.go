package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/daemon"

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

	return cmd
}

func runStatus(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")

	projectRoot, err := config.FindProjectRoot()
	if err != nil {
		return fmt.Errorf("not a belay project: %w", err)
	}

	cfg, err := config.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	daemonRunning, daemonPID := daemon.IsRunning(cfg)

	if jsonOutput {
		status := map[string]interface{}{
			"project_root":   projectRoot,
			"belay_path":    cfg.BelayPath,
			"daemon_running": daemonRunning,
			"daemon_pid":     daemonPID,
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
	} else {
		fmt.Println("  Daemon:  \033[31mstopped\033[0m")
		fmt.Println("           Run 'belay daemon start' to begin watching files")
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
	fmt.Println()

	return nil
}

func dirSize(path string) (int64, error) {
	var size int64
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !entry.IsDir() {
			size += info.Size()
		}
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
