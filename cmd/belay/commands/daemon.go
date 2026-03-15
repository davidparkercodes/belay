package commands

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/daemon"

	"github.com/spf13/cobra"
)

func newDaemonCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the Belay daemon",
		Long:  `Start, stop, and monitor the Belay file watcher daemon.`,
	}

	cmd.AddCommand(
		newDaemonStartCmd(version),
		newDaemonStopCmd(),
		newDaemonRestartCmd(version),
		newDaemonStatusCmd(),
	)

	return cmd
}

func newDaemonStartCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the file watcher daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStart(cmd, args, version)
		},
	}
	cmd.Flags().Bool("foreground", false, "Run in foreground (don't daemonize)")
	return cmd
}

func startDaemonBackground(projectRoot string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("find executable: %w", err)
	}

	bgCmd := exec.Command(exe, "daemon", "start", "--foreground")
	bgCmd.Dir = projectRoot
	bgCmd.Stdout = nil
	bgCmd.Stderr = nil

	if err := bgCmd.Start(); err != nil {
		return 0, fmt.Errorf("start background daemon: %w", err)
	}

	pid := bgCmd.Process.Pid

	if err := bgCmd.Process.Release(); err != nil {
		return pid, fmt.Errorf("release process: %w", err)
	}

	return pid, nil
}

func runDaemonStart(cmd *cobra.Command, args []string, version string) error {
	foreground, _ := cmd.Flags().GetBool("foreground")

	projectRoot, err := config.FindProjectRoot()
	if err != nil {
		return fmt.Errorf("not a belay project: %w\nRun 'belay init' first", err)
	}

	cfg, err := config.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if running, pid := daemon.IsRunning(cfg); running {
		fmt.Printf("Daemon is already running (PID %d)\n", pid)
		return nil
	}

	if foreground {
		d, err := daemon.New(cfg, version)
		if err != nil {
			return fmt.Errorf("create daemon: %w", err)
		}
		return d.Run()
	}

	pid, err := startDaemonBackground(projectRoot)
	if err != nil {
		return err
	}

	fmt.Printf("Belay daemon started (PID %d)\n", pid)
	fmt.Println("Watching for file changes...")
	return nil
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectRoot, err := config.FindProjectRoot()
			if err != nil {
				return fmt.Errorf("not a belay project: %w", err)
			}

			cfg, err := config.Load(projectRoot)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if err := daemon.Stop(cfg); err != nil {
				return err
			}

			fmt.Println("Belay daemon stopped")
			return nil
		},
	}
}

func newDaemonRestartCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectRoot, err := config.FindProjectRoot()
			if err != nil {
				return fmt.Errorf("not a belay project: %w", err)
			}

			cfg, err := config.Load(projectRoot)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if running, _ := daemon.IsRunning(cfg); running {
				if err := daemon.Stop(cfg); err != nil {
					fmt.Printf("Warning: stop failed: %v\n", err)
				}
				fmt.Println("Stopped existing daemon")
			}

			return runDaemonStart(cmd, args, version)
		},
	}
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, args)
		},
	}
}
