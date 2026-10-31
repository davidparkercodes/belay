# Changelog

All notable changes to Belay are documented here.

## v1.9.0 - 2026-10-31

### Added
- **Encryption at rest for the content store** -- every object in `.belay/objects/` is now encrypted (XChaCha20-Poly1305, compress then encrypt) under one per-user key kept in the OS keychain (`security` on macOS, `secret-tool` on Linux) or, silently, in a `0600` key file at `<user config dir>/belay/store.key`. `BELAY_KEY_FILE` overrides the location. Content ids become keyed HMAC-SHA256 values with the same 64-hex shape, so the index cannot confirm a guessed secret. `.belay/store.json` records the store format and key id, and opening a store with the wrong key says so instead of reporting corruption. Existing stores are migrated in place by the daemon in the background (resumable, legacy objects deleted only after their encrypted copy is read back and compared; corrupt legacy objects are kept and reported), including the event-log segments, so `belay rebuild-index` afterwards points at encrypted objects; ids of history whose objects were already collected are replaced by keyed tombstones, so no plain SHA-256 of old content survives. If legacy objects reappear later (an older `.belay` synced over a migrated one), the daemon notices at start and migrates again. A key is created on first use under a lock, so concurrent first runs share one key, and Belay never creates a key when the keychain could not be asked. With a missing or wrong key the daemon stays up, records nothing to the store, keeps hooks queueing, and points at `belay key import`. Opt out with `[store] encryption = false` before a store is first encrypted; a store that has encrypted keeps encrypting. It protects backups, synced and copied disks, and plain-text scanners; it does not protect against anyone running as your user. Losing the key loses the history; back it up with `belay key export`.
- **Secret detector (`internal/secrets`)** -- a pure, offline scanner for likely credentials, the building block for projection hold-back and agent log scrub. It finds known token formats (Anthropic, OpenAI, GitHub, GitLab, AWS, Stripe, Slack, Google, npm, Hugging Face, PyPI, SendGrid, DigitalOcean, Shopify, private keys, JWTs, credentials in URLs), credential assignments in env, shell, YAML, TOML, JSON and code with placeholder rejection, high-entropy values next to a credential keyword, and credentials written in plain sentences, including Google app passwords. Findings carry exact byte spans so callers can redact just the secret, and rules can be switched off by name.
- **Projection holds back files with likely credentials** -- `belay project` (session projections, working-tree reconciles and the daemon queue) scans the net content of every changed file before writing the tree. A file with a finding is left out of the commit: new files are not added, modified files stay at their previously projected version, deletions still project, and the commit still happens. A stderr line names the files, and the JSON result and projector status carry `held_back` (paths and rule names, never the secret). `belay project --dry-run` lists the files it would hold back. Allow paths or switch rules off with a `.belayallow` file (gitignore syntax plus `rule:<name>` lines), use `--allow-secrets` for one run (passed through `--queue`), or set `[projection] hold_back_secrets = false`. Binary and very large files are skipped, and a detector failure projects the file as before.
- **Agent log scrub (`belay scrub`)** -- redacts likely credentials from this project's idle AI agent session logs (Claude Code transcripts first; other agents plug in through a small log source interface). JSONL lines are redacted inside their JSON string values so every line stays valid, each finding becomes `[redacted:<rule>]`, and output is counts per rule, never the secret. Only logs with no write in the last 30 minutes and no active Belay session are touched. The original bytes go into the encrypted store first (pinned against `belay gc`) and `belay scrub --undo <log>` puts them back byte-exact, keeping anything the agent appended since. Rewrites use a temp file and rename, keep the file mode and mtime, and are abandoned if the log changes mid-scrub. `--dry-run`, `--json` and `--all-idle` (every project's idle logs) are available. New `[scrub] mode`: `notice` (default, one cached line in `belay status` when findings exist), `auto` (the daemon redacts idle logs in small bounded rounds and never redoes an undone scrub), or `off`. `belay init` asks one question about it (default No) and takes `--scrub <mode>`; init without a terminal no longer waits on the wizard.
- **`belay key status|export|import|migrate`** -- show where the key lives and the migration progress, back the key up, install it on another machine, or run the migration by hand while the daemon is stopped. `belay status` shows the store format too.
- **`belay project --push` syncs with the remote and recovers from rejected pushes** -- the remote is now the source of truth: before projecting, the local projection ref is fast-forwarded to the remote copy, and when the two diverged (another machine projected onto the same ref) the local-only projection commits are replayed on top of the remote tip with their trees, messages, authors and dates intact. A push that is still rejected re-syncs and retries. Before this, one machine's projector could be rejected non-fast-forward on every single push, forever, while reporting nothing but a log line. Pushes use `--no-verify`, since a projection is not a user push.
- **Daemon-side projector queue** -- `belay project --queue` hands the job to the running daemon (`POST /api/project`), which runs projections one at a time in the background and coalesces identical pending jobs. Hooks return immediately instead of serializing on a lock and running git themselves. `belay project --status` (`GET /api/project/status`) shows the queue, the running job and recent results. If the daemon is unreachable the job runs inline.
- **`[projection]` config section** -- defaults for `belay project`: `remote`, `ref`, `base_ref` and `working_tree`. `--session` and `--working-tree` can now be combined in one call.
- **Process sessions link to tool session ids** -- writes an agent makes through a shell (`cat >`, `sed -i`, build scripts) never pass through an editing-tool hook, so the watcher attributed them to a synthetic process session (`claude-pid-1234`) that nothing else knew about, and `belay project --session <tool id>` answered "no events found". `belay record` and `belay checkpoint` now send the calling agent's PID, the daemon links that process session to the tool session id (`POST /api/sessions/link`, `belay sessions link`), the watcher attributes the process's later writes to the tool id directly, and every session query (`log`, `diff`, `restore`, `replay`, `project`) also includes events recorded under the linked process id. Links persist in session metadata and survive daemon restarts. The Claude Code hook links on `SessionStart` and on any non-editing tool use.
- **`--short`** on `belay log`, `belay sessions`, `belay sessions list` and `belay sessions active` -- session ids are now shown in full by default; `--short` restores the abbreviated form.

