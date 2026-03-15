// Package replay provides session replay, point-in-time snapshots, and unified diff generation.
package replay

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"
)

// SessionResult summarizes the net file changes produced by a session replay.
type SessionResult struct {
	SessionID string
	Files     map[string]*FileChange
	Events    []*schema.Event
}

// FileChange describes the net change to a single file within a session.
type FileChange struct {
	Path        string
	Operation   string
	ContentHash string
	OldHash     string
	EventCount  int
}

// Snapshot represents the reconstructed state of all tracked files at a point in time.
type Snapshot struct {
	Timestamp int64
	Files     map[string]*FileState
}

// FileState holds the content hash and metadata for a file in a snapshot.
type FileState struct {
	Path        string
	ContentHash string
	Size        int64
	LastEvent   string
}

const maxReplayEvents = 100000

// maxDiffLines is the maximum number of lines per file for full LCS diff.
// Files exceeding this produce a summary instead to avoid excessive memory/CPU.
const maxDiffLines = 10000

// maxLCSCells is the safety limit for the LCS matrix (n*m cells).
// If exceeded, computeEdits falls back to a simple delete-all/insert-all diff.
const maxLCSCells = 100_000_000

// ReplaySession computes the net file changes for a session by replaying its events.
func ReplaySession(idx *index.Index, objStore *store.Store, sessionID string) (*SessionResult, error) {
	events, err := idx.QueryEvents(&index.Query{
		Sessions:  []string{sessionID},
		OrderDesc: false,
		Limit:     maxReplayEvents + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("query session events: %w", err)
	}

	if len(events) == 0 {
		return nil, fmt.Errorf("no events found for session: %s", sessionID)
	}

	if len(events) > maxReplayEvents {
		return nil, fmt.Errorf("session %s has more than %d events, replay aborted", sessionID, maxReplayEvents)
	}

	result := &SessionResult{
		SessionID: sessionID,
		Files:     make(map[string]*FileChange),
		Events:    events,
	}

	type fileTracker struct {
		firstOp   schema.Operation
		lastOp    schema.Operation
		firstHash string
		lastHash  string
		count     int
	}
	trackers := make(map[string]*fileTracker)

	for _, ev := range events {
		path := ev.FilePath
		t, exists := trackers[path]
		if !exists {
			t = &fileTracker{
				firstOp:   ev.Op,
				firstHash: ev.PreviousHash,
			}
			trackers[path] = t
		}
		t.lastOp = ev.Op
		t.lastHash = ev.ContentHash
		t.count++
	}

	for path, t := range trackers {
		netOp := computeNetOperation(t.firstOp, t.lastOp, t.firstHash, t.lastHash)
		if netOp == "" {
			continue
		}

		result.Files[path] = &FileChange{
			Path:        path,
			Operation:   netOp,
			ContentHash: t.lastHash,
			OldHash:     t.firstHash,
			EventCount:  t.count,
		}
	}

	return result, nil
}

func computeNetOperation(firstOp, lastOp schema.Operation, firstHash, lastHash string) string {
	if lastOp == schema.OpDelete {
		if firstOp == schema.OpCreate {
			return ""
		}
		return "delete"
	}

	if firstOp == schema.OpCreate {
		return "create"
	}

	if firstHash == lastHash {
		return ""
	}

	return "modify"
}

// SnapshotAt reconstructs the state of all tracked files at the given nanosecond timestamp.
func SnapshotAt(idx *index.Index, timestampNano int64) (*Snapshot, error) {
	events, err := idx.QueryEvents(&index.Query{
		Until:     timestampNano,
		OrderDesc: false,
	})
	if err != nil {
		return nil, fmt.Errorf("query events for snapshot: %w", err)
	}

	snapshot := &Snapshot{
		Timestamp: timestampNano,
		Files:     make(map[string]*FileState),
	}

	for _, ev := range events {
		if ev.Op == schema.OpDelete {
			delete(snapshot.Files, ev.FilePath)
		} else {
			snapshot.Files[ev.FilePath] = &FileState{
				Path:        ev.FilePath,
				ContentHash: ev.ContentHash,
				Size:        ev.ContentSize,
				LastEvent:   ev.EventID,
			}
		}

		if ev.Op == schema.OpRename && ev.OldPath != "" {
			delete(snapshot.Files, ev.OldPath)
		}
	}

	return snapshot, nil
}

// GeneratePatch produces a unified diff patch for all net changes in a session.
func GeneratePatch(idx *index.Index, objStore *store.Store, sessionID string) (string, error) {
	result, err := ReplaySession(idx, objStore, sessionID)
	if err != nil {
		return "", fmt.Errorf("replay session for patch: %w", err)
	}

	paths := make([]string, 0, len(result.Files))
	for path := range result.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var patch strings.Builder

	for _, path := range paths {
		fc := result.Files[path]

		var oldContent, newContent string

		switch fc.Operation {
		case "create":
			oldContent = ""
			content, err := objStore.Get(fc.ContentHash)
			if err != nil {
				return "", fmt.Errorf("get content for %s: %w", path, err)
			}
			newContent = string(content)

		case "delete":
			content, err := objStore.Get(fc.OldHash)
			if err != nil {
				return "", fmt.Errorf("get old content for %s: %w", path, err)
			}
			oldContent = string(content)
			newContent = ""

		case "modify":
			oldData, err := objStore.Get(fc.OldHash)
			if err != nil {
				return "", fmt.Errorf("get old content for %s: %w", path, err)
			}
			oldContent = string(oldData)

			newData, err := objStore.Get(fc.ContentHash)
			if err != nil {
				return "", fmt.Errorf("get new content for %s: %w", path, err)
			}
			newContent = string(newData)
		}

		diff := unifiedDiff("a/"+path, "b/"+path, oldContent, newContent)
		if diff != "" {
			patch.WriteString(diff)
		}
	}

	return patch.String(), nil
}

// ApplySession writes a session's net changes to the target directory.
func ApplySession(idx *index.Index, objStore *store.Store, sessionID string, targetDir string, dryRun bool) ([]string, error) {
	result, err := ReplaySession(idx, objStore, sessionID)
	if err != nil {
		return nil, fmt.Errorf("replay session for apply: %w", err)
	}

	paths := make([]string, 0, len(result.Files))
	for path := range result.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var affected []string

	for _, path := range paths {
		fc := result.Files[path]
		fullPath := filepath.Join(targetDir, path)
		affected = append(affected, path)

		if dryRun {
			continue
		}

		switch fc.Operation {
		case "create", "modify":
			content, err := objStore.Get(fc.ContentHash)
			if err != nil {
				return nil, fmt.Errorf("get content for %s: %w", path, err)
			}

			dir := filepath.Dir(fullPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("create directory %s: %w", dir, err)
			}

			if err := os.WriteFile(fullPath, content, 0644); err != nil {
				return nil, fmt.Errorf("write file %s: %w", fullPath, err)
			}

		case "delete":
			if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("delete file %s: %w", fullPath, err)
			}

			cleanEmptyParents(filepath.Dir(fullPath), targetDir)
		}
	}

	return affected, nil
}

