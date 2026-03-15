package session

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

// ─── DetectedSession ────────────────────────────────────────────────────────

func TestDetectedSession_Fields(t *testing.T) {
	now := time.Now()
	ds := &DetectedSession{
		SessionID:        "test-session-123",
		ToolName:         "claude-code",
		PID:              42,
		WorkingDirectory: "/home/user/project",
		StartedAt:        now,
		Metadata: map[string]string{
			"source": "test",
		},
	}

	if ds.SessionID != "test-session-123" {
		t.Errorf("SessionID = %q, want %q", ds.SessionID, "test-session-123")
	}
	if ds.ToolName != "claude-code" {
		t.Errorf("ToolName = %q, want %q", ds.ToolName, "claude-code")
	}
	if ds.PID != 42 {
		t.Errorf("PID = %d, want 42", ds.PID)
	}
	if ds.WorkingDirectory != "/home/user/project" {
		t.Errorf("WorkingDirectory = %q, want %q", ds.WorkingDirectory, "/home/user/project")
	}
	if ds.Metadata["source"] != "test" {
		t.Errorf("Metadata[source] = %q, want %q", ds.Metadata["source"], "test")
	}
}

// ─── FileWriteEvent ─────────────────────────────────────────────────────────

func TestFileWriteEvent_Fields(t *testing.T) {
	now := time.Now()
	fwe := &FileWriteEvent{
		FilePath:  "src/main.go",
		Operation: schema.OpModify,
		Timestamp: now,
		WriterPID: 1234,
		Size:      5678,
	}

	if fwe.FilePath != "src/main.go" {
		t.Errorf("FilePath = %q, want %q", fwe.FilePath, "src/main.go")
	}
	if fwe.Operation != schema.OpModify {
		t.Errorf("Operation = %v, want OpModify", fwe.Operation)
	}
	if fwe.WriterPID != 1234 {
		t.Errorf("WriterPID = %d, want 1234", fwe.WriterPID)
	}
	if fwe.Size != 5678 {
		t.Errorf("Size = %d, want 5678", fwe.Size)
	}
}

// ─── Registry ───────────────────────────────────────────────────────────────

// mockDetector implements the Detector interface for testing.
type mockDetector struct {
	name       string
	sessions   []*DetectedSession
	detectErr  error
	identifyFn func(pid int) (*DetectedSession, error)
	attributeFn func(event *FileWriteEvent, active []*DetectedSession) (string, float32, schema.AttributionMethod)
}

func (m *mockDetector) Name() string { return m.name }

func (m *mockDetector) Detect() ([]*DetectedSession, error) {
	return m.sessions, m.detectErr
}

func (m *mockDetector) Identify(pid int) (*DetectedSession, error) {
	if m.identifyFn != nil {
		return m.identifyFn(pid)
	}
	return nil, nil
}

func (m *mockDetector) Attribute(event *FileWriteEvent, activeSessions []*DetectedSession) (string, float32, schema.AttributionMethod) {
	if m.attributeFn != nil {
		return m.attributeFn(event, activeSessions)
	}
	return "", 0, schema.AttrNone
}

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if len(r.DetectorNames()) != 0 {
		t.Errorf("expected 0 detectors, got %d", len(r.DetectorNames()))
	}
}

func TestNewRegistry_WithDetectors(t *testing.T) {
	d1 := &mockDetector{name: "claude-code"}
	d2 := &mockDetector{name: "cursor"}
	r := NewRegistry(d1, d2)

	names := r.DetectorNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 detectors, got %d", len(names))
	}
	if names[0] != "claude-code" {
		t.Errorf("names[0] = %q, want %q", names[0], "claude-code")
	}
	if names[1] != "cursor" {
		t.Errorf("names[1] = %q, want %q", names[1], "cursor")
	}
}

func TestRegisterDetector(t *testing.T) {
	r := NewRegistry()
	d := &mockDetector{name: "new-tool"}
	r.RegisterDetector(d)

	names := r.DetectorNames()
	if len(names) != 1 {
		t.Fatalf("expected 1 detector, got %d", len(names))
	}
	if names[0] != "new-tool" {
		t.Errorf("name = %q, want %q", names[0], "new-tool")
	}
}

// ─── ActiveSessions / GetSession ────────────────────────────────────────────

func TestActiveSessions_Empty(t *testing.T) {
	r := NewRegistry()
	sessions := r.ActiveSessions()
	if len(sessions) != 0 {
		t.Errorf("expected 0 active sessions, got %d", len(sessions))
	}
}

func TestGetSession_NotFound(t *testing.T) {
	r := NewRegistry()
	s := r.GetSession("nonexistent")
	if s != nil {
		t.Errorf("expected nil, got %v", s)
	}
}

func confirmSession(r *Registry, sessionID string) {
	r.mu.Lock()
	ts, ok := r.sessions[sessionID]
	if !ok {
		r.mu.Unlock()
		return
	}
	if ts.confirmed {
		r.mu.Unlock()
		return
	}
	ts.confirmed = true
	cb := r.onSessionStart
	r.mu.Unlock()
	if cb != nil {
		cb(ts.session)
	}
}

// ─── Poll discovers sessions ────────────────────────────────────────────────

func TestPoll_DiscoversSessions(t *testing.T) {
	now := time.Now()
	d := &mockDetector{
		name: "test-detector",
		sessions: []*DetectedSession{
			{
				SessionID:        "poll-sess-1",
				ToolName:         "test-detector",
				PID:              100,
				WorkingDirectory: "/tmp/project",
				StartedAt:        now,
			},
		},
	}

	r := NewRegistry(d)

	var started []*schema.Session
	r.SetOnSessionStart(func(s *schema.Session) {
		started = append(started, s)
	})

	// First poll discovers the session (pending, not yet confirmed)
	r.poll()

	sessions := r.ActiveSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 active session, got %d", len(sessions))
	}
	if sessions[0].SessionID != "poll-sess-1" {
		t.Errorf("SessionID = %q, want %q", sessions[0].SessionID, "poll-sess-1")
	}
	if sessions[0].Status != schema.SessionActive {
		t.Errorf("Status = %v, want SessionActive", sessions[0].Status)
	}

	// Callback should NOT have fired yet (session still pending)
	if len(started) != 0 {
		t.Errorf("expected 0 onSessionStart callbacks before confirmation, got %d", len(started))
	}

	// Confirm the session (simulates first attributed event)
	confirmSession(r, "poll-sess-1")

	// Now the callback should have fired
	if len(started) != 1 {
		t.Errorf("expected 1 onSessionStart callback after confirmation, got %d", len(started))
	}

	// GetSession should work
	got := r.GetSession("poll-sess-1")
	if got == nil {
		t.Fatal("GetSession returned nil for known session")
	}
	if got.ToolName != "test-detector" {
		t.Errorf("ToolName = %q, want %q", got.ToolName, "test-detector")
	}
}

