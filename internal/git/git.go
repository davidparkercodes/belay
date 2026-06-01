// Package git provides a bridge between Belay sessions and git, supporting session-based
// commits, stashing, and git history import.
package git

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/schema"
	"github.com/davidparkercodes/belay/internal/store"
)

// CommitOptions configures how a session is committed to git.
type CommitOptions struct {
	SessionID  string
	Message    string
	Files      []string
	DryRun     bool
	NoMetadata bool
}

// CommitResult contains the outcome of a git commit operation.
type CommitResult struct {
	Hash          string
	FilesAdded    int
	FilesModified int
	FilesDeleted  int
	Message       string
}

// StashInfo describes a session stash stored in the .belay/stashes directory.
type StashInfo struct {
	SessionID string    `json:"session_id"`
	StashDir  string    `json:"stash_dir"`
	Files     []string  `json:"files"`
	CreatedAt time.Time `json:"created_at"`
}

// ImportOptions configures how git history is imported into Belay.
type ImportOptions struct {
	Since       string
	ProjectRoot string
}

// ImportResult contains the outcome of a git history import.
type ImportResult struct {
	CommitsImported int
	EventsCreated   int
}

type fileChange struct {
	filePath    string
	op          schema.Operation
	contentHash string
}

func gitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// IsGitRepo reports whether the given directory is inside a git repository.
func IsGitRepo(dir string) bool {
	_, err := gitCmd(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

// EnsureBelayIgnored adds .belay/ to .gitignore if not already present.
func EnsureBelayIgnored(projectRoot string) error {
	gitignorePath := filepath.Join(projectRoot, ".gitignore")

	content, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read .gitignore: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == ".belay/" || trimmed == ".belay" {
			return nil
		}
	}

	var newContent string
	if len(content) > 0 && !strings.HasSuffix(string(content), "\n") {
		newContent = string(content) + "\n.belay/\n"
	} else {
		newContent = string(content) + ".belay/\n"
	}

	if err := os.WriteFile(gitignorePath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}

	return nil
}

// CommitSession creates a git commit from a session's net file changes with Belay trailers.
func CommitSession(idx *index.Index, objStore *store.Store, projectRoot string, opts CommitOptions) (*CommitResult, error) {
	if opts.SessionID == "" {
		return nil, fmt.Errorf("session ID is required")
	}

	if !IsGitRepo(projectRoot) {
		return nil, fmt.Errorf("%s is not a git repository", projectRoot)
	}

	events, err := idx.QueryEvents(&index.Query{
		Sessions:  []string{opts.SessionID},
		OrderDesc: false,
	})
	if err != nil {
		return nil, fmt.Errorf("query session events: %w", err)
	}

	if len(events) == 0 {
		return nil, fmt.Errorf("no events found for session %s", opts.SessionID)
	}

	changes := computeNetChanges(events)

	if len(opts.Files) > 0 {
		fileSet := make(map[string]bool, len(opts.Files))
		for _, f := range opts.Files {
			fileSet[f] = true
		}
		var filtered []fileChange
		for _, c := range changes {
			if fileSet[c.filePath] {
				filtered = append(filtered, c)
			}
		}
		changes = filtered
	}

	if len(changes) == 0 {
		return nil, fmt.Errorf("no net changes to commit for session %s", opts.SessionID)
	}

	result := &CommitResult{}
	for _, c := range changes {
		switch c.op {
		case schema.OpCreate:
			result.FilesAdded++
		case schema.OpModify:
			result.FilesModified++
		case schema.OpDelete:
			result.FilesDeleted++
		}
	}

	result.Message = opts.Message
	if result.Message == "" {
		result.Message = buildCommitMessage(opts.SessionID, result)
	}
	if !opts.NoMetadata {
		result.Message = appendBelayTrailers(result.Message, opts.SessionID, events)
	}

	if opts.DryRun {
		return result, nil
	}

	for _, c := range changes {
		absPath := filepath.Join(projectRoot, c.filePath)

		switch c.op {
		case schema.OpCreate, schema.OpModify:
			data, err := objStore.Get(c.contentHash)
			if err != nil {
				return nil, fmt.Errorf("get object %s for %s: %w", c.contentHash, c.filePath, err)
			}

			if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
				return nil, fmt.Errorf("create directory for %s: %w", c.filePath, err)
			}

			if err := os.WriteFile(absPath, data, 0644); err != nil {
				return nil, fmt.Errorf("write file %s: %w", c.filePath, err)
			}

			if _, err := gitCmd(projectRoot, "add", c.filePath); err != nil {
				return nil, fmt.Errorf("git add %s: %w", c.filePath, err)
			}

		case schema.OpDelete:
			if _, err := gitCmd(projectRoot, "rm", "-f", "--", c.filePath); err != nil {
				if _, err2 := gitCmd(projectRoot, "rm", "--cached", "-f", "--", c.filePath); err2 != nil {
					return nil, fmt.Errorf("git rm %s: %w", c.filePath, err)
				}
			}
		}
	}

	hash, err := gitCmd(projectRoot, "commit", "-m", result.Message)
	if err != nil {
		return nil, fmt.Errorf("git commit: %w", err)
	}

	result.Hash = extractCommitHash(projectRoot)
	if result.Hash == "" {
		result.Hash = hash
	}

	return result, nil
}

