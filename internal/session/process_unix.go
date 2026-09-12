//go:build !windows

package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// lsofMu serializes lsof invocations. On macOS 26 (Darwin 25.2.0) concurrent
// lsof calls have triggered reproducible kernel panics (NULL+0x48 in proc
// file-table iteration). Single-PID calls hit a different syscall path than
// the multi-process enumeration that panicked, so they are much lower risk,
// but serializing + capping duration eliminates any residual race exposure.
var lsofMu sync.Mutex

// getProcessList returns the output of `ps -eo pid,ppid,command` for process discovery.
func getProcessList() (string, error) {
	out, err := exec.Command("ps", "-eo", "pid,ppid,command").Output()
	if err != nil {
		return "", fmt.Errorf("ps command: %w", err)
	}
	return string(out), nil
}

// getParentPID returns the parent PID of the given process using ps.
func getParentPID(pid int) int {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	ppid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return ppid
}

// getProcessCommand returns the command line of the given process using ps.
func getProcessCommand(pid int) string {
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getProcessCwd returns the current working directory of a process.
// Serialized + time-boxed because lsof on macOS 26 has triggered kernel panics
// under concurrency; see lsofMu for the full context.
func getProcessCwd(pid int) string {
	// Linux exposes the cwd directly as a kernel symlink, so no subprocess is
	// needed at all (cheaper, and avoids the macOS lsof panic path). On macOS
	// this path does not exist and Readlink fails, falling through to lsof.
	if cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil && cwd != "" {
		if strings.Contains(cwd, "(") {
			return ""
		}
		return cwd
	}

	lsofMu.Lock()
	defer lsofMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// -a is required: it ANDs the selectors so lsof reports only the target PID.
	// Without it lsof ORs -p and -d, scans every process on the machine (~1.2s vs
	// ~0.07s), and the parse below returns some unrelated process's cwd.
	out, err := exec.CommandContext(ctx, "lsof", "-a", "-p", strconv.Itoa(pid), "-Fn", "-d", "cwd").Output()
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			path := line[1:]
			if strings.Contains(path, "(") {
				return ""
			}
			return path
		}
	}
	return ""
}

// getProcessStartTime returns the start time of a process using ps.
func getProcessStartTime(pid int) time.Time {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return time.Now()
	}

	t, err := time.Parse("Mon Jan  2 15:04:05 2006", strings.TrimSpace(string(out)))
	if err != nil {
		t, err = time.Parse("Mon Jan 2 15:04:05 2006", strings.TrimSpace(string(out)))
		if err != nil {
			return time.Now()
		}
	}
	return t
}

// extractSessionIDFromEnv attempts to read the CLAUDE_SESSION_ID environment variable
// from /proc/<pid>/environ. This only works on Linux.
func extractSessionIDFromEnv(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return ""
	}
	for _, env := range strings.Split(string(data), "\x00") {
		if strings.HasPrefix(env, "CLAUDE_SESSION_ID=") {
			return strings.TrimPrefix(env, "CLAUDE_SESSION_ID=")
		}
	}
	return ""
}
