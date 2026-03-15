// Package schema defines the core data types for Belay events, sessions, and serialization.
package schema

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"time"
)

// SchemaVersion is the current binary encoding version for event frames.
const SchemaVersion = 1

// Operation represents the type of filesystem change captured by an event.
type Operation uint8

const (
	// OpCreate indicates a new file was created.
	OpCreate Operation = iota + 1
	// OpModify indicates an existing file was modified.
	OpModify
	// OpDelete indicates a file was deleted.
	OpDelete
	// OpRename indicates a file was renamed.
	OpRename
)

// String returns the uppercase string representation of the Operation.
func (o Operation) String() string {
	switch o {
	case OpCreate:
		return "CREATE"
	case OpModify:
		return "MODIFY"
	case OpDelete:
		return "DELETE"
	case OpRename:
		return "RENAME"
	default:
		return "UNKNOWN"
	}
}

// ParseOperation converts a string like "CREATE" or "create" into an Operation value.
func ParseOperation(s string) (Operation, error) {
	switch s {
	case "CREATE", "create":
		return OpCreate, nil
	case "MODIFY", "modify":
		return OpModify, nil
	case "DELETE", "delete":
		return OpDelete, nil
	case "RENAME", "rename":
		return OpRename, nil
	default:
		return 0, fmt.Errorf("unknown operation: %s", s)
	}
}

// MarshalJSON encodes the Operation as its string representation.
func (o Operation) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.String())
}

// UnmarshalJSON decodes an Operation from its JSON string representation.
func (o *Operation) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseOperation(s)
	if err != nil {
		return err
	}
	*o = parsed
	return nil
}

type AttributionMethod uint8

const (
	AttrNone AttributionMethod = iota
	AttrPID
	AttrTemporal
	AttrHeuristic
	AttrManual
	AttrHook
	AttrGit
)

func (a AttributionMethod) String() string {
	switch a {
	case AttrNone:
		return "none"
	case AttrPID:
		return "pid"
	case AttrTemporal:
		return "temporal"
	case AttrHeuristic:
		return "heuristic"
	case AttrManual:
		return "manual"
	case AttrHook:
		return "hook"
	case AttrGit:
		return "git"
	default:
		return "unknown"
	}
}

func ParseAttributionMethod(s string) AttributionMethod {
	switch s {
	case "pid":
		return AttrPID
	case "temporal":
		return AttrTemporal
	case "heuristic":
		return AttrHeuristic
	case "manual":
		return AttrManual
	case "hook":
		return AttrHook
	case "git":
		return AttrGit
	default:
		return AttrNone
	}
}

// Event represents a single filesystem change captured by Belay.
type Event struct {
	EventID string `json:"event_id"`

	Version uint16 `json:"version"`

	TimestampNano int64 `json:"timestamp_nano"`

	FilePath string `json:"file_path"`

	Op Operation `json:"operation"`

	ContentHash string `json:"content_hash,omitempty"`

	PreviousHash string `json:"previous_hash,omitempty"`

	ContentSize int64 `json:"content_size"`

	OldPath string `json:"old_path,omitempty"`

	SessionID string `json:"session_id,omitempty"`

	Attribution AttributionMethod `json:"attribution_method"`

	AttributionConfidence float32 `json:"attribution_confidence"`

	Metadata map[string]string `json:"metadata,omitempty"`

	IsConflict bool `json:"is_conflict,omitempty"`
}

// Timestamp returns the event's timestamp as a time.Time.
func (e *Event) Timestamp() time.Time {
	return time.Unix(0, e.TimestampNano)
}

// SetTimestamp sets the event's timestamp from a time.Time.
func (e *Event) SetTimestamp(t time.Time) {
	e.TimestampNano = t.UnixNano()
}

