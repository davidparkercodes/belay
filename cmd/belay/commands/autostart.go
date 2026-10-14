package commands

import (
	"os"
	"time"

	"github.com/davidparkercodes/belay/internal/apiclient"
	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/daemon"
)

const startLockStaleAfter = 60 * time.Second

var (
	autoStartWait     = 10 * time.Second
	autoStartLauncher = startDaemonBackground
)

func autoStartDaemon(cfg *config.Config) bool {
	if os.Getenv("BELAY_NO_AUTOSTART") == "1" {
		return false
	}
	if running, _ := daemon.IsRunning(cfg); running {
		return false
	}

	lock := cfg.StartLockPath()
	if err := os.Mkdir(lock, 0o755); err != nil {
		info, statErr := os.Stat(lock)
		if statErr != nil || time.Since(info.ModTime()) < startLockStaleAfter {
			return false
		}
		_ = os.Remove(lock)
		if err := os.Mkdir(lock, 0o755); err != nil {
			return false
		}
	}
	defer os.Remove(lock)

	if _, err := autoStartLauncher(cfg.ProjectRoot); err != nil {
		return false
	}

	deadline := time.Now().Add(autoStartWait)
	for time.Now().Before(deadline) {
		if running, _ := daemon.IsRunning(cfg); running {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return true
}

func waitForAPI(cfg *config.Config, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var err error
	for time.Now().Before(deadline) {
		var health map[string]interface{}
		if err = apiclient.New(cfg, time.Second).Get("/api/health", &health); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return err
}