func cleanEmptyParents(dir, root string) {
	for dir != root && dir != "." && dir != "/" {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			break
		}
		os.Remove(dir)
		dir = filepath.Dir(dir)
	}
}


func unifiedDiff(oldName, newName, oldText, newText string) string {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)

	// Guard: skip full LCS diff for large files
	if len(oldLines) > maxDiffLines || len(newLines) > maxDiffLines {
		return fmt.Sprintf("--- %s\n+++ %s\n@@ Large file - diff skipped (old: %d lines, new: %d lines, limit: %d) @@\n",
			oldName, newName, len(oldLines), len(newLines), maxDiffLines)
	}

	edits := computeEdits(oldLines, newLines)

	if len(edits) == 0 {
		return ""
	}

	var buf strings.Builder

	buf.WriteString("--- ")
	buf.WriteString(oldName)
	buf.WriteString("\n")
	buf.WriteString("+++ ")
	buf.WriteString(newName)
	buf.WriteString("\n")

	hunks := groupHunks(edits, len(oldLines), len(newLines), 3)
	for _, hunk := range hunks {
		writeHunk(&buf, hunk, oldLines, newLines)
	}

	return buf.String()
}

type editOp uint8

const (
	editEqual  editOp = iota
	editDelete
	editInsert
)

type edit struct {
	op      editOp
	oldIdx  int
	newIdx  int
	content string
}

