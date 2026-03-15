// Package api provides the HTTP REST and SSE streaming server embedded in the Belay daemon.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/conflict"
	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/replay"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/session"
	"github.com/davidparkercodes/belay/internal/store"
)

// RecordHandler is a callback for processing events pushed via the record API.
type RecordHandler func(event *schema.Event)

type WatcherHealthProvider interface {
	WatcherHealth() map[string]interface{}
	OverallStatus() string
}

// Server is the HTTP API server for querying events, sessions, and streaming real-time updates.
type Server struct {
	cfg      *config.Config
	idx      *index.Index
	objStore *store.Store
	registry *session.Registry
	logger   *log.Logger
	server   *http.Server
	version  string

	startedAt     time.Time
	rl            *rateLimiter
	watcherHealth WatcherHealthProvider

	onRecord RecordHandler

	subscribersMu sync.RWMutex
	subscribers   map[string]chan *EventMessage
}

// EventMessage is the payload sent to SSE subscribers.
type EventMessage struct {
	Type      string      `json:"type"`
	Timestamp int64       `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// New creates a new API Server with the given dependencies.
func New(cfg *config.Config, idx *index.Index, objStore *store.Store, registry *session.Registry, logger *log.Logger, onRecord RecordHandler, version string, watcherHealth WatcherHealthProvider) *Server {
	return &Server{
		cfg:           cfg,
		idx:           idx,
		objStore:      objStore,
		registry:      registry,
		logger:        logger,
		onRecord:      onRecord,
		version:       version,
		watcherHealth: watcherHealth,
		subscribers:   make(map[string]chan *EventMessage),
	}
}

// Start begins serving HTTP requests on the configured port.
func (s *Server) Start() error {
	mux := http.NewServeMux()

	s.rl = newRateLimiter(100, 100) // 100 req/s per IP, burst of 100
	handler := rateLimitMiddleware(s.rl, corsMiddleware(mux))

	mux.HandleFunc("GET /api/health", s.handleHealth)

	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/events/{id}", s.handleEventByID)
	mux.HandleFunc("POST /api/events/git", s.handleGitEvent)

	mux.HandleFunc("GET /api/sessions", s.handleSessions)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleSession)
	mux.HandleFunc("GET /api/sessions/{id}/events", s.handleSessionEvents)
	mux.HandleFunc("GET /api/sessions/{id}/replay", s.handleSessionReplay)

	mux.HandleFunc("GET /api/files", s.handleFiles)
	mux.HandleFunc("GET /api/files/history", s.handleFileHistory)
	mux.HandleFunc("GET /api/files/content", s.handleFileContent)

	mux.HandleFunc("GET /api/conflicts", s.handleConflicts)

	mux.HandleFunc("GET /api/stats", s.handleStats)

	mux.HandleFunc("POST /api/record", s.handleRecord)

	mux.HandleFunc("GET /api/stream", s.handleStream)

	port := s.cfg.API.Port
	if port == 0 {
		port = 33412
	}
	host := s.cfg.API.Host
	if host == "" {
		host = "127.0.0.1"
	}

	s.startedAt = time.Now()

	s.server = &http.Server{
		Addr:        fmt.Sprintf("%s:%d", host, port),
		Handler:     handler,
		ReadTimeout: 30 * time.Second,
	}

	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.server.Addr, err)
	}

	s.logger.Printf("API server listening on %s:%d", host, port)
	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Printf("API server error: %v", err)
		}
	}()

	return nil
}

// Stop gracefully shuts down the HTTP server with a 5-second timeout.
func (s *Server) Stop() error {
	if s.rl != nil {
		s.rl.Close()
	}
	if s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}

// Broadcast sends an event message to all connected SSE subscribers.
func (s *Server) Broadcast(msg *EventMessage) {
	s.subscribersMu.RLock()
	defer s.subscribersMu.RUnlock()
	for _, ch := range s.subscribers {
		select {
		case ch <- msg:
		default:
		}
	}
}


func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	overallStatus := "ok"
	resp := map[string]interface{}{
		"version": s.version,
		"uptime":  time.Since(s.startedAt).String(),
	}

	if s.watcherHealth != nil {
		resp["watcher"] = s.watcherHealth.WatcherHealth()
		overallStatus = s.watcherHealth.OverallStatus()
	}

	resp["status"] = overallStatus
	writeJSON(w, resp)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := &index.Query{}

	if v := r.URL.Query().Get("since"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			q.Since = time.Now().Add(-d).UnixNano()
		}
	}
	if v := r.URL.Query().Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.Until = t.UnixNano()
		}
	}
	if v := r.URL.Query().Get("file"); v != "" {
		q.FilePaths = []string{v}
	}
	if v := r.URL.Query().Get("session"); v != "" {
		q.Sessions = []string{v}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.Limit = n
		}
	}
	if v := r.URL.Query().Get("attribution"); v != "" {
		parts := strings.Split(v, ",")
		var attrValues []int
		for _, part := range parts {
			part = strings.TrimSpace(part)
			switch part {
			case "none":
				attrValues = append(attrValues, int(schema.AttrNone))
			case "ai":
				attrValues = append(attrValues, int(schema.AttrPID), int(schema.AttrTemporal), int(schema.AttrHeuristic), int(schema.AttrHook))
			case "git":
				attrValues = append(attrValues, int(schema.AttrGit))
			}
		}
		if len(attrValues) > 0 {
			seen := make(map[int]bool)
			var deduped []int
			for _, v := range attrValues {
				if !seen[v] {
					seen[v] = true
					deduped = append(deduped, v)
				}
			}
			q.Attributions = deduped
		}
	}
	if q.Limit == 0 {
		q.Limit = 100
	}
	q.OrderDesc = r.URL.Query().Get("order") != "asc"

	events, err := s.idx.QueryEvents(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, map[string]interface{}{
		"events": events,
		"count":  len(events),
	})
}

func (s *Server) handleEventByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	event, err := s.idx.GetEvent(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	writeJSON(w, event)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	activeOnly := r.URL.Query().Get("active") == "true"
	minEvents := 0

	// hide_empty=true is a convenience shorthand for min_events=1
	if r.URL.Query().Get("hide_empty") == "true" {
		minEvents = 1
	}

	// min_events takes precedence over hide_empty if both are set
	if v := r.URL.Query().Get("min_events"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			minEvents = n
		}
	}

	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	sessions, err := s.idx.ListSessions(activeOnly, minEvents, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonSessions := make([]schema.SessionJSON, 0, len(sessions))
	for _, sess := range sessions {
		jsonSessions = append(jsonSessions, sess.ToJSON())
	}

	writeJSON(w, map[string]interface{}{
		"sessions": jsonSessions,
		"count":    len(jsonSessions),
	})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.idx.GetSession(id)
	if err != nil {
		events, evErr := s.idx.QueryEvents(&index.Query{
			Sessions: []string{id},
			Limit:    1,
		})
		if evErr != nil || len(events) == 0 {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		toolName := "unknown"
		if events[0].Metadata != nil {
			if tn, ok := events[0].Metadata["tool_name"]; ok {
				toolName = tn
			}
		}
		sess = &schema.Session{
			SessionID: id,
			ToolName:  toolName,
			Status:    schema.SessionEnded,
			StartedAt: events[0].Timestamp(),
		}
	}
	allEvents, _ := s.idx.QueryEvents(&index.Query{
		Sessions: []string{id},
		Limit:    0,
	})
	sess.EventCount = len(allEvents)
	files := make(map[string]bool)
	for _, e := range allEvents {
		files[e.FilePath] = true
	}
	sess.FilesChanged = len(files)
	writeJSON(w, sess.ToJSON())
}

func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	q := &index.Query{
		Sessions:  []string{id},
		OrderDesc: false,
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.Limit = n
		}
	}

	events, err := s.idx.QueryEvents(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, map[string]interface{}{
		"session_id": id,
		"events":     events,
		"count":      len(events),
	})
}

func (s *Server) handleSessionReplay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	result, err := replay.ReplaySession(s.idx, s.objStore, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, result)
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	sinceStr := r.URL.Query().Get("since")
	since := time.Now().Add(-24 * time.Hour)
	if sinceStr != "" {
		if d, err := time.ParseDuration(sinceStr); err == nil {
			since = time.Now().Add(-d)
		}
	}

	q := &index.Query{
		Since:     since.UnixNano(),
		OrderDesc: true,
		Limit:     1000,
	}
	if v := r.URL.Query().Get("attribution"); v != "" {
		parts := strings.Split(v, ",")
		var attrValues []int
		for _, part := range parts {
			part = strings.TrimSpace(part)
			switch part {
			case "none":
				attrValues = append(attrValues, int(schema.AttrNone))
			case "ai":
				attrValues = append(attrValues, int(schema.AttrPID), int(schema.AttrTemporal), int(schema.AttrHeuristic), int(schema.AttrHook))
			case "git":
				attrValues = append(attrValues, int(schema.AttrGit))
			}
		}
		if len(attrValues) > 0 {
			seen := make(map[int]bool)
			var deduped []int
			for _, v := range attrValues {
				if !seen[v] {
					seen[v] = true
					deduped = append(deduped, v)
				}
			}
			q.Attributions = deduped
		}
	}
	events, err := s.idx.QueryEvents(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type fileInfo struct {
		Path      string `json:"path"`
		LastOp    string `json:"last_op"`
		LastTime  int64  `json:"last_time"`
		SessionID string `json:"session_id,omitempty"`
		Events    int    `json:"events"`
	}

	files := make(map[string]*fileInfo)
	for _, e := range events {
		fi, exists := files[e.FilePath]
		if !exists {
			fi = &fileInfo{
				Path:      e.FilePath,
				LastOp:    e.Op.String(),
				LastTime:  e.TimestampNano,
				SessionID: e.SessionID,
			}
			files[e.FilePath] = fi
		}
		fi.Events++
	}

	var result []*fileInfo
	for _, fi := range files {
		result = append(result, fi)
	}

	writeJSON(w, map[string]interface{}{
		"files": result,
		"count": len(result),
	})
}

func (s *Server) handleFileHistory(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path parameter required")
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}

	events, err := s.idx.QueryEvents(&index.Query{
		FilePaths: []string{path},
		OrderDesc: true,
		Limit:     limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, map[string]interface{}{
		"path":   path,
		"events": events,
		"count":  len(events),
	})
}

func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	hash := r.URL.Query().Get("hash")
	if hash == "" {
		writeError(w, http.StatusBadRequest, "hash parameter required")
		return
	}

	data, err := s.objStore.Get(hash)
	if err != nil {
		writeError(w, http.StatusNotFound, "content not found")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}

func (s *Server) handleConflicts(w http.ResponseWriter, r *http.Request) {
	sinceStr := r.URL.Query().Get("since")
	since := time.Now().Add(-24 * time.Hour)
	if sinceStr != "" {
		if d, err := time.ParseDuration(sinceStr); err == nil {
			since = time.Now().Add(-d)
		}
	}

	detector := conflict.NewDetector(s.idx, 60*time.Second)

	var conflicts []*conflict.Conflict
	var err error

	if file := r.URL.Query().Get("file"); file != "" {
		conflicts, err = detector.DetectForFile(file, since)
	} else {
		conflicts, err = detector.DetectSince(since)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, map[string]interface{}{
		"conflicts": conflicts,
		"count":     len(conflicts),
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	totalEvents, _ := s.idx.CountEvents()
	allSessions, _ := s.idx.ListSessions(false, 0, 0)
	activeSessions, _ := s.idx.ListSessions(true, 0, 0)

	storeBytes, objectCount, _ := s.objStore.Size()

	writeJSON(w, map[string]interface{}{
		"total_events":    totalEvents,
		"total_sessions":  len(allSessions),
		"active_sessions": len(activeSessions),
		"store_bytes":     storeBytes,
		"store_objects":   objectCount,
	})
}

// RecordRequest is the JSON payload for the POST /api/record endpoint.
type RecordRequest struct {
	FilePath  string `json:"file_path"`
	Operation string `json:"operation"`
	SessionID string `json:"session_id,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
}