// StashSession saves a session's changes aside and reverts the working tree to pre-session state.
func StashSession(idx *index.Index, objStore *store.Store, projectRoot string, belayDir string, sessionID string) (*StashInfo, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is required")
	}

	events, err := idx.QueryEvents(&index.Query{
		Sessions:  []string{sessionID},
		OrderDesc: false,
	})
	if err != nil {
		return nil, fmt.Errorf("query session events: %w", err)
	}

	if len(events) == 0 {
		return nil, fmt.Errorf("no events found for session %s", sessionID)
	}

	changes := computeNetChanges(events)
	if len(changes) == 0 {
		return nil, fmt.Errorf("no net changes to stash for session %s", sessionID)
	}

	stashDir := filepath.Join(belayDir, "stashes", sessionID)
	if err := os.MkdirAll(stashDir, 0755); err != nil {
		return nil, fmt.Errorf("create stash dir: %w", err)
	}

	var stashedFiles []string
	stashManifest := make(map[string]stashEntry)

	for _, c := range changes {
		absPath := filepath.Join(projectRoot, c.filePath)
		stashedFiles = append(stashedFiles, c.filePath)

		entry := stashEntry{
			FilePath: c.filePath,
			Op:       c.op.String(),
		}

		switch c.op {
		case schema.OpCreate, schema.OpModify:
			currentData, err := os.ReadFile(absPath)
			if err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("read file for stash %s: %w", c.filePath, err)
			}
			if err == nil {
				entry.ContentHash = c.contentHash
				stashFilePath := filepath.Join(stashDir, "files", c.filePath)
				if err := os.MkdirAll(filepath.Dir(stashFilePath), 0755); err != nil {
					return nil, fmt.Errorf("create stash file dir: %w", err)
				}
				if err := os.WriteFile(stashFilePath, currentData, 0644); err != nil {
					return nil, fmt.Errorf("write stash file: %w", err)
				}
			}

		case schema.OpDelete:
			entry.ContentHash = ""
		}

		stashManifest[c.filePath] = entry
	}

	for _, c := range changes {
		absPath := filepath.Join(projectRoot, c.filePath)

		switch c.op {
		case schema.OpCreate:
			os.Remove(absPath)

		case schema.OpModify:
			prevHash := findPreviousHash(events, c.filePath)
			if prevHash != "" {
				data, err := objStore.Get(prevHash)
				if err != nil {
					return nil, fmt.Errorf("get previous content for %s: %w", c.filePath, err)
				}
				if err := os.WriteFile(absPath, data, 0644); err != nil {
					return nil, fmt.Errorf("restore file %s: %w", c.filePath, err)
				}
			}

		case schema.OpDelete:
			prevHash := findPreviousHash(events, c.filePath)
			if prevHash != "" {
				data, err := objStore.Get(prevHash)
				if err != nil {
					return nil, fmt.Errorf("get deleted content for %s: %w", c.filePath, err)
				}
				if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
					return nil, fmt.Errorf("create dir for restore %s: %w", c.filePath, err)
				}
				if err := os.WriteFile(absPath, data, 0644); err != nil {
					return nil, fmt.Errorf("restore deleted file %s: %w", c.filePath, err)
				}
			}
		}
	}

	info := &StashInfo{
		SessionID: sessionID,
		StashDir:  stashDir,
		Files:     stashedFiles,
		CreatedAt: time.Now(),
	}

	manifestData := stashManifestFile{
		SessionID: sessionID,
		CreatedAt: info.CreatedAt,
		Entries:   stashManifest,
	}
	manifestJSON, err := json.MarshalIndent(manifestData, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal stash manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stashDir, "manifest.json"), manifestJSON, 0644); err != nil {
		return nil, fmt.Errorf("write stash manifest: %w", err)
	}

	return info, nil
}

