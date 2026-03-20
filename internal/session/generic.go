package session

import (
	"bufio"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/davidparkercodes/belay/internal/schema"
)

type processPattern struct {
	binary   string
	argument string
}

type GenericProcessDetector struct {
	toolName    string
	projectRoot string
	patterns    []processPattern
}

func NewCursorDetector(projectRoot string) *GenericProcessDetector {
	return &GenericProcessDetector{
		toolName:    "cursor",
		projectRoot: projectRoot,
		patterns: []processPattern{
			{binary: "cursor"},
			{argument: "cursor-agent"},
			{argument: ".cursor/"},
		},
	}
}

func NewWindsurfDetector(projectRoot string) *GenericProcessDetector {
	return &GenericProcessDetector{
		toolName:    "windsurf",
		projectRoot: projectRoot,
		patterns: []processPattern{
			{binary: "windsurf"},
			{argument: "windsurf"},
			{argument: "codeium"},
		},
	}
}

func NewAiderDetector(projectRoot string) *GenericProcessDetector {
	return &GenericProcessDetector{
		toolName:    "aider",
		projectRoot: projectRoot,
		patterns: []processPattern{
			{binary: "aider"},
			{argument: "aider"},
		},
	}
}

func NewCopilotDetector(projectRoot string) *GenericProcessDetector {
	return &GenericProcessDetector{
		toolName:    "copilot",
		projectRoot: projectRoot,
		patterns: []processPattern{
			{argument: "github.copilot"},
			{argument: "copilot-agent"},
		},
	}
}

func (d *GenericProcessDetector) Name() string {
	return d.toolName
}

func (d *GenericProcessDetector) Detect() ([]*DetectedSession, error) {
	out, err := getProcessList()
	if err != nil {
		return nil, err
	}

	var sessions []*DetectedSession
	seenPIDs := make(map[int]bool)

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !d.matchesProcess(line) {
			continue
		}

		pid, _, err := parsePSLine(line)
		if err != nil || seenPIDs[pid] {
			continue
		}
		seenPIDs[pid] = true

		cwd := getProcessCwd(pid)
		if cwd != "" && cwd != "/" && !strings.HasPrefix(cwd, d.projectRoot) {
			continue
		}
		if cwd == "/" || cwd == "" {
			cwd = d.projectRoot
		}

		sessionID := fmt.Sprintf("%s-pid-%d", d.toolName, pid)

		sessions = append(sessions, &DetectedSession{
			SessionID:        sessionID,
			ToolName:         d.toolName,
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

func (d *GenericProcessDetector) Identify(pid int) (*DetectedSession, error) {
	ancestorPID := d.findAncestor(pid)
	if ancestorPID == 0 {
		return nil, nil
	}

	return &DetectedSession{
		SessionID:        fmt.Sprintf("%s-pid-%d", d.toolName, ancestorPID),
		ToolName:         d.toolName,
		PID:              ancestorPID,
		WorkingDirectory: getProcessCwd(ancestorPID),
		StartedAt:        getProcessStartTime(ancestorPID),
		Metadata: map[string]string{
			"source":       "pid-identify",
			"original_pid": strconv.Itoa(pid),
		},
	}, nil
}

func (d *GenericProcessDetector) Attribute(event *FileWriteEvent, activeSessions []*DetectedSession) (string, float32, schema.AttributionMethod) {
	var toolSessions []*DetectedSession
	for _, s := range activeSessions {
		if s.ToolName == d.toolName {
			toolSessions = append(toolSessions, s)
		}
	}

	if len(toolSessions) == 0 {
		return "", 0, schema.AttrNone
	}

	if event.WriterPID > 0 {
		ancestorPID := d.findAncestor(event.WriterPID)
		if ancestorPID > 0 {
			for _, s := range toolSessions {
				if s.PID == ancestorPID {
					return s.SessionID, 0.95, schema.AttrPID
				}
			}
		}
	}

	for _, s := range toolSessions {
		if s.WorkingDirectory != "" {
			absPath := filepath.Join(d.projectRoot, event.FilePath)
			if strings.HasPrefix(absPath, s.WorkingDirectory) {
				return s.SessionID, 0.6, schema.AttrHeuristic
			}
		}
	}

	if len(toolSessions) == 1 {
		return toolSessions[0].SessionID, 0.7, schema.AttrTemporal
	}

	return "", 0, schema.AttrNone
}

func (d *GenericProcessDetector) matchesProcess(psLine string) bool {
	if strings.Contains(psLine, "belay") {
		return false
	}
	_, _, cmd := splitPSFields(psLine)
	if cmd == "" {
		return false
	}
	cmdLower := strings.ToLower(cmd)

	for _, p := range d.patterns {
		if p.binary != "" {
			binary := extractBinary(cmd)
			if strings.ToLower(binary) == p.binary {
				return true
			}
		}
		if p.argument != "" && strings.Contains(cmdLower, p.argument) {
			return true
		}
	}
	return false
}

func (d *GenericProcessDetector) findAncestor(pid int) int {
	current := pid
	for i := 0; i < 20; i++ {
		if current <= 1 {
			return 0
		}
		cmdLine := getProcessCommand(current)
		if d.matchesProcess(cmdLine) {
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
