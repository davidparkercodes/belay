# Changelog

All notable changes to Belay are documented here.

## v1.6.1 - 2026-09-12

### Fixed
- **`getProcessCwd` returned the wrong process's directory (missing `lsof -a`)**: session attribution asked `lsof -p PID -Fn -d cwd` for a process's working directory, but without `-a` lsof ORs its selectors instead of ANDing them, so the query read as "PID `PID`, or any process with a cwd" and enumerated every process on the machine. Each call scanned hundreds of processes (~1.2s, a full core) instead of one (~0.07s), and the parse returned an arbitrary process's cwd, misattributing file changes to the wrong session. The call now passes `-a` so lsof reports only the target PID.
- **`getProcessCwd` now reads `/proc/<pid>/cwd` directly on Linux**: where the kernel exposes the working directory as a symlink, Belay reads it with a single `readlink` and skips the `lsof` subprocess entirely. This is cheaper and sidesteps the macOS `lsof` panic path on the platforms that do not need it; macOS has no `/proc`, so `readlink` fails there and the code falls through to the (now correct) `lsof` call.

### Changed
- **Shell hooks serialize concurrent daemon starts with an atomic lock**: the emitted `zsh` and `bash` hooks now take an atomic `mkdir .belay/daemon.lock` (60s stale-lock reclaim, released in a backgrounded subshell once the pidfile lands, so the prompt never blocks) before running `daemon start`. A burst of shells entering the same repo at once no longer each fork/exec a doomed `daemon start`. The daemon's own `flock` remains the authoritative duplicate guard; this only trims the wasted process churn on top of it.

## v1.6.0 - 2026-09-12

### Added
- **`belay project`** (concurrency-safe Belay→git projection) -- Build a single git commit from a session's net changes and append it to a target ref (default `refs/heads/belay-history`) using git **plumbing only** (`hash-object` → throwaway index → `write-tree` → `commit-tree` → compare-and-swap `update-ref`). Unlike `belay commit`, it never touches the working tree, the index, or HEAD, so it is safe to run while other AI sessions edit the same checkout -- the core enabler for "many concurrent agents, one working tree, no collisions on HEAD." When the target ref does not yet exist it bootstraps a full base tree from `--base-ref` (default `HEAD`) so the projection branch is a complete, checkout-able tree rather than a sparse delta; later projections build on the prior tip. Paths inside git submodules (gitlinks in the superproject) are skipped. Flags: `--session`, `--to-ref`, `--base-ref`, `--message`, `--no-metadata`, `--dry-run`, `--push <remote>`. Pair with a Claude Code `Stop` hook to make git an automatic, write-only projection of Belay's live history.
- **`belay checkpoint`** (pre-bash safety net) -- Mark a labeled, restorable point in time before a risky operation. Pairs with `belay restore --to-checkpoint <id-or-label> --all --execute` to roll back. Native Claude Code `/rewind` only catches Write/Edit/NotebookEdit; this closes the gap for destructive bash (`rm -rf`, `git reset --hard`, `git clean`, `dropdb`, build scripts, one-shot shell commands). Flags: `--label`, `--reason`, `--session`, `--tool`, `--quiet`. CHECKPOINT events go through the daemon's canonical event path so they appear in `belay log` and survive restarts.
- **`belay checkpoints`** -- List recorded checkpoints with id, time, label, source, and session. Filters: `--since`, `--until`, `--limit`, `--json`.
- **`belay restore --to-checkpoint <id-or-label>`** -- Resolve a checkpoint by event ID (exact) or label (latest match wins) and restore to that moment. Mutually exclusive with `--roughly-around`; composes with `--all`, `--dry-run`, `--execute`.
- **`hooks/belay-pre-bash.sh`** -- PreToolUse Bash hook for Claude Code. Always-on, 2-second watchdog, never blocks the shell. Records a checkpoint labeled `pre-bash: <command>` in the cwd's Belay project before each Bash tool invocation. Install via `~/.claude/settings.json` PreToolUse hooks with matcher `Bash`.
- **`POST /api/checkpoint`** and **`GET /api/checkpoints`** -- Daemon endpoints backing the new CLI. `/api/checkpoint` writes a CHECKPOINT event via the canonical `processEvent` path so the event log, SQLite index, and SSE stream stay consistent.
- **Schema:** `OpCheckpoint` operation. Backwards-compatible (SchemaVersion stays 1; older readers see `UNKNOWN` and skip). `belay log` renders CHECKPOINT events with their label.
- **Scheduled compaction** -- The daemon runs retention compaction on a wall-clock interval (`retention.compaction_interval_min`, default 60) that survives laptop sleep and daemon restarts: the last run is persisted in a new index `meta` table, polled once a minute, with a first pass shortly after startup. Replaces the old fixed 6h in-process ticker. Exposed via `POST /api/compact` (`dry_run`), a `compaction` block in `GET /api/stats`, and a Compaction section in `belay status`.
- **`retention.compact_segments`** -- Rewrites sealed event-log segments after compaction so purged and compacted events actually leave `.belay/events` and cannot resurface on an index rebuild. Cross-process `flock`, a per-segment clean-cache skip, and the newest (still-appending) segment is never touched. `belay gc` reports segments rewritten/deleted and bytes freed.
- **`retention.max_versions_per_file`** (default 25) -- Caps retained MODIFY versions per file beyond the hot tier, so churny files (logs, lockfiles, build artifacts) stop pinning content blobs forever. `0` = unlimited.
- **Index VACUUM after compaction** -- A pass that removed events triggers a guarded `VACUUM` (free-page floor) so the SQLite index file shrinks on disk instead of only marking pages free.
- **Rotating daemon log** -- The daemon writes to a size-capped rotating log file (`daemon.log_max_size_mb`, `daemon.log_max_files`) in addition to stderr.

