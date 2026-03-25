# Changelog

All notable changes to Belay are documented here.

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
