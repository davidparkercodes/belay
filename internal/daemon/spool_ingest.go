package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/davidparkercodes/belay/internal/schema"

	"github.com/davidparkercodes/belay/internal/spool"
)

func (d *Daemon) publishSpoolRecipient() {
	if d.objStore == nil {
		return
	}
	dir := d.cfg.SpoolDir()
	pub, ok := d.objStore.SealRecipient()
	if !ok {
		if err := spool.RemoveRecipient(dir); err != nil {
			d.logger.Printf("warning: remove spool recipient: %v", err)
		}
		return
	}
	if err := spool.PublishRecipient(dir, spool.Recipient{KeyID: d.objStore.KeyID(), PublicKey: pub}); err != nil {
		d.logger.Printf("warning: publish spool recipient: %v", err)
	}
}

// requeueBadSpool gives entries set aside because their sealed content could not be
// opened another chance once this daemon holds a key that opens them.
func (d *Daemon) requeueBadSpool() {
	if d.objStore == nil {
		return
	}
	n, err := spool.Requeue(d.cfg.SpoolDir(), func(sealed []byte) bool {
		_, err := d.objStore.OpenSealed(sealed)
		return err == nil
	})
	if err != nil {
		d.logger.Printf("warning: requeue set-aside spool entries: %v", err)
	}
	if n > 0 {
		d.logger.Printf("requeued %d set-aside spool entr(ies) that the current store key opens", n)
	}
}

func (d *Daemon) ingestSpooled(e *spool.Entry) error {
	if d.objStore == nil {
		return errors.New("no object store")
	}
	if e.Sealed != nil {
		data, err := d.objStore.OpenSealed(e.Sealed)
		if err != nil {
			d.logger.Printf("warning: spooled event %s for %s: %v (kept as .bad)", e.Event.EventID, e.Event.FilePath, err)
			return &spool.Quarantine{Err: err}
		}
		hash, size, err := d.objStore.Put(data)
		if err != nil {
			return fmt.Errorf("store content: %w", err)
		}
		e.Event.ContentHash = hash
		e.Event.ContentSize = size
	}
	if spool.IsDeferred(e.Event) {
		if err := d.storeDeferredContent(e.Event); err != nil {
			return err
		}
	}
	d.processEvent(e.Event)
	return nil
}

func (d *Daemon) storeDeferredContent(ev *schema.Event) error {
	delete(ev.Metadata, spool.MetaDeferredContent)
	if ev.ContentHash != "" || ev.Op == schema.OpDelete || ev.FilePath == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(d.cfg.ProjectRoot, ev.FilePath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		spool.DeferContent(ev)
		return fmt.Errorf("read deferred content: %w", err)
	}
	hash, size, err := d.objStore.Put(data)
	if err != nil {
		spool.DeferContent(ev)
		return fmt.Errorf("store content: %w", err)
	}
	ev.ContentHash = hash
	ev.ContentSize = size
	return nil
}
