//go:build !windows

package daemon

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStop_TimesOutWhenProcessIgnoresSIGTERM(t *testing.T) {
	cfg := testConfig(t)

	cmd := exec.Command("sh", "-c", `trap "" TERM; echo ready; while :; do sleep 1; done`)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start subprocess: %v", err)
	}
	exited := make(chan struct{})
	defer func() {
		_ = cmd.Process.Kill()
		<-exited
	}()
	buf := make([]byte, 5)
	if _, err := out.Read(buf); err != nil {
		t.Fatalf("wait for trap: %v", err)
	}
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	if err := os.WriteFile(cfg.PIDPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	prev := stopTimeout
	stopTimeout = 300 * time.Millisecond
	defer func() { stopTimeout = prev }()

	err = Stop(cfg)
	if err == nil {
		t.Fatal("Stop should fail when the process does not exit")
	}
	if !strings.Contains(err.Error(), "did not exit") {
		t.Errorf("unexpected error: %v", err)
	}
	if running, _ := IsRunning(cfg); !running {
		t.Error("process should still be running after a failed Stop")
	}
}
