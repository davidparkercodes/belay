package schema

import "time"

// SessionStatus represents the lifecycle state of an AI tool session.
type SessionStatus uint8

const (
	// SessionActive indicates the session is currently running.
	SessionActive SessionStatus = iota + 1
	// SessionEnded indicates the session terminated normally.
	SessionEnded
	// SessionCrashed indicates the session disappeared without a clean shutdown.
	SessionCrashed
)

// String returns the lowercase name of the SessionStatus.
func (s SessionStatus) String() string {
	switch s {
	case SessionActive:
		return "active"
	case SessionEnded:
		return "ended"
	case SessionCrashed:
		return "crashed"
	default:
		return "unknown"
	}
}

// Session represents an AI tool session tracked by Belay.
type Session struct {
	SessionID string `json:"session_id"`

	ToolName string `json:"tool_name"`

	PID int `json:"pid"`

	WorkingDirectory string `json:"working_directory,omitempty"`

	Status SessionStatus `json:"status"`

	StartedAt time.Time `json:"started_at"`

	EndedAt time.Time `json:"ended_at,omitempty"`

	Label string `json:"label,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`

	FilesChanged int `json:"files_changed"`
	EventCount   int `json:"event_count"`
}

// Duration returns how long the session has been running, or its total runtime if ended.
func (s *Session) Duration() time.Duration {
	if s.EndedAt.IsZero() {
		return time.Since(s.StartedAt)
	}
	return s.EndedAt.Sub(s.StartedAt)
}

// SessionJSON is the JSON-serializable representation of a Session for API responses.
type SessionJSON struct {
	SessionID    string            `json:"session_id"`
	ToolName     string            `json:"tool_name"`
	PID          int               `json:"pid"`
	Status       string            `json:"status"`
	StartedAt    string            `json:"started_at"`
	EndedAt      string            `json:"ended_at,omitempty"`
	Duration     string            `json:"duration"`
	Label        string            `json:"label,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	FilesChanged int               `json:"files_changed"`
	EventCount   int               `json:"event_count"`
}

// ToJSON converts the Session to its JSON-friendly representation.
func (s *Session) ToJSON() SessionJSON {
	j := SessionJSON{
		SessionID:    s.SessionID,
		ToolName:     s.ToolName,
		PID:          s.PID,
		Status:       s.Status.String(),
		StartedAt:    s.StartedAt.Format(time.RFC3339),
		Duration:     s.Duration().Round(time.Second).String(),
		Label:        s.Label,
		Metadata:     s.Metadata,
		FilesChanged: s.FilesChanged,
		EventCount:   s.EventCount,
	}
	if !s.EndedAt.IsZero() {
		j.EndedAt = s.EndedAt.Format(time.RFC3339)
	}
	return j
}
