# Contributing to Belay

## Building

### Backend (Go)

```bash
go build -o bin/belay ./cmd/belay
```

Requires Go 1.24+.

## Running Tests

```bash
go test ./...
go vet ./...
```

## Pull Request Expectations

- One logical change per PR
- All tests must pass
- Include a clear description of what changed and why
- If adding a new CLI command or API endpoint, update the README

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Add godoc comments to all exported types and functions
- Prefer self-documenting function and variable names over explanatory comments
- Keep functions short and focused
- Error messages should be lowercase and actionable

## Project Structure

- `cmd/belay/` -- CLI entry point and subcommands (Cobra)
- `internal/` -- all business logic, not importable by external packages
- `hooks/` -- AI tool integration scripts
- `vscode-extension/` -- VS Code extension
