# Belay for VS Code

AI-aware local version control -- see which AI session changed every file.

## Features

- **Recent Changes Timeline**: Browse recent file changes grouped by time (last hour, today, yesterday), with operation type and session attribution.
- **Sessions View**: See active and recent AI sessions, expand to view files changed by each session.
- **Status Bar**: Live daemon status indicator (connected/disconnected) with 10-second polling.
- **File History**: View the full change history of any file tracked by Belay.
- **Restore**: Restore any file to a previous version captured by Belay.

## Requirements

- **Belay daemon** must be running on `localhost:33412` (default port).
- Start the daemon with: `belay daemon start`

## Commands

| Command | Description |
|---------|-------------|
| `Belay: Refresh` | Refresh the timeline and sessions views |
| `Belay: Show File History` | Show change history for a file |
| `Belay: Restore File to Version` | Restore a file to a previous Belay snapshot |

## Belay API

This extension connects to the Belay HTTP API at `http://127.0.0.1:33412`. Key endpoints used:

- `GET /api/health` -- Daemon health check
- `GET /api/events?limit=50` -- Recent file change events
- `GET /api/sessions?limit=20` -- List sessions
- `GET /api/sessions/{id}/events` -- Events for a specific session
- `GET /api/files/history?path=<path>` -- File change history
- `GET /api/files/content?hash=<sha256>` -- Retrieve file content by hash

## Development

```bash
cd vscode-extension
npm install
npm run compile
```

Press `F5` in VS Code to launch the Extension Development Host for testing.

## License

MIT
