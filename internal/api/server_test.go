package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/session"
	"github.com/davidparkercodes/belay/internal/store"
)

// ─── Test Fixture ───────────────────────────────────────────────────────────

type testFixture struct {
	t        *testing.T
	server   *Server
	mux      *http.ServeMux
	handler  http.Handler
	idx      *index.Index
	objStore *store.Store
	registry *session.Registry
	cfg      *config.Config
	tmpDir   string

	recordedEvents []*schema.Event
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()
	tmpDir := t.TempDir()

	// Resolve symlinks so that EvalSymlinks in handleRecord matches.
	// On macOS, /var -> /private/var, which causes prefix checks to fail.
	resolved, err := filepath.EvalSymlinks(tmpDir)
	if err == nil {
		tmpDir = resolved
	}

	dbPath := filepath.Join(tmpDir, "index.db")
	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { idx.Close() })

	objDir := filepath.Join(tmpDir, "objects")
	objStore, err := store.NewStore(objDir, false)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { objStore.Close() })

	reg := session.NewRegistry()

	cfg := config.DefaultConfig(tmpDir)

	f := &testFixture{
		t:        t,
		idx:      idx,
		objStore: objStore,
		registry: reg,
		cfg:      cfg,
		tmpDir:   tmpDir,
	}

	logger := log.New(io.Discard, "", 0)

	srv := New(cfg, idx, objStore, reg, logger, func(event *schema.Event) {
		f.recordedEvents = append(f.recordedEvents, event)
	}, "test", nil)
	srv.startedAt = time.Now()

	f.server = srv

	// Build a mux matching the one in Start() so we can use httptest
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", srv.handleHealth)
	mux.HandleFunc("GET /api/events", srv.handleEvents)
	mux.HandleFunc("GET /api/events/{id}", srv.handleEventByID)
	mux.HandleFunc("GET /api/sessions", srv.handleSessions)
	mux.HandleFunc("GET /api/sessions/{id}", srv.handleSession)
	mux.HandleFunc("GET /api/sessions/{id}/events", srv.handleSessionEvents)
	mux.HandleFunc("GET /api/sessions/{id}/replay", srv.handleSessionReplay)
	mux.HandleFunc("GET /api/files", srv.handleFiles)
	mux.HandleFunc("GET /api/files/history", srv.handleFileHistory)
	mux.HandleFunc("GET /api/files/content", srv.handleFileContent)
	mux.HandleFunc("GET /api/conflicts", srv.handleConflicts)
	mux.HandleFunc("GET /api/stats", srv.handleStats)
	mux.HandleFunc("POST /api/record", srv.handleRecord)
	mux.HandleFunc("GET /api/stream", srv.handleStream)

	rl := newRateLimiter(1000, 1000)
	handler := rateLimitMiddleware(rl, corsMiddleware(mux))

	f.mux = mux
	f.handler = handler

	return f
}

// addEvent inserts an event into the index.
func (f *testFixture) addEvent(eventID, filePath string, op schema.Operation, sessionID string, ts time.Time) {
	f.t.Helper()
	ev := &schema.Event{
		EventID:       eventID,
		TimestampNano: ts.UnixNano(),
		FilePath:      filePath,
		Op:            op,
		ContentHash:   "abc123",
		PreviousHash:  "def456",
		ContentSize:   100,
		SessionID:     sessionID,
		Attribution:   schema.AttrPID,
		AttributionConfidence: 0.95,
	}
	if err := f.idx.IndexEvent(ev, "seg.log", 0); err != nil {
		f.t.Fatalf("IndexEvent: %v", err)
	}
}

// addEventWithHash inserts an event with specific content hashes.
func (f *testFixture) addEventWithHash(eventID, filePath string, op schema.Operation, sessionID, contentHash, prevHash string, ts time.Time) {
	f.t.Helper()
	ev := &schema.Event{
		EventID:       eventID,
		TimestampNano: ts.UnixNano(),
		FilePath:      filePath,
		Op:            op,
		ContentHash:   contentHash,
		PreviousHash:  prevHash,
		ContentSize:   100,
		SessionID:     sessionID,
		Attribution:   schema.AttrPID,
		AttributionConfidence: 0.95,
	}
	if err := f.idx.IndexEvent(ev, "seg.log", 0); err != nil {
		f.t.Fatalf("IndexEvent: %v", err)
	}
}

// addSession inserts a session into the index.
func (f *testFixture) addSession(sessionID, toolName string, status schema.SessionStatus, startedAt time.Time, eventCount int) {
	f.t.Helper()
	s := &schema.Session{
		SessionID:  sessionID,
		ToolName:   toolName,
		Status:     status,
		StartedAt:  startedAt,
		EventCount: eventCount,
	}
	if err := f.idx.UpsertSession(s); err != nil {
		f.t.Fatalf("UpsertSession: %v", err)
	}
}

// putContent stores content in the object store and returns its hash.
func (f *testFixture) putContent(content string) string {
	f.t.Helper()
	hash, _, err := f.objStore.Put([]byte(content))
	if err != nil {
		f.t.Fatalf("put content: %v", err)
	}
	return hash
}

// doRequest performs an HTTP request via the test handler and returns the response.
func (f *testFixture) doRequest(method, path string, body io.Reader) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, body)
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

// parseJSON parses the response body into a map.
func parseJSON(t *testing.T, body *bytes.Buffer) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	if err := json.Unmarshal(body.Bytes(), &result); err != nil {
		t.Fatalf("parse JSON: %v (body: %s)", err, body.String())
	}
	return result
}

// ─── Health Endpoint ────────────────────────────────────────────────────────