// ─── Poll: session disappears ───────────────────────────────────────────────

func TestPoll_UnconfirmedSessionDisappears_SilentlyRemoved(t *testing.T) {
	now := time.Now()
	d := &mockDetector{
		name: "test-detector",
		sessions: []*DetectedSession{
			{
				SessionID: "ghost-sess",
				ToolName:  "test",
				StartedAt: now,
			},
		},
	}

	r := NewRegistry(d)

	var ended []*schema.Session
	r.SetOnSessionEnd(func(s *schema.Session) {
		ended = append(ended, s)
	})

	// First poll discovers session (unconfirmed)
	r.poll()
	if len(r.ActiveSessions()) != 1 {
		t.Fatal("session not discovered on first poll")
	}

	// Session disappears before being confirmed
	d.sessions = nil

	for i := 0; i < maxMissCount; i++ {
		r.poll()
	}

	// Unconfirmed sessions are silently deleted, not crashed
	active := r.ActiveSessions()
	if len(active) != 0 {
		t.Errorf("expected 0 active sessions, got %d", len(active))
	}

	s := r.GetSession("ghost-sess")
	if s != nil {
		t.Error("unconfirmed session should be deleted from registry, got non-nil")
	}

	// onSessionEnd should NOT fire for unconfirmed sessions
	if len(ended) != 0 {
		t.Errorf("expected 0 onSessionEnd callbacks for unconfirmed session, got %d", len(ended))
	}
}

func TestPoll_ConfirmedSessionDisappears_MarkedCrashed(t *testing.T) {
	now := time.Now()
	d := &mockDetector{
		name: "test-detector",
		sessions: []*DetectedSession{
			{
				SessionID: "vanish-sess",
				ToolName:  "test",
				StartedAt: now,
			},
		},
	}

	r := NewRegistry(d)

	var ended []*schema.Session
	r.SetOnSessionEnd(func(s *schema.Session) {
		ended = append(ended, s)
	})

	// Discover and confirm the session
	r.poll()
	confirmSession(r, "vanish-sess")

	if len(r.ActiveSessions()) != 1 {
		t.Fatal("session not active after confirmation")
	}

	// Session disappears
	d.sessions = nil

	for i := 0; i < maxMissCount; i++ {
		r.poll()
	}

	// Confirmed session should be marked as crashed
	active := r.ActiveSessions()
	if len(active) != 0 {
		t.Errorf("expected 0 active sessions after disappearance, got %d", len(active))
	}

	s := r.GetSession("vanish-sess")
	if s == nil {
		t.Fatal("confirmed session should still exist in registry")
	}
	if s.Status != schema.SessionCrashed {
		t.Errorf("Status = %v, want SessionCrashed", s.Status)
	}

	if len(ended) != 1 {
		t.Errorf("expected 1 onSessionEnd callback, got %d", len(ended))
	}
}

// ─── Poll: session seen again resets missCount ──────────────────────────────

func TestPoll_SessionReappearsResetsMissCount(t *testing.T) {
	now := time.Now()
	d := &mockDetector{
		name: "test-detector",
		sessions: []*DetectedSession{
			{SessionID: "flaky-sess", ToolName: "test", StartedAt: now},
		},
	}

	r := NewRegistry(d)
	r.poll() // discover

	// Disappear for a few polls (but not enough to crash)
	d.sessions = nil
	for i := 0; i < maxMissCount-1; i++ {
		r.poll()
	}

	// Reappear
	d.sessions = []*DetectedSession{
		{SessionID: "flaky-sess", ToolName: "test", StartedAt: now},
	}
	r.poll()

	// Should still be active
	if len(r.ActiveSessions()) != 1 {
		t.Error("session should still be active after reappearing")
	}
}

// ─── Attribute ──────────────────────────────────────────────────────────────

func TestRegistry_Attribute_SingleDetector(t *testing.T) {
	d := &mockDetector{
		name: "test-detector",
		sessions: []*DetectedSession{
			{SessionID: "attr-sess", ToolName: "test", StartedAt: time.Now()},
		},
		attributeFn: func(event *FileWriteEvent, active []*DetectedSession) (string, float32, schema.AttributionMethod) {
			return "attr-sess", 0.9, schema.AttrPID
		},
	}

	r := NewRegistry(d)
	r.poll() // discover session

	event := &FileWriteEvent{
		FilePath:  "src/main.go",
		Operation: schema.OpModify,
		Timestamp: time.Now(),
		WriterPID: 1234,
	}

	sid, conf, method := r.Attribute(event)
	if sid != "attr-sess" {
		t.Errorf("sessionID = %q, want %q", sid, "attr-sess")
	}
	if conf != 0.9 {
		t.Errorf("confidence = %v, want 0.9", conf)
	}
	if method != schema.AttrPID {
		t.Errorf("method = %v, want AttrPID", method)
	}
}

func TestRegistry_Attribute_PicksHighestConfidence(t *testing.T) {
	d1 := &mockDetector{
		name: "low-conf",
		sessions: []*DetectedSession{
			{SessionID: "low-sess", ToolName: "low", StartedAt: time.Now()},
		},
		attributeFn: func(event *FileWriteEvent, active []*DetectedSession) (string, float32, schema.AttributionMethod) {
			return "low-sess", 0.5, schema.AttrHeuristic
		},
	}
	d2 := &mockDetector{
		name: "high-conf",
		sessions: []*DetectedSession{
			{SessionID: "high-sess", ToolName: "high", StartedAt: time.Now()},
		},
		attributeFn: func(event *FileWriteEvent, active []*DetectedSession) (string, float32, schema.AttributionMethod) {
			return "high-sess", 0.95, schema.AttrPID
		},
	}

	r := NewRegistry(d1, d2)
	r.poll()

	event := &FileWriteEvent{FilePath: "file.go", Operation: schema.OpModify, Timestamp: time.Now()}
	sid, conf, _ := r.Attribute(event)
	if sid != "high-sess" {
		t.Errorf("sessionID = %q, want %q (highest confidence)", sid, "high-sess")
	}
	if conf != 0.95 {
		t.Errorf("confidence = %v, want 0.95", conf)
	}
}

