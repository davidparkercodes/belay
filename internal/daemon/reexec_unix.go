//go:build !windows

package daemon

import "syscall"

func execSelf(path string, args, env []string) error {
	return syscall.Exec(path, args, env)
}

func reexecSupported() bool { return true }
