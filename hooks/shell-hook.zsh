#!/usr/bin/env zsh
# Belay Shell Hook for Zsh
#
# Uses the chpwd_functions array for clean integration with other tools.
#
# Usage: eval "$(belay hook init zsh)"

_belay_check_and_start() {
  # Check if .belay/ exists in the current directory
  [[ -d ".belay" ]] || return

  # Check if daemon is already running via PID file
  local pidfile=".belay/daemon.pid"
  if [[ -f "$pidfile" ]]; then
    local pid
    pid=$(<"$pidfile" 2>/dev/null)
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      return
    fi
  fi

  # Find belay binary
  local belay_bin="${BELAY_BIN:-}"
  if [[ -z "$belay_bin" ]]; then
    belay_bin=${commands[belay]:-}
  fi
  [[ -z "$belay_bin" ]] && return

  # Start daemon silently in background
  "$belay_bin" daemon start >/dev/null 2>&1 || {
    echo "belay: warning: failed to auto-start daemon in $(pwd)" >&2
  }
}

# Register with zsh's chpwd hook system
if (( ${+chpwd_functions} )); then
  if [[ ${chpwd_functions[(I)_belay_check_and_start]} -eq 0 ]]; then
    chpwd_functions+=(_belay_check_and_start)
  fi
else
  chpwd_functions=(_belay_check_and_start)
fi

# Run once on shell startup for the initial directory
_belay_check_and_start