func TestRegistry_Attribute_FallbackToSingleSession(t *testing.T) {
	// Detector that returns no attribution
	d := &mockDetector{
		name: "no-match",
		sessions: []*DetectedSession{
			{SessionID: "solo-sess", ToolName: "test", StartedAt: time.Now()},
		},
		attributeFn: func(event *FileWriteEvent, active []*DetectedSession) (string, float32, schema.AttributionMethod) {
			return "", 0, schema.AttrNone
		},
	}

	r := NewRegistry(d)
	r.poll()

	event := &FileWriteEvent{FilePath: "file.go", Operation: schema.OpModify, Timestamp: time.Now()}
	sid, conf, method := r.Attribute(event)

	// Should fall back to the single active session
	if sid != "solo-sess" {
		t.Errorf("sessionID = %q, want %q (fallback to single session)", sid, "solo-sess")
	}
	if conf != 0.7 {
		t.Errorf("confidence = %v, want 0.7", conf)
	}
	if method != schema.AttrTemporal {
		t.Errorf("method = %v, want AttrTemporal", method)
	}
}

func TestRegistry_Attribute_NoSessions(t *testing.T) {
	r := NewRegistry()
	event := &FileWriteEvent{FilePath: "file.go", Operation: schema.OpModify, Timestamp: time.Now()}
	sid, conf, method := r.Attribute(event)

	if sid != "" {
		t.Errorf("sessionID = %q, want empty", sid)
	}
	if conf != 0 {
		t.Errorf("confidence = %v, want 0", conf)
	}
	if method != schema.AttrNone {
		t.Errorf("method = %v, want AttrNone", method)
	}
}

// ─── Start / Stop ───────────────────────────────────────────────────────────

func TestStartStop(t *testing.T) {
	d := &mockDetector{
		name:     "test-detector",
		sessions: []*DetectedSession{},
	}
	r := NewRegistry(d)

	var ended []*schema.Session
	r.SetOnSessionEnd(func(s *schema.Session) {
		ended = append(ended, s)
	})

	r.Start()
	// Give it a tiny bit of time to start polling
	time.Sleep(10 * time.Millisecond)
	r.Stop() // should not panic or hang

	// Stop should complete cleanly — that's the main assertion
}

func TestStop_EndsConfirmedActiveSessions(t *testing.T) {
	now := time.Now()
	d := &mockDetector{
		name: "test-detector",
		sessions: []*DetectedSession{
			{SessionID: "stop-sess", ToolName: "test", StartedAt: now},
		},
	}
	r := NewRegistry(d)

	var ended []*schema.Session
	r.SetOnSessionEnd(func(s *schema.Session) {
		ended = append(ended, s)
	})

	// Discover and confirm the session synchronusly before Start
	r.poll()
	confirmSession(r, "stop-sess")

	r.Start()
	time.Sleep(50 * time.Millisecond)
	r.Stop()

	// The confirmed session should have been ended on Stop
	if len(ended) != 1 {
		t.Errorf("expected 1 ended session on Stop, got %d", len(ended))
	}
	if len(ended) > 0 && ended[0].Status != schema.SessionEnded {
		t.Errorf("status = %v, want SessionEnded", ended[0].Status)
	}
}

func TestStop_SkipsUnconfirmedSessions(t *testing.T) {
	now := time.Now()
	d := &mockDetector{
		name: "test-detector",
		sessions: []*DetectedSession{
			{SessionID: "unconf-sess", ToolName: "test", StartedAt: now},
		},
	}
	r := NewRegistry(d)

	var ended []*schema.Session
	r.SetOnSessionEnd(func(s *schema.Session) {
		ended = append(ended, s)
	})

	// Discover but do NOT confirm
	r.poll()

	r.Start()
	time.Sleep(50 * time.Millisecond)
	r.Stop()

	// Unconfirmed sessions should not trigger onSessionEnd
	if len(ended) != 0 {
		t.Errorf("expected 0 ended sessions for unconfirmed, got %d", len(ended))
	}
}

// ─── ClaudeDetector pure functions ──────────────────────────────────────────