func (s *Server) handleRecord(w http.ResponseWriter, r *http.Request) {
	if s.onRecord == nil {
		writeError(w, http.StatusServiceUnavailable, "record handler not configured")
		return
	}

	var req RecordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.FilePath == "" {
		writeError(w, http.StatusBadRequest, "file_path is required")
		return
	}

	op := schema.OpModify
	if req.Operation != "" {
		parsed, err := schema.ParseOperation(req.Operation)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		op = parsed
	}

	absPath := filepath.Join(s.cfg.ProjectRoot, filepath.Clean(req.FilePath))
	// Resolve symlinks before checking prefix to prevent symlink escapes
	realPath, evalErr := filepath.EvalSymlinks(absPath)
	if evalErr != nil {
		// file may not exist yet, fall back to cleaned path
		realPath = absPath
	}
	realRoot, evalErr := filepath.EvalSymlinks(s.cfg.ProjectRoot)
	if evalErr != nil {
		realRoot = s.cfg.ProjectRoot
	}
	if !strings.HasPrefix(realPath, realRoot+string(filepath.Separator)) && realPath != realRoot {
		writeError(w, http.StatusBadRequest, "file path escapes project root")
		return
	}
	var contentHash string
	var contentSize int64

	if op != schema.OpDelete {
		data, err := os.ReadFile(absPath)
		if err != nil {
			s.logger.Printf("record: cannot read %s: %v", req.FilePath, err)
			writeError(w, http.StatusNotFound, "cannot read file: "+err.Error())
			return
		}
		hash, size, err := s.objStore.Put(data)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store content: "+err.Error())
			return
		}
		contentHash = hash
		contentSize = size
	}

	event := &schema.Event{
		EventID:               schema.NewEventID(),
		Version:               schema.SchemaVersion,
		FilePath:              req.FilePath,
		Op:                    op,
		ContentHash:           contentHash,
		ContentSize:           contentSize,
		SessionID:             req.SessionID,
		Attribution:           schema.AttrHook,
		AttributionConfidence: 1.0,
		Metadata: map[string]string{
			"source": "hook",
		},
	}
	if req.ToolName != "" {
		event.Metadata["tool_name"] = req.ToolName
	}
	event.SetTimestamp(time.Now())

	s.onRecord(event)

	writeJSON(w, map[string]interface{}{
		"status":   "recorded",
		"event_id": event.EventID,
	})
}

