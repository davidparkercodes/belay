package index

import (
	"database/sql"
	"fmt"
	"time"
)

// SessionLastActivity returns the timestamp of the newest event attributed to the session,
// falling back to the session's start time when it has no events yet.
func (idx *Index) SessionLastActivity(sessionID string) (time.Time, error) {
	var nanos int64
	err := idx.db.QueryRow(`
		SELECT COALESCE((SELECT MAX(timestamp_nano) FROM events WHERE session_id = ?), started_at)
		FROM sessions WHERE session_id = ?`, sessionID, sessionID).Scan(&nanos)
	if err == sql.ErrNoRows {
		return time.Time{}, fmt.Errorf("session not found: %s", sessionID)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("session last activity %s: %w", sessionID, err)
	}
	return time.Unix(0, nanos), nil
}

// MarkSessionEnded closes an active session with a normal "ended" status.
func (idx *Index) MarkSessionEnded(sessionID string, endedAt time.Time) error {
	_, err := idx.db.Exec(
		`UPDATE sessions SET status = 'ended', ended_at = ? WHERE session_id = ? AND status = 'active'`,
		endedAt.UnixNano(), sessionID,
	)
	if err != nil {
		return fmt.Errorf("mark session ended %s: %w", sessionID, err)
	}
	return nil
}
