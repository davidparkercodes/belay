# Belay Demo — AI-aware local version control
# Generate: vhs demo/demo.tape
# Requires: https://github.com/charmbracelet/vhs

Output demo/belay-demo.gif

Set FontSize 16
Set Width 1200
Set Height 800
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
# Act 1: Initialize Belay
# -------------------------------------------------------------------
Type "# Belay — Never lose work when AI agents write your code"
Enter
Sleep 2s

Type "belay init"
Enter
Sleep 2s

# -------------------------------------------------------------------
# Act 2: Start the daemon
# -------------------------------------------------------------------
Type "belay daemon start"
Enter
Sleep 2.5s

# -------------------------------------------------------------------
# Act 3: Two AI sessions start editing the same project
# -------------------------------------------------------------------
Type "# Two Claude Code sessions are working on the same project..."
Enter
Sleep 1.5s

# Session 1 (claude-session-a7f3) modifies handler.go and main.go
Type `echo 'package main\n\nimport "net/http"\n\nfunc handler(w http.ResponseWriter, r *http.Request) {\n\tw.Write([]byte("hello"))\n}' > src/handler.go`
Enter
Sleep 300ms
Type "belay record src/handler.go -s claude-a7f3e9b1 --tool claude-code -o modify"
Enter
Sleep 500ms

Type `echo 'package main\n\nfunc main() {\n\thttp.HandleFunc("/", handler)\n\thttp.ListenAndServe(":8080", nil)\n}' > src/main.go`
Enter
Sleep 300ms
Type "belay record src/main.go -s claude-a7f3e9b1 --tool claude-code -o modify"
Enter
Sleep 500ms

# Session 2 (claude-session-d2c8) ALSO modifies handler.go — conflict!
Type `echo 'package main\n\nimport "net/http"\n\nfunc handler(w http.ResponseWriter, r *http.Request) {\n\tw.Header().Set("Content-Type", "application/json")\n\tw.Write([]byte("{}"))\n}' > src/handler.go`
Enter
Sleep 300ms
Type "belay record src/handler.go -s claude-d2c8f401 --tool claude-code -o modify"
Enter
Sleep 500ms

# Session 2 also creates a new file
Type `echo 'package main\n\nimport "testing"\n\nfunc TestHandler(t *testing.T) {\n\t// TODO\n}' > src/handler_test.go`
Enter
Sleep 300ms
Type "belay record src/handler_test.go -s claude-d2c8f401 --tool claude-code -o create"
Enter
Sleep 1s

# -------------------------------------------------------------------
# Act 4: See the event timeline
# -------------------------------------------------------------------
Type "belay log --since 5m"
Enter
Sleep 3s

# -------------------------------------------------------------------
# Act 5: See sessions with attribution
# -------------------------------------------------------------------
Type "belay sessions"
Enter
Sleep 3s

# -------------------------------------------------------------------
# Act 6: Detect the conflict!
# -------------------------------------------------------------------
Type "# Both sessions edited handler.go — did Belay catch it?"
Enter
Sleep 1.5s
Type "belay conflicts --since 5m"
Enter
Sleep 3.5s

# -------------------------------------------------------------------
# Act 7: Recover the file from before the conflict
# -------------------------------------------------------------------
Type "# Restore handler.go to Session A's version"
Enter
Sleep 1.5s
Type "belay restore src/handler.go --session claude-d2c8f401 --dry-run"
Enter
Sleep 3s

# -------------------------------------------------------------------
# Closing
# -------------------------------------------------------------------
Type ""
Enter
Sleep 500ms
Type "# Every file change. Every session. Always recoverable."
Enter
Sleep 1s
Type "# github.com/davidparkercodes/belay"
Enter
Sleep 3s
