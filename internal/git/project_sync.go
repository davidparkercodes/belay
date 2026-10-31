package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/davidparkercodes/belay/internal/index"
	"github.com/davidparkercodes/belay/internal/store"
)

const (
	SyncNone          = "none"
	SyncCreated       = "created"
	SyncFastForwarded = "fast-forwarded"
	SyncAhead         = "ahead"
	SyncRebased       = "rebased"
	SyncNoRemoteRef   = "no-remote-ref"
)

const maxPushAttempts = 3

// SyncResult describes how a local projection ref was reconciled with its remote.
type SyncResult struct {
	Remote      string `json:"remote"`
	Ref         string `json:"ref"`
	LocalBefore string `json:"local_before"`
	RemoteTip   string `json:"remote_tip"`
	LocalAfter  string `json:"local_after"`
	Action      string `json:"action"`
	Rebased     int    `json:"rebased"`
}

// ProjectionJob is one unit of projector work: an optional per-session projection,
// an optional working-tree reconcile, and an optional remote to sync with and push to.
type ProjectionJob struct {
	SessionID    string `json:"session_id,omitempty"`
	WorkingTree  bool   `json:"working_tree"`
	Remote       string `json:"remote,omitempty"`
	Ref          string `json:"ref,omitempty"`
	BaseRef      string `json:"base_ref,omitempty"`
	Message      string `json:"message,omitempty"`
	NoMetadata   bool   `json:"no_metadata,omitempty"`
	AllowSecrets bool   `json:"allow_secrets,omitempty"`
}

// ProjectionReport is the outcome of RunProjection.
type ProjectionReport struct {
	Job          ProjectionJob  `json:"job"`
	Session      *ProjectResult `json:"session,omitempty"`
	SessionError string         `json:"session_error,omitempty"`
	WorkingTree  *ProjectResult `json:"working_tree,omitempty"`
	Sync         *SyncResult    `json:"sync,omitempty"`
	SyncError    string         `json:"sync_error,omitempty"`
	Pushed       bool           `json:"pushed"`
	PushAttempts int            `json:"push_attempts"`
	PushError    string         `json:"push_error,omitempty"`
	HeldBack     []HeldBackFile `json:"held_back,omitempty"`
	Warnings     []string       `json:"warnings,omitempty"`
	StartedAt    time.Time      `json:"started_at"`
	FinishedAt   time.Time      `json:"finished_at"`
}

func (j ProjectionJob) normalized() ProjectionJob {
	if j.Ref == "" {
		j.Ref = "refs/heads/belay-history"
	}
	if j.BaseRef == "" {
		j.BaseRef = "HEAD"
	}
	return j
}

func (j ProjectionJob) Equal(o ProjectionJob) bool {
	return j.normalized() == o.normalized()
}

func (j ProjectionJob) String() string {
	j = j.normalized()
	var parts []string
	if j.SessionID != "" {
		parts = append(parts, "session "+j.SessionID)
	}
	if j.WorkingTree {
		parts = append(parts, "working tree")
	}
	if len(parts) == 0 {
		parts = append(parts, "nothing")
	}
	s := strings.Join(parts, " + ") + " -> " + j.Ref
	if j.Remote != "" {
		s += " (push " + j.Remote + ")"
	}
	if j.AllowSecrets {
		s += " (allow secrets)"
	}
	return s
}

func trackingRef(remote, ref string) string {
	name := strings.TrimPrefix(ref, "refs/heads/")
	name = strings.TrimPrefix(name, "refs/")
	return "refs/belay-remote/" + remote + "/" + name
}

func isMissingRemoteRef(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "couldn't find remote ref") || strings.Contains(msg, "could not find remote ref")
}

// IsPushRejected reports whether a push error is a non-fast-forward rejection that a
// re-sync can resolve, as opposed to a network or auth failure.
func IsPushRejected(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "[rejected]") || strings.Contains(msg, "non-fast-forward") || strings.Contains(msg, "fetch first") || strings.Contains(msg, "stale info")
}

