# Belay QA Checklist -- Pre-Release Manual Testing

Personal testing checklist before any public release. Go through every section. Check the box when verified. If something fails, note the issue inline and fix before releasing.

---

## Installation

- [ ] `brew install davidparkercodes/tap/belay` installs cleanly on a fresh machine
- [ ] `go install github.com/davidparkercodes/belay/cmd/belay@latest` works
- [ ] Pre-built binary from GitHub Releases runs without issues (macOS ARM)
- [ ] Pre-built binary from GitHub Releases runs without issues (macOS Intel)
- [ ] Pre-built binary from GitHub Releases runs without issues (Linux amd64)
- [ ] `belay --version` prints correct version after install

## Init & Config

- [ ] `belay init` creates `.belay/` directory with config.toml and correct defaults
- [ ] Running `belay init` in an already-initialized directory warns and does not overwrite
- [ ] `.belayignore` patterns work (add node_modules, verify it's excluded)
- [ ] Custom config values in `.belay/config.toml` are respected (change port, log level)

## Daemon Lifecycle

- [ ] `belay daemon start` starts the daemon and API server
- [ ] `belay daemon status` shows running state
- [ ] `belay daemon stop` stops cleanly
- [ ] Starting daemon when already running gives a clear error (not a crash)
- [ ] Daemon survives and recovers from a signal kill (`kill -9`)
- [ ] Daemon auto-creates missing directories on startup if `.belay/` exists

## File Watching & Event Capture

- [ ] Creating a new file generates a `create` event
- [ ] Modifying a file generates a `modify` event
- [ ] Deleting a file generates a `delete` event
- [ ] Renaming a file generates a `rename` event
- [ ] Rapid file writes (save 10 times in 2 seconds) all captured
- [ ] Large file (>50MB) skips content capture but still logs the event
- [ ] Binary files are stored correctly and recoverable
- [ ] Files in ignored directories (node_modules, .git) are NOT captured
- [ ] Nested directory creation + file writes are captured
- [ ] Symlinked files/directories handled correctly (or explicitly documented as unsupported)

## Session Attribution

- [ ] Claude Code session detected and attributed via hook
- [ ] Manual edits (no AI tool) show as unattributed or "manual"
- [ ] `belay sessions` lists all detected sessions with correct metadata
- [ ] Session ID persists across multiple file writes in the same session
- [ ] Multiple concurrent sessions are distinguished correctly

## Recovery & Restore

- [ ] `belay restore <file> --at "5m ago"` restores correct content
- [ ] `belay restore <file> --session <id>` restores to session state
- [ ] `belay restore <file> --event <id>` restores to specific event
- [ ] Restoring a deleted file recreates it
- [ ] Restoring with `safety.allow_writes = false` shows dry-run output only
- [ ] Restoring with `safety.allow_writes = true` actually writes the file
- [ ] Content integrity -- restored file is byte-identical to the original

## Diff & Replay

- [ ] `belay diff --session <id>` shows unified diff of all session changes
- [ ] `belay diff --session <id> --stat` shows file change summary
- [ ] `belay replay <session> --stat` shows what changed
- [ ] `belay replay <session> --patch` shows applicable patches
- [ ] `belay replay <session> --apply` replays changes (with allow_writes)

## Log & History

- [ ] `belay log` shows recent events in chronological order
- [ ] `belay log --since 1h` filters correctly
- [ ] `belay log --file <path>` filters to specific file
- [ ] `belay log --session <id>` filters to specific session
- [ ] Event output is readable and well-formatted in terminal

## Snapshots

- [ ] `belay snapshot --at "1h ago"` reconstructs project state
- [ ] `belay snapshot --session <id>` reconstructs state at session end
- [ ] Snapshot output directory contains correct files

## Conflict Detection

- [ ] `belay conflicts` detects overlapping edits from multiple sessions
- [ ] Conflict severity levels (low/medium/high/critical) are assigned correctly
- [ ] No false positives on sequential (non-overlapping) edits

## Git Integration

- [ ] `belay commit --session <id>` generates a git commit with Belay trailers
- [ ] Commit message includes session attribution
- [ ] Commit only includes files changed by the specified session

## Garbage Collection & Retention

- [ ] `belay gc --dry-run` shows what would be cleaned without deleting
- [ ] `belay gc` compacts events according to retention tiers
- [ ] After GC, recent events (hot tier) are still fully intact
- [ ] After GC, old events are compacted but key snapshots preserved
- [ ] `belay rebuild-index` reconstructs SQLite index from event log
- [ ] After rebuild-index, all queries return same results as before

## API Endpoints

- [ ] `GET /api/health` returns 200
- [ ] `GET /api/stats` returns aggregate statistics
- [ ] `GET /api/events` returns paginated events
- [ ] `GET /api/events?since=<time>` filters correctly
- [ ] `GET /api/sessions` returns session list
- [ ] `GET /api/sessions/:id` returns session details
- [ ] `GET /api/files` returns modified file list
- [ ] `GET /api/files/history?path=<path>` returns file history
- [ ] `GET /api/files/content?hash=<sha256>` returns file content
- [ ] `GET /api/conflicts` returns conflict data
- [ ] `POST /api/record` accepts and stores external events
- [ ] `GET /api/stream` streams SSE events in real-time

## Dashboard (Frontend)

- [ ] Dashboard loads at `http://localhost:33411`
- [ ] Timeline view shows events in real-time
- [ ] Sessions view lists all sessions
- [ ] Session detail view shows files changed and diffs
- [ ] Files view lists all tracked files
- [ ] File detail view shows change history
- [ ] Conflicts view displays detected conflicts
- [ ] Live view streams events via SSE
- [ ] No console errors in browser dev tools

## Claude Code Hook

- [ ] Hook installed in `~/.claude/settings.json`
- [ ] Write tool triggers hook and creates event with session attribution
- [ ] Edit tool triggers hook and creates event with session attribution
- [ ] Hook is non-blocking (does not slow down Claude Code)
- [ ] Hook handles missing belay binary gracefully (no error spam)

## Edge Cases & Stress

- [ ] 1,000+ rapid file writes in <1 second -- all captured
- [ ] Daemon restart preserves all prior events
- [ ] Corrupt/truncated segment file -- daemon starts and reports error
- [ ] Disk full scenario -- daemon handles gracefully (no crash, clear error)
- [ ] Very long file paths (200+ chars) -- handled or clearly errored
- [ ] Unicode filenames -- captured and recoverable
- [ ] Empty files -- create/modify events recorded correctly

## Cross-Platform (if testing on multiple machines)

- [ ] macOS ARM (M-series) -- full test pass
- [ ] macOS Intel -- full test pass
- [ ] Linux (Ubuntu/Debian) -- full test pass
- [ ] Windows (WSL2) -- basic test pass
- [ ] Windows (native) -- basic test pass

---

## Notes

_Add notes, issues found, or observations here during testing._