// PopStash restores a previously stashed session's files and removes the stash.
func PopStash(belayDir string, sessionID string, projectRoot string) error {
	stashDir := filepath.Join(belayDir, "stashes", sessionID)

	manifestPath := filepath.Join(stashDir, "manifest.json")
	manifestJSON, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read stash manifest: %w", err)
	}

	var manifest stashManifestFile
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return fmt.Errorf("parse stash manifest: %w", err)
	}

	for filePath, entry := range manifest.Entries {
		absPath := filepath.Join(projectRoot, filePath)

		switch entry.Op {
		case "CREATE", "MODIFY":
			stashFilePath := filepath.Join(stashDir, "files", filePath)
			data, err := os.ReadFile(stashFilePath)
			if err != nil {
				return fmt.Errorf("read stashed file %s: %w", filePath, err)
			}
			if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
				return fmt.Errorf("create dir for %s: %w", filePath, err)
			}
			if err := os.WriteFile(absPath, data, 0644); err != nil {
				return fmt.Errorf("restore stashed file %s: %w", filePath, err)
			}

		case "DELETE":
			os.Remove(absPath)
		}
	}

	if err := os.RemoveAll(stashDir); err != nil {
		return fmt.Errorf("remove stash dir: %w", err)
	}

	return nil
}

// ListStashes returns all session stashes, sorted by creation time descending.
func ListStashes(belayDir string) ([]*StashInfo, error) {
	stashesDir := filepath.Join(belayDir, "stashes")

	entries, err := os.ReadDir(stashesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read stashes dir: %w", err)
	}

	var stashes []*StashInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		manifestPath := filepath.Join(stashesDir, entry.Name(), "manifest.json")
		manifestJSON, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}

		var manifest stashManifestFile
		if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
			continue
		}

		files := make([]string, 0, len(manifest.Entries))
		for f := range manifest.Entries {
			files = append(files, f)
		}
		sort.Strings(files)

		stashes = append(stashes, &StashInfo{
			SessionID: manifest.SessionID,
			StashDir:  filepath.Join(stashesDir, entry.Name()),
			Files:     files,
			CreatedAt: manifest.CreatedAt,
		})
	}

	sort.Slice(stashes, func(i, j int) bool {
		return stashes[i].CreatedAt.After(stashes[j].CreatedAt)
	})

	return stashes, nil
}

