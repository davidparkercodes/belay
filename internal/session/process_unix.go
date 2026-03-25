//go:build !windows

package session

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

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

// getProcessCwd returns the current working directory of a process using lsof.
func getProcessCwd(pid int) string {
	out, err := exec.Command("lsof", "-p", strconv.Itoa(pid), "-Fn", "-d", "cwd").Output()
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