func TestIsClaudeProcess(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"1234 5678 /usr/bin/claude --session abc", true},
		{"1234 5678 claude-code start", true},
		{"1234 5678 node @anthropic/cli", true},
		{"1234 5678 /bin/bash", false},
		{"1234 5678 /usr/bin/belay daemon start", false}, // belay excluded
		{"1234 5678 Claude Desktop", true},
		{"1234 5678 vim main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got := isClaudeProcess(tt.line)
			if got != tt.want {
				t.Errorf("isClaudeProcess(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestParsePSLine(t *testing.T) {
	tests := []struct {
		line    string
		pid     int
		ppid    int
		wantErr bool
	}{
		{"1234 5678 /usr/bin/claude", 1234, 5678, false},
		{"42 1 init", 42, 1, false},
		{"bad line", 0, 0, true},
		{"1234", 0, 0, true},
		{"abc 123 cmd", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			pid, ppid, err := parsePSLine(tt.line)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if pid != tt.pid {
				t.Errorf("pid = %d, want %d", pid, tt.pid)
			}
			if ppid != tt.ppid {
				t.Errorf("ppid = %d, want %d", ppid, tt.ppid)
			}
		})
	}
}

func TestExtractSessionID(t *testing.T) {
	tests := []struct {
		name    string
		cmdLine string
		want    string
	}{
		{"with --session-id flag space", "1234 1 claude --session-id abc-123", "abc-123"},
		{"with --session-id= flag", "1234 1 claude --session-id=def-456", "def-456"},
		{"no session id", "1234 1 claude start", ""},
		{"empty after flag", "1234 1 claude --session-id", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSessionID(1234, tt.cmdLine)
			if got != tt.want {
				t.Errorf("extractSessionID = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEncodeProjectPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/home/user/project", "-home-user-project"},
		{"/", "-"},
		{"no-slashes", "no-slashes"},
		{"/a/b/c/d", "-a-b-c-d"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := encodeProjectPath(tt.path)
			if got != tt.want {
				t.Errorf("encodeProjectPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestClaudeDetector_Name(t *testing.T) {
	d := NewClaudeDetector("/tmp/project")
	if d.Name() != "claude-code" {
		t.Errorf("Name() = %q, want %q", d.Name(), "claude-code")
	}
}

// ─── Attribution Method Types ───────────────────────────────────────────────

func TestAttributionMethodTypes(t *testing.T) {
	tests := []struct {
		method schema.AttributionMethod
		str    string
	}{
		{schema.AttrNone, "none"},
		{schema.AttrPID, "pid"},
		{schema.AttrTemporal, "temporal"},
		{schema.AttrHeuristic, "heuristic"},
		{schema.AttrManual, "manual"},
		{schema.AttrHook, "hook"},
	}

	for _, tt := range tests {
		t.Run(tt.str, func(t *testing.T) {
			if got := tt.method.String(); got != tt.str {
				t.Errorf("String() = %q, want %q", got, tt.str)
			}
			parsed := schema.ParseAttributionMethod(tt.str)
			if parsed != tt.method {
				t.Errorf("ParseAttributionMethod(%q) = %v, want %v", tt.str, parsed, tt.method)
			}
		})
	}
}

// ─── ClaudeDetector.Attribute ───────────────────────────────────────────────

func TestClaudeDetector_Attribute_NoClaudeSessions(t *testing.T) {
	d := NewClaudeDetector("/tmp/project")
	event := &FileWriteEvent{FilePath: "main.go", Operation: schema.OpModify}
	active := []*DetectedSession{
		{SessionID: "s1", ToolName: "other-tool"},
	}

	sid, conf, method := d.Attribute(event, active)
	if sid != "" || conf != 0 || method != schema.AttrNone {
		t.Errorf("expected no attribution, got (%q, %v, %v)", sid, conf, method)
	}
}

func TestClaudeDetector_Attribute_SingleClaudeSession_Heuristic(t *testing.T) {
	d := NewClaudeDetector("/tmp/project")
	event := &FileWriteEvent{
		FilePath:  "src/main.go",
		Operation: schema.OpModify,
		WriterPID: 0, // no PID info
	}
	active := []*DetectedSession{
		{
			SessionID:        "claude-sess",
			ToolName:         "claude-code",
			WorkingDirectory: "/tmp/project",
		},
	}

	sid, conf, method := d.Attribute(event, active)
	// Single claude session with matching working directory = heuristic
	if sid != "claude-sess" {
		t.Errorf("sessionID = %q, want %q", sid, "claude-sess")
	}
	if conf != 0.6 {
		t.Errorf("confidence = %v, want 0.6", conf)
	}
	if method != schema.AttrHeuristic {
		t.Errorf("method = %v, want AttrHeuristic", method)
	}
}

// ─── Process helper functions (claude.go) ───────────────────────────────────

func TestGetParentPID_ValidPID(t *testing.T) {
	// os.Getpid() is always a valid process; its parent should be > 0
	ppid := getParentPID(os.Getpid())
	if ppid <= 0 {
		t.Errorf("getParentPID(os.Getpid()) = %d, want > 0", ppid)
	}
	// The result should match os.Getppid()
	if ppid != os.Getppid() {
		t.Errorf("getParentPID(os.Getpid()) = %d, want %d (os.Getppid())", ppid, os.Getppid())
	}
}

func TestGetParentPID_InvalidPID(t *testing.T) {
	// PID 999999999 almost certainly doesn't exist
	ppid := getParentPID(999999999)
	if ppid != 0 {
		t.Errorf("getParentPID(999999999) = %d, want 0", ppid)
	}
}

func TestGetProcessCommand_ValidPID(t *testing.T) {
	cmd := getProcessCommand(os.Getpid())
	if cmd == "" {
		t.Error("getProcessCommand(os.Getpid()) returned empty string")
	}
	// The test binary should contain "session.test" or "go" in its command
	// (it varies by how tests are run, but it should never be empty)
}

func TestGetProcessCommand_InvalidPID(t *testing.T) {
	cmd := getProcessCommand(999999999)
	if cmd != "" {
		t.Errorf("getProcessCommand(999999999) = %q, want empty string", cmd)
	}
}

func TestGetProcessCwd_ValidPID(t *testing.T) {
	cwd := getProcessCwd(os.Getpid())
	// On macOS, lsof should return the cwd for our own process
	if cwd == "" {
		t.Skip("getProcessCwd returned empty (lsof may not be available or insufficient permissions)")
	}
	// The cwd should be a valid directory
	info, err := os.Stat(cwd)
	if err != nil {
		t.Errorf("getProcessCwd returned path that doesn't exist: %q", cwd)
	} else if !info.IsDir() {
		t.Errorf("getProcessCwd returned non-directory: %q", cwd)
	}
}

func TestGetProcessCwd_InvalidPID(t *testing.T) {
	cwd := getProcessCwd(999999999)
	if cwd != "" {
		t.Errorf("getProcessCwd(999999999) = %q, want empty string", cwd)
	}
}

func TestGetProcessStartTime_ValidPID(t *testing.T) {
	startTime := getProcessStartTime(os.Getpid())
	// Should return a time in the past (our process started before now)
	if startTime.After(time.Now()) {
		t.Errorf("getProcessStartTime returned future time: %v", startTime)
	}
	// Should be reasonably recent (within the last 24 hours for a test process)
	if time.Since(startTime) > 24*time.Hour {
		t.Errorf("getProcessStartTime returned time more than 24 hours ago: %v", startTime)
	}
}

func TestGetProcessStartTime_InvalidPID(t *testing.T) {
	before := time.Now()
	startTime := getProcessStartTime(999999999)
	// When PID doesn't exist, function returns time.Now()
	if startTime.Before(before) {
		t.Errorf("getProcessStartTime(invalid) returned time before call: %v < %v", startTime, before)
	}
}

// ─── findClaudeAncestor ─────────────────────────────────────────────────────

func TestFindClaudeAncestor_PID1(t *testing.T) {
	// PID 1 should immediately return 0 (init/launchd is not claude)
	result := findClaudeAncestor(1)
	if result != 0 {
		t.Errorf("findClaudeAncestor(1) = %d, want 0", result)
	}
}

func TestFindClaudeAncestor_PID0(t *testing.T) {
	result := findClaudeAncestor(0)
	if result != 0 {
		t.Errorf("findClaudeAncestor(0) = %d, want 0", result)
	}
}

func TestFindClaudeAncestor_OwnPID(t *testing.T) {
	// Our own process is a Go test binary. If running inside Claude Code,
	// an ancestor WILL be a Claude process and that's correct behavior.
	// We just verify it returns a valid result (0 or a positive PID) without crashing.
	result := findClaudeAncestor(os.Getpid())
	if result < 0 {
		t.Errorf("findClaudeAncestor(os.Getpid()) = %d, want >= 0", result)
	}
	// If a Claude ancestor was found, verify the command contains "claude"
	if result > 0 {
		cmd := getProcessCommand(result)
		if !isClaudeProcess(cmd) {
			t.Errorf("findClaudeAncestor returned PID %d but its command %q is not a Claude process", result, cmd)
		}
	}
}

func TestFindClaudeAncestor_InvalidPID(t *testing.T) {
	result := findClaudeAncestor(999999999)
	if result != 0 {
		t.Errorf("findClaudeAncestor(999999999) = %d, want 0", result)
	}
}

// ─── ClaudeDetector.Attribute — more cases ──────────────────────────────────

func TestClaudeDetector_Attribute_TemporalFallback_MultipleClaudeSessions(t *testing.T) {
	// When there are multiple Claude sessions but no PID match and no cwd match,
	// the detector should return empty (can't disambiguate)
	d := NewClaudeDetector("/tmp/project")
	event := &FileWriteEvent{
		FilePath:  "src/main.go",
		Operation: schema.OpModify,
		WriterPID: 0,
	}
	active := []*DetectedSession{
		{SessionID: "sess-1", ToolName: "claude-code", WorkingDirectory: "/other/path"},
		{SessionID: "sess-2", ToolName: "claude-code", WorkingDirectory: "/another/path"},
	}

	sid, conf, method := d.Attribute(event, active)
	if sid != "" {
		t.Errorf("sessionID = %q, want empty (ambiguous sessions)", sid)
	}
	if conf != 0 {
		t.Errorf("confidence = %v, want 0", conf)
	}
	if method != schema.AttrNone {
		t.Errorf("method = %v, want AttrNone", method)
	}
}

func TestClaudeDetector_Attribute_SingleClaudeNoWorkingDir(t *testing.T) {
	// Single Claude session with no working directory — should fall through to
	// the "single session" temporal fallback
	d := NewClaudeDetector("/tmp/project")
	event := &FileWriteEvent{
		FilePath:  "src/main.go",
		Operation: schema.OpModify,
		WriterPID: 0,
	}
	active := []*DetectedSession{
		{SessionID: "lone-sess", ToolName: "claude-code", WorkingDirectory: ""},
	}

	sid, conf, method := d.Attribute(event, active)
	// Working directory is empty, so heuristic check won't match.
	// But there's only one Claude session, so temporal fallback applies.
	if sid != "lone-sess" {
		t.Errorf("sessionID = %q, want %q", sid, "lone-sess")
	}
	if conf != 0.7 {
		t.Errorf("confidence = %v, want 0.7", conf)
	}
	if method != schema.AttrTemporal {
		t.Errorf("method = %v, want AttrTemporal", method)
	}
}

func TestClaudeDetector_Attribute_MixedToolSessions(t *testing.T) {
	// Non-claude sessions should be filtered out; only claude sessions count
	d := NewClaudeDetector("/tmp/project")
	event := &FileWriteEvent{
		FilePath:  "src/main.go",
		Operation: schema.OpModify,
		WriterPID: 0,
	}
	active := []*DetectedSession{
		{SessionID: "cursor-sess", ToolName: "cursor"},
		{SessionID: "claude-sess", ToolName: "claude-code", WorkingDirectory: "/tmp/project"},
		{SessionID: "vscode-sess", ToolName: "vscode"},
	}

	sid, conf, method := d.Attribute(event, active)
	if sid != "claude-sess" {
		t.Errorf("sessionID = %q, want %q", sid, "claude-sess")
	}
	if conf != 0.6 {
		t.Errorf("confidence = %v, want 0.6", conf)
	}
	if method != schema.AttrHeuristic {
		t.Errorf("method = %v, want AttrHeuristic", method)
	}
}

func TestClaudeDetector_Attribute_WorkingDirMismatch(t *testing.T) {
	// Claude session exists but its working directory doesn't match the file path
	d := NewClaudeDetector("/tmp/project")
	event := &FileWriteEvent{
		FilePath:  "src/main.go",
		Operation: schema.OpModify,
		WriterPID: 0,
	}
	active := []*DetectedSession{
		{SessionID: "claude-sess", ToolName: "claude-code", WorkingDirectory: "/other/project"},
	}

	sid, conf, method := d.Attribute(event, active)
	// Directory doesn't match but it's the only Claude session => temporal fallback
	if sid != "claude-sess" {
		t.Errorf("sessionID = %q, want %q (temporal fallback)", sid, "claude-sess")
	}
	if conf != 0.7 {
		t.Errorf("confidence = %v, want 0.7", conf)
	}
	if method != schema.AttrTemporal {
		t.Errorf("method = %v, want AttrTemporal", method)
	}
}

// ─── ClaudeDetector.Identify ────────────────────────────────────────────────

func TestClaudeDetector_Identify_OwnPID(t *testing.T) {
	d := NewClaudeDetector("/tmp/project")
	// If running inside Claude Code, a Claude ancestor exists and Identify
	// will return a session. If running standalone, it returns nil.
	// Either outcome is correct — we verify no error and valid structure.
	sess, err := d.Identify(os.Getpid())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess != nil {
		// If a session was found, validate its fields
		if sess.ToolName != "claude-code" {
			t.Errorf("ToolName = %q, want %q", sess.ToolName, "claude-code")
		}
		if sess.PID <= 0 {
			t.Errorf("PID = %d, want > 0", sess.PID)
		}
		if sess.Metadata["source"] != "pid-identify" {
			t.Errorf("Metadata[source] = %q, want %q", sess.Metadata["source"], "pid-identify")
		}
	}
}

func TestClaudeDetector_Identify_InvalidPID(t *testing.T) {
	d := NewClaudeDetector("/tmp/project")
	sess, err := d.Identify(999999999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil for invalid PID, got session %+v", sess)
	}
}

func TestClaudeDetector_Identify_PID1(t *testing.T) {
	d := NewClaudeDetector("/tmp/project")
	sess, err := d.Identify(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil for PID 1, got session %+v", sess)
	}
}

// ─── ClaudeDetector.Detect ──────────────────────────────────────────────────

func TestClaudeDetector_Detect_RunsWithoutError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process-scanning test in short mode")
	}
	// Use a non-existent project root so session files won't be found,
	// forcing it to fall through to process detection
	d := NewClaudeDetector("/nonexistent/project/path/that/doesnt/exist")
	sessions, err := d.Detect()
	if err != nil {
		t.Fatalf("Detect() returned error: %v", err)
	}
	// We don't know exactly what processes are running, but it shouldn't crash
	_ = sessions
}

func TestClaudeDetector_DetectFromProcesses_RunsWithoutError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process-scanning test in short mode")
	}
	d := NewClaudeDetector("/nonexistent/project/path")
	sessions, err := d.detectFromProcesses()
	if err != nil {
		t.Fatalf("detectFromProcesses() returned error: %v", err)
	}
	// Should return a (possibly empty) slice without error
	_ = sessions
}

func TestClaudeDetector_DetectFromSessionFiles_NoProjectDir(t *testing.T) {
	// Use a path that won't have a matching .claude/projects/<encoded-path> dir
	d := NewClaudeDetector("/nonexistent/path/for/test")
	sessions, err := d.detectFromSessionFiles()
	if err != nil {
		t.Fatalf("detectFromSessionFiles() returned error: %v", err)
	}
	// Should return nil/empty because there are no matching session files
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions for nonexistent project, got %d", len(sessions))
	}
}

// ─── Registry.Attribute with multiple detectors ─────────────────────────────

func TestRegistry_Attribute_MultipleDetectors_TieBreaking(t *testing.T) {
	// Two detectors return the same confidence — the first one checked wins
	// because > is strict (not >=)
	d1 := &mockDetector{
		name: "first",
		sessions: []*DetectedSession{
			{SessionID: "first-sess", ToolName: "first", StartedAt: time.Now()},
		},
		attributeFn: func(event *FileWriteEvent, active []*DetectedSession) (string, float32, schema.AttributionMethod) {
			return "first-sess", 0.8, schema.AttrHeuristic
		},
	}
	d2 := &mockDetector{
		name: "second",
		sessions: []*DetectedSession{
			{SessionID: "second-sess", ToolName: "second", StartedAt: time.Now()},
		},
		attributeFn: func(event *FileWriteEvent, active []*DetectedSession) (string, float32, schema.AttributionMethod) {
			return "second-sess", 0.8, schema.AttrHeuristic
		},
	}

	r := NewRegistry(d1, d2)
	r.poll()

	event := &FileWriteEvent{FilePath: "file.go", Operation: schema.OpModify, Timestamp: time.Now()}
	sid, conf, _ := r.Attribute(event)

	// Equal confidence: first detector wins (> not >=)
	if sid != "first-sess" {
		t.Errorf("sessionID = %q, want %q (first detector wins tie)", sid, "first-sess")
	}
	if conf != 0.8 {
		t.Errorf("confidence = %v, want 0.8", conf)
	}
}

func TestRegistry_Attribute_NoDetectors(t *testing.T) {
	// Registry with no detectors at all
	r := NewRegistry()

	event := &FileWriteEvent{FilePath: "file.go", Operation: schema.OpModify, Timestamp: time.Now()}
	sid, conf, method := r.Attribute(event)

	if sid != "" {
		t.Errorf("sessionID = %q, want empty", sid)
	}
	if conf != 0 {
		t.Errorf("confidence = %v, want 0", conf)
	}
	if method != schema.AttrNone {
		t.Errorf("method = %v, want AttrNone", method)
	}
}

func TestRegistry_Attribute_DetectorReturnsEmpty(t *testing.T) {
	// Detector returns empty attribution but there are 2 active sessions
	// (no single-session fallback)
	d := &mockDetector{
		name: "no-match",
		sessions: []*DetectedSession{
			{SessionID: "sess-1", ToolName: "test", StartedAt: time.Now()},
			{SessionID: "sess-2", ToolName: "test", StartedAt: time.Now()},
		},
		attributeFn: func(event *FileWriteEvent, active []*DetectedSession) (string, float32, schema.AttributionMethod) {
			return "", 0, schema.AttrNone
		},
	}

	r := NewRegistry(d)
	r.poll()

	event := &FileWriteEvent{FilePath: "file.go", Operation: schema.OpModify, Timestamp: time.Now()}
	sid, conf, method := r.Attribute(event)

	// Two active sessions, no detector match => no fallback
	if sid != "" {
		t.Errorf("sessionID = %q, want empty (two sessions, no fallback)", sid)
	}
	if conf != 0 {
		t.Errorf("confidence = %v, want 0", conf)
	}
	if method != schema.AttrNone {
		t.Errorf("method = %v, want AttrNone", method)
	}
}

// ─── confirmSession side effects ────────────────────────────────────────────

func TestConfirmSession_TriggersOnSessionStart(t *testing.T) {
	d := &mockDetector{
		name: "test",
		sessions: []*DetectedSession{
			{SessionID: "pending-sess", ToolName: "test", StartedAt: time.Now()},
		},
	}

	r := NewRegistry(d)

	var started []*schema.Session
	r.SetOnSessionStart(func(s *schema.Session) {
		started = append(started, s)
	})

	// Discover session (it starts as unconfirmed)
	r.poll()

	if len(started) != 0 {
		t.Fatal("callback should not fire before confirmation")
	}

	// Manually confirm via confirmSession
	r.confirmSession("pending-sess")

	if len(started) != 1 {
		t.Fatalf("expected 1 onSessionStart callback, got %d", len(started))
	}
	if started[0].SessionID != "pending-sess" {
		t.Errorf("started session ID = %q, want %q", started[0].SessionID, "pending-sess")
	}
}

func TestConfirmSession_NoOpIfAlreadyConfirmed(t *testing.T) {
	d := &mockDetector{
		name: "test",
		sessions: []*DetectedSession{
			{SessionID: "already-conf", ToolName: "test", StartedAt: time.Now()},
		},
	}

	r := NewRegistry(d)

	callCount := 0
	r.SetOnSessionStart(func(s *schema.Session) {
		callCount++
	})

	r.poll()
	r.confirmSession("already-conf")
	if callCount != 1 {
		t.Fatalf("first confirm: expected 1 call, got %d", callCount)
	}

	// Confirm again — should be a no-op
	r.confirmSession("already-conf")
	if callCount != 1 {
		t.Errorf("second confirm: expected still 1 call, got %d", callCount)
	}
}

func TestConfirmSession_NonexistentSession(t *testing.T) {
	r := NewRegistry()

	callCount := 0
	r.SetOnSessionStart(func(s *schema.Session) {
		callCount++
	})

	// Should not panic or call callback
	r.confirmSession("nonexistent")
	if callCount != 0 {
		t.Errorf("expected 0 callbacks for nonexistent session, got %d", callCount)
	}
}

func TestConfirmSession_NoCallback(t *testing.T) {
	// Confirm a session when onSessionStart is nil — should not panic
	d := &mockDetector{
		name: "test",
		sessions: []*DetectedSession{
			{SessionID: "no-cb-sess", ToolName: "test", StartedAt: time.Now()},
		},
	}

	r := NewRegistry(d)
	// Do NOT set onSessionStart
	r.poll()

	// Should not panic
	r.confirmSession("no-cb-sess")
}

// ─── Attribution confirms pending sessions ──────────────────────────────────

func TestRegistry_Attribute_ConfirmsPendingSession(t *testing.T) {
	d := &mockDetector{
		name: "test",
		sessions: []*DetectedSession{
			{SessionID: "pending-attr", ToolName: "test", StartedAt: time.Now()},
		},
		attributeFn: func(event *FileWriteEvent, active []*DetectedSession) (string, float32, schema.AttributionMethod) {
			if len(active) > 0 {
				return active[0].SessionID, 0.9, schema.AttrPID
			}
			return "", 0, schema.AttrNone
		},
	}

	r := NewRegistry(d)

	var started []*schema.Session
	r.SetOnSessionStart(func(s *schema.Session) {
		started = append(started, s)
	})

	// Discover session (pending/unconfirmed)
	r.poll()
	if len(started) != 0 {
		t.Fatal("callback should not fire before attribution")
	}

	// Attribute an event — should confirm the session
	event := &FileWriteEvent{FilePath: "file.go", Operation: schema.OpModify, Timestamp: time.Now()}
	sid, _, _ := r.Attribute(event)

	if sid != "pending-attr" {
		t.Errorf("sessionID = %q, want %q", sid, "pending-attr")
	}
	if len(started) != 1 {
		t.Errorf("expected 1 onSessionStart callback after attribution, got %d", len(started))
	}
}

func TestSession_OnlyConfirmedByAttribution(t *testing.T) {
	d := &mockDetector{
		name: "test",
		sessions: []*DetectedSession{
			{SessionID: "no-event-sess", ToolName: "test", StartedAt: time.Now()},
		},
		attributeFn: func(event *FileWriteEvent, active []*DetectedSession) (string, float32, schema.AttributionMethod) {
			if len(active) > 0 {
				return active[0].SessionID, 0.9, schema.AttrPID
			}
			return "", 0, schema.AttrNone
		},
	}

	r := NewRegistry(d)

	var started []*schema.Session
	r.SetOnSessionStart(func(s *schema.Session) {
		started = append(started, s)
	})

	r.poll()

	if len(r.ActiveSessions()) != 1 {
		t.Fatal("session should be active in memory")
	}
	if len(started) != 0 {
		t.Error("session should NOT be confirmed without an attributed event")
	}

	for i := 0; i < 10; i++ {
		r.poll()
	}
	if len(started) != 0 {
		t.Error("session should NEVER be confirmed by polling alone, no matter how many polls")
	}

	event := &FileWriteEvent{FilePath: "file.go", Operation: schema.OpModify, Timestamp: time.Now()}
	r.Attribute(event)

	if len(started) != 1 {
		t.Errorf("expected 1 onSessionStart after attribution, got %d", len(started))
	}
}

// ─── MaxMissCount ───────────────────────────────────────────────────────────

func TestMaxMissCount_ExactThreshold(t *testing.T) {
	d := &mockDetector{
		name: "test",
		sessions: []*DetectedSession{
			{SessionID: "miss-sess", ToolName: "test", PID: 9999, StartedAt: time.Now()},
		},
	}

	r := NewRegistry(d)
	r.poll() // discover
	confirmSession(r, "miss-sess")

	// Remove the session from detection
	d.sessions = nil

	// Poll maxMissCount - 1 times — should still be active
	for i := 0; i < maxMissCount-1; i++ {
		r.poll()
	}

	active := r.ActiveSessions()
	if len(active) != 1 {
		t.Fatalf("session should still be active after %d misses, got %d active", maxMissCount-1, len(active))
	}

	// One more miss should crash it
	r.poll()

	active = r.ActiveSessions()
	if len(active) != 0 {
		t.Errorf("session should be crashed after %d misses, got %d active", maxMissCount, len(active))
	}

	s := r.GetSession("miss-sess")
	if s == nil {
		t.Fatal("session should still exist in registry")
	}
	if s.Status != schema.SessionCrashed {
		t.Errorf("status = %v, want SessionCrashed", s.Status)
	}
}

// ─── GetSession ─────────────────────────────────────────────────────────────

func TestGetSession_Found(t *testing.T) {
	d := &mockDetector{
		name: "test",
		sessions: []*DetectedSession{
			{SessionID: "find-me", ToolName: "test", PID: 42, StartedAt: time.Now()},
		},
	}

	r := NewRegistry(d)
	r.poll()

	s := r.GetSession("find-me")
	if s == nil {
		t.Fatal("GetSession returned nil for existing session")
	}
	if s.SessionID != "find-me" {
		t.Errorf("SessionID = %q, want %q", s.SessionID, "find-me")
	}
	if s.PID != 42 {
		t.Errorf("PID = %d, want 42", s.PID)
	}
	if s.Status != schema.SessionActive {
		t.Errorf("Status = %v, want SessionActive", s.Status)
	}
}

func TestGetSession_NotFoundAfterCrash(t *testing.T) {
	// Unconfirmed sessions are deleted on crash — GetSession should return nil
	d := &mockDetector{
		name: "test",
		sessions: []*DetectedSession{
			{SessionID: "ephemeral", ToolName: "test", StartedAt: time.Now()},
		},
	}

	r := NewRegistry(d)
	r.poll() // discover (unconfirmed)

	// Disappear
	d.sessions = nil
	for i := 0; i < maxMissCount; i++ {
		r.poll()
	}

	s := r.GetSession("ephemeral")
	if s != nil {
		t.Errorf("expected nil for unconfirmed crashed session, got %v", s)
	}
}

// ─── Concurrent Poll and Attribute ──────────────────────────────────────────

func TestConcurrent_PollAndAttribute(t *testing.T) {
	d := &mockDetector{
		name: "test",
		sessions: []*DetectedSession{
			{SessionID: "concurrent-sess", ToolName: "test", StartedAt: time.Now()},
		},
		attributeFn: func(event *FileWriteEvent, active []*DetectedSession) (string, float32, schema.AttributionMethod) {
			if len(active) > 0 {
				return active[0].SessionID, 0.9, schema.AttrPID
			}
			return "", 0, schema.AttrNone
		},
	}

	r := NewRegistry(d)
	r.poll() // seed initial session

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 50

	// Concurrent polls
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				r.poll()
			}
		}()
	}

	// Concurrent attributions
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				event := &FileWriteEvent{FilePath: "file.go", Operation: schema.OpModify, Timestamp: time.Now()}
				r.Attribute(event)
			}
		}()
	}

	// Concurrent reads
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				r.ActiveSessions()
				r.GetSession("concurrent-sess")
				r.DetectorNames()
			}
		}()
	}

	wg.Wait()
	// If we get here without a race condition panic, the test passes
}