func TestHandleHealth(t *testing.T) {
	f := newTestFixture(t)
	rr := f.doRequest("GET", "/api/health", nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("health returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	if result["status"] != "ok" {
		t.Errorf("status = %v, want 'ok'", result["status"])
	}
	if result["version"] != "test" {
		t.Errorf("version = %v, want 'test'", result["version"])
	}
	if _, ok := result["uptime"]; !ok {
		t.Error("missing uptime field")
	}
}

func TestHandleHealth_ContentType(t *testing.T) {
	f := newTestFixture(t)
	rr := f.doRequest("GET", "/api/health", nil)

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want 'application/json'", ct)
	}
}

// ─── Events Endpoint ────────────────────────────────────────────────────────

func TestHandleEvents_Empty(t *testing.T) {
	f := newTestFixture(t)
	rr := f.doRequest("GET", "/api/events", nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("events returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	count := result["count"].(float64)
	if count != 0 {
		t.Errorf("count = %v, want 0", count)
	}
}

func TestHandleEvents_WithEvents(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-1", "src/main.go", schema.OpCreate, "sess-1", now)
	f.addEvent("evt-2", "src/util.go", schema.OpModify, "sess-1", now.Add(time.Second))

	rr := f.doRequest("GET", "/api/events", nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("events returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestHandleEvents_DefaultLimit(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()

	// Default limit is 100, add 3 events
	for i := 0; i < 3; i++ {
		f.addEvent(fmt.Sprintf("evt-%d", i), "file.go", schema.OpModify, "", now.Add(time.Duration(i)*time.Second))
	}

	rr := f.doRequest("GET", "/api/events", nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestHandleEvents_WithLimit(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()

	for i := 0; i < 5; i++ {
		f.addEvent(fmt.Sprintf("evt-%d", i), "file.go", schema.OpModify, "", now.Add(time.Duration(i)*time.Second))
	}

	rr := f.doRequest("GET", "/api/events?limit=2", nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestHandleEvents_WithSinceDuration(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-old", "file.go", schema.OpModify, "", now.Add(-2*time.Hour))
	f.addEvent("evt-recent", "file.go", schema.OpModify, "", now.Add(-30*time.Minute))

	rr := f.doRequest("GET", "/api/events?since=1h", nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1 (only recent event within 1h)", count)
	}
}

func TestHandleEvents_WithFileFilter(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-1", "src/main.go", schema.OpModify, "", now)
	f.addEvent("evt-2", "src/util.go", schema.OpModify, "", now.Add(time.Second))

	rr := f.doRequest("GET", "/api/events?file=src/main.go", nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestHandleEvents_WithSessionFilter(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-1", "file.go", schema.OpModify, "sess-alpha", now)
	f.addEvent("evt-2", "file.go", schema.OpModify, "sess-beta", now.Add(time.Second))

	rr := f.doRequest("GET", "/api/events?session=sess-alpha", nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestHandleEvents_OrderAsc(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-a", "file.go", schema.OpModify, "", now)
	f.addEvent("evt-b", "file.go", schema.OpModify, "", now.Add(time.Second))

	rr := f.doRequest("GET", "/api/events?order=asc", nil)
	result := parseJSON(t, rr.Body)
	events := result["events"].([]interface{})
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	// Ascending: first event should be evt-a (earlier)
	first := events[0].(map[string]interface{})
	if first["event_id"] != "evt-a" {
		t.Errorf("first event in asc order = %v, want evt-a", first["event_id"])
	}
}

func TestHandleEvents_OrderDesc(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-a", "file.go", schema.OpModify, "", now)
	f.addEvent("evt-b", "file.go", schema.OpModify, "", now.Add(time.Second))

	// Default order is desc
	rr := f.doRequest("GET", "/api/events", nil)
	result := parseJSON(t, rr.Body)
	events := result["events"].([]interface{})
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	first := events[0].(map[string]interface{})
	if first["event_id"] != "evt-b" {
		t.Errorf("first event in desc order = %v, want evt-b", first["event_id"])
	}
}

func TestHandleEvents_InvalidLimit(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-1", "file.go", schema.OpModify, "", now)

	// Invalid limit should be ignored, default of 100 used
	rr := f.doRequest("GET", "/api/events?limit=notanumber", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("events returned %d, want %d", rr.Code, http.StatusOK)
	}
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestHandleEvents_InvalidSince(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-1", "file.go", schema.OpModify, "", now)

	// Invalid duration should be ignored
	rr := f.doRequest("GET", "/api/events?since=notaduration", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("events returned %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestHandleEvents_WithUntilRFC3339(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	past := now.Add(-1 * time.Hour)
	future := now.Add(1 * time.Hour)
	f.addEvent("evt-1", "file.go", schema.OpModify, "", past)
	f.addEvent("evt-2", "file.go", schema.OpModify, "", future)

	until := now.Format(time.RFC3339)
	rr := f.doRequest("GET", "/api/events?until="+until, nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1 (only event before until)", count)
	}
}

// ─── Event By ID Endpoint ───────────────────────────────────────────────────

func TestHandleEventByID_Found(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-find-me", "src/main.go", schema.OpCreate, "sess-1", now)

	rr := f.doRequest("GET", "/api/events/evt-find-me", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("event by ID returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	if result["event_id"] != "evt-find-me" {
		t.Errorf("event_id = %v, want 'evt-find-me'", result["event_id"])
	}
}

func TestHandleEventByID_NotFound(t *testing.T) {
	f := newTestFixture(t)
	rr := f.doRequest("GET", "/api/events/nonexistent", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("event by ID returned %d, want %d", rr.Code, http.StatusNotFound)
	}

	result := parseJSON(t, rr.Body)
	if _, ok := result["error"]; !ok {
		t.Error("expected error field in response")
	}
}

// ─── Sessions Endpoint ──────────────────────────────────────────────────────

func TestHandleSessions_Empty(t *testing.T) {
	f := newTestFixture(t)
	rr := f.doRequest("GET", "/api/sessions", nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("sessions returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestHandleSessions_WithSessions(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addSession("sess-1", "claude-code", schema.SessionActive, now, 5)
	f.addSession("sess-2", "cursor", schema.SessionEnded, now.Add(-time.Hour), 10)

	rr := f.doRequest("GET", "/api/sessions", nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestHandleSessions_ActiveFilter(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addSession("sess-active", "claude-code", schema.SessionActive, now, 5)
	f.addSession("sess-ended", "cursor", schema.SessionEnded, now.Add(-time.Hour), 10)

	rr := f.doRequest("GET", "/api/sessions?active=true", nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1 (active only)", count)
	}
}

func TestHandleSessions_HideEmpty(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addSession("sess-with", "claude", schema.SessionActive, now, 5)
	f.addSession("sess-empty", "claude", schema.SessionActive, now.Add(-time.Hour), 0)

	rr := f.doRequest("GET", "/api/sessions?hide_empty=true", nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1 (hide empty)", count)
	}
}

func TestHandleSessions_MinEvents(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addSession("sess-few", "claude", schema.SessionActive, now, 2)
	f.addSession("sess-many", "claude", schema.SessionActive, now.Add(-time.Hour), 10)

	rr := f.doRequest("GET", "/api/sessions?min_events=5", nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1 (min_events=5)", count)
	}
}

func TestHandleSessions_MinEventsOverridesHideEmpty(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addSession("sess-1", "claude", schema.SessionActive, now, 3)
	f.addSession("sess-2", "claude", schema.SessionActive, now.Add(-time.Hour), 10)

	// hide_empty=true sets min to 1, but min_events=5 should override
	rr := f.doRequest("GET", "/api/sessions?hide_empty=true&min_events=5", nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1 (min_events=5 overrides hide_empty)", count)
	}
}

func TestHandleSessions_WithLimit(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	for i := 0; i < 5; i++ {
		f.addSession(fmt.Sprintf("sess-%d", i), "claude", schema.SessionActive, now.Add(time.Duration(i)*time.Hour), 1)
	}

	rr := f.doRequest("GET", "/api/sessions?limit=2", nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestHandleSessions_ReturnsSessionJSON(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addSession("sess-json", "claude-code", schema.SessionActive, now, 5)

	rr := f.doRequest("GET", "/api/sessions", nil)
	result := parseJSON(t, rr.Body)
	sessions := result["sessions"].([]interface{})
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	sess := sessions[0].(map[string]interface{})
	if sess["session_id"] != "sess-json" {
		t.Errorf("session_id = %v, want 'sess-json'", sess["session_id"])
	}
	if sess["tool_name"] != "claude-code" {
		t.Errorf("tool_name = %v, want 'claude-code'", sess["tool_name"])
	}
	if sess["status"] != "active" {
		t.Errorf("status = %v, want 'active'", sess["status"])
	}
}

// ─── Session By ID Endpoint ─────────────────────────────────────────────────

func TestHandleSession_Found(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addSession("sess-detail", "claude-code", schema.SessionActive, now, 5)
	// Add some events for this session
	f.addEvent("evt-sd-1", "src/main.go", schema.OpCreate, "sess-detail", now)
	f.addEvent("evt-sd-2", "src/util.go", schema.OpModify, "sess-detail", now.Add(time.Second))

	rr := f.doRequest("GET", "/api/sessions/sess-detail", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("session by ID returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	if result["session_id"] != "sess-detail" {
		t.Errorf("session_id = %v, want 'sess-detail'", result["session_id"])
	}
	eventCount := int(result["event_count"].(float64))
	if eventCount != 2 {
		t.Errorf("event_count = %d, want 2", eventCount)
	}
	filesChanged := int(result["files_changed"].(float64))
	if filesChanged != 2 {
		t.Errorf("files_changed = %d, want 2", filesChanged)
	}
}

func TestHandleSession_NotFound(t *testing.T) {
	f := newTestFixture(t)
	rr := f.doRequest("GET", "/api/sessions/nonexistent", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("session by ID returned %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestHandleSession_FallbackToEvents(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	// Don't create a session in the index, but add events with a session ID
	ev := &schema.Event{
		EventID:       "evt-fb-1",
		TimestampNano: now.UnixNano(),
		FilePath:      "src/main.go",
		Op:            schema.OpModify,
		ContentHash:   "abc123",
		ContentSize:   100,
		SessionID:     "orphan-sess",
		Attribution:   schema.AttrPID,
		Metadata:      map[string]string{"tool_name": "claude-code"},
	}
	if err := f.idx.IndexEvent(ev, "seg.log", 0); err != nil {
		t.Fatalf("IndexEvent: %v", err)
	}

	rr := f.doRequest("GET", "/api/sessions/orphan-sess", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("session fallback returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	if result["session_id"] != "orphan-sess" {
		t.Errorf("session_id = %v, want 'orphan-sess'", result["session_id"])
	}
	if result["tool_name"] != "claude-code" {
		t.Errorf("tool_name = %v, want 'claude-code'", result["tool_name"])
	}
	if result["status"] != "ended" {
		t.Errorf("status = %v, want 'ended'", result["status"])
	}
}

// ─── Session Events Endpoint ────────────────────────────────────────────────

func TestHandleSessionEvents(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-se-1", "src/a.go", schema.OpCreate, "sess-events-1", now)
	f.addEvent("evt-se-2", "src/b.go", schema.OpModify, "sess-events-1", now.Add(time.Second))
	f.addEvent("evt-se-3", "src/c.go", schema.OpModify, "sess-events-2", now.Add(2*time.Second))

	rr := f.doRequest("GET", "/api/sessions/sess-events-1/events", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("session events returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if result["session_id"] != "sess-events-1" {
		t.Errorf("session_id = %v, want 'sess-events-1'", result["session_id"])
	}
}

func TestHandleSessionEvents_WithLimit(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	for i := 0; i < 5; i++ {
		f.addEvent(fmt.Sprintf("evt-sel-%d", i), "file.go", schema.OpModify, "sess-limit", now.Add(time.Duration(i)*time.Second))
	}

	rr := f.doRequest("GET", "/api/sessions/sess-limit/events?limit=2", nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestHandleSessionEvents_Empty(t *testing.T) {
	f := newTestFixture(t)
	rr := f.doRequest("GET", "/api/sessions/nonexistent/events", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("session events returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

// ─── Session Replay Endpoint ────────────────────────────────────────────────

func TestHandleSessionReplay_Success(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	hash := f.putContent("file content")
	f.addEventWithHash("evt-replay-1", "src/main.go", schema.OpCreate, "sess-replay", hash, "", now)

	rr := f.doRequest("GET", "/api/sessions/sess-replay/replay", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("session replay returned %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestHandleSessionReplay_NoEvents(t *testing.T) {
	f := newTestFixture(t)
	rr := f.doRequest("GET", "/api/sessions/nonexistent/replay", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("session replay returned %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

// ─── Files Endpoint ─────────────────────────────────────────────────────────

func TestHandleFiles(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-f-1", "src/main.go", schema.OpCreate, "sess-1", now)
	f.addEvent("evt-f-2", "src/main.go", schema.OpModify, "sess-1", now.Add(time.Second))
	f.addEvent("evt-f-3", "src/util.go", schema.OpCreate, "sess-1", now.Add(2*time.Second))

	rr := f.doRequest("GET", "/api/files", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("files returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 2 {
		t.Errorf("count = %d, want 2 (unique files)", count)
	}
}

func TestHandleFiles_WithSince(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-old", "old.go", schema.OpModify, "", now.Add(-48*time.Hour))
	f.addEvent("evt-new", "new.go", schema.OpModify, "", now.Add(-30*time.Minute))

	rr := f.doRequest("GET", "/api/files?since=1h", nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1 (only recent file)", count)
	}
}

// ─── File History Endpoint ──────────────────────────────────────────────────

func TestHandleFileHistory(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-fh-1", "src/main.go", schema.OpCreate, "", now)
	f.addEvent("evt-fh-2", "src/main.go", schema.OpModify, "", now.Add(time.Second))
	f.addEvent("evt-fh-3", "src/util.go", schema.OpCreate, "", now.Add(2*time.Second))

	rr := f.doRequest("GET", "/api/files/history?path=src/main.go", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("file history returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	if result["path"] != "src/main.go" {
		t.Errorf("path = %v, want 'src/main.go'", result["path"])
	}
	count := int(result["count"].(float64))
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestHandleFileHistory_MissingPath(t *testing.T) {
	f := newTestFixture(t)
	rr := f.doRequest("GET", "/api/files/history", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("file history returned %d, want %d", rr.Code, http.StatusBadRequest)
	}

	result := parseJSON(t, rr.Body)
	if _, ok := result["error"]; !ok {
		t.Error("expected error field in response")
	}
}

func TestHandleFileHistory_WithLimit(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	for i := 0; i < 10; i++ {
		f.addEvent(fmt.Sprintf("evt-fhl-%d", i), "file.go", schema.OpModify, "", now.Add(time.Duration(i)*time.Second))
	}

	rr := f.doRequest("GET", "/api/files/history?path=file.go&limit=3", nil)
	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

// ─── File Content Endpoint ──────────────────────────────────────────────────

func TestHandleFileContent_Found(t *testing.T) {
	f := newTestFixture(t)
	hash := f.putContent("hello world")

	rr := f.doRequest("GET", "/api/files/content?hash="+hash, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("file content returned %d, want %d", rr.Code, http.StatusOK)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want 'text/plain; charset=utf-8'", ct)
	}

	if rr.Body.String() != "hello world" {
		t.Errorf("body = %q, want 'hello world'", rr.Body.String())
	}
}

func TestHandleFileContent_MissingHash(t *testing.T) {
	f := newTestFixture(t)
	rr := f.doRequest("GET", "/api/files/content", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("file content returned %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleFileContent_NotFound(t *testing.T) {
	f := newTestFixture(t)
	rr := f.doRequest("GET", "/api/files/content?hash=0000000000000000000000000000000000000000000000000000000000000000", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("file content returned %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestHandleFileContent_NoSniff(t *testing.T) {
	f := newTestFixture(t)
	hash := f.putContent("some content")

	rr := f.doRequest("GET", "/api/files/content?hash="+hash, nil)
	nosniff := rr.Header().Get("X-Content-Type-Options")
	if nosniff != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want 'nosniff'", nosniff)
	}
}

// ─── Conflicts Endpoint ─────────────────────────────────────────────────────

func TestHandleConflicts_NoConflicts(t *testing.T) {
	f := newTestFixture(t)
	rr := f.doRequest("GET", "/api/conflicts", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("conflicts returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestHandleConflicts_WithConflicts(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	// Two sessions editing the same file within the conflict window
	f.addEvent("evt-cf-1", "contested.go", schema.OpModify, "sess-A", now)
	f.addEvent("evt-cf-2", "contested.go", schema.OpModify, "sess-B", now.Add(5*time.Second))

	rr := f.doRequest("GET", "/api/conflicts", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("conflicts returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestHandleConflicts_WithFileFilter(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-cff-1", "contested.go", schema.OpModify, "sess-A", now)
	f.addEvent("evt-cff-2", "contested.go", schema.OpModify, "sess-B", now.Add(5*time.Second))
	f.addEvent("evt-cff-3", "other.go", schema.OpModify, "sess-C", now)
	f.addEvent("evt-cff-4", "other.go", schema.OpModify, "sess-D", now.Add(5*time.Second))

	rr := f.doRequest("GET", "/api/conflicts?file=contested.go", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("conflicts returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1 (only contested.go)", count)
	}
}

func TestHandleConflicts_WithSince(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-cs-1", "file.go", schema.OpModify, "sess-A", now.Add(-30*time.Minute))
	f.addEvent("evt-cs-2", "file.go", schema.OpModify, "sess-B", now.Add(-30*time.Minute+5*time.Second))

	rr := f.doRequest("GET", "/api/conflicts?since=1h", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("conflicts returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	count := int(result["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

// ─── Stats Endpoint ─────────────────────────────────────────────────────────

func TestHandleStats_Empty(t *testing.T) {
	f := newTestFixture(t)
	rr := f.doRequest("GET", "/api/stats", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("stats returned %d, want %d", rr.Code, http.StatusOK)
	}

	result := parseJSON(t, rr.Body)
	if int(result["total_events"].(float64)) != 0 {
		t.Errorf("total_events = %v, want 0", result["total_events"])
	}
	if int(result["total_sessions"].(float64)) != 0 {
		t.Errorf("total_sessions = %v, want 0", result["total_sessions"])
	}
}

func TestHandleStats_WithData(t *testing.T) {
	f := newTestFixture(t)
	now := time.Now()
	f.addEvent("evt-s-1", "file.go", schema.OpCreate, "sess-1", now)
	f.addEvent("evt-s-2", "file.go", schema.OpModify, "sess-1", now.Add(time.Second))
	f.addSession("sess-1", "claude", schema.SessionActive, now, 2)
	f.addSession("sess-2", "cursor", schema.SessionEnded, now.Add(-time.Hour), 0)

	// Add something to the object store
	f.putContent("some content")

	rr := f.doRequest("GET", "/api/stats", nil)
	result := parseJSON(t, rr.Body)

	totalEvents := int(result["total_events"].(float64))
	if totalEvents != 2 {
		t.Errorf("total_events = %d, want 2", totalEvents)
	}
	totalSessions := int(result["total_sessions"].(float64))
	if totalSessions != 2 {
		t.Errorf("total_sessions = %d, want 2", totalSessions)
	}
	activeSessions := int(result["active_sessions"].(float64))
	if activeSessions != 1 {
		t.Errorf("active_sessions = %d, want 1", activeSessions)
	}
	storeObjects := int(result["store_objects"].(float64))
	if storeObjects < 1 {
		t.Errorf("store_objects = %d, want >= 1", storeObjects)
	}
}

// ─── Record Endpoint ────────────────────────────────────────────────────────

func TestHandleRecord_Success(t *testing.T) {
	f := newTestFixture(t)

	// Create a test file
	filePath := "testfile.txt"
	absPath := filepath.Join(f.tmpDir, filePath)
	if err := os.WriteFile(absPath, []byte("test content"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	body := `{"file_path":"testfile.txt","operation":"modify","session_id":"sess-record","tool_name":"claude"}`
	rr := f.doRequest("POST", "/api/record", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("record returned %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	result := parseJSON(t, rr.Body)
	if result["status"] != "recorded" {
		t.Errorf("status = %v, want 'recorded'", result["status"])
	}
	if _, ok := result["event_id"]; !ok {
		t.Error("missing event_id in response")
	}

	// Verify onRecord was called
	if len(f.recordedEvents) != 1 {
		t.Fatalf("expected 1 recorded event, got %d", len(f.recordedEvents))
	}
	ev := f.recordedEvents[0]
	if ev.FilePath != filePath {
		t.Errorf("recorded event FilePath = %q, want %q", ev.FilePath, filePath)
	}
	if ev.SessionID != "sess-record" {
		t.Errorf("recorded event SessionID = %q, want 'sess-record'", ev.SessionID)
	}
	if ev.Metadata["tool_name"] != "claude" {
		t.Errorf("recorded event tool_name = %q, want 'claude'", ev.Metadata["tool_name"])
	}
	if ev.Metadata["source"] != "hook" {
		t.Errorf("recorded event source = %q, want 'hook'", ev.Metadata["source"])
	}
}

func TestHandleRecord_DefaultOperation(t *testing.T) {
	f := newTestFixture(t)

	filePath := "testfile.txt"
	absPath := filepath.Join(f.tmpDir, filePath)
	if err := os.WriteFile(absPath, []byte("content"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	// No operation specified, should default to modify
	body := `{"file_path":"testfile.txt"}`
	rr := f.doRequest("POST", "/api/record", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("record returned %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	if len(f.recordedEvents) != 1 {
		t.Fatalf("expected 1 recorded event, got %d", len(f.recordedEvents))
	}
	if f.recordedEvents[0].Op != schema.OpModify {
		t.Errorf("op = %v, want MODIFY", f.recordedEvents[0].Op)
	}
}

func TestHandleRecord_CreateOperation(t *testing.T) {
	f := newTestFixture(t)

	filePath := "newfile.txt"
	absPath := filepath.Join(f.tmpDir, filePath)
	if err := os.WriteFile(absPath, []byte("new content"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	body := `{"file_path":"newfile.txt","operation":"create"}`
	rr := f.doRequest("POST", "/api/record", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("record returned %d, want %d", rr.Code, http.StatusOK)
	}

	if f.recordedEvents[0].Op != schema.OpCreate {
		t.Errorf("op = %v, want CREATE", f.recordedEvents[0].Op)
	}
}

func TestHandleRecord_DeleteOperation(t *testing.T) {
	f := newTestFixture(t)

	// Delete op should not try to read the file
	body := `{"file_path":"deleted.txt","operation":"delete"}`
	rr := f.doRequest("POST", "/api/record", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("record returned %d, want %d (body: %s)", rr.Code, http.StatusOK, rr.Body.String())
	}

	if f.recordedEvents[0].Op != schema.OpDelete {
		t.Errorf("op = %v, want DELETE", f.recordedEvents[0].Op)
	}
	if f.recordedEvents[0].ContentHash != "" {
		t.Errorf("delete event should have empty content hash, got %q", f.recordedEvents[0].ContentHash)
	}
}

func TestHandleRecord_MissingFilePath(t *testing.T) {
	f := newTestFixture(t)

	body := `{"operation":"modify"}`
	rr := f.doRequest("POST", "/api/record", strings.NewReader(body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("record returned %d, want %d", rr.Code, http.StatusBadRequest)
	}

	result := parseJSON(t, rr.Body)
	if !strings.Contains(result["error"].(string), "file_path is required") {
		t.Errorf("error = %v, want file_path is required", result["error"])
	}
}

func TestHandleRecord_InvalidJSON(t *testing.T) {
	f := newTestFixture(t)

	body := `{invalid json}`
	rr := f.doRequest("POST", "/api/record", strings.NewReader(body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("record returned %d, want %d", rr.Code, http.StatusBadRequest)
	}

	result := parseJSON(t, rr.Body)
	errStr := result["error"].(string)
	if !strings.Contains(errStr, "invalid JSON") {
		t.Errorf("error = %v, expected 'invalid JSON'", errStr)
	}
}

func TestHandleRecord_InvalidOperation(t *testing.T) {
	f := newTestFixture(t)

	body := `{"file_path":"test.txt","operation":"explode"}`
	rr := f.doRequest("POST", "/api/record", strings.NewReader(body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("record returned %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleRecord_FileNotFound(t *testing.T) {
	f := newTestFixture(t)

	// File doesn't exist; non-delete op should fail
	body := `{"file_path":"nonexistent.txt","operation":"modify"}`
	rr := f.doRequest("POST", "/api/record", strings.NewReader(body))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("record returned %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestHandleRecord_PathEscapeAttempt(t *testing.T) {
	f := newTestFixture(t)

	body := `{"file_path":"../../etc/passwd","operation":"modify"}`
	rr := f.doRequest("POST", "/api/record", strings.NewReader(body))
	if rr.Code != http.StatusBadRequest {
		// Could also be 404 if the resolved path doesn't exist, but path traversal should be caught first
		if rr.Code != http.StatusNotFound {
			t.Fatalf("record returned %d, expected 400 or 404 for path escape", rr.Code)
		}
	}
}

func TestHandleRecord_NoRecordHandler(t *testing.T) {
	f := newTestFixture(t)
	// Set onRecord to nil
	f.server.onRecord = nil

	body := `{"file_path":"test.txt"}`
	req := httptest.NewRequest("POST", "/api/record", strings.NewReader(body))
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("record returned %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

// ─── Stream (SSE) Endpoint ──────────────────────────────────────────────────

func TestHandleStream_Connect(t *testing.T) {
	f := newTestFixture(t)

	// Use a context with cancel to disconnect the SSE stream
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/stream", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		f.handler.ServeHTTP(rr, req)
	}()

	// Give the handler a moment to start
	time.Sleep(50 * time.Millisecond)

	// Cancel to disconnect
	cancel()
	<-done

	body := rr.Body.String()
	if !strings.Contains(body, `"type":"connected"`) {
		t.Errorf("stream should send connected message, got: %s", body)
	}
}

func TestHandleStream_ReceiveBroadcast(t *testing.T) {
	f := newTestFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/stream", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		f.handler.ServeHTTP(rr, req)
	}()

	// Wait for the handler to register the subscriber
	time.Sleep(50 * time.Millisecond)

	// Broadcast a message
	f.server.Broadcast(&EventMessage{
		Type:      "file_change",
		Timestamp: time.Now().Unix(),
		Data:      map[string]string{"file": "test.go"},
	})

	// Give it a moment
	time.Sleep(50 * time.Millisecond)

	cancel()
	<-done

	body := rr.Body.String()
	if !strings.Contains(body, "file_change") {
		t.Errorf("stream should contain broadcast message, got: %s", body)
	}
}

func TestHandleStream_ContentType(t *testing.T) {
	f := newTestFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/stream", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		f.handler.ServeHTTP(rr, req)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	ct := rr.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want 'text/event-stream'", ct)
	}
}

// ─── Broadcast ──────────────────────────────────────────────────────────────

func TestBroadcast_NoSubscribers(t *testing.T) {
	f := newTestFixture(t)
	// Should not panic with no subscribers
	f.server.Broadcast(&EventMessage{
		Type:      "test",
		Timestamp: time.Now().Unix(),
	})
}

func TestBroadcast_DropsWhenFull(t *testing.T) {
	f := newTestFixture(t)

	// Manually add a subscriber with a tiny buffer
	ch := make(chan *EventMessage, 1)
	f.server.subscribersMu.Lock()
	f.server.subscribers["test-sub"] = ch
	f.server.subscribersMu.Unlock()

	// Fill the buffer
	f.server.Broadcast(&EventMessage{Type: "first"})
	// This should be silently dropped (buffer full, non-blocking send)
	f.server.Broadcast(&EventMessage{Type: "second"})

	// Verify only the first message is in the channel
	msg := <-ch
	if msg.Type != "first" {
		t.Errorf("expected 'first', got %q", msg.Type)
	}
	select {
	case extra := <-ch:
		t.Errorf("expected no more messages, got %q", extra.Type)
	default:
		// Good, buffer was full and second message was dropped
	}

	// Clean up
	f.server.subscribersMu.Lock()
	delete(f.server.subscribers, "test-sub")
	f.server.subscribersMu.Unlock()
}

// ─── CORS Middleware ────────────────────────────────────────────────────────

func TestCORS_LocalhostOrigin(t *testing.T) {
	f := newTestFixture(t)

	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Header.Set("Origin", "http://localhost:33411")
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)

	acao := rr.Header().Get("Access-Control-Allow-Origin")
	if acao != "http://localhost:33411" {
		t.Errorf("Access-Control-Allow-Origin = %q, want 'http://localhost:33411'", acao)
	}
}

func TestCORS_127Origin(t *testing.T) {
	f := newTestFixture(t)

	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Header.Set("Origin", "http://127.0.0.1:3000")
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)

	acao := rr.Header().Get("Access-Control-Allow-Origin")
	if acao != "http://127.0.0.1:3000" {
		t.Errorf("Access-Control-Allow-Origin = %q, want 'http://127.0.0.1:3000'", acao)
	}
}

func TestCORS_HttpsLocalhost(t *testing.T) {
	f := newTestFixture(t)

	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Header.Set("Origin", "https://localhost:8443")
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)

	acao := rr.Header().Get("Access-Control-Allow-Origin")
	if acao != "https://localhost:8443" {
		t.Errorf("Access-Control-Allow-Origin = %q, want 'https://localhost:8443'", acao)
	}
}

func TestCORS_NonLocalOrigin_NoHeader(t *testing.T) {
	f := newTestFixture(t)

	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Header.Set("Origin", "https://evil.com")
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)

	acao := rr.Header().Get("Access-Control-Allow-Origin")
	if acao != "" {
		t.Errorf("Access-Control-Allow-Origin should be empty for non-local origin, got %q", acao)
	}
}

func TestCORS_NoOrigin_NoHeader(t *testing.T) {
	f := newTestFixture(t)

	req := httptest.NewRequest("GET", "/api/health", nil)
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)

	acao := rr.Header().Get("Access-Control-Allow-Origin")
	if acao != "" {
		t.Errorf("Access-Control-Allow-Origin should be empty when no origin, got %q", acao)
	}
}

func TestCORS_Preflight(t *testing.T) {
	f := newTestFixture(t)

	req := httptest.NewRequest("OPTIONS", "/api/events", nil)
	req.Header.Set("Origin", "http://localhost:33411")
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("OPTIONS returned %d, want %d", rr.Code, http.StatusNoContent)
	}

	acao := rr.Header().Get("Access-Control-Allow-Origin")
	if acao != "http://localhost:33411" {
		t.Errorf("Access-Control-Allow-Origin = %q, want 'http://localhost:33411'", acao)
	}

	acam := rr.Header().Get("Access-Control-Allow-Methods")
	if !strings.Contains(acam, "GET") || !strings.Contains(acam, "POST") {
		t.Errorf("Access-Control-Allow-Methods = %q, expected GET and POST", acam)
	}
}

func TestCORS_LocalhostNoPort(t *testing.T) {
	f := newTestFixture(t)

	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Header.Set("Origin", "http://localhost")
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)

	acao := rr.Header().Get("Access-Control-Allow-Origin")
	if acao != "http://localhost" {
		t.Errorf("Access-Control-Allow-Origin = %q, want 'http://localhost'", acao)
	}
}

// ─── Rate Limiter ───────────────────────────────────────────────────────────

func TestRateLimiter_Allow(t *testing.T) {
	rl := newRateLimiter(10, 10)

	// First 10 requests should be allowed (burst)
	for i := 0; i < 10; i++ {
		if !rl.allow("127.0.0.1") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	// 11th should be denied (burst exhausted, not enough time elapsed)
	if rl.allow("127.0.0.1") {
		t.Error("request 11 should be denied (burst exhausted)")
	}
}

func TestRateLimiter_DifferentIPs(t *testing.T) {
	rl := newRateLimiter(1, 1)

	if !rl.allow("10.0.0.1") {
		t.Error("first request from 10.0.0.1 should be allowed")
	}
	if !rl.allow("10.0.0.2") {
		t.Error("first request from 10.0.0.2 should be allowed")
	}

	// Both should now be rate limited
	if rl.allow("10.0.0.1") {
		t.Error("second request from 10.0.0.1 should be denied")
	}
	if rl.allow("10.0.0.2") {
		t.Error("second request from 10.0.0.2 should be denied")
	}
}

func TestRateLimiter_TokenRefill(t *testing.T) {
	rl := newRateLimiter(1000, 1)

	if !rl.allow("10.0.0.1") {
		t.Error("first request should be allowed")
	}
	if rl.allow("10.0.0.1") {
		t.Error("second immediate request should be denied")
	}

	// Wait for tokens to refill (1000 per second = 1 per millisecond)
	time.Sleep(5 * time.Millisecond)

	if !rl.allow("10.0.0.1") {
		t.Error("request after refill should be allowed")
	}
}

func TestRateLimiter_BurstCap(t *testing.T) {
	rl := newRateLimiter(100, 5)

	// Exhaust burst of 5
	for i := 0; i < 5; i++ {
		if !rl.allow("10.0.0.1") {
			t.Fatalf("request %d should be allowed (within burst)", i+1)
		}
	}

	if rl.allow("10.0.0.1") {
		t.Error("request after burst should be denied")
	}
}

// ─── Rate Limit Middleware ──────────────────────────────────────────────────

func TestRateLimitMiddleware_Normal(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rl := newRateLimiter(100, 100)
	handler := rateLimitMiddleware(rl, inner)

	req := httptest.NewRequest("GET", "/api/health", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestRateLimitMiddleware_Exceeded(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rl := newRateLimiter(1, 1)
	handler := rateLimitMiddleware(rl, inner)

	// First request consumes the one token
	req := httptest.NewRequest("GET", "/api/health", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got %d", rr.Code)
	}

	// Second request should be rate limited
	req2 := httptest.NewRequest("GET", "/api/health", nil)
	req2.RemoteAddr = "192.168.1.1:12346"
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("second request should be rate limited, got %d", rr2.Code)
	}

	retryAfter := rr2.Header().Get("Retry-After")
	if retryAfter != "1" {
		t.Errorf("Retry-After = %q, want '1'", retryAfter)
	}
}

func TestRateLimitMiddleware_SkipsStream(t *testing.T) {
	callCount := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
	})

	// Rate limit of 0 would deny everything
	rl := newRateLimiter(0, 0)
	handler := rateLimitMiddleware(rl, inner)

	req := httptest.NewRequest("GET", "/api/stream", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if callCount != 1 {
		t.Errorf("expected inner handler to be called for /api/stream, callCount = %d", callCount)
	}
}

func TestRateLimitMiddleware_StripsPort(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rl := newRateLimiter(1, 1)
	handler := rateLimitMiddleware(rl, inner)

	// Two requests from same IP but different ports should share the same bucket
	req1 := httptest.NewRequest("GET", "/api/health", nil)
	req1.RemoteAddr = "10.0.0.5:11111"
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got %d", rr1.Code)
	}

	req2 := httptest.NewRequest("GET", "/api/health", nil)
	req2.RemoteAddr = "10.0.0.5:22222"
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("second request from same IP (different port) should be rate limited, got %d", rr2.Code)
	}
}

// ─── writeJSON / writeError Helpers ─────────────────────────────────────────

func TestWriteJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, map[string]string{"key": "value"})

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if rr.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want 'application/json'", rr.Header().Get("Content-Type"))
	}

	var result map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["key"] != "value" {
		t.Errorf("key = %q, want 'value'", result["key"])
	}
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		code    int
		message string
	}{
		{http.StatusBadRequest, "bad request"},
		{http.StatusNotFound, "not found"},
		{http.StatusInternalServerError, "internal error"},
		{http.StatusServiceUnavailable, "service unavailable"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.code), func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeError(rr, tt.code, tt.message)

			if rr.Code != tt.code {
				t.Errorf("status = %d, want %d", rr.Code, tt.code)
			}
			if rr.Header().Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type = %q, want 'application/json'", rr.Header().Get("Content-Type"))
			}

			var result map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if result["error"] != tt.message {
				t.Errorf("error = %q, want %q", result["error"], tt.message)
			}
		})
	}
}

// ─── Server Start / Stop ────────────────────────────────────────────────────

func TestServer_StartAndStop(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "index.db")
	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer idx.Close()

	objDir := filepath.Join(tmpDir, "objects")
	objStore, err := store.NewStore(objDir, false)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer objStore.Close()

	reg := session.NewRegistry()
	cfg := config.DefaultConfig(tmpDir)
	cfg.API.Port = 0 // Will use a random available port

	logger := log.New(io.Discard, "", 0)
	srv := New(cfg, idx, objStore, reg, logger, nil, "test", nil)

	// Port 0 defaults to 33412 in the code, which might be in use.
	// Use a random high port instead.
	cfg.API.Port = 44999

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give it a moment
	time.Sleep(50 * time.Millisecond)

	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestServer_Stop_NilServer(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.DefaultConfig(tmpDir)
	logger := log.New(io.Discard, "", 0)
	srv := New(cfg, nil, nil, nil, logger, nil, "test", nil)

	// Stop before Start should be a no-op
	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop on nil server should not error: %v", err)
	}
}

// ─── localOriginPattern Tests ───────────────────────────────────────────────

func TestLocalOriginPattern(t *testing.T) {
	tests := []struct {
		origin string
		match  bool
	}{
		{"http://localhost", true},
		{"http://localhost:3000", true},
		{"http://localhost:33411", true},
		{"https://localhost", true},
		{"https://localhost:8443", true},
		{"http://127.0.0.1", true},
		{"http://127.0.0.1:3000", true},
		{"https://127.0.0.1:443", true},
		{"http://evil.com", false},
		{"http://localhost.evil.com", false},
		{"http://notlocalhost:3000", false},
		{"http://192.168.1.1:3000", false},
		{"http://10.0.0.1:3000", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.origin, func(t *testing.T) {
			got := localOriginPattern.MatchString(tt.origin)
			if got != tt.match {
				t.Errorf("localOriginPattern.MatchString(%q) = %v, want %v", tt.origin, got, tt.match)
			}
		})
	}
}

// ─── EventMessage JSON ──────────────────────────────────────────────────────

func TestEventMessage_JSON(t *testing.T) {
	msg := &EventMessage{
		Type:      "file_change",
		Timestamp: 1700000000,
		Data:      map[string]string{"file": "test.go", "op": "modify"},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded EventMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != "file_change" {
		t.Errorf("Type = %q, want 'file_change'", decoded.Type)
	}
	if decoded.Timestamp != 1700000000 {
		t.Errorf("Timestamp = %d, want 1700000000", decoded.Timestamp)
	}
}

// ─── New() Constructor ──────────────────────────────────────────────────────

func TestNew(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.DefaultConfig(tmpDir)
	logger := log.New(io.Discard, "", 0)

	srv := New(cfg, nil, nil, nil, logger, nil, "test", nil)
	if srv == nil {
		t.Fatal("New returned nil")
	}
	if srv.subscribers == nil {
		t.Error("subscribers map should be initialized")
	}
	if srv.cfg != cfg {
		t.Error("cfg not set correctly")
	}
}
