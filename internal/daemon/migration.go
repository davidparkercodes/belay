package daemon

import (
	"errors"
	"time"

	"github.com/davidparkercodes/belay/internal/store"
)

const (
	migrationStartupSettle = 30 * time.Second
	migrationRetry         = 10 * time.Minute
)

var migrationSettle = migrationStartupSettle

func (d *Daemon) newMigrator() *store.Migrator {
	return &store.Migrator{
		Store:     d.objStore,
		Index:     d.idx,
		EventsDir: d.cfg.EventsDir(),
		LockPath:  store.MigrationLockPath(d.cfg.BelayPath),
		Logf:      d.logger.Printf,
		Exclusive: func(fn func() error) error {
			d.eventMu.Lock()
			defer d.eventMu.Unlock()
			if err := d.logWriter.Rotate(); err != nil {
				return err
			}
			return fn()
		},
		ActiveSegment: d.logWriter.CurrentSegment,
		Stop:          d.stopMigration,
	}
}

func (d *Daemon) startMigration() {
	d.stopMigration = make(chan struct{})
	d.migrationDone = make(chan struct{})
	go d.runMigrationLoop()
}

func (d *Daemon) runMigrationLoop() {
	defer close(d.migrationDone)
	d.detectLegacyObjects()
	wait := migrationSettle
	for {
		if !d.objStore.NeedsMigration() {
			return
		}
		select {
		case <-time.After(wait):
		case <-d.stopMigration:
			return
		}
		d.migrating.Store(true)
		res, err := d.newMigrator().Run()
		d.migrating.Store(false)
		switch {
		case err == nil:
			d.logger.Printf("store migration complete: %d objects encrypted, %d corrupt kept, %d legacy objects removed",
				res.Copied, res.Corrupt, res.LegacyDeleted)
			return
		case errors.Is(err, store.ErrMigrationStopped):
			return
		default:
			d.logger.Printf("store migration paused, will retry: %v", err)
		}
		wait = migrationRetry
	}
}

func (d *Daemon) stopMigrationLoop() {
	if d.stopMigration == nil {
		return
	}
	close(d.stopMigration)
	<-d.migrationDone
	d.stopMigration = nil
}

// HoldDaemonLock keeps a daemon from starting while a manual task rewrites the store.
func HoldDaemonLock(pidPath string) (func(), error) {
	l, err := acquirePIDLock(pidPath)
	if err != nil {
		return nil, err
	}
	return l.Release, nil
}

// detectLegacyObjects reopens a finished migration when legacy objects showed up again,
// for example after an older copy of .belay was synced over this one.
func (d *Daemon) detectLegacyObjects() {
	if found, ok := store.ReopenMigrationIfLegacy(d.objStore, d.idx); ok {
		d.logger.Printf("store holds legacy object %s again; resuming migration", found[:8])
	}
}