type GitEventRequest struct {
	Operation    string `json:"operation"`
	RefFrom      string `json:"ref_from"`
	RefTo        string `json:"ref_to"`
	Branch       string `json:"branch"`
	FilesChanged int    `json:"files_changed"`
}

func (s *Server) handleGitEvent(w http.ResponseWriter, r *http.Request) {
	var req GitEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Operation == "" {
		writeError(w, http.StatusBadRequest, "operation is required")
		return
	}

	event := &schema.Event{
		EventID:               schema.NewEventID(),
		Version:               schema.SchemaVersion,
		FilePath:              fmt.Sprintf("[git:%s]", req.Operation),
		Op:                    schema.OpModify,
		Attribution:           schema.AttrGit,
		AttributionConfidence: 1.0,
		Metadata: map[string]string{
			"source":    "git",
			"operation": req.Operation,
		},
	}
	if req.RefFrom != "" {
		event.Metadata["ref_from"] = req.RefFrom
	}
	if req.RefTo != "" {
		event.Metadata["ref_to"] = req.RefTo
	}
	if req.Branch != "" {
		event.Metadata["branch"] = req.Branch
	}
	if req.FilesChanged > 0 {
		event.Metadata["files_changed"] = strconv.Itoa(req.FilesChanged)
	}
	event.SetTimestamp(time.Now())

	if err := s.idx.IndexEvent(event, "", 0); err != nil {
		writeError(w, http.StatusInternalServerError, "index event: "+err.Error())
		return
	}

	s.Broadcast(&EventMessage{
		Type:      "git_event",
		Timestamp: event.TimestampNano,
		Data:      event.ToJSON(),
	})

	writeJSON(w, map[string]interface{}{
		"status":   "recorded",
		"event_id": event.EventID,
	})
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	id := fmt.Sprintf("sub-%d", time.Now().UnixNano())
	ch := make(chan *EventMessage, 64)

	s.subscribersMu.Lock()
	s.subscribers[id] = ch
	s.subscribersMu.Unlock()

	defer func() {
		s.subscribersMu.Lock()
		delete(s.subscribers, id)
		s.subscribersMu.Unlock()
		close(ch)
	}()

	fmt.Fprintf(w, "data: {\"type\":\"connected\",\"id\":\"%s\"}\n\n", id)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}


