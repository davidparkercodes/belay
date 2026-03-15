#!/usr/bin/env bash
# Belay Shell Hook for Bash
#
# Wraps the builtin cd command to check for .belay/ directories.
#
# Usage: eval "$(belay hook init bash)"

_belay_check_and_start() {
  # Check if .belay/ exists in the current directory
  [[ -d ".belay" ]] || return

  # Check if daemon is already running via PID file
  local pidfile=".belay/daemon.pid"
  if [[ -f "$pidfile" ]]; then
    local pid
    pid=$(cat "$pidfile" 2>/dev/null)
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      return
    fi
  fi

  # Find belay binary
  local belay_bin="${BELAY_BIN:-}"
  if [[ -z "$belay_bin" ]]; then
    belay_bin=$(command -v belay 2>/dev/null || true)
  fi
  [[ -z "$belay_bin" ]] && return

  # Start daemon silently in background
  "$belay_bin" daemon start >/dev/null 2>&1 || {
    echo "belay: warning: failed to auto-start daemon in $(pwd)" >&2
  }
}

# Wrap cd to trigger the hook
if [[ -z "$_BELAY_HOOK_INSTALLED" ]]; then
  _BELAY_HOOK_INSTALLED=1

  cd() {
    builtin cd "$@" || return $?
    _belay_check_and_start
  }

  pushd() {
    builtin pushd "$@" || return $?
    _belay_check_and_start
  }

  popd() {
    builtin popd "$@" || return $?
    _belay_check_and_start
  }

  # Run once on shell startup for the initial directory
  _belay_check_and_start
fi
