# Belay Demo — background capture, session attribution, instant recovery
# Generate: vhs demo/demo.tape
# Requires: https://github.com/charmbracelet/vhs

Output demo/belay-demo.gif

Set FontSize 16
Set Width 1200
Set Height 700
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set Padding 20

# -------------------------------------------------------------------
# Setup: create an isolated temp project so the demo is self-contained
# -------------------------------------------------------------------
Hide
Type "export TMPDIR=$(mktemp -d) && cd $TMPDIR && mkdir my-project && cd my-project && git init -q"
Enter
Sleep 500ms
Type "mkdir -p src && echo 'package main' > src/main.go && echo 'func handler() {}' > src/handler.go && echo '# My Project' > README.md"
Enter
Sleep 500ms
Show

# -------------------------------------------------------------------
# Act 1: Set up Belay (two commands, done)
# -------------------------------------------------------------------
Type "belay init && belay daemon start"
Enter
Sleep 3s

# -------------------------------------------------------------------
# Act 2: Simulate AI agents editing files in the background
# The watcher captures these automatically — no belay commands needed
# -------------------------------------------------------------------
Type "# Two AI agents start editing the same project..."
Enter
Sleep 2s

# Agent A rewrites handler.go
Hide
Type `echo 'package main\n\nimport "net/http"\n\nfunc handler(w http.ResponseWriter, r *http.Request) {\n\tw.Write([]byte("hello"))\n}' > src/handler.go`
Enter
Sleep 1s
Type `echo 'package main\n\nfunc main() {\n\thttp.HandleFunc("/", handler)\n\thttp.ListenAndServe(":8080", nil)\n}' > src/main.go`
Enter
Sleep 1s
# Agent B also edits handler.go — conflict
Type `echo 'package main\n\nimport "net/http"\n\nfunc handler(w http.ResponseWriter, r *http.Request) {\n\tw.Header().Set("Content-Type", "application/json")\n\tw.Write([]byte("{}"))\n}' > src/handler.go`
Enter
Sleep 1s
Type `echo 'package main\n\nimport "testing"\n\nfunc TestHandler(t *testing.T) {\n\t// TODO\n}' > src/handler_test.go`
Enter
Sleep 1s
# Push hook events for attribution (simulating what hooks do automatically)
Type "belay record src/handler.go -s claude-opus-a3f9 --tool claude-code -o modify 2>/dev/null; belay record src/main.go -s claude-opus-a3f9 --tool claude-code -o modify 2>/dev/null; belay record src/handler.go -s claude-sonnet-b7c2 --tool claude-code -o modify 2>/dev/null; belay record src/handler_test.go -s claude-sonnet-b7c2 --tool claude-code -o create 2>/dev/null"
Enter
Sleep 1s
Show

# -------------------------------------------------------------------
# Act 3: Check what happened — Belay captured everything
# -------------------------------------------------------------------
Type "belay status"
Enter
Sleep 3s

# -------------------------------------------------------------------
# Act 4: Both agents edited the same file — Belay caught it
# -------------------------------------------------------------------
Type "belay conflicts --since 5m"
Enter
Sleep 3.5s

# -------------------------------------------------------------------
# Act 5: Recover the version you want
# -------------------------------------------------------------------
Type "belay restore src/handler.go --session claude-opus-a3f9 --dry-run"
Enter
Sleep 3.5s

Sleep 2s