// SyncRef makes the local projection ref build on top of the remote's copy. The remote
// is the source of truth: a local ref that is behind fast-forwards, and one that has
// diverged (another machine projected onto the same ref) has its local-only commits
// replayed on top of the remote tip. Projection commits are snapshots, so replaying
// them is a parent swap that keeps every tree, message, author and date intact.
func SyncRef(projectRoot, remote, ref string) (*SyncResult, error) {
	if remote == "" {
		return nil, fmt.Errorf("remote is required")
	}
	if ref == "" {
		ref = "refs/heads/belay-history"
	}
	res := &SyncResult{Remote: remote, Ref: ref, Action: SyncNone}

	local, _ := gitCmd(projectRoot, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	res.LocalBefore = local
	res.LocalAfter = local

	tracking := trackingRef(remote, ref)
	if _, err := gitCmd(projectRoot, "-c", "http.lowSpeedLimit=1000", "-c", "http.lowSpeedTime=30",
		"fetch", "--no-tags", "--quiet", remote, "+"+ref+":"+tracking); err != nil {
		if isMissingRemoteRef(err) {
			res.Action = SyncNoRemoteRef
			return res, nil
		}
		return res, fmt.Errorf("fetch %s %s: %w", remote, ref, err)
	}
	remoteTip, _ := gitCmd(projectRoot, "rev-parse", "--verify", "--quiet", tracking+"^{commit}")
	res.RemoteTip = remoteTip
	if remoteTip == "" {
		res.Action = SyncNoRemoteRef
		return res, nil
	}

	switch {
	case local == "":
		if _, err := gitCmd(projectRoot, "update-ref", ref, remoteTip); err != nil {
			return res, fmt.Errorf("create %s from %s: %w", ref, remote, err)
		}
		res.LocalAfter = remoteTip
		res.Action = SyncCreated
	case local == remoteTip:
		res.Action = SyncNone
	case isAncestor(projectRoot, remoteTip, local):
		res.Action = SyncAhead
	case isAncestor(projectRoot, local, remoteTip):
		if _, err := gitCmd(projectRoot, "update-ref", ref, remoteTip, local); err != nil {
			return res, fmt.Errorf("fast-forward %s: %w", ref, err)
		}
		res.LocalAfter = remoteTip
		res.Action = SyncFastForwarded
	default:
		newTip, n, err := replayCommits(projectRoot, local, remoteTip)
		if err != nil {
			return res, fmt.Errorf("replay local projections onto %s: %w", remote, err)
		}
		if _, err := gitCmd(projectRoot, "update-ref", ref, newTip, local); err != nil {
			return res, fmt.Errorf("update-ref %s after replay (concurrent projection?): %w", ref, err)
		}
		res.LocalAfter = newTip
		res.Rebased = n
		res.Action = SyncRebased
	}
	return res, nil
}

func isAncestor(projectRoot, ancestor, descendant string) bool {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", ancestor, descendant)
	cmd.Dir = projectRoot
	return cmd.Run() == nil
}

func replayCommits(projectRoot, localTip, onto string) (string, int, error) {
	list, err := gitCmd(projectRoot, "rev-list", "--reverse", onto+".."+localTip)
	if err != nil {
		return "", 0, err
	}
	parent := onto
	n := 0
	for _, commit := range strings.Split(list, "\n") {
		commit = strings.TrimSpace(commit)
		if commit == "" {
			continue
		}
		raw, err := gitCmd(projectRoot, "log", "-1", "--format=%T%x00%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%B", commit)
		if err != nil {
			return "", n, err
		}
		fields := strings.SplitN(raw, "\x00", 8)
		if len(fields) != 8 {
			return "", n, fmt.Errorf("unexpected commit metadata for %s", commit)
		}
		env := []string{
			"GIT_AUTHOR_NAME=" + fields[1],
			"GIT_AUTHOR_EMAIL=" + fields[2],
			"GIT_AUTHOR_DATE=" + fields[3],
			"GIT_COMMITTER_NAME=" + fields[4],
			"GIT_COMMITTER_EMAIL=" + fields[5],
			"GIT_COMMITTER_DATE=" + fields[6],
		}
		newCommit, err := gitCommitTree(projectRoot, env, fields[0], parent, fields[7])
		if err != nil {
			return "", n, err
		}
		parent = newCommit
		n++
	}
	return parent, n, nil
}

func gitCommitTree(dir string, extraEnv []string, tree, parent, message string) (string, error) {
	args := []string{"commit-tree", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), extraEnv...)
	cmd.Stdin = strings.NewReader(message)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("commit-tree: %s: %w", out.String(), err)
	}
	return strings.TrimSpace(out.String()), nil
}

