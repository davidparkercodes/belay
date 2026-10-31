#!/usr/bin/env bash
# Belay PostToolUse Hook for Claude Code
#
# Called by Claude Code after every tool use. Reads JSON from stdin and
# notifies the Belay daemon about file writes for hook-based attribution.
#
# Install: Add to ~/.claude/settings.json PostToolUse hooks array
#
# Env:
#   BELAY_BIN           - path to the belay binary (default: PATH, then common install dirs)
#   BELAY_HOOK_DISABLE  - set to 1 to make this hook a no-op on this machine
#
# Stdin JSON fields used:
#   tool_name       - Tool that was used (Write, Edit, NotebookEdit, Bash, etc.)
#   tool_input      - Tool input params (file_path for Write/Edit)
#   session_id      - Claude Code session identifier
#   cwd             - Current working directory
#
# Write, Edit and NotebookEdit record the written file with exact attribution.
# Every other hook event (SessionStart, a Bash tool use, ...) only links the
# calling agent process to the session id, so writes the agent makes through a
# shell are attributed to the same session as its editor writes.

set -euo pipefail

# Read JSON payload from stdin
PAYLOAD=$(cat)

# Extract fields with python3
PARSED=$(python3 -c "
import json, sys, os

try:
    p = json.loads(sys.argv[1])
except Exception:
    sys.exit(0)

tool = p.get('tool_name', '')
sid = p.get('session_id', '')
cwd = p.get('cwd', '')

if tool not in ('Write', 'Edit', 'NotebookEdit'):
    if sid:
        print(f'LINK\n\n{sid}\n{cwd}')
    sys.exit(0)

# Get file path from tool_input
ti = p.get('tool_input', {})
fp = ti.get('file_path', ti.get('notebook_path', ''))
if not fp:
    sys.exit(0)

if not os.path.isabs(fp) and cwd:
    fp = os.path.join(cwd, fp)

# Determine operation
if tool == 'Write':
    op = 'create' if not os.path.exists(ti.get('file_path', '')) else 'modify'
else:
    op = 'modify'

print(f'{fp}\n{op}\n{sid}\n{cwd}')
" "$PAYLOAD" 2>/dev/null) || exit 0

# Parse the output
FILE_PATH=$(echo "$PARSED" | sed -n '1p')
OP=$(echo "$PARSED" | sed -n '2p')
SESSION_ID=$(echo "$PARSED" | sed -n '3p')
CWD=$(echo "$PARSED" | sed -n '4p')

[ -z "$FILE_PATH" ] && exit 0

[ "${BELAY_HOOK_DISABLE:-}" = 1 ] && exit 0

# Find belay binary
BELAY_BIN="${BELAY_BIN:-}"
if [ -z "$BELAY_BIN" ]; then
  BELAY_BIN=$(command -v belay 2>/dev/null || true)
fi
if [ -z "$BELAY_BIN" ]; then
  for candidate in \
    "$(dirname "$0")/../bin/belay" \
    "$HOME/go/bin/belay" \
    "$HOME/.local/bin/belay" \
    "/usr/local/bin/belay" \
    "/opt/homebrew/bin/belay" \
    "/home/linuxbrew/.linuxbrew/bin/belay"; do
    if [ -x "$candidate" ]; then
      BELAY_BIN="$candidate"
      break
    fi
  done
fi

[ -z "$BELAY_BIN" ] && exit 0

if [ "$FILE_PATH" = "LINK" ]; then
  (
    cd "${CWD:-.}" 2>/dev/null || exit 0
    "$BELAY_BIN" sessions link "$SESSION_ID" --tool "claude-code" --quiet
  ) &>/dev/null &
  exit 0
fi

# Fire and forget -- don't block Claude Code
"$BELAY_BIN" record "$FILE_PATH" \
  -o "$OP" \
  --tool "claude-code" \
  ${SESSION_ID:+--session "$SESSION_ID"} \
  &>/dev/null &

exit 0