type hunk struct {
	oldStart int
	oldCount int
	newStart int
	newCount int
	edits    []edit
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func computeEdits(oldLines, newLines []string) []edit {
	n := len(oldLines)
	m := len(newLines)

	// Safety net: if the LCS matrix would exceed maxLCSCells, fall back to
	// a simple delete-all-old + insert-all-new edit list to prevent OOM.
	if int64(n)*int64(m) > maxLCSCells {
		var edits []edit
		for i, line := range oldLines {
			edits = append(edits, edit{op: editDelete, oldIdx: i, newIdx: -1, content: line})
		}
		for j, line := range newLines {
			edits = append(edits, edit{op: editInsert, oldIdx: -1, newIdx: j, content: line})
		}
		return edits
	}

	lcs := make([][]int, n+1)
	for i := 0; i <= n; i++ {
		lcs[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if oldLines[i-1] == newLines[j-1] {
				lcs[i][j] = lcs[i-1][j-1] + 1
			} else if lcs[i-1][j] >= lcs[i][j-1] {
				lcs[i][j] = lcs[i-1][j]
			} else {
				lcs[i][j] = lcs[i][j-1]
			}
		}
	}

	var edits []edit
	i, j := n, m
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			edits = append(edits, edit{
				op:      editEqual,
				oldIdx:  i - 1,
				newIdx:  j - 1,
				content: oldLines[i-1],
			})
			i--
			j--
		} else if j > 0 && (i == 0 || lcs[i][j-1] >= lcs[i-1][j]) {
			edits = append(edits, edit{
				op:      editInsert,
				oldIdx:  -1,
				newIdx:  j - 1,
				content: newLines[j-1],
			})
			j--
		} else {
			edits = append(edits, edit{
				op:      editDelete,
				oldIdx:  i - 1,
				newIdx:  -1,
				content: oldLines[i-1],
			})
			i--
		}
	}

	for left, right := 0, len(edits)-1; left < right; left, right = left+1, right-1 {
		edits[left], edits[right] = edits[right], edits[left]
	}

	return edits
}

func groupHunks(edits []edit, oldLen, newLen, context int) []hunk {
	if len(edits) == 0 {
		return nil
	}

	type changeRange struct {
		startIdx int
		endIdx   int
	}

	var changes []changeRange
	inChange := false
	var cur changeRange

	for i, e := range edits {
		if e.op != editEqual {
			if !inChange {
				cur.startIdx = i
				inChange = true
			}
			cur.endIdx = i + 1
		} else {
			if inChange {
				changes = append(changes, cur)
				inChange = false
			}
		}
	}
	if inChange {
		changes = append(changes, cur)
	}

	if len(changes) == 0 {
		return nil
	}

	type hunkRange struct {
		editStart int
		editEnd   int
	}

	var hunkRanges []hunkRange
	current := hunkRange{
		editStart: max(0, changes[0].startIdx-context),
		editEnd:   min(len(edits), changes[0].endIdx+context),
	}

	for i := 1; i < len(changes); i++ {
		nextStart := max(0, changes[i].startIdx-context)
		nextEnd := min(len(edits), changes[i].endIdx+context)

		if nextStart <= current.editEnd {
			current.editEnd = nextEnd
		} else {
			hunkRanges = append(hunkRanges, current)
			current = hunkRange{editStart: nextStart, editEnd: nextEnd}
		}
	}
	hunkRanges = append(hunkRanges, current)

	var hunks []hunk
	for _, hr := range hunkRanges {
		h := hunk{}
		h.edits = edits[hr.editStart:hr.editEnd]

		oldCount := 0
		newCount := 0
		oldStart := -1
		newStart := -1

		for _, e := range h.edits {
			switch e.op {
			case editEqual:
				if oldStart == -1 {
					oldStart = e.oldIdx
				}
				if newStart == -1 {
					newStart = e.newIdx
				}
				oldCount++
				newCount++
			case editDelete:
				if oldStart == -1 {
					oldStart = e.oldIdx
				}
				oldCount++
			case editInsert:
				if newStart == -1 {
					newStart = e.newIdx
				}
				newCount++
			}
		}

		if oldStart < 0 {
			oldStart = 0
		}
		if newStart < 0 {
			newStart = 0
		}
		h.oldStart = oldStart + 1
		h.newStart = newStart + 1

		if oldCount == 0 {
			h.oldStart = oldStart
		}
		if newCount == 0 {
			h.newStart = newStart
		}

		h.oldCount = oldCount
		h.newCount = newCount

		hunks = append(hunks, h)
	}

	return hunks
}

func writeHunk(buf *strings.Builder, h hunk, oldLines, newLines []string) {
	buf.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
		h.oldStart, h.oldCount, h.newStart, h.newCount))

	for _, e := range h.edits {
		switch e.op {
		case editEqual:
			buf.WriteString(" ")
			buf.WriteString(e.content)
			buf.WriteString("\n")
		case editDelete:
			buf.WriteString("-")
			buf.WriteString(e.content)
			buf.WriteString("\n")
		case editInsert:
			buf.WriteString("+")
			buf.WriteString(e.content)
			buf.WriteString("\n")
		}
	}
}
