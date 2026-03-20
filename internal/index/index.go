// Package index provides a SQLite-backed event and session index for querying Belay history.
package index

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"

	_ "modernc.org/sqlite"
)

// Index is a SQLite-backed database for querying events and sessions by time, file, or session.
type Index struct {
	db *sql.DB
}

type Query struct {
	Since        int64
	Until        int64
	Sessions     []string
	FilePaths    []string
	Operations   []string
	Attributions []int
	Limit        int
	Offset       int
	OrderDesc    bool
}

const createSchema = `
CREATE TABLE IF NOT EXISTS events (
    event_id TEXT PRIMARY KEY,
    timestamp_nano INTEGER NOT NULL,
    file_path TEXT NOT NULL,
    operation TEXT NOT NULL,
    content_hash TEXT DEFAULT '',
    previous_hash TEXT DEFAULT '',
    content_size INTEGER DEFAULT 0,
    old_path TEXT DEFAULT '',
    session_id TEXT DEFAULT '',
    attribution_method INTEGER DEFAULT 0,
    attribution_confidence REAL DEFAULT 0,
    is_conflict INTEGER DEFAULT 0,
    segment_file TEXT DEFAULT '',
    segment_offset INTEGER DEFAULT 0,
    metadata TEXT DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp_nano);
CREATE INDEX IF NOT EXISTS idx_events_file_path ON events(file_path);
CREATE INDEX IF NOT EXISTS idx_events_session_id ON events(session_id);
CREATE INDEX IF NOT EXISTS idx_events_operation ON events(operation);
CREATE INDEX IF NOT EXISTS idx_events_file_time ON events(file_path, timestamp_nano);

CREATE TABLE IF NOT EXISTS sessions (
    session_id TEXT PRIMARY KEY,
    tool_name TEXT NOT NULL DEFAULT 'unknown',
    pid INTEGER DEFAULT 0,
    working_directory TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    started_at INTEGER NOT NULL,
    ended_at INTEGER DEFAULT 0,
    label TEXT DEFAULT '',
    metadata TEXT DEFAULT '{}',
    files_changed INTEGER DEFAULT 0,
    event_count INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
CREATE INDEX IF NOT EXISTS idx_sessions_started ON sessions(started_at);
`

// Open opens or creates a SQLite index database at the given path with WAL mode enabled.
func Open(dbPath string) (*Index, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=-64000",
		"PRAGMA temp_store=MEMORY",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("set pragma: %w", err)
		}
	}

	if _, err := db.Exec(createSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &Index{db: db}, nil
}

// Checkpoint forces a WAL checkpoint, flushing pending writes to the main database file.
func (idx *Index) Checkpoint() error {
	_, err := idx.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	if err != nil {
		return fmt.Errorf("wal checkpoint: %w", err)
	}
	return nil
}

// Close closes the underlying database connection.
func (idx *Index) Close() error {
	if idx.db != nil {
		return idx.db.Close()
	}
	return nil
}