### Changed
- **Hooks never touch the keychain** -- with the daemon up, `belay record` and `belay checkpoint` only talk to its API. With the daemon down, `record` seals the file content to a public key the daemon publishes in `.belay/spool/.recipient` and queues it, so the store key is not loaded and the queue holds no plaintext; the daemon decrypts and stores it when it next drains the queue. The recipient is checked against a fingerprint in `.belay/store.json` before anything is sealed to it. If the store cannot be opened at all, the event is still queued and the daemon reads the file itself. A queued event that fails to store is retried on the next drain; only entries that cannot be decoded or opened are set aside as `*.json.bad`, sealed ones are retried when a daemon with the matching key starts, and `belay status` shows how many are set aside. Only a project whose daemon has never run loads the key from a hook. `belay checkpoint` with the daemon down now queues the checkpoint and returns its id right away instead of waiting for a daemon to start (or failing when autostart is off). Within one process the store key is loaded once.
- **Hook and watcher duplicates are collapsed** -- the watcher and a tool hook both report the same write. When the content hash, session and operation match within 10 seconds, the second copy is dropped, so session event counts are honest.
- **Checkpoints older than the warm tier are purged by compaction** -- the file versions they point at are gone by then anyway, and a PreToolUse Bash hook can create thousands a day.
- **Default `.belayignore`** now also excludes `*.tsbuildinfo`, `coverage/`, `.cache/`, `.pytest_cache/`, `.mypy_cache/`, `.ruff_cache/` and `*.egg-info/`.

### Fixed
- **`belay status` reported `Objects: 0 B`** -- the size helper did not descend into the sharded object directories, so the objects line and the total were always wrong.
- **Sessions mislabeled `crashed`** -- hook-created sessions (no PID) were marked crashed on the next 30-second sweep, and a process that exited normally was called crashed too. Sessions without a PID now close only after 30 minutes without events, and closed sessions are `ended`; `crashed` is reserved for the registry's own liveness tracking.
- **Hook-created sessions always showed 1 event** -- each hook event built a fresh session object instead of reading the indexed one, so `event_count` and `files_changed` never accumulated.
- **Claude Code transcripts not detected** -- the session-file detector looked for `.json` files; Claude Code writes `.jsonl`.
- **`project_mismatch` through a bind mount** -- two spellings of the same directory that are not symlinks (a bind mount) now compare equal by device and inode instead of being rejected.

## v1.8.0 - 2026-10-13

### Added
- **`belay service install|uninstall|status`** -- runs the project's daemon as a user LaunchAgent (macOS) or `systemd --user` unit (Linux), so it starts at login and restarts after a crash. Before this, only an interactive shell's hook started a daemon, so after a reboot a repo used only by AI agents (whose shells are non-interactive) could go unrecorded for days.
- **`belay status --scan <dir>`** -- health-checks every Belay project under a directory and exits 1 if any daemon is stopped or has no API. `belay status` now also shows the API port and whether it answers, plus any queued events.
- **`belay record` starts a stopped daemon** -- when the daemon is unreachable, `record` (used by the Claude Code hook) now starts it, guarded by the same `.belay/daemon.lock` the shell hook uses. Set `BELAY_NO_AUTOSTART=1` to opt out. `belay checkpoint` does the same and retries once.

### Changed
- **Each daemon gets a reachable API port** -- if `[api] port` is taken (every project defaulted to 33412), the daemon now binds a free port instead of running with no API. The bound port is written to `.belay/daemon.port`, and the CLI, Claude Code hooks and git hooks read it.
- **Unreachable-daemon events are queued, not written into the event log** -- `belay record` used to append directly to `.belay/events` when the daemon was down, which raced the daemon's own writer and was never indexed, so the event and its session attribution were invisible. Events now go to `.belay/spool/`, and the daemon indexes them at startup and every 2 seconds.
- **`belay record` paths are relative to the current directory** -- they used to be treated as relative to the project root, so recording `a.ts` from `web/` recorded the root `a.ts` instead of `web/a.ts`. Absolute paths are recorded in the project that contains the file, wherever the command runs. The Claude Code hook now passes absolute paths.

