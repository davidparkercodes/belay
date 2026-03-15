//go:build windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
)

func shutdownSignalChannel() <-chan os.Signal {
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt)
	return sigCh
}

func isProcessAlive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), fmt.Sprintf("%d", pid))
}

func terminateProcess(pid int) error {
	return exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid)).Run()
}