// ─── Registry with multiple detectors returning different sessions ──────────

func TestRegistry_MultipleDetectors_DifferentSessions(t *testing.T) {
	d1 := &mockDetector{
		name: "detector-a",
		sessions: []*DetectedSession{
			{SessionID: "sess-a", ToolName: "tool-a", PID: 100, StartedAt: time.Now()},
		},
	}
	d2 := &mockDetector{
		name: "detector-b",
		sessions: []*DetectedSession{
			{SessionID: "sess-b", ToolName: "tool-b", PID: 200, StartedAt: time.Now()},
		},
	}

	r := NewRegistry(d1, d2)
	r.poll()

	active := r.ActiveSessions()
	if len(active) != 2 {
		t.Fatalf("expected 2 active sessions, got %d", len(active))
	}

	// Both sessions should be findable
	sa := r.GetSession("sess-a")
	sb := r.GetSession("sess-b")
	if sa == nil {
		t.Error("GetSession(sess-a) returned nil")
	}
	if sb == nil {
		t.Error("GetSession(sess-b) returned nil")
	}
}

// ─── Poll with detector error ───────────────────────────────────────────────

func TestPoll_DetectorError_SkipsGracefully(t *testing.T) {
	errDetector := &mockDetector{
		name:      "failing",
		sessions:  nil,
		detectErr: fmt.Errorf("detector failed"),
	}
	goodDetector := &mockDetector{
		name: "working",
		sessions: []*DetectedSession{
			{SessionID: "good-sess", ToolName: "working", StartedAt: time.Now()},
		},
	}

	r := NewRegistry(errDetector, goodDetector)
	r.poll()

	// Should still have the session from the working detector
	active := r.ActiveSessions()
	if len(active) != 1 {
		t.Fatalf("expected 1 active session, got %d", len(active))
	}
	if active[0].SessionID != "good-sess" {
		t.Errorf("SessionID = %q, want %q", active[0].SessionID, "good-sess")
	}
}

