# Belay

[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/davidparkercodes/belay?color=green)](https://github.com/davidparkercodes/belay/releases)
[![macOS](https://img.shields.io/badge/macOS-arm64%20%7C%20amd64-lightgrey?logo=apple)](https://github.com/davidparkercodes/belay/releases)
[![Linux](https://img.shields.io/badge/Linux-arm64%20%7C%20amd64-lightgrey?logo=linux&logoColor=white)](https://github.com/davidparkercodes/belay/releases)
[![Windows](https://img.shields.io/badge/Windows-amd64-lightgrey?logo=windows)](https://github.com/davidparkercodes/belay/releases)

**Never lose code to an AI session again.**

Belay tracks every file change between git commits, attributes each change to the AI session that made it, and gives you a complete, recoverable history. Install it once, forget about it. When something goes wrong, recover any file in under a second.

Free, local, and open source.

## Why Belay?

AI agents move fast. A working codebase can break in a few prompts, and if you didn't commit, that version is gone. Run multiple agents in parallel and they silently overwrite each other's work. Git can't help -- it doesn't know about AI sessions.

Belay watches your filesystem, records every change, and tags each one to the AI session that made it. When two agents edit the same file, Belay catches it. When an agent destroys your work, Belay recovers it. Your AI agents can also use Belay directly to check what changed, detect conflicts, and restore files.

## Install

**Homebrew (macOS)**
```bash
brew install davidparkercodes/tap/belay
```

**Go Install (any platform with Go 1.24+)**
```bash
go install github.com/davidparkercodes/belay/cmd/belay@latest
```

**Pre-built Binaries** -- Download from the [Releases page](https://github.com/davidparkercodes/belay/releases).

**Build from Source**
```bash
git clone https://github.com/davidparkercodes/belay.git
cd belay && go build -o bin/belay ./cmd/belay
```

Belay is a single static binary with zero runtime dependencies.

> **Windows note:** Belay builds and runs on Windows but has not been tested in real-world use. macOS and Linux are the primary platforms. If you're a Windows user and want to help, we'd love testers. [Open an issue](https://github.com/davidparkercodes/belay/issues) with your experience.

## Quick Start

```bash
belay init
```

<p align="center">
  <img src="docs/belay-init.png" alt="belay init" width="600" />
</p>

That's it. Belay is now capturing every file change. Go back to work.

## Features

- **Continuous Capture** -- Runs silently in the background. Every file change is saved the instant it happens. No commits, no staging, no action needed.
- **Session Attribution** -- Every change is tagged to the session that made it. Claude Code, Cursor, Copilot, or your own saves. Belay tracks who changed what, when, and in what order.
- **Instant Recovery** -- One command to restore any file to any previous state. Browse history by time or by session and get back what you lost.
- **Conflict Detection** -- Alerts when multiple sessions edit the same file. Untangle overlapping work between agents with a full attribution trail.
- **Zero Intrusion** -- Never modifies your tools or slows them down. Watches the filesystem quietly, but when something goes wrong, your AI agent can use Belay directly.
- **Fully Local** -- All data stays on your machine. No cloud accounts, no sign-ups. A single init command creates your config and starts watching.

## How It Works

Belay uses kernel-level filesystem notifications (FSEvents on macOS, fsnotify on Linux/Windows) to watch your project. Every file write is hashed (SHA-256) and stored in a content-addressable object store with gzip compression. An append-only event log and SQLite index make queries fast across thousands of events.

Session attribution works automatically via process-tree heuristics, or with exact precision through hook-based integration with AI tools like Claude Code.

## AI Tool Integration

### Claude Code

Belay attributes changes to sessions automatically via process-tree heuristics, but adding the hook gives exact attribution. It fires after every file write, telling Belay precisely which session made the change.

Add the hook to your Claude Code settings (`~/.claude/settings.json`):

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Write|Edit|NotebookEdit",
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/belay/hooks/belay-hook.sh"
          }
        ]
      }
    ]
  }
}
```

The hook reads tool use JSON from stdin, extracts the file path and session ID, and calls `belay record` in the background. Non-blocking, will not slow down your AI tool. Without hooks, Belay still captures all changes via filesystem watching and uses process-tree heuristics to attribute sessions.

#### Pre-bash safety net (catches what `/rewind` misses)

Native Claude Code `/rewind` only rolls back edits made through the `Write`, `Edit`, and `NotebookEdit` tools. It does not see destructive bash: `rm -rf`, `git reset --hard`, `git clean`, `dropdb`, build scripts that overwrite files, or one-shot shell commands. Belay's `PreToolUse` Bash hook records a labeled checkpoint before every Bash invocation so you can roll back even those.

Add this hook alongside the PostToolUse one above:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/belay/hooks/belay-pre-bash.sh"
          }
        ]
      }
    ]
  }
}
```

The hook never blocks Claude Code: it runs the checkpoint in the background with a 2-second watchdog and exits 0 even on failure. If a destructive command takes out a file, roll back with:

```bash
belay checkpoints                              # find the checkpoint label or id
belay restore --to-checkpoint <id-or-label> --all --execute
```

You can also create checkpoints manually outside Claude Code:

```bash
belay checkpoint --label "before refactor" --reason "about to move 30 files"
```

### Any Tool

For AI tools that don't support hooks, or custom integrations, push change events directly via the REST API:

```bash
curl -X POST http://localhost:33412/api/record \
  -H "Content-Type: application/json" \
  -d '{"file_path": "src/main.go", "operation": "modify", "tool_name": "my-tool", "session_id": "abc123"}'
```

## CLI Reference

| Command | Description |
|---------|-------------|
| `belay init` | Initialize `.belay/` in the current directory |
| `belay remove` | Remove Belay from the current directory (opposite of init) |
| `belay daemon start` | Start the background watcher daemon |
| `belay daemon stop` | Stop the daemon |
| `belay status` | System and session overview |
| `belay log` | Browse event history with filters |
| `belay grep` | Find events that added or removed a string (pickaxe, like `git log -S`) |
| `belay diff` | View changes between time points or sessions |
| `belay restore` | Recover files by time, event, or session |
| `belay sessions` | List and inspect AI sessions |
| `belay replay` | Replay a session's changes as diffs or patches |
| `belay conflicts` | Detect overlapping modifications across sessions |
| `belay commit` | Generate a git commit from a session |
| `belay snapshot` | Reconstruct project state at any point in time |
| `belay checkpoint` | Mark a labeled, restorable point in time (pairs with `restore --to-checkpoint`) |
| `belay checkpoints` | List recorded checkpoints |
| `belay gc` | Garbage collection and storage compaction |

## API Endpoints

All endpoints served on `:33412` when the daemon is running.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/health` | Health check |
| GET | `/api/stats` | Aggregate statistics |
| GET | `/api/events` | Query events (since, until, file, session, limit) |
| GET | `/api/sessions` | List sessions |
| GET | `/api/sessions/:id` | Session details and events |
| GET | `/api/files/history` | File change history |
| GET | `/api/files/content` | Get file content by hash |
| GET | `/api/conflicts` | Detect conflicts |
| POST | `/api/record` | Push file event from hook |
| POST | `/api/checkpoint` | Record a labeled checkpoint marker |
| GET | `/api/checkpoints` | List recorded checkpoints |
| GET | `/api/stream` | SSE real-time event stream |

## Configuration

Belay stores data in `.belay/` at the project root. Configuration is optional via `.belay/config.toml`:

```toml
[daemon]
log_level = "info"

[watcher]
debounce_ms = 50
max_file_size_mb = 50

[storage]
compression_enabled = true

[retention]
hot_hours = 24        # full fidelity
warm_days = 7         # rapid edits collapsed
cold_days = 30        # session boundaries only
archive_days = 365    # daily snapshots (0 = forever)
max_storage_gb = 10

[api]
port = 33412
host = "127.0.0.1"

[safety]
allow_writes = false  # dry-run mode until you're ready
```

All settings have sensible defaults. `belay init` generates a config with defaults. `safety.allow_writes` defaults to false so destructive commands (restore, replay, commit) show what they'd do without writing. Set to `true` once you trust the setup.

## Platform Support

- **macOS** -- FSEvents for efficient recursive file watching
- **Linux** -- inotify via fsnotify (large repos may need `fs.inotify.max_user_watches` increased)
- **Windows** -- ReadDirectoryChangesW via fsnotify

## Editor Integration

A VS Code extension is included in the `vscode-extension/` directory. It adds a Belay sidebar with recent changes and session views, plus commands for file history and restore.

## License

MIT. See [LICENSE](LICENSE).