// ImportHistory imports git commit history into Belay as events.
func ImportHistory(idx *index.Index, objStore *store.Store, opts ImportOptions) (*ImportResult, error) {
	if opts.ProjectRoot == "" {
		return nil, fmt.Errorf("project root is required")
	}

	if !IsGitRepo(opts.ProjectRoot) {
		return nil, fmt.Errorf("%s is not a git repository", opts.ProjectRoot)
	}

	logArgs := []string{"log", "--format=%H%x00%aI%x00%an%x00%s", "--reverse"}
	if opts.Since != "" {
		logArgs = append(logArgs, "--since="+opts.Since)
	}

	logOutput, err := gitCmd(opts.ProjectRoot, logArgs...)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	if logOutput == "" {
		return &ImportResult{}, nil
	}

	result := &ImportResult{}
	commitLines := strings.Split(logOutput, "\n")

	for _, line := range commitLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "\x00", 4)
		if len(parts) < 4 {
			continue
		}

		commitHash := parts[0]
		commitDate := parts[1]
		commitAuthor := parts[2]
		commitSubject := parts[3]

		commitTime, err := time.Parse(time.RFC3339, commitDate)
		if err != nil {
			commitTime, err = time.Parse("2006-01-02T15:04:05-07:00", commitDate)
			if err != nil {
				commitTime = time.Now()
			}
		}

		diffOutput, err := gitCmd(opts.ProjectRoot, "diff-tree", "--no-commit-id", "-r", "--name-status", commitHash)
		if err != nil {
			continue
		}

		if diffOutput == "" {
			continue
		}

		eventsForCommit := 0
		diffLines := strings.Split(diffOutput, "\n")

		for _, diffLine := range diffLines {
			diffLine = strings.TrimSpace(diffLine)
			if diffLine == "" {
				continue
			}

			diffParts := strings.SplitN(diffLine, "\t", 2)
			if len(diffParts) < 2 {
				continue
			}

			statusChar := diffParts[0]
			filePath := diffParts[1]

			var op schema.Operation
			switch {
			case strings.HasPrefix(statusChar, "A"):
				op = schema.OpCreate
			case strings.HasPrefix(statusChar, "M"):
				op = schema.OpModify
			case strings.HasPrefix(statusChar, "D"):
				op = schema.OpDelete
			case strings.HasPrefix(statusChar, "R"):
				op = schema.OpRename
			default:
				continue
			}

			event := &schema.Event{
				EventID:       schema.NewEventID(),
				Version:       schema.SchemaVersion,
				TimestampNano: commitTime.UnixNano(),
				FilePath:      filePath,
				Op:            op,
				SessionID:     "git-import",
				Attribution:   schema.AttrManual,
				AttributionConfidence: 1.0,
				Metadata: map[string]string{
					"git_commit": commitHash,
					"git_author": commitAuthor,
					"git_subject": commitSubject,
					"source":     "git-import",
				},
			}

			if op != schema.OpDelete {
				fileContent, err := gitCmd(opts.ProjectRoot, "show", commitHash+":"+filePath)
				if err == nil {
					hash, size, err := objStore.Put([]byte(fileContent))
					if err == nil {
						event.ContentHash = hash
						event.ContentSize = size
					}
				}
			}

			if op == schema.OpModify || op == schema.OpDelete {
				prevContent, err := gitCmd(opts.ProjectRoot, "show", commitHash+"^:"+filePath)
				if err == nil {
					prevHash, _, err := objStore.Put([]byte(prevContent))
					if err == nil {
						event.PreviousHash = prevHash
					}
				}
			}

			if err := idx.IndexEvent(event, "git-import", 0); err != nil {
				return nil, fmt.Errorf("index imported event: %w", err)
			}

			eventsForCommit++
		}

		if eventsForCommit > 0 {
			result.CommitsImported++
			result.EventsCreated += eventsForCommit
		}
	}

	return result, nil
}


type stashEntry struct {
	FilePath    string `json:"file_path"`
	Op          string `json:"operation"`
	ContentHash string `json:"content_hash,omitempty"`
}

type stashManifestFile struct {
	SessionID string                `json:"session_id"`
	CreatedAt time.Time             `json:"created_at"`
	Entries   map[string]stashEntry `json:"entries"`
}

func computeNetChanges(events []*schema.Event) []fileChange {
	type fileState struct {
		firstOp      schema.Operation
		lastOp       schema.Operation
		firstPrevHash string
		lastHash     string
	}

	files := make(map[string]*fileState)

	for _, e := range events {
		state, exists := files[e.FilePath]
		if !exists {
			state = &fileState{
				firstOp:      e.Op,
				firstPrevHash: e.PreviousHash,
			}
			files[e.FilePath] = state
		}
		state.lastOp = e.Op
		state.lastHash = e.ContentHash
	}

	var changes []fileChange
	for filePath, state := range files {
		var netOp schema.Operation
		switch {
		case state.firstOp == schema.OpCreate && state.lastOp == schema.OpDelete:
			continue
		case state.firstOp == schema.OpCreate:
			netOp = schema.OpCreate
		case state.lastOp == schema.OpDelete:
			netOp = schema.OpDelete
		default:
			netOp = schema.OpModify
		}

		if netOp == schema.OpModify && state.lastHash == state.firstPrevHash {
			continue
		}

		changes = append(changes, fileChange{
			filePath:    filePath,
			op:          netOp,
			contentHash: state.lastHash,
		})
	}

	sort.Slice(changes, func(i, j int) bool {
		return changes[i].filePath < changes[j].filePath
	})

	return changes
}