### Fixed
- **Edits and checkpoints recorded in another project's history** -- with several projects on the default port, the first daemon to bind 33412 accepted every other project's `record` and `checkpoint` calls. An agent edit to `src/routes.ts` in one repo was stored in another repo under a phantom session, with the other repo's file content, and `record` exited 0. Requests now carry `X-Belay-Project`, and a daemon for a different project answers 409 `project_mismatch`, so the event is queued for the right project instead.
- **Git hooks never recorded anything** -- the installed hooks probed `/health`, which does not exist (the endpoint is `/api/health`), so every checkout, merge and rewrite was silently skipped. They also ignored the configured port. Re-run `belay git-hooks install` in each repo to pick up the fixed hooks.

## v1.7.2 - 2026-10-05

### Fixed
- **macOS blocked the Homebrew install**: since v1.6.0 Belay ships as a Homebrew cask instead of a formula. Homebrew quarantines cask downloads, and macOS refused to run the unsigned binary ("cannot be opened because Apple cannot check it for malicious software"). The cask now clears the quarantine flag on install, so `brew install davidparkercodes/tap/belay` works again. Release builds for macOS are also set up to be signed and notarized once signing credentials are configured.

## v1.7.1 - 2026-10-04

### Fixed
- **`belay daemon restart` left the old daemon running**: restart sent SIGTERM and started the new daemon immediately, before the old one had exited. The start saw the old PID still alive, printed "Daemon is already running", and the previous binary kept running, so upgrading with `restart` silently did nothing. `belay daemon stop` (and `restart` and `remove`) now wait for the daemon to exit, up to 15 seconds. If it doesn't exit in time, `restart` stops with an error instead of reporting success.
- **Quiet projects reported "watcher may be stalled"**: health turned `degraded` after 30 minutes without a file change, which is normal for an idle project. Health now reflects only the watcher's real state. `degraded` means a real problem, such as an exhausted inotify limit.

## v1.7.0 - 2026-10-04

### Changed
- **Short retention by default**: new projects now keep `hot_hours = 24`, `warm_days = 7`, `cold_days = 14`, `archive_days = 14` and `max_storage_gb = 3` (was cold 30, archive 365, 10 GB). A year of daily snapshots grew real repos to 9+ GB of history that nobody restores from. Existing `config.toml` files keep whatever they set explicitly; edit them to adopt the new policy. `archive_days = 0` still means keep forever.
- **`retention.compact_segments` defaults to `true`**: sealed event-log segments are now rewritten after compaction out of the box, so purged events actually leave `.belay/events` instead of accumulating there indefinitely.
- **Linux/Windows watcher covers large repos**: the recursive watcher no longer stops at 2,048 directories or 6 levels deep. It watches every non-ignored directory (up to 32 levels). If the kernel's inotify limit runs out, the watcher reports `degraded` with the `sysctl fs.inotify.max_user_watches` command to raise it, instead of logging a warning per directory and reporting healthy.
- **Claude Code hook finds `belay` anywhere**: `hooks/belay-hook.sh` now resolves the binary from `PATH`, then `~/go/bin`, `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin` and Linuxbrew. Set `BELAY_HOOK_DISABLE=1` to turn the hook off on a machine.

### Added
- **`belay gc --purge-ignored`**: deletes all history of files that `.belayignore` now excludes, so adding a screenshots, logs or build-output directory reclaims its space immediately instead of waiting for it to age out.

### Fixed
- **`max_storage_gb` could never be met**: when over budget, compaction only collapsed the older half of the hot tier to hourly granularity, which frees almost nothing. It now evicts the oldest history in time slices (re-measuring after each) and only then shrinks the hot tier. The hot tier itself is never evicted.
- **GC could delete an object mid-write**: the watcher stores an object before indexing its event, so a compaction pass landing between the two deleted the new version as an orphan. GC now skips unreferenced objects written in the last 10 minutes.
- **`database is locked` during auto-compaction**: `busy_timeout` was set with a one-off `Exec` on a pooled `database/sql` handle, so only one connection waited on locks and the rest failed with `SQLITE_BUSY`. Per-connection pragmas now go in the DSN.
- **`.belayignore` edits needed a daemon restart**: the daemon now reloads patterns when `.belayignore` changes.
- **Linux/Windows missed files in new nested directories**: creating `a/b/c/file` in one step only watched `a`, so the file and anything later written under `b` or `c` were never recorded. New directories are now watched recursively, and files already inside them are recorded.
- **Data race on watcher shutdown**: `Stop()` cleared the fsnotify handle while the event goroutine could still read it.

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
- Private hostname reference removed from CORS docs
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
