package daemon

import (
	"time"

	"github.com/davidparkercodes/belay/internal/config"
	"github.com/davidparkercodes/belay/internal/scrub"
	"github.com/davidparkercodes/belay/internal/secrets"
)

const (
	scrubStartupSettle = 5 * time.Minute
	scrubInterval      = 15 * time.Minute
)

var scrubSources = scrub.DefaultSources

func (d *Daemon) startScrubLoop() {
	d.stopScrub = make(chan struct{})
	d.scrubDone = make(chan struct{})
	go func() {
		defer close(d.scrubDone)
		select {
		case <-time.After(scrubStartupSettle):
		case <-d.stopScrub:
			return
		}
		ticker := time.NewTicker(scrubInterval)
		defer ticker.Stop()
		for {
			d.scrubTick()
			select {
			case <-ticker.C:
			case <-d.stopScrub:
				return
			}
		}
	}()
}

func (d *Daemon) scrubTick() {
	cfg, err := config.Load(d.cfg.ProjectRoot)
	if err != nil {
		cfg = d.cfg
	}
	if cfg.Scrub.Mode == config.ScrubOff {
		return
	}
	s := &scrub.Scrubber{
		BelayPath: d.cfg.BelayPath,
		Store:     d.objStore,
		Sources:   scrubSources(d.cfg.ProjectRoot),
		Scanner:   secrets.NewScanner(secrets.Options{}),
		Active: func() map[string]bool {
			active := scrub.ActiveSessionIDs(d.idx)
			if d.registry != nil {
				for _, s := range d.registry.ActiveSessions() {
					active[s.SessionID] = true
				}
			}
			return active
		},
	}
	rep, err := s.Tick(cfg.Scrub.Mode, d.stopScrub)
	if err != nil {
		d.logger.Printf("agent log scrub: %v", err)
		return
	}
	if rep != nil && rep.Redacted > 0 {
		d.logger.Printf("agent log scrub: redacted %d log(s), %d finding(s)", rep.Redacted, rep.Findings)
	}
}

func (d *Daemon) stopScrubLoop() {
	if d.stopScrub == nil {
		return
	}
	close(d.stopScrub)
	<-d.scrubDone
	d.stopScrub = nil
}
