//go:build windows

package storekey

import (
	"fmt"
	"os"
	"time"
)

const (
	lockWait  = 30 * time.Second
	lockStale = 2 * time.Minute
)

func lockFile(path string) (func(), error) {
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("create key lock: %w", err)
		}
		if info, serr := os.Stat(path); serr == nil && time.Since(info.ModTime()) > lockStale {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lock key: timed out waiting for %s", path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
