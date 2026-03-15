// Package conflict detects overlapping file modifications across concurrent AI sessions.
package conflict

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"time"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
)

// Severity indicates how critical a conflict is, based on timing and operation types.
type Severity int

const (
	// SeverityLow indicates modifications with large time gaps between sessions.
	SeverityLow Severity = iota + 1
	// SeverityMedium indicates modifications within 30 seconds across sessions.
	SeverityMedium
	// SeverityHigh indicates conflicting deletes and writes across sessions.
	SeverityHigh
	// SeverityCritical indicates modifications within 5 seconds across sessions.
	SeverityCritical
)

// String returns the uppercase severity level name.
func (s Severity) String() string {
	switch s {
	case SeverityLow:
		return "LOW"
	case SeverityMedium:
		return "MEDIUM"
	case SeverityHigh:
		return "HIGH"
	case SeverityCritical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

// Conflict represents overlapping modifications to a file by multiple sessions.
type Conflict struct {
	ID         string
	FilePath   string
	Severity   Severity
	Events     []*schema.Event
	Sessions   []string
	Window     time.Duration
	DetectedAt time.Time
	Resolved   bool
	Resolution string
}

const (
	criticalThreshold = 5 * time.Second
	mediumThreshold   = 30 * time.Second
)

// Detector analyzes events to find cross-session file modification conflicts.
type Detector struct {
	idx        *index.Index
	windowSize time.Duration
}

// NewDetector creates a Detector with the given time window for grouping concurrent changes.
func NewDetector(idx *index.Index, windowSize time.Duration) *Detector {
	if windowSize <= 0 {
		windowSize = 60 * time.Second
	}
	return &Detector{
		idx:        idx,
		windowSize: windowSize,
	}
}

// DetectSince scans all events after the given time for cross-session conflicts.
func (d *Detector) DetectSince(since time.Time) ([]*Conflict, error) {
	events, err := d.idx.QueryEvents(&index.Query{
		Since:     since.UnixNano(),
		OrderDesc: false,
	})
	if err != nil {
		return nil, fmt.Errorf("query events since %s: %w", since.Format(time.RFC3339), err)
	}

	byFile := groupByFile(events)

	var conflicts []*Conflict
	for filePath, fileEvents := range byFile {
		found := d.detectInGroup(filePath, fileEvents)
		conflicts = append(conflicts, found...)
	}

	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].DetectedAt.Before(conflicts[j].DetectedAt)
	})

	return conflicts, nil
}

// DetectForFile checks a specific file for cross-session conflicts since the given time.
func (d *Detector) DetectForFile(filePath string, since time.Time) ([]*Conflict, error) {
	events, err := d.idx.QueryEvents(&index.Query{
		Since:     since.UnixNano(),
		FilePaths: []string{filePath},
		OrderDesc: false,
	})
	if err != nil {
		return nil, fmt.Errorf("query events for %s: %w", filePath, err)
	}

	return d.detectInGroup(filePath, events), nil
}

// DetectRealtime checks whether a new event conflicts with recent events from other sessions.
func (d *Detector) DetectRealtime(event *schema.Event) (*Conflict, error) {
	if event.SessionID == "" {
		return nil, nil
	}

	windowStart := event.Timestamp().Add(-d.windowSize)

	recent, err := d.idx.QueryEvents(&index.Query{
		Since:     windowStart.UnixNano(),
		Until:     event.TimestampNano,
		FilePaths: []string{event.FilePath},
		OrderDesc: false,
	})
	if err != nil {
		return nil, fmt.Errorf("query recent events for %s: %w", event.FilePath, err)
	}

	var conflicting []*schema.Event
	for _, e := range recent {
		if e.EventID == event.EventID {
			continue
		}
		if e.SessionID == "" || e.SessionID == event.SessionID {
			continue
		}
		conflicting = append(conflicting, e)
	}

	if len(conflicting) == 0 {
		return nil, nil
	}

	all := append(conflicting, event)

	sort.Slice(all, func(i, j int) bool {
		return all[i].TimestampNano < all[j].TimestampNano
	})

	severity := classifySeverity(all)
	window := all[len(all)-1].Timestamp().Sub(all[0].Timestamp())

	c := &Conflict{
		ID:         generateID(event.FilePath, all),
		FilePath:   event.FilePath,
		Severity:   severity,
		Events:     all,
		Sessions:   uniqueSessions(all),
		Window:     window,
		DetectedAt: time.Now(),
	}

	return c, nil
}

func (d *Detector) detectInGroup(filePath string, events []*schema.Event) []*Conflict {
	if len(events) < 2 {
		return nil
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].TimestampNano < events[j].TimestampNano
	})

	var conflicts []*Conflict

	n := len(events)
	visited := make([]bool, n)

	for i := 0; i < n; i++ {
		if visited[i] {
			continue
		}
		if events[i].SessionID == "" {
			continue
		}

		group := []*schema.Event{events[i]}
		hasCrossSession := false

		for j := i + 1; j < n; j++ {
			gap := events[j].Timestamp().Sub(events[i].Timestamp())
			if gap > d.windowSize {
				break
			}
			if events[j].SessionID == "" {
				continue
			}
			if events[j].SessionID != events[i].SessionID {
				hasCrossSession = true
			}
			group = append(group, events[j])
			visited[j] = true
		}

		if !hasCrossSession || len(group) < 2 {
			continue
		}

		sessions := uniqueSessions(group)
		if len(sessions) < 2 {
			continue
		}

		severity := classifySeverity(group)
		window := group[len(group)-1].Timestamp().Sub(group[0].Timestamp())

		c := &Conflict{
			ID:         generateID(filePath, group),
			FilePath:   filePath,
			Severity:   severity,
			Events:     group,
			Sessions:   sessions,
			Window:     window,
			DetectedAt: time.Now(),
		}
		conflicts = append(conflicts, c)
	}

	return conflicts
}

func classifySeverity(events []*schema.Event) Severity {
	if len(events) < 2 {
		return SeverityLow
	}

	hasDelete := false
	hasWriteOp := false
	for _, e := range events {
		if e.Op == schema.OpDelete {
			hasDelete = true
		}
		if e.Op == schema.OpCreate || e.Op == schema.OpModify {
			hasWriteOp = true
		}
	}

	minGap := time.Duration(1<<63 - 1)
	for i := 0; i < len(events); i++ {
		for j := i + 1; j < len(events); j++ {
			if events[i].SessionID == events[j].SessionID {
				continue
			}
			gap := events[j].Timestamp().Sub(events[i].Timestamp())
			if gap < 0 {
				gap = -gap
			}
			if gap < minGap {
				minGap = gap
			}
		}
	}

	if minGap <= criticalThreshold {
		return SeverityCritical
	}

	if hasDelete && hasWriteOp {
		return SeverityHigh
	}

	if minGap <= mediumThreshold {
		return SeverityMedium
	}

	return SeverityLow
}

func generateID(filePath string, events []*schema.Event) string {
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.EventID
	}
	sort.Strings(ids)

	h := sha256.New()
	h.Write([]byte(filePath))
	for _, id := range ids {
		h.Write([]byte(id))
	}
	sum := h.Sum(nil)
	return fmt.Sprintf("cf-%x", sum[:8])
}

func uniqueSessions(events []*schema.Event) []string {
	seen := make(map[string]struct{})
	for _, e := range events {
		if e.SessionID != "" {
			seen[e.SessionID] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func groupByFile(events []*schema.Event) map[string][]*schema.Event {
	m := make(map[string][]*schema.Event)
	for _, e := range events {
		m[e.FilePath] = append(m[e.FilePath], e)
	}
	return m
}