// ─── Stop ends confirmed sessions, skips unconfirmed ────────────────────────

func TestStop_MixedConfirmedAndUnconfirmed(t *testing.T) {
	d := &mockDetector{
		name: "test",
		sessions: []*DetectedSession{
			{SessionID: "confirmed-sess", ToolName: "test", StartedAt: time.Now()},
			{SessionID: "unconfirmed-sess", ToolName: "test", StartedAt: time.Now()},
		},
	}

	r := NewRegistry(d)

	var ended []string
	r.SetOnSessionEnd(func(s *schema.Session) {
		ended = append(ended, s.SessionID)
	})

	// Discover both sessions
	r.poll()

	// Only confirm one
	confirmSession(r, "confirmed-sess")

	// Start and stop
	r.Start()
	time.Sleep(50 * time.Millisecond)
	r.Stop()

	// Only the confirmed session should be ended
	if len(ended) != 1 {
		t.Fatalf("expected 1 ended session, got %d: %v", len(ended), ended)
	}
	if ended[0] != "confirmed-sess" {
		t.Errorf("ended session = %q, want %q", ended[0], "confirmed-sess")
	}
}

// ─── Poll deduplicates sessions from multiple detectors ─────────────────────

func TestPoll_DeduplicatesSameSessionID(t *testing.T) {
	// Two detectors returning the same session ID — should only have one tracked session
	d1 := &mockDetector{
		name: "detector-1",
		sessions: []*DetectedSession{
			{SessionID: "shared-sess", ToolName: "tool-1", StartedAt: time.Now()},
		},
	}
	d2 := &mockDetector{
		name: "detector-2",
		sessions: []*DetectedSession{
			{SessionID: "shared-sess", ToolName: "tool-2", StartedAt: time.Now()},
		},
	}

	r := NewRegistry(d1, d2)
	r.poll()

	active := r.ActiveSessions()
	if len(active) != 1 {
		t.Errorf("expected 1 active session (deduplicated), got %d", len(active))
	}
}

