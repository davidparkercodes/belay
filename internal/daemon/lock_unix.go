//go:build !windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

type pidLock struct {
	file *os.File
	path string
}

func acquirePIDLock(path string) (*pidLock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open pidfile: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			data, _ := os.ReadFile(path)
			pid, _ := strconv.Atoi(string(data))
			return nil, &alreadyRunningError{pid: pid}
		}
		return nil, fmt.Errorf("flock pidfile: %w", err)
	}

	if err := f.Truncate(0); err != nil {
		releasePIDLockFile(f)
		return nil, fmt.Errorf("truncate pidfile: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		releasePIDLockFile(f)
		return nil, fmt.Errorf("seek pidfile: %w", err)
	}
	if _, err := f.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		releasePIDLockFile(f)
		return nil, fmt.Errorf("write pidfile: %w", err)
	}
	if err := f.Sync(); err != nil {
		releasePIDLockFile(f)
		return nil, fmt.Errorf("sync pidfile: %w", err)
	}

	return &pidLock{file: f, path: path}, nil
}

func releasePIDLockFile(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

func (l *pidLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	releasePIDLockFile(l.file)
	_ = os.Remove(l.path)
	l.file = nil
}
