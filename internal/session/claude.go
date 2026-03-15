package session

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

// ClaudeDetector detects and attributes file changes to Claude Code sessions
// by inspecting session files and process trees.
type ClaudeDetector struct {
	projectRoot string
}

// NewClaudeDetector creates a ClaudeDetector scoped to the given project root.
func NewClaudeDetector(projectRoot string) *ClaudeDetector {
	return &ClaudeDetector{projectRoot: projectRoot}
}

// Name returns "claude-code" as the detector identifier.
func (d *ClaudeDetector) Name() string {
	return "claude-code"
}

func (d *ClaudeDetector) Detect() ([]*DetectedSession, error) {
	fileSessions, _ := d.detectFromSessionFiles()
	procSessions, _ := d.detectFromProcesses()

	if len(fileSessions) == 0 {
		return procSessions, nil
	}
	if len(procSessions) == 0 {
		return d.filterDeadFileSessions(fileSessions), nil
	}

	procByID := make(map[string]*DetectedSession, len(procSessions))
	for _, s := range procSessions {
		procByID[s.SessionID] = s
	}

	var merged []*DetectedSession
	seen := make(map[string]bool)

	for _, fs := range fileSessions {
		if ps, ok := procByID[fs.SessionID]; ok {
			merged = append(merged, ps)
			seen[fs.SessionID] = true
		} else {
			if d.hasLiveClaudeProcess() {
				merged = append(merged, fs)
			}
			seen[fs.SessionID] = true
		}
	}

	for _, ps := range procSessions {
		if !seen[ps.SessionID] {
			merged = append(merged, ps)
		}
	}

	return merged, nil
}

func (d *ClaudeDetector) filterDeadFileSessions(sessions []*DetectedSession) []*DetectedSession {
	if len(sessions) == 0 {
		return nil
	}
	if !d.hasLiveClaudeProcess() {
		return nil
	}
	return sessions
}

func (d *ClaudeDetector) hasLiveClaudeProcess() bool {
	out, err := getProcessList()
	if err != nil {
		return false
	}

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !isClaudeProcess(line) {
			continue
		}
		pid, _, err := parsePSLine(line)
		if err != nil {
			continue
		}
		cwd := getProcessCwd(pid)
		if cwd == "" || strings.HasPrefix(cwd, d.projectRoot) {
			return true
		}
	}
	return false
}

func (d *ClaudeDetector) detectFromSessionFiles() ([]*DetectedSession, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	projectsDir := filepath.Join(homeDir, ".claude", "projects")
	if _, err := os.Stat(projectsDir); os.IsNotExist(err) {
		return nil, nil
	}

	var sessions []*DetectedSession

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, fmt.Errorf("read projects dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		encodedRoot := encodeProjectPath(d.projectRoot)
		if entry.Name() != encodedRoot {
			continue
		}

		sessDir := filepath.Join(projectsDir, entry.Name())
		sessEntries, err := os.ReadDir(sessDir)
		if err != nil {
			continue
		}

		for _, sessEntry := range sessEntries {
			if sessEntry.IsDir() {
				continue
			}

			if filepath.Ext(sessEntry.Name()) == ".json" {
				sessionID := strings.TrimSuffix(sessEntry.Name(), ".json")
				info, _ := sessEntry.Info()
				var modTime time.Time
				if info != nil {
					modTime = info.ModTime()
				}

				if time.Since(modTime) < 30*time.Minute {
					sessions = append(sessions, &DetectedSession{
						SessionID:        sessionID,
						ToolName:         "claude-code",
						WorkingDirectory: d.projectRoot,
						StartedAt:        modTime,
						Metadata: map[string]string{
							"source":      "session-file",
							"session_dir": sessDir,
						},
					})
				}
			}
		}
	}

	return sessions, nil
}

func (d *ClaudeDetector) detectFromProcesses() ([]*DetectedSession, error) {
	out, err := getProcessList()
	if err != nil {
		return nil, err
	}

	var sessions []*DetectedSession
	seenPIDs := make(map[int]bool)

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !isClaudeProcess(line) {
			continue
		}

		pid, _, err := parsePSLine(line)
		if err != nil {
			continue
		}

		if seenPIDs[pid] {
			continue
		}
		seenPIDs[pid] = true

		sessionID := extractSessionID(pid, line)
		if sessionID == "" {
			sessionID = fmt.Sprintf("claude-pid-%d", pid)
		}

		cwd := getProcessCwd(pid)

		if cwd != "" && cwd != "/" && !strings.HasPrefix(cwd, d.projectRoot) {
			continue
		}
		if cwd == "/" || cwd == "" {
			cwd = d.projectRoot
		}

		sessions = append(sessions, &DetectedSession{
			SessionID:        sessionID,
			ToolName:         "claude-code",
			PID:              pid,
			WorkingDirectory: cwd,
			StartedAt:        getProcessStartTime(pid),
			Metadata: map[string]string{
				"source": "process",
			},
		})
	}

	return sessions, nil
}

