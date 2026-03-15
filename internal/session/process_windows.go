//go:build windows

package session

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// getProcessList returns process information using wmic on Windows.
// Returns output in a format compatible with parsePSLine (pid ppid command).
func getProcessList() (string, error) {
	out, err := exec.Command("wmic", "process", "get",
		"ProcessId,ParentProcessId,CommandLine", "/FORMAT:LIST").Output()
	if err != nil {
		return "", fmt.Errorf("wmic command: %w", err)
	}

	// Parse WMIC LIST output into "pid ppid command" lines for compatibility.
	var lines []string
	var cmdLine, parentPID, processID string

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
		if line == "" {
			if processID != "" {
				lines = append(lines, fmt.Sprintf("%s %s %s", processID, parentPID, cmdLine))
				cmdLine, parentPID, processID = "", "", ""
			}
			continue
		}
		if strings.HasPrefix(line, "CommandLine=") {
			cmdLine = strings.TrimPrefix(line, "CommandLine=")
		} else if strings.HasPrefix(line, "ParentProcessId=") {
			parentPID = strings.TrimPrefix(line, "ParentProcessId=")
		} else if strings.HasPrefix(line, "ProcessId=") {
			processID = strings.TrimPrefix(line, "ProcessId=")
		}
	}
	// Flush last entry
	if processID != "" {
		lines = append(lines, fmt.Sprintf("%s %s %s", processID, parentPID, cmdLine))
	}

	return strings.Join(lines, "\n"), nil
}

// getParentPID returns the parent PID of the given process on Windows.
func getParentPID(pid int) int {
	out, err := exec.Command("wmic", "process", "where",
		fmt.Sprintf("ProcessId=%d", pid), "get", "ParentProcessId", "/FORMAT:VALUE").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
		if strings.HasPrefix(line, "ParentProcessId=") {
			val := strings.TrimPrefix(line, "ParentProcessId=")
			ppid, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				return 0
			}
			return ppid
		}
	}
	return 0
}

// getProcessCommand returns the command line of the given process on Windows.
func getProcessCommand(pid int) string {
	out, err := exec.Command("wmic", "process", "where",
		fmt.Sprintf("ProcessId=%d", pid), "get", "CommandLine", "/FORMAT:VALUE").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
		if strings.HasPrefix(line, "CommandLine=") {
			return strings.TrimPrefix(line, "CommandLine=")
		}
	}
	return ""
}

// getProcessCwd returns the current working directory of a process.
// On Windows, retrieving the CWD of another process requires special privileges
// and is not reliably possible without elevated access. Returns empty string
// as a fallback; session detection via session files still works.
func getProcessCwd(pid int) string {
	// CWD introspection is not reliably available on Windows without admin rights.
	// Session detection will fall back to session file-based discovery.
	return ""
}

// getProcessStartTime returns the start time of a process on Windows.
func getProcessStartTime(pid int) time.Time {
	out, err := exec.Command("wmic", "process", "where",
		fmt.Sprintf("ProcessId=%d", pid), "get", "CreationDate", "/FORMAT:VALUE").Output()
	if err != nil {
		return time.Now()
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
		if strings.HasPrefix(line, "CreationDate=") {
			val := strings.TrimPrefix(line, "CreationDate=")
			// WMIC CreationDate format: 20060102150405.000000-420
			if len(val) >= 14 {
				t, err := time.Parse("20060102150405", val[:14])
				if err == nil {
					return t
				}
			}
		}
	}
	return time.Now()
}

// extractSessionIDFromEnv attempts to read the CLAUDE_SESSION_ID environment variable
// from the process. On Windows, /proc does not exist, so this is a no-op.
func extractSessionIDFromEnv(pid int) string {
	// /proc filesystem does not exist on Windows.
	// Session ID extraction relies on command-line parsing instead.
	return ""
}