var localOriginPattern = regexp.MustCompile(`^https?://(localhost|127\.0\.0\.1|[a-zA-Z0-9-]+\.ts\.net)(:\d+)?$`)

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && localOriginPattern.MatchString(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// --- Rate Limiter ---

type ipBucket struct {
	tokens    float64
	lastCheck time.Time
}

type rateLimiter struct {
	buckets sync.Map // map[string]*ipBucket
	rate    float64  // tokens per second
	burst   float64  // max tokens (bucket capacity)
	mu      sync.Mutex
	stop    chan struct{}
}

func newRateLimiter(ratePerSec float64, burst float64) *rateLimiter {
	rl := &rateLimiter{
		rate: ratePerSec,
		burst: burst,
		stop: make(chan struct{}),
	}
	// Periodically clean up stale entries
	go func() {
		for {
			select {
			case <-time.After(60 * time.Second):
				now := time.Now()
				rl.buckets.Range(func(key, value interface{}) bool {
					b := value.(*ipBucket)
					if now.Sub(b.lastCheck) > 5*time.Minute {
						rl.buckets.Delete(key)
					}
					return true
				})
			case <-rl.stop:
				return
			}
		}
	}()
	return rl
}

// Close stops the rate limiter's cleanup goroutine.
func (rl *rateLimiter) Close() {
	close(rl.stop)
}

func (rl *rateLimiter) allow(ip string) bool {
	now := time.Now()

	val, _ := rl.buckets.LoadOrStore(ip, &ipBucket{
		tokens:    rl.burst,
		lastCheck: now,
	})
	b := val.(*ipBucket)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	elapsed := now.Sub(b.lastCheck).Seconds()
	b.lastCheck = now
	b.tokens += elapsed * rl.rate
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func rateLimitMiddleware(rl *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting for SSE endpoints (long-lived connections)
		if r.URL.Path == "/api/stream" {
			next.ServeHTTP(w, r)
			return
		}

		ip := r.RemoteAddr
		// Strip port from RemoteAddr
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		}

		if !rl.allow(ip) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "rate limit exceeded",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

