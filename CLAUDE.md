# Belay -- Developer Reference

## Public repository

Belay is open source. Everything here (code, comments, tests, docs, hooks, changelog) is public. Never reference a specific person's machines, hostnames, IP addresses, home paths, private projects or infrastructure. Behavior must work the same on any macOS, Linux or Windows machine; anything machine-specific belongs in the user's own environment (env vars such as `BELAY_BIN` / `BELAY_HOOK_DISABLE`, their service manager, their `.belay/config.toml`), not in this repo.

## Architecture

```
belay/
├── cmd/belay/              # CLI entry point (Cobra)
│   └── commands/            # All CLI subcommands
├── internal/
│   ├── schema/              # Event types, serialization, data model
│   ├── store/               # Encrypted content-addressable object store + legacy migration
│   ├── storekey/            # Per-user store key (OS keychain or 0600 key file)
│   ├── eventlog/            # Append-only event log (segment files)
│   ├── index/               # SQLite event index (WAL mode)
│   ├── ignore/              # .belayignore pattern matching
│   ├── watcher/             # Filesystem watcher (FSEvents on macOS, fsnotify elsewhere)
│   ├── daemon/              # Daemon lifecycle management
│   ├── session/             # AI session detection & attribution (plugin architecture)
│   ├── api/                 # HTTP API server (REST + SSE streaming)
│   ├── replay/              # Session replay, snapshots, unified diff
│   ├── conflict/            # Conflict detection across concurrent sessions
│   ├── git/                 # Git bridge (commit, stash, import)
│   ├── selfupdate/          # Release check, verified download, binary swap, update cache
│   └── config/              # Configuration (TOML), plus the per-user config
├── hooks/                   # AI tool integration scripts
└── go.mod
```

## Building

```bash
go build -o bin/belay ./cmd/belay
go test ./... -v -race
go vet ./...
```

`bin/belay` is gitignored build output. After a source change, run `task build` and restart any running daemons (`belay daemon restart`) so they pick up the new binary.

## Key Design Decisions

- **Event Store:** Append-only log with segment files + SQLite index
- **Object Store:** Content-addressable, gzip then XChaCha20-Poly1305, keyed HMAC-SHA256 ids (`Store.ContentID`; never hash file content with `sha256` to compare against stored ids). Legacy SHA-256 objects stay readable and are migrated by `store.Migrator` (phases copy, remap, segments, cleanup; progress in the index). Tests must use `BELAY_KEY_FILE` or a `storekey.MemoryProvider`, never the real keychain
- **Config:** TOML format at `.belay/config.toml`
- **CLI:** Cobra command framework
- **API:** net/http with SSE for real-time streaming (embedded in daemon)
- **Session Detection:** Plugin architecture; Claude Code detector included
- **File Watcher:** macOS uses FSEvents; Linux/Windows use fsnotify
- **Git projection:** `internal/git` builds projection commits with plumbing only; `SyncRef` makes the remote the source of truth (fast-forward or replay local-only projections onto the remote tip) and `RunProjection` is the one job runner shared by the CLI and the daemon's single-worker projector queue (`internal/daemon/projector.go`, `POST /api/project`)
- **Self-update:** `internal/selfupdate` detects the install method (Homebrew, `go install`, release binary, dev) and installs through it; release binaries are verified against `checksums.txt` and renamed into place. The daemon's update loop (`internal/daemon/update.go`) checks at most once per 24h into `<user cache dir>/belay/update.json` and, in `auto` mode (`<user config dir>/belay/config.toml`), installs. Its binary watch re-execs the daemon (same PID) once the launch path's file is replaced, settled, probed and the daemon is idle. The exec, the probe and all commands run through package variables that tests stub; tests never exec the test binary or touch the network
- **Session linking:** process-detected sessions (`<tool>-pid-N`) are linked to the tool's own session id via `record`/`checkpoint` PIDs or `POST /api/sessions/link`; links live in session metadata (`linked_session_id`) and every session query expands through them (`index.ExpandSessions`)

## Event Schema (v1)

Each event captures:
- `event_id`: UUID v7 (time-sortable)
- `timestamp`: nanosecond precision UTC
- `file_path`: relative to project root (empty string for `checkpoint` markers)
- `operation`: create | modify | delete | rename | checkpoint
- `content_hash`: content id of the file content (keyed HMAC-SHA256; SHA-256 in legacy stores)
- `previous_hash`: content id of the previous content
- `session_id`: nullable AI session identifier
- `attribution`: method used (pid, temporal, heuristic, manual, hook)
- `attribution_confidence`: 0.0--1.0

`checkpoint` events have an empty `file_path` and a `label` in `metadata`. Snapshot reconstruction and `restore --all` skip them; `belay log` renders them with a CHECKPOINT marker. Resolve a checkpoint by event ID or by label (latest match wins) via `restore --to-checkpoint`.

## Ports (defaults, configurable in .belay/config.toml)

- API (embedded in daemon): 33412 preferred; falls back to a free port if taken. Bound port is written to `.belay/daemon.port`. Requests carrying `X-Belay-Project` for another project get 409 `project_mismatch`.
