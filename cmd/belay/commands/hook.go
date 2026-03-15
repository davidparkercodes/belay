package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Shell hook for automatic daemon start",
		Long: `Manage the Belay shell hook that automatically starts the daemon
when you cd into a directory containing a .belay/ directory.

The hook is silent on success and only warns if something goes wrong.`,
	}

	cmd.AddCommand(
		newHookInitCmd(),
		newHookInstallCmd(),
	)

	return cmd
}

func newHookInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init <shell>",
		Short: "Output shell hook code for eval",
		Long: `Output the shell hook code to stdout for the given shell.

Usage:
  # Add to your ~/.zshrc:
  eval "$(belay hook init zsh)"

  # Add to your ~/.bashrc:
  eval "$(belay hook init bash)"`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh"},
		RunE:      runHookInit,
	}
}

func runHookInit(cmd *cobra.Command, args []string) error {
	shell := strings.ToLower(args[0])

	// Find the hooks directory relative to the belay binary
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	switch shell {
	case "zsh":
		fmt.Print(zshHookCode(exe))
	case "bash":
		fmt.Print(bashHookCode(exe))
	default:
		return fmt.Errorf("unsupported shell: %s (supported: bash, zsh)", shell)
	}

	return nil
}

func newHookInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Show instructions to install the shell hook",
		Long: `Detects your shell and prints the line you need to add to your
shell configuration file to enable automatic daemon start.`,
		RunE: runHookInstall,
	}
}

func runHookInstall(cmd *cobra.Command, args []string) error {
	shell := detectShell()
	rcFile := shellRCFile(shell)

	evalLine := fmt.Sprintf(`eval "$(belay hook init %s)"`, shell)

	// Check if already installed
	if rcFile != "" {
		if content, err := os.ReadFile(rcFile); err == nil {
			if strings.Contains(string(content), "belay hook init") {
				fmt.Printf("Belay shell hook is already installed in %s\n", rcFile)
				return nil
			}
		}
	}

	fmt.Printf("Add this to your %s:\n\n", rcFile)
	fmt.Printf("  %s\n\n", evalLine)
	fmt.Println("Then restart your shell or run:")
	fmt.Printf("  source %s\n", rcFile)

	return nil
}

func detectShell() string {
	shellEnv := os.Getenv("SHELL")
	if strings.HasSuffix(shellEnv, "/zsh") {
		return "zsh"
	}
	if strings.HasSuffix(shellEnv, "/bash") {
		return "bash"
	}
	// Default based on OS
	if runtime.GOOS == "darwin" {
		return "zsh"
	}
	return "bash"
}

func shellRCFile(shell string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zshrc")
	case "bash":
		return filepath.Join(home, ".bashrc")
	default:
		return filepath.Join(home, ".profile")
	}
}

// zshHookCode returns the shell code that zsh should eval.
// It inlines the hook function so there's no dependency on finding the hooks/ directory at runtime.
// The binary resolution order is: $BELAY_BIN > hardcoded exe path > command -v belay.
func zshHookCode(belayExe string) string {
	return fmt.Sprintf(`# Belay shell hook -- auto-start daemon on cd
_belay_check_and_start() {
  [[ -d ".belay" ]] || return

  local pidfile=".belay/daemon.pid"
  if [[ -f "$pidfile" ]]; then
    local pid
    pid=$(<"$pidfile" 2>/dev/null)
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      return
    fi
  fi

  local belay_bin="${BELAY_BIN:-}"
  if [[ -z "$belay_bin" ]]; then
    if [[ -x "%s" ]]; then
      belay_bin="%s"
    else
      belay_bin=${commands[belay]:-}
    fi
  fi
  [[ -z "$belay_bin" ]] && return

  "$belay_bin" daemon start >/dev/null 2>&1 || {
    echo "belay: warning: failed to auto-start daemon in $(pwd)" >&2
  }
}

if (( ${+chpwd_functions} )); then
  if [[ ${chpwd_functions[(I)_belay_check_and_start]} -eq 0 ]]; then
    chpwd_functions+=(_belay_check_and_start)
  fi
else
  chpwd_functions=(_belay_check_and_start)
fi

_belay_check_and_start
`, belayExe, belayExe)
}

// bashHookCode returns the shell code that bash should eval.
func bashHookCode(belayExe string) string {
	return fmt.Sprintf(`# Belay shell hook -- auto-start daemon on cd
_belay_check_and_start() {
  [[ -d ".belay" ]] || return

  local pidfile=".belay/daemon.pid"
  if [[ -f "$pidfile" ]]; then
    local pid
    pid=$(cat "$pidfile" 2>/dev/null)
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      return
    fi
  fi

  local belay_bin="${BELAY_BIN:-}"
  if [[ -z "$belay_bin" ]]; then
    if [[ -x "%s" ]]; then
      belay_bin="%s"
    else
      belay_bin=$(command -v belay 2>/dev/null || true)
    fi
  fi
  [[ -z "$belay_bin" ]] && return

  "$belay_bin" daemon start >/dev/null 2>&1 || {
    echo "belay: warning: failed to auto-start daemon in $(pwd)" >&2
  }
}

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

  _belay_check_and_start
fi
`, belayExe, belayExe)
}
