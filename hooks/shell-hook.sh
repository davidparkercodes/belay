#!/usr/bin/env sh
# Belay Shell Hook (universal -- works in bash and zsh)
#
# Automatically starts the Belay daemon when you cd into a directory
# that contains a .belay/ directory.
#
# Usage: eval "$(belay hook init bash)" or eval "$(belay hook init zsh)"
# This file is sourced by the shell-specific init output.

_belay_check_and_start() {
  # Check if .belay/ exists in the current directory
  if [ ! -d ".belay" ]; then
    return
  fi

  # Check if daemon is already running via PID file
  local pidfile=".belay/daemon.pid"
  if [ -f "$pidfile" ]; then
    local pid
    pid=$(cat "$pidfile" 2>/dev/null)
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      # Daemon is already running
      return
    fi
  fi

  # Find belay binary
  local belay_bin="${BELAY_BIN:-}"
  if [ -z "$belay_bin" ]; then
    belay_bin=$(command -v belay 2>/dev/null || true)
  fi
  if [ -z "$belay_bin" ]; then
    return
  fi

  # Start daemon silently in background
  "$belay_bin" daemon start >/dev/null 2>&1 || {
    echo "belay: warning: failed to auto-start daemon in $(pwd)" >&2
  }
}
