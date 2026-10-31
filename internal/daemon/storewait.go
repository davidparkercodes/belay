package daemon

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/davidparkercodes/belay/internal/store"
	"github.com/davidparkercodes/belay/internal/storekey"
)

var storeRetryInterval = time.Minute

var errShutdownWhileLocked = errors.New("shut down while the store was locked")

// storeLocked reports errors that a missing or wrong key causes. The daemon waits them
// out instead of exiting, and never creates or switches keys to get past them.
func storeLocked(err error) bool {
	return errors.Is(err, store.ErrWrongKey) || errors.Is(err, storekey.ErrKeyUnavailable)
}

// waitForStore opens the object store. While the key is missing or wrong the daemon
// stays up holding its PID lock, writes nothing, and retries; hooks keep queueing.
func (d *Daemon) waitForStore(sigCh <-chan os.Signal, retry time.Duration) error {
	if d.objStore != nil {
		return nil
	}
	logged := false
	for {
		st, err := store.OpenConfig(d.cfg)
		if err == nil {
			if logged {
				d.logger.Println("store key available, starting")
			}
			d.objStore = st
			return nil
		}
		if !storeLocked(err) {
			return fmt.Errorf("init object store: %w", err)
		}
		if !logged {
			d.logger.Printf("store locked, recording paused (events stay queued): %v", err)
			logged = true
		}
		select {
		case <-sigCh:
			return errShutdownWhileLocked
		case <-time.After(retry):
		}
	}
}
