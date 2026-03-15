// Package session provides AI tool session detection, tracking, and file change attribution.
package session

import (
	"sync"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

// Detector is the interface for AI tool session detection plugins.
type Detector interface {
	// Name returns the detector's identifier (e.g., "claude-code").
	Name() string

	// Detect discovers currently active sessions for this tool.
	Detect() ([]*DetectedSession, error)

	// Identify checks if the given PID belongs to a session of this tool.
	Identify(pid int) (*DetectedSession, error)

	// Attribute determines which active session is responsible for a file write.
	Attribute(event *FileWriteEvent, activeSessions []*DetectedSession) (sessionID string, confidence float32, method schema.AttributionMethod)
}

// DetectedSession represents an AI tool session discovered by a Detector.
type DetectedSession struct {
	SessionID        string
	ToolName         string
	PID              int
	WorkingDirectory string
	StartedAt        time.Time
	Metadata         map[string]string
}

// FileWriteEvent contains the details of a file write used for session attribution.
type FileWriteEvent struct {
	FilePath  string
	Operation schema.Operation
	Timestamp time.Time
	WriterPID int
	Size      int64
}

// Registry manages session detectors, tracks active sessions, and attributes file changes.
type Registry struct {
	detectors []Detector
	sessions  map[string]*trackedSession
	mu        sync.RWMutex

	pollInterval time.Duration

	onSessionStart func(session *schema.Session)
	onSessionEnd   func(session *schema.Session)

	done chan struct{}
	wg   sync.WaitGroup
}

type trackedSession struct {
	session   *schema.Session
	detected  *DetectedSession
	lastSeen  time.Time
	missCount int
	confirmed bool // false = pending (not yet persisted), true = persisted via onSessionStart
}

const (
	defaultPollInterval = 2 * time.Second

	maxMissCount = 5

	maxMissCountNoPID = 2

)

// NewRegistry creates a Registry with the given detectors.
func NewRegistry(detectors ...Detector) *Registry {
	return &Registry{
		detectors:    detectors,
		sessions:     make(map[string]*trackedSession),
		pollInterval: defaultPollInterval,
		done:         make(chan struct{}),
	}
}

// RegisterDetector adds a new session detector to the registry.
func (r *Registry) RegisterDetector(d Detector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.detectors = append(r.detectors, d)
}

// DetectorNames returns the names of all registered detectors.
func (r *Registry) DetectorNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, len(r.detectors))
	for i, d := range r.detectors {
		names[i] = d.Name()
	}
	return names
}

// Start begins the background polling loop for session detection.
func (r *Registry) Start() {
	r.wg.Add(1)
	go r.pollLoop()
}

// Stop terminates the polling loop and marks active sessions as ended.
func (r *Registry) Stop() {
	close(r.done)
	r.wg.Wait()
}

// ActiveSessions returns all currently active sessions.
func (r *Registry) ActiveSessions() []*schema.Session {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var sessions []*schema.Session
	for _, ts := range r.sessions {
		if ts.session.Status == schema.SessionActive {
			sessions = append(sessions, ts.session)
		}
	}
	return sessions
}

// GetSession returns the session with the given ID, or nil if not found.
func (r *Registry) GetSession(sessionID string) *schema.Session {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if ts, ok := r.sessions[sessionID]; ok {
		return ts.session
	}
	return nil
}