// Identify checks whether the given PID belongs to a Claude Code process tree.
func (d *ClaudeDetector) Identify(pid int) (*DetectedSession, error) {
	claudePID := findClaudeAncestor(pid)
	if claudePID == 0 {
		return nil, nil
	}

	sessionID := fmt.Sprintf("claude-pid-%d", claudePID)
	return &DetectedSession{
		SessionID:        sessionID,
		ToolName:         "claude-code",
		PID:              claudePID,
		WorkingDirectory: getProcessCwd(claudePID),
		StartedAt:        getProcessStartTime(claudePID),
		Metadata: map[string]string{
			"source":       "pid-identify",
			"original_pid": strconv.Itoa(pid),
		},
	}, nil
}

// Attribute determines which Claude Code session is responsible for a file write event.
func (d *ClaudeDetector) Attribute(event *FileWriteEvent, activeSessions []*DetectedSession) (string, float32, schema.AttributionMethod) {
	var claudeSessions []*DetectedSession
	for _, s := range activeSessions {
		if s.ToolName == "claude-code" {
			claudeSessions = append(claudeSessions, s)
		}
	}

	if len(claudeSessions) == 0 {
		return "", 0, schema.AttrNone
	}

	if event.WriterPID > 0 {
		claudePID := findClaudeAncestor(event.WriterPID)
		if claudePID > 0 {
			for _, s := range claudeSessions {
				if s.PID == claudePID {
					return s.SessionID, 0.95, schema.AttrPID
				}
			}
		}
	}

	for _, s := range claudeSessions {
		if s.WorkingDirectory != "" {
			absPath := filepath.Join(d.projectRoot, event.FilePath)
			if strings.HasPrefix(absPath, s.WorkingDirectory) {
				return s.SessionID, 0.6, schema.AttrHeuristic
			}
		}
	}

	if len(claudeSessions) == 1 {
		return claudeSessions[0].SessionID, 0.7, schema.AttrTemporal
	}

	return "", 0, schema.AttrNone
}


func isClaudeProcess(psLine string) bool {
	if strings.Contains(psLine, "belay") {
		return false
	}
	_, _, cmd := splitPSFields(psLine)
	if cmd == "" {
		return false
	}
	cmdLower := strings.ToLower(cmd)
	if strings.Contains(cmdLower, "@anthropic-ai/claude-code") || strings.Contains(cmdLower, "@anthropic/") {
		return true
	}
	binary := extractBinary(cmd)
	lower := strings.ToLower(binary)
	return lower == "claude" || lower == "claude-code"
}

func splitPSFields(line string) (pid, ppid, cmd string) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return "", "", ""
	}
	return fields[0], fields[1], strings.Join(fields[2:], " ")
}

func extractBinary(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	bin := fields[0]
	if idx := strings.LastIndex(bin, "/"); idx >= 0 {
		bin = bin[idx+1:]
	}
	return bin
}

func parsePSLine(line string) (pid, ppid int, err error) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return 0, 0, fmt.Errorf("too few fields")
	}
	pid, err = strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, fmt.Errorf("parse PID: %w", err)
	}
	ppid, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, fmt.Errorf("parse PPID: %w", err)
	}
	return pid, ppid, nil
}

func extractSessionID(pid int, cmdLine string) string {
	if idx := strings.Index(cmdLine, "--session-id"); idx >= 0 {
		rest := strings.TrimSpace(cmdLine[idx+len("--session-id"):])
		fields := strings.Fields(rest)
		if len(fields) > 0 {
			val := strings.TrimPrefix(fields[0], "=")
			if val != "" {
				return val
			}
			if len(fields) > 1 {
				return fields[1]
			}
		}
	}

	if sid := extractSessionIDFromEnv(pid); sid != "" {
		return sid
	}

	return ""
}

func findClaudeAncestor(pid int) int {
	current := pid
	for i := 0; i < 20; i++ {
		if current <= 1 {
			return 0
		}

		cmdLine := getProcessCommand(current)
		if isClaudeProcess(cmdLine) {
			return current
		}

		parent := getParentPID(current)
		if parent <= 1 || parent == current {
			return 0
		}
		current = parent
	}
	return 0
}


func encodeProjectPath(path string) string {
	// Normalize to forward slashes first (handles Windows backslashes)
	normalized := strings.ReplaceAll(path, "\\", "/")
	// Remove drive letter colon on Windows (e.g., "C:" -> "C")
	normalized = strings.ReplaceAll(normalized, ":", "")
	return strings.ReplaceAll(normalized, "/", "-")
}
