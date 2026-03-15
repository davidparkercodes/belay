package daemon

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/davidparkercodes/belay/internal/schema"
)

func makeTestEvent(path, sessionID string) *schema.Event {
	return &schema.Event{
		EventID:   schema.NewEventID(),
		Version:   schema.SchemaVersion,
		FilePath:  path,
		Op:        schema.OpModify,
		SessionID: sessionID,
	}
}

func TestBurstDetector_SessionEventsAlwaysPassThrough(t *testing.T) {
	var mu sync.Mutex
	var flushed []*schema.Event
	bd := newBurstDetector(func(evt *schema.Event) {
		mu.Lock()
		flushed = append(flushed, evt)
		mu.Unlock()
	})
	bd.threshold = 10

	for i := 0; i < 100; i++ {
		evt := makeTestEvent(fmt.Sprintf("src/file_%d.go", i), "session-ai-123")
		handled := bd.handle(evt)
		if handled {
			t.Errorf("event %d with session should not be handled by burst detector", i)
		}
	}

	_, _, inBurst, buffered := bd.stats()
	if inBurst {
		t.Error("should not enter burst mode from session events")
	}
	if buffered != 0 {
		t.Errorf("expected 0 buffered, got %d", buffered)
	}
}

func TestBurstDetector_EntersBurstOnThreshold(t *testing.T) {
	var mu sync.Mutex
	var flushed []*schema.Event
	bd := newBurstDetector(func(evt *schema.Event) {
		mu.Lock()
		flushed = append(flushed, evt)
		mu.Unlock()
	})
	bd.threshold = 50
	bd.window = 5 * time.Second
	bd.settleTime = 100 * time.Millisecond

	for i := 0; i < 49; i++ {
		evt := makeTestEvent(fmt.Sprintf("src/file_%d.go", i), "")
		handled := bd.handle(evt)
		if handled {
			t.Errorf("event %d should not trigger burst (below threshold)", i)
		}
	}

	evt50 := makeTestEvent("src/file_50.go", "")
	handled := bd.handle(evt50)
	if !handled {
		t.Error("event 50 should trigger burst mode")
	}

	_, _, inBurst, _ := bd.stats()
	if !inBurst {
		t.Error("should be in burst mode after threshold reached")
	}

	for i := 51; i < 100; i++ {
		evt := makeTestEvent(fmt.Sprintf("src/file_%d.go", i), "")
		handled := bd.handle(evt)
		if !handled {
			t.Errorf("event %d should be buffered during burst", i)
		}
	}

	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	flushedCount := len(flushed)
	mu.Unlock()

	if flushedCount != 50 {
		t.Errorf("expected 50 flushed events after settle (event 50 triggered burst + events 51-99), got %d", flushedCount)
	}

	_, _, inBurst, buffered := bd.stats()
	if inBurst {
		t.Error("should exit burst mode after flush")
	}
	if buffered != 0 {
		t.Errorf("expected 0 buffered after flush, got %d", buffered)
	}
}

func TestBurstDetector_CoalescesPerFile(t *testing.T) {
	var mu sync.Mutex
	var flushed []*schema.Event
	bd := newBurstDetector(func(evt *schema.Event) {
		mu.Lock()
		flushed = append(flushed, evt)
		mu.Unlock()
	})
	bd.threshold = 10
	bd.settleTime = 100 * time.Millisecond

	for i := 0; i < 10; i++ {
		bd.handle(makeTestEvent(fmt.Sprintf("src/file_%d.go", i), ""))
	}

	for i := 0; i < 50; i++ {
		evt := makeTestEvent("src/target.go", "")
		evt.ContentHash = fmt.Sprintf("hash-v%d", i)
		bd.handle(evt)
	}

	_, coalesced, _, _ := bd.stats()
	if coalesced < 49 {
		t.Errorf("expected at least 49 coalesced events, got %d", coalesced)
	}

	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	targetCount := 0
	var lastHash string
	for _, evt := range flushed {
		if evt.FilePath == "src/target.go" {
			targetCount++
			lastHash = evt.ContentHash
		}
	}

	if targetCount != 1 {
		t.Errorf("expected 1 flushed event for src/target.go (coalesced), got %d", targetCount)
	}
	if lastHash != "hash-v49" {
		t.Errorf("expected last version hash-v49, got %s", lastHash)
	}
}

func TestBurstDetector_BurstMetadataTagged(t *testing.T) {
	var mu sync.Mutex
	var flushed []*schema.Event
	bd := newBurstDetector(func(evt *schema.Event) {
		mu.Lock()
		flushed = append(flushed, evt)
		mu.Unlock()
	})
	bd.threshold = 5
	bd.settleTime = 100 * time.Millisecond

	for i := 0; i < 20; i++ {
		bd.handle(makeTestEvent(fmt.Sprintf("src/file_%d.go", i), ""))
	}

	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	for _, evt := range flushed {
		if evt.Metadata == nil || evt.Metadata["burst"] != "true" {
			t.Errorf("burst event %s missing burst metadata", evt.FilePath)
		}
	}
}

func TestBurstDetector_MixedSessionAndNonSession(t *testing.T) {
	var mu sync.Mutex
	var flushed []*schema.Event
	bd := newBurstDetector(func(evt *schema.Event) {
		mu.Lock()
		flushed = append(flushed, evt)
		mu.Unlock()
	})
	bd.threshold = 10
	bd.settleTime = 100 * time.Millisecond

	nonSessionHandled := 0
	sessionBypassed := 0

	for i := 0; i < 50; i++ {
		if i%5 == 0 {
			evt := makeTestEvent(fmt.Sprintf("src/ai_file_%d.go", i), "session-ai")
			if !bd.handle(evt) {
				sessionBypassed++
			}
		} else {
			evt := makeTestEvent(fmt.Sprintf("src/format_%d.go", i), "")
			if bd.handle(evt) {
				nonSessionHandled++
			}
		}
	}

	if sessionBypassed != 10 {
		t.Errorf("expected 10 session events to bypass burst, got %d", sessionBypassed)
	}

	if nonSessionHandled < 30 {
		t.Errorf("expected at least 30 non-session events buffered, got %d", nonSessionHandled)
	}
}

func TestBurstDetector_StopFlushesPending(t *testing.T) {
	var mu sync.Mutex
	var flushed []*schema.Event
	bd := newBurstDetector(func(evt *schema.Event) {
		mu.Lock()
		flushed = append(flushed, evt)
		mu.Unlock()
	})
	bd.threshold = 5
	bd.settleTime = 10 * time.Second

	for i := 0; i < 20; i++ {
		bd.handle(makeTestEvent(fmt.Sprintf("src/file_%d.go", i), ""))
	}

	_, _, inBurst, buffered := bd.stats()
	if !inBurst {
		t.Error("should be in burst mode")
	}
	if buffered == 0 {
		t.Error("should have buffered events")
	}

	bd.stop()

	mu.Lock()
	flushedCount := len(flushed)
	mu.Unlock()

	if flushedCount == 0 {
		t.Error("stop() should flush all pending events")
	}

	_, _, inBurst, buffered = bd.stats()
	if inBurst {
		t.Error("should not be in burst mode after stop")
	}
	if buffered != 0 {
		t.Errorf("should have 0 buffered after stop, got %d", buffered)
	}
}