// EventJSON is the JSON-serializable representation of an Event for API responses.
type EventJSON struct {
	EventID               string            `json:"event_id"`
	Timestamp             string            `json:"timestamp"`
	TimestampNano         int64             `json:"timestamp_nano"`
	FilePath              string            `json:"file_path"`
	Operation             string            `json:"operation"`
	ContentHash           string            `json:"content_hash,omitempty"`
	PreviousHash          string            `json:"previous_hash,omitempty"`
	ContentSize           int64             `json:"content_size"`
	OldPath               string            `json:"old_path,omitempty"`
	SessionID             string            `json:"session_id,omitempty"`
	AttributionMethod     string            `json:"attribution_method"`
	AttributionConfidence float32           `json:"attribution_confidence"`
	Metadata              map[string]string `json:"metadata,omitempty"`
	IsConflict            bool              `json:"is_conflict,omitempty"`
}

// ToJSON converts the Event to its JSON-friendly representation.
func (e *Event) ToJSON() EventJSON {
	return EventJSON{
		EventID:               e.EventID,
		Timestamp:             e.Timestamp().Format(time.RFC3339Nano),
		TimestampNano:         e.TimestampNano,
		FilePath:              e.FilePath,
		Operation:             e.Op.String(),
		ContentHash:           e.ContentHash,
		PreviousHash:          e.PreviousHash,
		ContentSize:           e.ContentSize,
		OldPath:               e.OldPath,
		SessionID:             e.SessionID,
		AttributionMethod:     e.Attribution.String(),
		AttributionConfidence: e.AttributionConfidence,
		Metadata:              e.Metadata,
		IsConflict:            e.IsConflict,
	}
}

// MarshalBinary encodes the Event into a length-prefixed, checksummed binary frame.
func (e *Event) MarshalBinary() ([]byte, error) {
	inner, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}

	totalLen := 4 + 2 + len(inner) + 4
	buf := make([]byte, totalLen)

	binary.BigEndian.PutUint32(buf[0:4], uint32(totalLen))

	binary.BigEndian.PutUint16(buf[4:6], SchemaVersion)

	copy(buf[6:6+len(inner)], inner)

	checksum := checksumBytes(buf[4 : 6+len(inner)])
	binary.BigEndian.PutUint32(buf[6+len(inner):], checksum)

	return buf, nil
}

// UnmarshalBinaryFrame reads a single binary event frame from the reader, returning
// the decoded Event and the total bytes consumed.
func UnmarshalBinaryFrame(r io.Reader) (*Event, int, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, 0, fmt.Errorf("read frame length: %w", err)
	}
	totalLen := binary.BigEndian.Uint32(lenBuf[:])

	if totalLen < 10 {
		return nil, 0, fmt.Errorf("frame too short: %d bytes", totalLen)
	}
	if totalLen > 100*1024*1024 {
		return nil, 0, fmt.Errorf("frame too large: %d bytes", totalLen)
	}

	remaining := make([]byte, totalLen-4)
	if _, err := io.ReadFull(r, remaining); err != nil {
		return nil, 0, fmt.Errorf("read frame body: %w", err)
	}

	version := binary.BigEndian.Uint16(remaining[0:2])
	if version != SchemaVersion {
		return nil, 0, fmt.Errorf("unsupported schema version: %d (expected %d)", version, SchemaVersion)
	}

	dataEnd := len(remaining) - 4
	expectedChecksum := binary.BigEndian.Uint32(remaining[dataEnd:])
	actualChecksum := checksumBytes(remaining[:dataEnd])
	if expectedChecksum != actualChecksum {
		return nil, 0, fmt.Errorf("checksum mismatch: expected %x, got %x", expectedChecksum, actualChecksum)
	}

	var event Event
	if err := json.Unmarshal(remaining[2:dataEnd], &event); err != nil {
		return nil, 0, fmt.Errorf("unmarshal event: %w", err)
	}

	return &event, int(totalLen), nil
}

func checksumBytes(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}

// ContentHashForBytes returns the hex-encoded SHA-256 hash of the given data.
func ContentHashForBytes(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
