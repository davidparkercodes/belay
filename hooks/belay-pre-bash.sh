#!/usr/bin/env bash
# Belay PreToolUse Bash hook for Claude Code.
#
# Records a labeled checkpoint BEFORE Claude Code runs a Bash command, so you
# can roll back even when the command is destructive (rm -rf, git reset --hard,
# build scripts, dropdb). Native /rewind only tracks Write/Edit/NotebookEdit;
# this closes that gap.
#
# Install: add to ~/.claude/settings.json under hooks.PreToolUse with matcher "Bash":
#   {
#     "hooks": {
#       "PreToolUse": [
#         {
#           "matcher": "Bash",
#           "hooks": [{ "type": "command", "command": "/path/to/hooks/belay-pre-bash.sh" }]
#         }
#       ]
#     }
#   }
#
# This hook never blocks Claude Code: total wall time is capped at 2 seconds
# and any failure exits 0 silently.

set -u

PAYLOAD=$(cat)

PARSED=$(python3 -c "
import json, sys

try:
    p = json.loads(sys.argv[1])
except Exception:
    sys.exit(0)

if p.get('tool_name', '') != 'Bash':
    sys.exit(0)

ti = p.get('tool_input', {}) or {}
cmd = (ti.get('command', '') or '').strip()
if not cmd:
    sys.exit(0)

cmd_one_line = ' '.join(cmd.split())
if len(cmd_one_line) > 120:
    cmd_one_line = cmd_one_line[:117] + '...'

sid = p.get('session_id', '') or ''
cwd = p.get('cwd', '') or ''

print(cmd_one_line)
print(sid)
print(cwd)
" "$PAYLOAD" 2>/dev/null) || exit 0

CMD_LABEL=$(echo "$PARSED" | sed -n '1p')
SESSION_ID=$(echo "$PARSED" | sed -n '2p')
CWD=$(echo "$PARSED" | sed -n '3p')

[ -z "$CMD_LABEL" ] && exit 0

if [ -n "$CWD" ] && [ -d "$CWD" ]; then
  if [ ! -d "$CWD/.belay" ]; then
    parent="$CWD"
    while [ "$parent" != "/" ] && [ ! -d "$parent/.belay" ]; do
      parent=$(dirname "$parent")
    done
    [ "$parent" = "/" ] && exit 0
  fi
fi

BELAY_BIN="${BELAY_BIN:-}"
if [ -z "$BELAY_BIN" ]; then
  for candidate in \
    "$(dirname "$0")/../bin/belay" \
    "$HOME/go/bin/belay" \
    "/usr/local/bin/belay" \
    "/opt/homebrew/bin/belay"; do
    if [ -x "$candidate" ]; then
      BELAY_BIN="$candidate"
      break
    fi
  done
fi

[ -z "$BELAY_BIN" ] && exit 0

LABEL="pre-bash: $CMD_LABEL"

(
  cd "${CWD:-.}" 2>/dev/null || exit 0
  "$BELAY_BIN" checkpoint \
    --label "$LABEL" \
    --reason "claude-code PreToolUse Bash" \
    --tool "claude-code" \
    ${SESSION_ID:+--session "$SESSION_ID"} \
    --quiet
) >/dev/null 2>&1 &

HOOK_PID=$!

(
  sleep 2
  kill -9 "$HOOK_PID" 2>/dev/null
) &
WATCHDOG_PID=$!

wait "$HOOK_PID" 2>/dev/null
kill "$WATCHDOG_PID" 2>/dev/null
wait "$WATCHDOG_PID" 2>/dev/null

exit 0
