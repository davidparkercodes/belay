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
│   ├── store/               # Content-addressable object store (SHA-256)
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
│   └── config/              # Configuration (TOML)
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
- **Object Store:** Content-addressable (SHA-256) with gzip compression
- **Config:** TOML format at `.belay/config.toml`
- **CLI:** Cobra command framework
- **API:** net/http with SSE for real-time streaming (embedded in daemon)
- **Session Detection:** Plugin architecture; Claude Code detector included
- **File Watcher:** macOS uses FSEvents; Linux/Windows use fsnotify

## Event Schema (v1)

Each event captures:
- `event_id`: UUID v7 (time-sortable)
- `timestamp`: nanosecond precision UTC
- `file_path`: relative to project root (empty string for `checkpoint` markers)
- `operation`: create | modify | delete | rename | checkpoint
- `content_hash`: SHA-256 of file content
- `previous_hash`: SHA-256 of previous content
- `session_id`: nullable AI session identifier
- `attribution`: method used (pid, temporal, heuristic, manual, hook)
- `attribution_confidence`: 0.0--1.0

`checkpoint` events have an empty `file_path` and a `label` in `metadata`. Snapshot reconstruction and `restore --all` skip them; `belay log` renders them with a CHECKPOINT marker. Resolve a checkpoint by event ID or by label (latest match wins) via `restore --to-checkpoint`.

## Ports (defaults, configurable in .belay/config.toml)

- API (embedded in daemon): 33412
