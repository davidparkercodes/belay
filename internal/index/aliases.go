package index

import (
	"fmt"
)

const linkedSessionKey = "linked_session_id"

// SetSessionMetadata sets one key in a session's metadata JSON, creating the
// session row if it does not exist yet.
func (idx *Index) SetSessionMetadata(sessionID, key, value string) error {
	res, err := idx.db.Exec(
		`UPDATE sessions SET metadata = json_set(COALESCE(NULLIF(metadata, ''), '{}'), '$.' || ?, ?) WHERE session_id = ?`,
		key, value, sessionID,
	)
	if err != nil {
		return fmt.Errorf("set session metadata %s: %w", sessionID, err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = idx.db.Exec(
		`INSERT INTO sessions (session_id, tool_name, status, started_at, metadata)
		 VALUES (?, 'unknown', 'active', (strftime('%s','now') * 1000000000), json_set('{}', '$.' || ?, ?))`,
		sessionID, key, value,
	)
	if err != nil {
		return fmt.Errorf("insert session metadata %s: %w", sessionID, err)
	}
	return nil
}

// LinkedSessions returns the process-detected session ids whose metadata links
// them to the given tool session id.
func (idx *Index) LinkedSessions(sessionID string) ([]string, error) {
	rows, err := idx.db.Query(
		`SELECT session_id FROM sessions WHERE json_extract(metadata, '$.' || ?) = ? AND session_id != ?`,
		linkedSessionKey, sessionID, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("linked sessions for %s: %w", sessionID, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ExpandSessions returns the given session ids plus every process session linked to
// them, so queries by a tool's session id also see events recorded under the
// synthetic process id before the link was known.
func (idx *Index) ExpandSessions(sessionIDs []string) []string {
	seen := make(map[string]bool, len(sessionIDs))
	out := make([]string, 0, len(sessionIDs))
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, id := range sessionIDs {
		add(id)
		linked, err := idx.LinkedSessions(id)
		if err != nil {
			continue
		}
		for _, l := range linked {
			add(l)
		}
	}
	return out
}
