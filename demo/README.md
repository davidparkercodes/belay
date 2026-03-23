# Belay Demo

Terminal recording of Belay's key features using [VHS](https://github.com/charmbracelet/vhs).

## Generate the GIF

```bash
brew install charmbracelet/tap/vhs
vhs demo/demo.tape
```

The output GIF will be written to `demo/belay-demo.gif`.

## What the demo shows

1. Initialize Belay and start the daemon (`belay init && belay daemon start`)
2. AI agents edit files in the background (the watcher captures everything automatically)
3. Check system status and active sessions (`belay status`)
4. Detect a conflict where both agents edited the same file (`belay conflicts`)
5. Recover the version you want (`belay restore --dry-run`)

## Notes

- The demo executes real `belay` commands against a temp project directory
- `belay` must be in your PATH (run `go install ./cmd/belay` from the repo root)
- File edits happen off-screen (hidden) to simulate background agent activity
- Total runtime is approximately 25 seconds
