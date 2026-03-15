# Belay -- Developer Reference

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
├── frontend/                # React + TypeScript dashboard
└── go.mod
```

## Building

```bash
go build -o bin/belay ./cmd/belay
go test ./... -v -race
go vet ./...
```

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
- `file_path`: relative to project root
- `operation`: create | modify | delete | rename
- `content_hash`: SHA-256 of file content
- `previous_hash`: SHA-256 of previous content
- `session_id`: nullable AI session identifier
- `attribution`: method used (pid, temporal, heuristic, manual, hook)
- `attribution_confidence`: 0.0--1.0

## Ports (defaults, configurable in .belay/config.toml)

- Dashboard dev server: 33411
- API (embedded in daemon): 33412
