package daemon

import (
	"sync"
	"time"

	gitbridge "github.com/davidparkercodes/belay/internal/git"
)

const projectorHistoryLimit = 20

type projectionRecord struct {
	Job        gitbridge.ProjectionJob  `json:"job"`
	Summary    string                   `json:"summary"`
	Error      string                   `json:"error,omitempty"`
	StartedAt  time.Time                `json:"started_at"`
	FinishedAt time.Time                `json:"finished_at"`
	Pushed     bool                     `json:"pushed"`
	HeldBack   []gitbridge.HeldBackFile `json:"held_back,omitempty"`
	Warnings   []string                 `json:"warnings,omitempty"`
}

// projector serializes git projections inside the daemon: one worker, one queue,
// identical pending jobs coalesced, so any number of hooks can hand off a session
// and return immediately instead of each running git and fighting over a lock.
type projector struct {
	d *Daemon

	mu      sync.Mutex
	queue   []gitbridge.ProjectionJob
	running *gitbridge.ProjectionJob
	history []projectionRecord
	total   int
	failed  int

	wake chan struct{}
	stop chan struct{}
	done chan struct{}
}

func newProjector(d *Daemon) *projector {
	return &projector{
		d:    d,
		wake: make(chan struct{}, 1),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
}

func (p *projector) Start() {
	go p.run()
}

func (p *projector) Stop() {
	close(p.stop)
	<-p.done
}

// Enqueue adds a job unless an identical one is already waiting. It returns the
// number of jobs waiting after the call (including this one) and whether it was added.
func (p *projector) Enqueue(job gitbridge.ProjectionJob) (pending int, added bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, q := range p.queue {
		if q.Equal(job) {
			return len(p.queue), false
		}
	}
	p.queue = append(p.queue, job)
	select {
	case p.wake <- struct{}{}:
	default:
	}
	return len(p.queue), true
}

func (p *projector) Status() map[string]interface{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	pending := make([]string, 0, len(p.queue))
	for _, q := range p.queue {
		pending = append(pending, q.String())
	}
	status := map[string]interface{}{
		"pending":      len(p.queue),
		"pending_jobs": pending,
		"running":      nil,
		"total_runs":   p.total,
		"failed_runs":  p.failed,
		"recent":       append([]projectionRecord(nil), p.history...),
	}
	if p.running != nil {
		status["running"] = p.running.String()
	}
	if len(p.history) > 0 {
		last := p.history[len(p.history)-1]
		status["last"] = last
	}
	return status
}

func (p *projector) next() (gitbridge.ProjectionJob, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queue) == 0 {
		return gitbridge.ProjectionJob{}, false
	}
	job := p.queue[0]
	p.queue = p.queue[1:]
	p.running = &job
	return job, true
}

func (p *projector) finish(rec projectionRecord) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running = nil
	p.total++
	if rec.Error != "" {
		p.failed++
	}
	p.history = append(p.history, rec)
	if len(p.history) > projectorHistoryLimit {
		p.history = p.history[len(p.history)-projectorHistoryLimit:]
	}
}

func (p *projector) run() {
	defer close(p.done)
	for {
		for {
			job, ok := p.next()
			if !ok {
				break
			}
			p.execute(job)
		}
		select {
		case <-p.stop:
			return
		case <-p.wake:
		}
	}
}

func (p *projector) execute(job gitbridge.ProjectionJob) {
	if !p.d.cfg.Projection.HoldBackSecrets {
		job.AllowSecrets = true
	}
	rec := projectionRecord{Job: job, StartedAt: time.Now()}
	report, err := gitbridge.RunProjection(p.d.idx, p.d.objStore, p.d.cfg.ProjectRoot, job)
	rec.FinishedAt = time.Now()
	if report != nil {
		rec.Summary = report.Summary()
		rec.Pushed = report.Pushed
		rec.HeldBack = report.HeldBack
		rec.Warnings = report.Warnings
		for _, w := range report.Warnings {
			p.d.logger.Printf("projection guard: %s", w)
		}
		if notice := gitbridge.HeldBackNotice(report.HeldBack); notice != "" {
			p.d.logger.Println(notice)
		}
	}
	if err != nil {
		rec.Error = err.Error()
		p.d.logger.Printf("projection failed (%s): %v", job.String(), err)
	} else {
		p.d.logger.Printf("projection done (%s): %s", job.String(), rec.Summary)
	}
	p.finish(rec)
}