// IndexEvent inserts or updates a single event in the index, recording its segment location.
func (idx *Index) IndexEvent(event *schema.Event, segmentFile string, segmentOffset int64) error {
	metadataJSON := "{}"
	if event.Metadata != nil {
		data, err := json.Marshal(event.Metadata)
		if err == nil {
			metadataJSON = string(data)
		}
	}

	_, err := idx.db.Exec(`
		INSERT OR REPLACE INTO events (
			event_id, timestamp_nano, file_path, operation,
			content_hash, previous_hash, content_size, old_path,
			session_id, attribution_method, attribution_confidence,
			is_conflict, segment_file, segment_offset, metadata
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventID, event.TimestampNano, event.FilePath, event.Op.String(),
		event.ContentHash, event.PreviousHash, event.ContentSize, event.OldPath,
		event.SessionID, int(event.Attribution), event.AttributionConfidence,
		boolToInt(event.IsConflict), segmentFile, segmentOffset, metadataJSON,
	)
	if err != nil {
		return fmt.Errorf("index event %s: %w", event.EventID, err)
	}
	return nil
}

// IndexEventBatch inserts multiple events in a single transaction for better performance.
func (idx *Index) IndexEventBatch(events []struct {
	Event         *schema.Event
	SegmentFile   string
	SegmentOffset int64
}) error {
	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO events (
			event_id, timestamp_nano, file_path, operation,
			content_hash, previous_hash, content_size, old_path,
			session_id, attribution_method, attribution_confidence,
			is_conflict, segment_file, segment_offset, metadata
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, e := range events {
		metadataJSON := "{}"
		if e.Event.Metadata != nil {
			data, err := json.Marshal(e.Event.Metadata)
			if err == nil {
				metadataJSON = string(data)
			}
		}

		_, err := stmt.Exec(
			e.Event.EventID, e.Event.TimestampNano, e.Event.FilePath, e.Event.Op.String(),
			e.Event.ContentHash, e.Event.PreviousHash, e.Event.ContentSize, e.Event.OldPath,
			e.Event.SessionID, int(e.Event.Attribution), e.Event.AttributionConfidence,
			boolToInt(e.Event.IsConflict), e.SegmentFile, e.SegmentOffset, metadataJSON,
		)
		if err != nil {
			return fmt.Errorf("index event %s: %w", e.Event.EventID, err)
		}
	}

	return tx.Commit()
}

// QueryEvents returns events matching the given query filters, ordered by timestamp.
func (idx *Index) QueryEvents(q *Query) ([]*schema.Event, error) {
	where, args := buildWhereClause(q)

	order := "ASC"
	if q.OrderDesc {
		order = "DESC"
	}

	query := fmt.Sprintf(`
		SELECT event_id, timestamp_nano, file_path, operation,
			content_hash, previous_hash, content_size, old_path,
			session_id, attribution_method, attribution_confidence,
			is_conflict, metadata
		FROM events
		%s
		ORDER BY timestamp_nano %s`,
		where, order)

	if q.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", q.Limit)
	}
	if q.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", q.Offset)
	}

	rows, err := idx.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

// GetEvent retrieves a single event by its ID.
func (idx *Index) GetEvent(eventID string) (*schema.Event, error) {
	row := idx.db.QueryRow(`
		SELECT event_id, timestamp_nano, file_path, operation,
			content_hash, previous_hash, content_size, old_path,
			session_id, attribution_method, attribution_confidence,
			is_conflict, metadata
		FROM events WHERE event_id = ?`, eventID)

	return scanEvent(row)
}

// CountEvents returns the total number of events in the index.
func (idx *Index) CountEvents() (int64, error) {
	var count int64
	err := idx.db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	return count, err
}

// CountSessionEvents returns the number of events for a specific session.
func (idx *Index) CountSessionEvents(sessionID string) (int, error) {
	var count int
	err := idx.db.QueryRow("SELECT COUNT(*) FROM events WHERE session_id = ?", sessionID).Scan(&count)
	return count, err
}

// CountSessionFiles returns the number of distinct files changed in a session.
func (idx *Index) CountSessionFiles(sessionID string) (int, error) {
	var count int
	err := idx.db.QueryRow("SELECT COUNT(DISTINCT file_path) FROM events WHERE session_id = ?", sessionID).Scan(&count)
	return count, err
}

// FileHistory returns the most recent events for a specific file path, newest first.
func (idx *Index) FileHistory(filePath string, limit int) ([]*schema.Event, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := idx.db.Query(`
		SELECT event_id, timestamp_nano, file_path, operation,
			content_hash, previous_hash, content_size, old_path,
			session_id, attribution_method, attribution_confidence,
			is_conflict, metadata
		FROM events
		WHERE file_path = ?
		ORDER BY timestamp_nano DESC
		LIMIT ?`, filePath, limit)
	if err != nil {
		return nil, fmt.Errorf("file history: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

// LatestEvent returns the most recent event for the given file path.
func (idx *Index) LatestEvent(filePath string) (*schema.Event, error) {
	row := idx.db.QueryRow(`
		SELECT event_id, timestamp_nano, file_path, operation,
			content_hash, previous_hash, content_size, old_path,
			session_id, attribution_method, attribution_confidence,
			is_conflict, metadata
		FROM events
		WHERE file_path = ?
		ORDER BY timestamp_nano DESC
		LIMIT 1`, filePath)

	return scanEvent(row)
}

// UpsertSession inserts or updates a session record in the index.
func (idx *Index) UpsertSession(session *schema.Session) error {
	metadataJSON := "{}"
	if session.Metadata != nil {
		data, err := json.Marshal(session.Metadata)
		if err == nil {
			metadataJSON = string(data)
		}
	}

	var endedAt int64
	if !session.EndedAt.IsZero() {
		endedAt = session.EndedAt.UnixNano()
	}

	_, err := idx.db.Exec(`
		INSERT OR REPLACE INTO sessions (
			session_id, tool_name, pid, working_directory,
			status, started_at, ended_at, label, metadata,
			files_changed, event_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.SessionID, session.ToolName, session.PID, session.WorkingDirectory,
		session.Status.String(), session.StartedAt.UnixNano(), endedAt,
		session.Label, metadataJSON,
		session.FilesChanged, session.EventCount,
	)
	if err != nil {
		return fmt.Errorf("upsert session %s: %w", session.SessionID, err)
	}
	return nil
}

// CountSessions returns the total number of sessions.
func (idx *Index) CountSessions() (int64, error) {
	var count int64
	err := idx.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count)
	return count, err
}

