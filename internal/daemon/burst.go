package daemon

import (
	"sync"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

const (
	defaultBurstThreshold  = 500
	defaultBurstWindow     = 5 * time.Second
	defaultBurstSettleTime = 3 * time.Second
)

type burstDetector struct {
	mu sync.Mutex

	threshold  int
	window     time.Duration
	settleTime time.Duration

	timestamps []time.Time

	inBurst bool
	buffer  map[string]*schema.Event

	settleTimer *time.Timer
	flushFn     func(*schema.Event)

	totalCoalesced int64
	totalBursts    int64
}

func newBurstDetector(flushFn func(*schema.Event)) *burstDetector {
	return &burstDetector{
		threshold:  defaultBurstThreshold,
		window:     defaultBurstWindow,
		settleTime: defaultBurstSettleTime,
		buffer:     make(map[string]*schema.Event),
		flushFn:    flushFn,
	}
}

func (bd *burstDetector) handle(event *schema.Event) bool {
	if event.SessionID != "" {
		return false
	}

	bd.mu.Lock()
	defer bd.mu.Unlock()

	now := time.Now()
	bd.recordTimestamp(now)

	if !bd.inBurst {
		rate := bd.countInWindow(now)
		if rate >= bd.threshold {
			bd.inBurst = true
			bd.totalBursts++
		} else {
			return false
		}
	}

	prev, existed := bd.buffer[event.FilePath]
	bd.buffer[event.FilePath] = event
	if existed && prev != nil {
		bd.totalCoalesced++
	}

	bd.resetSettleTimer()

	return true
}

func (bd *burstDetector) recordTimestamp(now time.Time) {
	cutoff := now.Add(-bd.window)
	start := 0
	for start < len(bd.timestamps) && bd.timestamps[start].Before(cutoff) {
		start++
	}
	if start > 0 {
		bd.timestamps = bd.timestamps[start:]
	}
	bd.timestamps = append(bd.timestamps, now)
}

func (bd *burstDetector) countInWindow(now time.Time) int {
	cutoff := now.Add(-bd.window)
	count := 0
	for i := len(bd.timestamps) - 1; i >= 0; i-- {
		if bd.timestamps[i].Before(cutoff) {
			break
		}
		count++
	}
	return count
}

func (bd *burstDetector) resetSettleTimer() {
	if bd.settleTimer != nil {
		bd.settleTimer.Stop()
	}
	bd.settleTimer = time.AfterFunc(bd.settleTime, func() {
		bd.flush()
	})
}

func (bd *burstDetector) flush() {
	bd.mu.Lock()
	events := make([]*schema.Event, 0, len(bd.buffer))
	for _, evt := range bd.buffer {
		if evt.Metadata == nil {
			evt.Metadata = make(map[string]string)
		}
		evt.Metadata["burst"] = "true"
		events = append(events, evt)
	}
	bd.buffer = make(map[string]*schema.Event)
	bd.inBurst = false
	bd.timestamps = bd.timestamps[:0]
	bd.mu.Unlock()

	for _, evt := range events {
		bd.flushFn(evt)
	}
}

func (bd *burstDetector) stop() {
	bd.mu.Lock()
	if bd.settleTimer != nil {
		bd.settleTimer.Stop()
	}
	bd.mu.Unlock()

	bd.flush()
}

func (bd *burstDetector) stats() (totalBursts, totalCoalesced int64, inBurst bool, buffered int) {
	bd.mu.Lock()
	defer bd.mu.Unlock()
	return bd.totalBursts, bd.totalCoalesced, bd.inBurst, len(bd.buffer)
}