// RunProjection executes a ProjectionJob end to end: sync with the remote, project the
// session, reconcile the working tree, then push, re-syncing and retrying when the
// remote moved underneath us. Local projection never depends on the remote being up.
func RunProjection(idx *index.Index, objStore *store.Store, projectRoot string, job ProjectionJob) (*ProjectionReport, error) {
	job = job.normalized()
	report := &ProjectionReport{Job: job, StartedAt: time.Now()}
	defer func() { report.FinishedAt = time.Now() }()

	if job.SessionID == "" && !job.WorkingTree {
		return report, fmt.Errorf("nothing to project: give a session ID or enable the working-tree reconcile")
	}
	if !IsGitRepo(projectRoot) {
		return report, fmt.Errorf("%s is not a git repository", projectRoot)
	}

	if job.Remote != "" {
		sync, err := SyncRef(projectRoot, job.Remote, job.Ref)
		report.Sync = sync
		if err != nil {
			report.SyncError = err.Error()
		}
	}

	var guard *SecretGuard
	if !job.AllowSecrets {
		guard = LoadSecretGuard(projectRoot)
	}
	defer func() {
		var held []HeldBackFile
		var warnings []string
		seen := map[string]bool{}
		for _, r := range []*ProjectResult{report.Session, report.WorkingTree} {
			if r == nil {
				continue
			}
			held = mergeHeldBack(held, r.HeldBack)
			for _, w := range r.GuardWarnings {
				if !seen[w] {
					seen[w] = true
					warnings = append(warnings, w)
				}
			}
		}
		if len(held) > 0 {
			report.HeldBack = held
		}
		report.Warnings = warnings
	}()

	if job.SessionID != "" {
		if idx == nil || objStore == nil {
			return report, fmt.Errorf("session projection needs the index and object store")
		}
		res, err := ProjectSession(idx, objStore, projectRoot, ProjectOptions{
			SessionID:  job.SessionID,
			TargetRef:  job.Ref,
			BaseRef:    job.BaseRef,
			Message:    job.Message,
			NoMetadata: job.NoMetadata,
			Guard:      guard,
		})
		if err != nil {
			report.SessionError = err.Error()
		} else {
			report.Session = res
		}
	}

	if job.WorkingTree {
		res, err := ProjectWorkingTree(projectRoot, ProjectWorkingTreeOptions{
			TargetRef: job.Ref,
			BaseRef:   job.BaseRef,
			Guard:     guard,
		})
		if err != nil {
			return report, fmt.Errorf("reconcile working tree: %w", err)
		}
		report.WorkingTree = res
	}

	if job.Remote == "" {
		return report, nil
	}

	for attempt := 1; attempt <= maxPushAttempts; attempt++ {
		report.PushAttempts = attempt
		local, _ := gitCmd(projectRoot, "rev-parse", "--verify", "--quiet", job.Ref+"^{commit}")
		if local == "" {
			report.PushError = "nothing to push: " + job.Ref + " does not exist"
			return report, nil
		}
		if report.Sync != nil && report.Sync.RemoteTip == local && report.SyncError == "" {
			report.Pushed = false
			report.PushError = ""
			return report, nil
		}
		_, err := PushRef(projectRoot, job.Remote, job.Ref+":"+job.Ref)
		if err == nil {
			report.Pushed = true
			report.PushError = ""
			return report, nil
		}
		report.PushError = err.Error()
		if !IsPushRejected(err) || attempt == maxPushAttempts {
			return report, fmt.Errorf("push %s to %s: %w", job.Ref, job.Remote, err)
		}
		sync, serr := SyncRef(projectRoot, job.Remote, job.Ref)
		report.Sync = sync
		if serr != nil {
			report.SyncError = serr.Error()
			return report, fmt.Errorf("re-sync after rejected push: %w", serr)
		}
		report.SyncError = ""
	}
	return report, nil
}

// Summary renders a one-line human summary of the report.
func (r *ProjectionReport) Summary() string {
	var parts []string
	if r.Session != nil {
		if r.Session.Skipped {
			parts = append(parts, "session: no net changes")
		} else {
			parts = append(parts, fmt.Sprintf("session: +%d ~%d -%d", r.Session.FilesAdded, r.Session.FilesModified, r.Session.FilesDeleted))
		}
	} else if r.SessionError != "" {
		parts = append(parts, "session: "+r.SessionError)
	}
	if r.WorkingTree != nil {
		if r.WorkingTree.Skipped {
			parts = append(parts, "reconcile: up to date")
		} else {
			parts = append(parts, fmt.Sprintf("reconcile: +%d ~%d -%d", r.WorkingTree.FilesAdded, r.WorkingTree.FilesModified, r.WorkingTree.FilesDeleted))
		}
	}
	if len(r.HeldBack) > 0 {
		parts = append(parts, fmt.Sprintf("held back %d file(s) with likely credentials", len(r.HeldBack)))
	}
	if r.Sync != nil {
		s := "sync: " + r.Sync.Action
		if r.Sync.Rebased > 0 {
			s += fmt.Sprintf(" (%d replayed)", r.Sync.Rebased)
		}
		parts = append(parts, s)
	}
	if r.SyncError != "" {
		parts = append(parts, "sync error: "+r.SyncError)
	}
	if r.Job.Remote != "" {
		switch {
		case r.Pushed:
			parts = append(parts, fmt.Sprintf("pushed to %s (attempt %d)", r.Job.Remote, r.PushAttempts))
		case r.PushError != "":
			parts = append(parts, "push failed: "+r.PushError)
		default:
			parts = append(parts, r.Job.Remote+" already up to date")
		}
	}
	return strings.Join(parts, "; ")
}