// CountActiveSessions returns the number of active sessions.
func (idx *Index) CountActiveSessions() (int64, error) {
	var count int64
	err := idx.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE status = 'active'").Scan(&count)
	return count, err
}

// GetSession retrieves a single session by its ID.
func (idx *Index) GetSession(sessionID string) (*schema.Session, error) {
	row := idx.db.QueryRow(`
		SELECT session_id, tool_name, pid, working_directory,
			status, started_at, ended_at, label, metadata,
			files_changed, event_count
		FROM sessions WHERE session_id = ?`, sessionID)

	return scanSession(row)
}

// ListSessions returns sessions matching the given filters, ordered by start time descending.
func (idx *Index) ListSessions(activeOnly bool, minEvents int, limit int) ([]*schema.Session, error) {
	query := `SELECT session_id, tool_name, pid, working_directory,
		status, started_at, ended_at, label, metadata,
		files_changed, event_count
		FROM sessions`

	var conditions []string
	var args []interface{}

	if activeOnly {
		conditions = append(conditions, "status = 'active'")
	}
	if minEvents > 0 {
		conditions = append(conditions, "event_count >= ?")
		args = append(args, minEvents)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY started_at DESC"

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := idx.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*schema.Session
	for rows.Next() {
		s, err := scanSessionFromRows(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

type ActiveSessionRow struct {
	SessionID string
	PID       int
}

func (idx *Index) ActiveSessionsWithPID() ([]ActiveSessionRow, error) {
	rows, err := idx.db.Query(`SELECT session_id, pid FROM sessions WHERE status = 'active' AND pid > 0`)
	if err != nil {
		return nil, fmt.Errorf("query active sessions: %w", err)
	}
	defer rows.Close()

	var result []ActiveSessionRow
	for rows.Next() {
		var r ActiveSessionRow
		if err := rows.Scan(&r.SessionID, &r.PID); err != nil {
			return nil, fmt.Errorf("scan active session: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (idx *Index) AllActiveSessions() ([]ActiveSessionRow, error) {
	rows, err := idx.db.Query(`SELECT session_id, pid FROM sessions WHERE status = 'active'`)
	if err != nil {
		return nil, fmt.Errorf("query all active sessions: %w", err)
	}
	defer rows.Close()

	var result []ActiveSessionRow
	for rows.Next() {
		var r ActiveSessionRow
		if err := rows.Scan(&r.SessionID, &r.PID); err != nil {
			return nil, fmt.Errorf("scan active session: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (idx *Index) MarkSessionCrashed(sessionID string, endedAt time.Time) error {
	_, err := idx.db.Exec(
		`UPDATE sessions SET status = 'crashed', ended_at = ? WHERE session_id = ? AND status = 'active'`,
		endedAt.UnixNano(), sessionID,
	)
	if err != nil {
		return fmt.Errorf("mark session crashed %s: %w", sessionID, err)
	}
	return nil
}

// UpdateSessionLabel sets the human-readable label for a session.
func (idx *Index) UpdateSessionLabel(sessionID, label string) error {
	result, err := idx.db.Exec("UPDATE sessions SET label = ? WHERE session_id = ?", label, sessionID)
	if err != nil {
		return fmt.Errorf("update label: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	return nil
}


func buildWhereClause(q *Query) (string, []interface{}) {
	var conditions []string
	var args []interface{}

	if q.Since > 0 {
		conditions = append(conditions, "timestamp_nano >= ?")
		args = append(args, q.Since)
	}
	if q.Until > 0 {
		conditions = append(conditions, "timestamp_nano <= ?")
		args = append(args, q.Until)
	}
	if len(q.Sessions) > 0 {
		placeholders := make([]string, len(q.Sessions))
		for i, s := range q.Sessions {
			placeholders[i] = "?"
			args = append(args, s)
		}
		conditions = append(conditions, fmt.Sprintf("session_id IN (%s)", strings.Join(placeholders, ",")))
	}
	if len(q.FilePaths) > 0 {
		var fileConditions []string
		for _, fp := range q.FilePaths {
			if strings.Contains(fp, "*") || strings.Contains(fp, "?") {
				like := strings.ReplaceAll(fp, "*", "%")
				like = strings.ReplaceAll(like, "?", "_")
				fileConditions = append(fileConditions, "file_path LIKE ?")
				args = append(args, like)
			} else {
				fileConditions = append(fileConditions, "file_path = ?")
				args = append(args, fp)
			}
		}
		conditions = append(conditions, "("+strings.Join(fileConditions, " OR ")+")")
	}
	if len(q.Operations) > 0 {
		placeholders := make([]string, len(q.Operations))
		for i, op := range q.Operations {
			placeholders[i] = "?"
			args = append(args, strings.ToUpper(op))
		}
		conditions = append(conditions, fmt.Sprintf("operation IN (%s)", strings.Join(placeholders, ",")))
	}
	if len(q.Attributions) > 0 {
		hasUnattributed := false
		var attrValues []int
		for _, v := range q.Attributions {
			if v == -1 {
				hasUnattributed = true
			} else {
				attrValues = append(attrValues, v)
			}
		}
		var parts []string
		if len(attrValues) > 0 {
			placeholders := make([]string, len(attrValues))
			for i, v := range attrValues {
				placeholders[i] = "?"
				args = append(args, v)
			}
			parts = append(parts, fmt.Sprintf("attribution_method IN (%s)", strings.Join(placeholders, ",")))
		}
		if hasUnattributed {
			parts = append(parts, "(attribution_method = 0 AND session_id = '')")
		}
		if len(parts) > 0 {
			conditions = append(conditions, "("+strings.Join(parts, " OR ")+")")
		}
	}

	if len(conditions) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(conditions, " AND "), args
}

func scanEvents(rows *sql.Rows) ([]*schema.Event, error) {
	var events []*schema.Event
	for rows.Next() {
		var event schema.Event
		var opStr, metadataJSON string
		var attrMethod int
		var isConflict int

		err := rows.Scan(
			&event.EventID, &event.TimestampNano, &event.FilePath, &opStr,
			&event.ContentHash, &event.PreviousHash, &event.ContentSize, &event.OldPath,
			&event.SessionID, &attrMethod, &event.AttributionConfidence,
			&isConflict, &metadataJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}

		event.Op, _ = schema.ParseOperation(opStr)
		event.Attribution = schema.AttributionMethod(attrMethod)
		event.IsConflict = isConflict != 0
		if metadataJSON != "" && metadataJSON != "{}" {
			if err := json.Unmarshal([]byte(metadataJSON), &event.Metadata); err != nil {
				log.Printf("belay: corrupt metadata for event %s: %v", event.EventID, err)
			}
		}

		events = append(events, &event)
	}
	return events, rows.Err()
}

type scannable interface {
	Scan(dest ...interface{}) error
}

func scanEvent(row scannable) (*schema.Event, error) {
	var event schema.Event
	var opStr, metadataJSON string
	var attrMethod int
	var isConflict int

	err := row.Scan(
		&event.EventID, &event.TimestampNano, &event.FilePath, &opStr,
		&event.ContentHash, &event.PreviousHash, &event.ContentSize, &event.OldPath,
		&event.SessionID, &attrMethod, &event.AttributionConfidence,
		&isConflict, &metadataJSON,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("event not found")
		}
		return nil, fmt.Errorf("scan event: %w", err)
	}

	event.Op, _ = schema.ParseOperation(opStr)
	event.Attribution = schema.AttributionMethod(attrMethod)
	event.IsConflict = isConflict != 0
	if metadataJSON != "" && metadataJSON != "{}" {
		if err := json.Unmarshal([]byte(metadataJSON), &event.Metadata); err != nil {
			log.Printf("belay: corrupt metadata for event %s: %v", event.EventID, err)
		}
	}

	return &event, nil
}

func scanSession(row scannable) (*schema.Session, error) {
	var s schema.Session
	var statusStr, metadataJSON string
	var startedAt, endedAt int64

	err := row.Scan(
		&s.SessionID, &s.ToolName, &s.PID, &s.WorkingDirectory,
		&statusStr, &startedAt, &endedAt, &s.Label, &metadataJSON,
		&s.FilesChanged, &s.EventCount,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("session not found")
		}
		return nil, fmt.Errorf("scan session: %w", err)
	}

	fillSessionFields(&s, statusStr, startedAt, endedAt, metadataJSON)
	return &s, nil
}

func scanSessionFromRows(rows *sql.Rows) (*schema.Session, error) {
	var s schema.Session
	var statusStr, metadataJSON string
	var startedAt, endedAt int64

	err := rows.Scan(
		&s.SessionID, &s.ToolName, &s.PID, &s.WorkingDirectory,
		&statusStr, &startedAt, &endedAt, &s.Label, &metadataJSON,
		&s.FilesChanged, &s.EventCount,
	)
	if err != nil {
		return nil, fmt.Errorf("scan session: %w", err)
	}

	fillSessionFields(&s, statusStr, startedAt, endedAt, metadataJSON)
	return &s, nil
}

func fillSessionFields(s *schema.Session, statusStr string, startedAt, endedAt int64, metadataJSON string) {
	switch statusStr {
	case "active":
		s.Status = schema.SessionActive
	case "ended":
		s.Status = schema.SessionEnded
	case "crashed":
		s.Status = schema.SessionCrashed
	}

	if startedAt > 0 {
		s.StartedAt = time.Unix(0, startedAt)
	}
	if endedAt > 0 {
		s.EndedAt = time.Unix(0, endedAt)
	}

	if metadataJSON != "" && metadataJSON != "{}" {
		if err := json.Unmarshal([]byte(metadataJSON), &s.Metadata); err != nil {
			log.Printf("belay: corrupt metadata for session %s: %v", s.SessionID, err)
		}
	}
}

// DeleteEvent removes a single event from the index by its ID.
func (idx *Index) DeleteEvent(eventID string) error {
	result, err := idx.db.Exec("DELETE FROM events WHERE event_id = ?", eventID)
	if err != nil {
		return fmt.Errorf("delete event %s: %w", eventID, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("event not found: %s", eventID)
	}
	return nil
}

// DeleteEventsBatch removes multiple events in a single transaction. Returns the number of events deleted.
func (idx *Index) DeleteEventsBatch(eventIDs []string) (int64, error) {
	if len(eventIDs) == 0 {
		return 0, nil
	}

	tx, err := idx.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("DELETE FROM events WHERE event_id = ?")
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	var total int64
	for _, id := range eventIDs {
		result, err := stmt.Exec(id)
		if err != nil {
			return total, fmt.Errorf("delete event %s: %w", id, err)
		}
		n, _ := result.RowsAffected()
		total += n
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit transaction: %w", err)
	}
	return total, nil
}

// DeleteEventsBefore removes all events with timestamps older than the given cutoff.
// Returns the number of events deleted.
func (idx *Index) DeleteEventsBefore(cutoffNano int64) (int64, error) {
	result, err := idx.db.Exec("DELETE FROM events WHERE timestamp_nano < ?", cutoffNano)
	if err != nil {
		return 0, fmt.Errorf("delete events before cutoff: %w", err)
	}
	return result.RowsAffected()
}

// ActiveContentHashes returns the set of all content hashes referenced by events in the index.
func (idx *Index) ActiveContentHashes() (map[string]bool, error) {
	rows, err := idx.db.Query(`
		SELECT DISTINCT content_hash FROM events WHERE content_hash != ''
		UNION
		SELECT DISTINCT previous_hash FROM events WHERE previous_hash != ''`)
	if err != nil {
		return nil, fmt.Errorf("query active hashes: %w", err)
	}
	defer rows.Close()

	hashes := make(map[string]bool)
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("scan hash: %w", err)
		}
		if h != "" {
			hashes[h] = true
		}
	}
	return hashes, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
