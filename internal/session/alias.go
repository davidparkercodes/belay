package session

import (
	"os"

	"github.com/davidparkercodes/belay/internal/schema"
)

// LinkedSessionKey is the session metadata key that points a process-detected
// session (for example claude-pid-1234) at the tool's own session identifier.
const LinkedSessionKey = "linked_session_id"

// AliasLink describes one process session that now resolves to a tool session id.
type AliasLink struct {
	PID            int
	ProcessSession string
	SessionID      string
	ToolName       string
}

// LinkAlias records that the agent process PID belongs to tool session sessionID.
// Every process-detected session with that PID, now and later, is attributed under
// sessionID instead of its synthetic process id. It returns the links that applied
// to sessions already tracked so the caller can persist them.
func (r *Registry) LinkAlias(pid int, sessionID, toolName string) []AliasLink {
	if pid <= 0 || sessionID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.aliasByPID == nil {
		r.aliasByPID = make(map[int]string)
		r.aliasOf = make(map[string]string)
	}
	r.aliasByPID[pid] = sessionID

	var links []AliasLink
	for id, ts := range r.sessions {
		if ts.session.PID != pid || id == sessionID {
			continue
		}
		if r.applyAliasLocked(ts, sessionID) {
			links = append(links, AliasLink{PID: pid, ProcessSession: id, SessionID: sessionID, ToolName: toolName})
		}
	}
	return links
}

func (r *Registry) applyAliasLocked(ts *trackedSession, sessionID string) bool {
	id := ts.session.SessionID
	if r.aliasOf[id] == sessionID {
		return false
	}
	r.aliasOf[id] = sessionID
	if ts.session.Metadata == nil {
		ts.session.Metadata = make(map[string]string)
	}
	ts.session.Metadata[LinkedSessionKey] = sessionID
	return true
}

// ResolveAlias maps a process session id to its linked tool session id, or returns
// the input unchanged when no link exists.
func (r *Registry) ResolveAlias(sessionID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if alias, ok := r.aliasOf[sessionID]; ok && alias != "" {
		return alias
	}
	return sessionID
}

// AliasFor returns the linked tool session id for a process session, if any.
func (r *Registry) AliasFor(sessionID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	alias, ok := r.aliasOf[sessionID]
	return alias, ok && alias != ""
}

func (r *Registry) aliasForPIDLocked(pid int) string {
	if pid <= 0 || r.aliasByPID == nil {
		return ""
	}
	return r.aliasByPID[pid]
}

// SeedAliases restores links persisted in session metadata (for example after a
// daemon restart) so attribution keeps resolving without waiting for a new hook call.
func (r *Registry) SeedAliases(sessions []*schema.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.aliasByPID == nil {
		r.aliasByPID = make(map[int]string)
		r.aliasOf = make(map[string]string)
	}
	for _, s := range sessions {
		if s == nil || s.Metadata == nil {
			continue
		}
		alias := s.Metadata[LinkedSessionKey]
		if alias == "" {
			continue
		}
		r.aliasOf[s.SessionID] = alias
		if s.PID > 0 {
			r.aliasByPID[s.PID] = alias
		}
	}
}

// CallerAgentPID walks up from this process to find the AI agent process that
// spawned it (hooks run as children of the agent), so a hook can tell the daemon
// which process a tool session id belongs to.
func CallerAgentPID() int {
	return findClaudeAncestor(os.Getpid())
}