func findPreviousHash(events []*schema.Event, filePath string) string {
	for _, e := range events {
		if e.FilePath == filePath {
			return e.PreviousHash
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func buildCommitMessage(sessionID string, result *CommitResult) string {
	var parts []string
	if result.FilesAdded > 0 {
		parts = append(parts, fmt.Sprintf("%d added", result.FilesAdded))
	}
	if result.FilesModified > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", result.FilesModified))
	}
	if result.FilesDeleted > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", result.FilesDeleted))
	}

	summary := strings.Join(parts, ", ")
	return fmt.Sprintf("belay: session %s (%s)", truncate(sessionID, 8), summary)
}

func appendBelayTrailers(message string, sessionID string, events []*schema.Event) string {
	var earliest, latest time.Time
	for _, e := range events {
		t := e.Timestamp()
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
		if latest.IsZero() || t.After(latest) {
			latest = t
		}
	}

	trailers := fmt.Sprintf("\n\nBelay-Session: %s\nBelay-Events: %d\nBelay-Start: %s\nBelay-End: %s",
		sessionID,
		len(events),
		earliest.Format(time.RFC3339),
		latest.Format(time.RFC3339),
	)

	return message + trailers
}

func extractCommitHash(projectRoot string) string {
	hash, err := gitCmd(projectRoot, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return hash
}

// ProjectOptions configures projecting a session's net changes onto a git ref
// using plumbing only (no working tree, index, or HEAD mutation).
type ProjectOptions struct {
	SessionID  string
	TargetRef  string
	BaseRef    string
	Message    string
	NoMetadata bool
	DryRun     bool
}

// ProjectResult contains the outcome of a session projection.
type ProjectResult struct {
	Hash          string
	Parent        string
	Base          string
	Tree          string
	TargetRef     string
	FilesAdded    int
	FilesModified int
	FilesDeleted  int
	FilesSkipped  int
	Message       string
	Skipped       bool
}

// ProjectSession projects a session's net file changes onto TargetRef as a single
// commit built entirely through git plumbing (hash-object, a throwaway index,
// write-tree, commit-tree, update-ref). It never touches the working tree, the
// real index, or HEAD, so it is safe to run while other AI sessions edit the same
// checkout. The new commit's parent is the current ref tip, so the ref always
// fast-forwards; the update-ref compare-and-swap rejects concurrent projections
// rather than clobbering them.
func ProjectSession(idx *index.Index, objStore *store.Store, projectRoot string, opts ProjectOptions) (*ProjectResult, error) {
	if opts.SessionID == "" {
		return nil, fmt.Errorf("session ID is required")
	}
	if opts.TargetRef == "" {
		opts.TargetRef = "refs/heads/belay-history"
	}
	if !IsGitRepo(projectRoot) {
		return nil, fmt.Errorf("%s is not a git repository", projectRoot)
	}

	events, err := idx.QueryEvents(&index.Query{
		Sessions:  []string{opts.SessionID},
		OrderDesc: false,
	})
	if err != nil {
		return nil, fmt.Errorf("query session events: %w", err)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("no events found for session %s", opts.SessionID)
	}

	changes := computeNetChanges(events)

	// Skip paths inside git submodules: in the superproject tree those are gitlinks,
	// not files, so they cannot be projected here (they belong to the submodule's repo).
	skipped := 0
	if subs := submodulePaths(projectRoot); len(subs) > 0 {
		var kept []fileChange
		for _, c := range changes {
			if pathUnderAny(c.filePath, subs) {
				skipped++
				continue
			}
			kept = append(kept, c)
		}
		changes = kept
	}

	if len(changes) == 0 {
		return &ProjectResult{TargetRef: opts.TargetRef, Skipped: true, FilesSkipped: skipped}, nil
	}

	result := &ProjectResult{TargetRef: opts.TargetRef, FilesSkipped: skipped}
	for _, c := range changes {
		switch c.op {
		case schema.OpCreate:
			result.FilesAdded++
		case schema.OpModify:
			result.FilesModified++
		case schema.OpDelete:
			result.FilesDeleted++
		}
	}

	result.Message = opts.Message
	if result.Message == "" {
		result.Message = buildCommitMessage(opts.SessionID, &CommitResult{
			FilesAdded:    result.FilesAdded,
			FilesModified: result.FilesModified,
			FilesDeleted:  result.FilesDeleted,
		})
	}
	if !opts.NoMetadata {
		result.Message = appendBelayTrailers(result.Message, opts.SessionID, events)
	}

	parent, _ := gitCmd(projectRoot, "rev-parse", "--verify", "--quiet", opts.TargetRef+"^{commit}")
	result.Parent = parent

	// Build on the prior projection tip if the target ref exists; otherwise bootstrap
	// from BaseRef (the real repo state) so the projection carries a full, coherent
	// tree rather than just this session's delta.
	baseCommit := parent
	if baseCommit == "" && opts.BaseRef != "" {
		baseCommit, _ = gitCmd(projectRoot, "rev-parse", "--verify", "--quiet", opts.BaseRef+"^{commit}")
	}
	result.Base = baseCommit

	if opts.DryRun {
		return result, nil
	}

	tmpDir, err := os.MkdirTemp("", "belay-project-")
	if err != nil {
		return nil, fmt.Errorf("create temp index dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(tmpDir, "index"))

	if baseCommit != "" {
		if _, err := gitCmdEnv(projectRoot, env, "read-tree", baseCommit+"^{tree}"); err != nil {
			return nil, fmt.Errorf("read-tree base %s: %w", baseCommit, err)
		}
	}

	for _, c := range changes {
		switch c.op {
		case schema.OpCreate, schema.OpModify:
			data, err := objStore.Get(c.contentHash)
			if err != nil {
				return nil, fmt.Errorf("get object %s for %s: %w", c.contentHash, c.filePath, err)
			}
			blob, err := gitHashObject(projectRoot, env, data)
			if err != nil {
				return nil, fmt.Errorf("hash-object %s: %w", c.filePath, err)
			}
			if _, err := gitCmdEnv(projectRoot, env, "update-index", "--add", "--cacheinfo", "100644", blob, c.filePath); err != nil {
				return nil, fmt.Errorf("update-index add %s: %w", c.filePath, err)
			}
		case schema.OpDelete:
			_, _ = gitCmdEnv(projectRoot, env, "update-index", "--force-remove", c.filePath)
		}
	}

	tree, err := gitCmdEnv(projectRoot, env, "write-tree")
	if err != nil {
		return nil, fmt.Errorf("write-tree: %w", err)
	}
	result.Tree = tree

	commitArgs := []string{"commit-tree", tree, "-m", result.Message}
	if baseCommit != "" {
		commitArgs = append(commitArgs, "-p", baseCommit)
	}
	commit, err := gitCmd(projectRoot, commitArgs...)
	if err != nil {
		return nil, fmt.Errorf("commit-tree: %w", err)
	}
	result.Hash = commit

	if _, err := gitCmd(projectRoot, "update-ref", opts.TargetRef, commit, parent); err != nil {
		return nil, fmt.Errorf("update-ref %s (concurrent projection?): %w", opts.TargetRef, err)
	}

	return result, nil
}

// ProjectWorkingTreeOptions configures a session-independent reconcile projection.
type ProjectWorkingTreeOptions struct {
	TargetRef string
	BaseRef   string
	Message   string
	DryRun    bool
}

// ProjectWorkingTree projects a faithful snapshot of the current working tree onto
// TargetRef as a single commit, built entirely through git plumbing (a throwaway
// index seeded from HEAD, git add -A, write-tree, commit-tree, update-ref). It never
// touches the real working tree, index, or HEAD.
//
// Unlike ProjectSession it needs no session ID and no Belay attribution: it captures
// whatever is on disk right now (tracked changes, untracked non-ignored files, and
// deletions), so it is the reconcile safety net for changes that no per-session
// projection can see -- edits made from another repo's session, or changes that
// arrived with empty/absent Belay attribution. Submodule paths are left exactly as
// HEAD has them, and .gitignore is honored via git add. If the snapshot already
// matches the projection tip, it is a no-op.
func ProjectWorkingTree(projectRoot string, opts ProjectWorkingTreeOptions) (*ProjectResult, error) {
	if opts.TargetRef == "" {
		opts.TargetRef = "refs/heads/belay-history"
	}
	if !IsGitRepo(projectRoot) {
		return nil, fmt.Errorf("%s is not a git repository", projectRoot)
	}

	parent, _ := gitCmd(projectRoot, "rev-parse", "--verify", "--quiet", opts.TargetRef+"^{commit}")

	baseRef := opts.BaseRef
	if baseRef == "" {
		baseRef = "HEAD"
	}
	seedCommit, _ := gitCmd(projectRoot, "rev-parse", "--verify", "--quiet", baseRef+"^{commit}")

	tmpDir, err := os.MkdirTemp("", "belay-reconcile-")
	if err != nil {
		return nil, fmt.Errorf("create temp index dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(tmpDir, "index"))

	if seedCommit != "" {
		if _, err := gitCmdEnv(projectRoot, env, "read-tree", seedCommit+"^{tree}"); err != nil {
			return nil, fmt.Errorf("read-tree %s: %w", baseRef, err)
		}
	}

	addArgs := []string{"add", "-A", "--", "."}
	for _, p := range submodulePaths(projectRoot) {
		addArgs = append(addArgs, ":(exclude)"+p)
	}
	if _, err := gitCmdEnv(projectRoot, env, addArgs...); err != nil {
		return nil, fmt.Errorf("stage working tree: %w", err)
	}

	tree, err := gitCmdEnv(projectRoot, env, "write-tree")
	if err != nil {
		return nil, fmt.Errorf("write-tree: %w", err)
	}

	if parent != "" {
		if parentTree, _ := gitCmd(projectRoot, "rev-parse", "--verify", "--quiet", parent+"^{tree}"); parentTree == tree {
			return &ProjectResult{TargetRef: opts.TargetRef, Skipped: true}, nil
		}
	}

	result := &ProjectResult{TargetRef: opts.TargetRef, Tree: tree, Parent: parent, Base: seedCommit}

	diffBase := parent
	if diffBase == "" {
		diffBase = seedCommit
	}
	if diffBase != "" {
		if out, _ := gitCmd(projectRoot, "diff-tree", "-r", "--name-status", diffBase+"^{tree}", tree); out != "" {
			for _, line := range strings.Split(out, "\n") {
				if line == "" {
					continue
				}
				switch line[0] {
				case 'A':
					result.FilesAdded++
				case 'M':
					result.FilesModified++
				case 'D':
					result.FilesDeleted++
				}
			}
		}
	}

	result.Message = opts.Message
	if result.Message == "" {
		var parts []string
		if result.FilesAdded > 0 {
			parts = append(parts, fmt.Sprintf("%d added", result.FilesAdded))
		}
		if result.FilesModified > 0 {
			parts = append(parts, fmt.Sprintf("%d modified", result.FilesModified))
		}
		if result.FilesDeleted > 0 {
			parts = append(parts, fmt.Sprintf("%d deleted", result.FilesDeleted))
		}
		if len(parts) == 0 {
			parts = append(parts, "snapshot")
		}
		result.Message = fmt.Sprintf("belay: reconcile working tree (%s)", strings.Join(parts, ", "))
	}

	if opts.DryRun {
		return result, nil
	}

	commitArgs := []string{"commit-tree", tree, "-m", result.Message}
	if parent != "" {
		commitArgs = append(commitArgs, "-p", parent)
	} else if seedCommit != "" {
		commitArgs = append(commitArgs, "-p", seedCommit)
	}
	commit, err := gitCmd(projectRoot, commitArgs...)
	if err != nil {
		return nil, fmt.Errorf("commit-tree: %w", err)
	}
	result.Hash = commit

	if _, err := gitCmd(projectRoot, "update-ref", opts.TargetRef, commit, parent); err != nil {
		return nil, fmt.Errorf("update-ref %s (concurrent projection?): %w", opts.TargetRef, err)
	}
	return result, nil
}

// PushRef pushes a single refspec to a remote using the repo's configured credentials,
// bounding a stalled transfer with git's own low-speed abort (no external timeout).
func PushRef(projectRoot, remote, refspec string) (string, error) {
	return gitCmd(projectRoot, "-c", "http.lowSpeedLimit=1000", "-c", "http.lowSpeedTime=30", "push", remote, refspec)
}

func submodulePaths(projectRoot string) []string {
	out, err := gitCmd(projectRoot, "config", "--file", filepath.Join(projectRoot, ".gitmodules"), "--get-regexp", `^submodule\..*\.path$`)
	if err != nil || out == "" {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			paths = append(paths, fields[1])
		}
	}
	return paths
}

func pathUnderAny(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

func gitCmdEnv(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func gitHashObject(dir string, env []string, data []byte) (string, error) {
	cmd := exec.Command("git", "hash-object", "-w", "--stdin")
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("hash-object: %s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}
