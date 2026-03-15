# Belay Demo

Terminal recording of Belay's key features using [VHS](https://github.com/charmbracelet/vhs).

## Generate the GIF

```bash
# Install VHS (requires Go 1.22+ and ffmpeg)
brew install charmbracelet/tap/vhs

# Generate the recording
vhs demo/demo.tape
```

The output GIF will be written to `demo/belay-demo.gif`.

## What the demo shows

1. Initialize Belay in a project (`belay init`)
2. Start the file watcher daemon (`belay daemon start`)
3. Two AI sessions editing the same codebase via `belay record`
4. Event timeline with session attribution (`belay log`)
5. Session overview (`belay sessions`)
6. Conflict detection when both sessions edit the same file (`belay conflicts`)
7. File recovery to a pre-conflict state (`belay restore --dry-run`)

## Notes

- The demo executes real `belay` commands against a temp project directory
- `belay` must be in your PATH (run `go install ./cmd/belay` from the repo root)
- The daemon starts and stops within the recording
- Total runtime is approximately 45 seconds
