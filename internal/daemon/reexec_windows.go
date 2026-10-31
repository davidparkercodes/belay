//go:build windows

package daemon

import "errors"

func execSelf(path string, args, env []string) error {
	return errors.New("re-exec is not supported on Windows")
}

func reexecSupported() bool { return false }
