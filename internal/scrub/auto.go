package scrub

import (
	"errors"
	"fmt"
	"time"

	"github.com/davidparkercodes/belay/internal/config"
)

const (
	// AutoTickBytes caps how many bytes of logs one background tick reads.
	AutoTickBytes = 64 << 20
	// AutoTickBudget caps how long one background tick runs.
	AutoTickBudget = 30 * time.Second
	// NoticeTickBudget caps how long a background cache refresh runs in notice mode.
	NoticeTickBudget = 10 * time.Second
	autoTickPause    = 50 * time.Millisecond
)

// Tick is one round of background work for mode: in notice mode it warms the
// findings cache so belay status stays fast, in auto mode it redacts idle logs.
// Work per tick is bounded by AutoTickBytes and AutoTickBudget.
func (s *Scrubber) Tick(mode string, stop <-chan struct{}) (*Report, error) {
	switch mode {
	case config.ScrubNotice:
		CountFindings(s.BelayPath, s.Sources, s.scanner(), NoticeTickBudget)
		return nil, nil
	case config.ScrubAuto:
	default:
		return nil, nil
	}
	rep, err := s.Run(RunOptions{
		MaxBytes:   AutoTickBytes,
		Deadline:   s.now().Add(AutoTickBudget),
		Pause:      autoTickPause,
		Stop:       stop,
		SkipUndone: true,
	})
	if errors.Is(err, ErrBusy) {
		return nil, nil
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	} else {
		failed := 0
		for _, f := range rep.Files {
			if f.Status == StatusError {
				if failed == 0 {
					msg = f.Path + ": " + f.Reason
				}
				failed++
			}
		}
		if failed > 1 {
			msg = fmt.Sprintf("%d logs could not be redacted, first: %s", failed, msg)
		}
	}
	cache := LoadCache(s.BelayPath)
	cache.LastAutoAt = s.now().UTC()
	cache.LastAutoError = msg
	_ = cache.Save(s.BelayPath)
	return rep, err
}
