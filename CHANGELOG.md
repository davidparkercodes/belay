# Changelog

All notable changes to Belay are documented here.

## v1.5.0 - 2026-04-13

### Added
- **`belay grep PATTERN`** (pickaxe) -- Find events that changed the number of occurrences of a string, the Belay equivalent of `git log -S`. Reports each matching event with its session attribution and the delta (`+3`, `-2`). Supports `--file`, `--session`, `--since`, `--until` pre-filters, `-i` for case-insensitive, `-G` for Go regex, `--json` for structured output, and `--scan-limit` as a safety cap on very long histories. Per-invocation blob count cache so files touched repeatedly are only scanned once. Closes the one gap in Belay forensics that still required falling back to git -- "did this string ever appear in history, and who added it?"

## v1.4.1 - 2026-04-13

### Fixed
- **Worktree cleanup DELETE cascade poisoning canonical history**: `git worktree remove` emits a DELETE event for every file in the worktree. These were being mapped from `.claude/worktrees/<name>/<path>` back to the canonical `<path>` and recorded as DELETE events against the real file on main, making `belay log --file <path>` show phantom deletes for files that still exist. DELETE events from a worktree are now suppressed when the worktree root is gone (cleanup cascade), with a 30-second straggler window for events that arrive right after root removal. Agent-initiated deletes while the worktree still exists continue to pass through with `worktree` metadata. Closes BEL-225.

## v1.4.0 - 2026-03-30

### Fixed
- **Worktree event loss**: Replaced fragile git-status-based CREATE filter with timestamp-based burst window. The old approach ran `git diff`/`git ls-files` against barely-initialized worktrees, got empty results, cached them for 3 seconds, and silently dropped all CREATE events during that window. Agent-written files in `.claude/worktrees/` were never captured.
- **FSEvents flag misclassification**: When macOS FSEvents delivers combined `ItemCreated|ItemModified` flags (common for recently-created files), the event is now correctly classified as MODIFY instead of CREATE. Previously, these were sent through the CREATE filter and dropped.
- **Silent event loss on content capture failure**: Events where the file disappears between the FSEvents notification and content read (race condition) are now emitted with an empty content hash instead of being silently dropped. The event metadata (path, operation, timestamp, session) is preserved.

## v1.1.0 - 2026-03-24

### Added
- Interactive init wizard with TUI (project type detection, .belayignore templates, shell hook setup, git hook installation)
- `--roughly-around` flag on restore and diff (renamed from `--at` for clarity)
- `--all` flag on restore to restore all tracked files from a session or time
- Session detection for Cursor, GitHub Copilot, Windsurf, and Aider via process-tree detection
- Git history import (`belay import-history`) for unified timeline across committed and uncommitted changes
- VS Code integration improvements
- CI pipeline with golangci-lint

### Changed
- Removed embedded frontend dashboard (standalone website at belay.sh instead)
- Claude Code hook simplified and cleaned up
- README updated with badges, aligned with website messaging

### Fixed
- Leaked *.ts.net reference removed from CORS docs
- dist/.gitkeep tracked so go:embed works in CI
- All golangci-lint errors resolved

## v1.0.0 - 2026-03-15

Initial release.

- Continuous file watching via FSEvents (macOS) / fsnotify (Linux)
- Content-addressable object store with SHA-256 deduplication
- Session attribution for Claude Code via hooks
- Cross-session conflict detection in real time (SSE streaming)
- File restore by session, event, or time
- Tiered retention with automatic compaction (belay gc)
- 14 chaos test scenarios (rapid-fire, concurrent writers, corruption recovery, worktree tracking)
- CLI commands: init, status, log, sessions, restore, diff, gc, daemon
- .belayignore for file exclusion patterns
- Git hook integration (post-commit, post-checkout)