// Attribute determines which session is responsible for a file write, consulting all detectors.
func (r *Registry) Attribute(event *FileWriteEvent) (sessionID string, confidence float32, method schema.AttributionMethod) {
	r.mu.RLock()
	detectors := make([]Detector, len(r.detectors))
	copy(detectors, r.detectors)

	var activeSessions []*DetectedSession
	for _, ts := range r.sessions {
		if ts.session.Status == schema.SessionActive {
			activeSessions = append(activeSessions, ts.detected)
		}
	}
	r.mu.RUnlock()

	var bestSession string
	var bestConfidence float32
	var bestMethod schema.AttributionMethod

	for _, d := range detectors {
		sid, conf, meth := d.Attribute(event, activeSessions)
		if sid != "" && conf > bestConfidence {
			bestSession = sid
			bestConfidence = conf
			bestMethod = meth
		}
	}

	if bestSession == "" && len(activeSessions) == 1 {
		bestSession = activeSessions[0].SessionID
		bestConfidence = 0.7
		bestMethod = schema.AttrTemporal
	}

	// If a pending session gets attributed an event, confirm it immediately
	if bestSession != "" {
		r.confirmSession(bestSession)
	}

	return bestSession, bestConfidence, bestMethod
}

// confirmSession promotes a pending session to confirmed, calling onSessionStart.
// Safe to call on already-confirmed sessions (no-op).
func (r *Registry) confirmSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ts, exists := r.sessions[sessionID]
	if !exists || ts.confirmed {
		return
	}
	ts.confirmed = true
	if r.onSessionStart != nil {
		r.onSessionStart(ts.session)
	}
}

// SetOnSessionStart registers a callback invoked when a new session is confirmed.
func (r *Registry) SetOnSessionStart(fn func(session *schema.Session)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onSessionStart = fn
}

// SetOnSessionEnd registers a callback invoked when a session ends or crashes.
func (r *Registry) SetOnSessionEnd(fn func(session *schema.Session)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onSessionEnd = fn
}

func (r *Registry) pollLoop() {
	defer r.wg.Done()

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	r.poll()

	for {
		select {
		case <-r.done:
			r.mu.Lock()
			for _, ts := range r.sessions {
				if ts.session.Status == schema.SessionActive {
					// Only persist end events for confirmed sessions
					if !ts.confirmed {
						continue
					}
					ts.session.Status = schema.SessionEnded
					ts.session.EndedAt = time.Now()
					if r.onSessionEnd != nil {
						r.onSessionEnd(ts.session)
					}
				}
			}
			r.mu.Unlock()
			return
		case <-ticker.C:
			r.poll()
		}
	}
}

func (r *Registry) poll() {
	r.mu.RLock()
	detectors := make([]Detector, len(r.detectors))
	copy(detectors, r.detectors)
	r.mu.RUnlock()

	seen := make(map[string]*DetectedSession)
	for _, d := range detectors {
		sessions, err := d.Detect()
		if err != nil {
			continue
		}
		for _, s := range sessions {
			seen[s.SessionID] = s
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	for id, detected := range seen {
		if _, exists := r.sessions[id]; !exists {
			// New session detected — create as pending (not yet persisted).
			// It will be confirmed once it receives an attributed file event.
			session := &schema.Session{
				SessionID:        detected.SessionID,
				ToolName:         detected.ToolName,
				PID:              detected.PID,
				WorkingDirectory: detected.WorkingDirectory,
				Status:           schema.SessionActive,
				StartedAt:        detected.StartedAt,
				Metadata:         detected.Metadata,
			}
			r.sessions[id] = &trackedSession{
				session:   session,
				detected:  detected,
				lastSeen:  now,
				confirmed: false,
			}
		} else {
			r.sessions[id].lastSeen = now
			r.sessions[id].missCount = 0
			r.sessions[id].detected = detected
		}
	}

	for id, ts := range r.sessions {
		if ts.session.Status != schema.SessionActive {
			continue
		}
		if _, stillAlive := seen[id]; !stillAlive {
			ts.missCount++
			threshold := maxMissCount
			if ts.session.PID == 0 {
				threshold = maxMissCountNoPID
			}
			if ts.missCount >= threshold {
				if !ts.confirmed {
					delete(r.sessions, id)
					continue
				}
				ts.session.Status = schema.SessionCrashed
				ts.session.EndedAt = ts.lastSeen
				if r.onSessionEnd != nil {
					r.onSessionEnd(ts.session)
				}
			}
		}
	}
}