### Changed
- **Warm-tier compaction is now hourly granularity** -- Collapses to the last MODIFY per file+session per clock hour, replacing the previous 60s "rapid-edit burst" heuristic. Creates, deletes, renames, checkpoints, and session meta-events are never removed by tier compaction.
- **`belay gc`** now reports objects freed, segment rewrites, and index vacuum, and its help text documents the new per-file version cap and the automatic compaction schedule.

### Fixed
- **Multi-daemon spawn from PID-file TOCTOU race**: The "is daemon already running?" pre-check read the PID file but never held a lock on it, so two `belay daemon start` invocations against the same project could both pass the check and start. One observed instance ended with five daemons live on the same `.belay/`, racing on the SQLite index, double-recording every event, and ballooning the object store to ~98 GB before any visible symptom. The PID file is now claimed via `flock(2)` (`syscall.Flock` on Unix, `O_EXCL`-with-stale-cleanup on Windows) and the lock is held for the daemon's lifetime; a losing second daemon returns `IsAlreadyRunning(err)` and exits without writing anything. Stale-PID cleanup moved into lock acquisition (atomic) so it cannot race against a live daemon that owns the flock.
- **`target/` added to default ignore patterns**: Rust build artifacts are now ignored alongside `node_modules/`, `build/`, `dist/`, etc. Previously `target/` was added only when `belay init` detected a `Cargo.toml` at the project root, which missed Rust crates nested inside polyglot monorepos.
- **`getProcessCwd` hardened against macOS 26 kernel panic**: macOS 26 (Darwin 25.2.0) has a reproducible kernel bug where `lsof` can trigger a NULL+0x48 data abort during proc/file-table iteration. Belay's single-PID `lsof -p PID -d cwd` call is on a different, lower-risk kernel path than the multi-process enumeration that panicked, but the call is now serialized behind a `sync.Mutex` and bounded by a 2s `context.WithTimeout` to eliminate any residual concurrency exposure.

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