// ─── parsePSLine — additional edge cases ────────────────────────────────────

func TestParsePSLine_PPIDNotNumber(t *testing.T) {
	_, _, err := parsePSLine("1234 abc /usr/bin/cmd")
	if err == nil {
		t.Error("expected error for non-numeric PPID, got nil")
	}
}

// ─── NewClaudeDetector ──────────────────────────────────────────────────────

func TestNewClaudeDetector_StoresProjectRoot(t *testing.T) {
	d := NewClaudeDetector("/my/project")
	if d.projectRoot != "/my/project" {
		t.Errorf("projectRoot = %q, want %q", d.projectRoot, "/my/project")
	}
}

// ─── DetectorNames ordering ─────────────────────────────────────────────────

func TestDetectorNames_PreservesOrder(t *testing.T) {
	d1 := &mockDetector{name: "alpha"}
	d2 := &mockDetector{name: "beta"}
	d3 := &mockDetector{name: "gamma"}

	r := NewRegistry(d1, d2, d3)
	names := r.DetectorNames()

	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}
	if names[0] != "alpha" || names[1] != "beta" || names[2] != "gamma" {
		t.Errorf("names = %v, want [alpha beta gamma]", names)
	}
}

// ─── SetOnSessionStart / SetOnSessionEnd ────────────────────────────────────

func TestSetOnSessionStart_ReplacesCallback(t *testing.T) {
	d := &mockDetector{
		name: "test",
		sessions: []*DetectedSession{
			{SessionID: "cb-sess", ToolName: "test", StartedAt: time.Now()},
		},
	}

	r := NewRegistry(d)

	callbackACalled := false
	callbackBCalled := false

	r.SetOnSessionStart(func(s *schema.Session) {
		callbackACalled = true
	})
	r.SetOnSessionStart(func(s *schema.Session) {
		callbackBCalled = true
	})

	r.poll()
	confirmSession(r, "cb-sess")

	if callbackACalled {
		t.Error("callback A should NOT have been called (replaced by B)")
	}
	if !callbackBCalled {
		t.Error("callback B should have been called")
	}
}
