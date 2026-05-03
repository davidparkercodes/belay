//go:build windows

package daemon

import (
	"fmt"
	"os"
	"strconv"
)

type pidLock struct {
	path string
}

func acquirePIDLock(path string) (*pidLock, error) {
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err == nil {
			if _, werr := f.WriteString(strconv.Itoa(os.Getpid())); werr != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return nil, fmt.Errorf("write pidfile: %w", werr)
			}
			_ = f.Sync()
			_ = f.Close()
			return &pidLock{path: path}, nil
		}

		if !os.IsExist(err) {
			return nil, fmt.Errorf("open pidfile: %w", err)
		}

		data, rerr := os.ReadFile(path)
		if rerr == nil {
			if pid, perr := strconv.Atoi(string(data)); perr == nil && isProcessAlive(pid) {
				return nil, &alreadyRunningError{pid: pid}
			}
		}
		if rerr := os.Remove(path); rerr != nil {
			return nil, fmt.Errorf("remove stale pidfile: %w", rerr)
		}
	}
	return nil, fmt.Errorf("could not acquire pidfile lock")
}

func (l *pidLock) Release() {
	if l == nil {
		return
	}
	_ = os.Remove(l.path)
}
