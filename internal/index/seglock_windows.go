//go:build windows

package index

import (
	"fmt"
	"os"
)

func acquireSegmentLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrSegmentCompactionBusy
		}
		return nil, fmt.Errorf("create segment lock: %w", err)
	}
	_ = f.Close()
	return func() { _ = os.Remove(path) }, nil
}
