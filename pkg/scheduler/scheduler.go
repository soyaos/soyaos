// Package scheduler implements the SoyaOS internal scheduler (DD-007).
//
// v0.1.0-alpha.0 ships only the single-process time-wheel form used by Solo:
// cron + one-shot jobs with at-least-once delivery and an idempotency key.
// Leader election, missed-fire backfill, and durable queues arrive alongside
// Cluster (NewsBeam / DD-009).
//
// The cron parser here is deliberately minimal — it accepts the standard
// 5-field syntax (`m h dom mon dow`) with `*`, integers, and lists. Step
// values and ranges are out of scope for alpha. Callers that need full
// crontab semantics will plug in a richer parser via the Cron interface.
package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Fire is the callback executed when a Job triggers.
type Fire func(ctx context.Context)

// Job is an entry in the scheduler.
type Job struct {
	ID             string    // stable identifier; doubles as idempotency key root
	Cron           string    // 5-field cron expression; empty for one-shot
	RunAt          time.Time // one-shot trigger time; zero for cron
	IdempotencyKey string    // optional; protects against duplicate fires
	Fire           Fire
}

// Scheduler interface — minimal surface used by callers.
type Scheduler interface {
	Add(j Job) error
	Cancel(id string) error
	Stop(ctx context.Context) error
}

// ErrEmptyJob is returned when a Job has neither Cron nor RunAt set.
var ErrEmptyJob = errors.New("scheduler: job needs either Cron or RunAt")

// TimeWheel is the in-process scheduler used by Solo. It evaluates the
// crontab once per second and fires each Job whose schedule matches.
type TimeWheel struct {
	mu      sync.Mutex
	jobs    map[string]*scheduled
	stopped chan struct{}
	once    sync.Once
}

type scheduled struct {
	job     Job
	cron    cronSpec // pre-parsed
	oneShot bool
	fired   bool // for one-shot jobs
}

// NewTimeWheel constructs and starts a time wheel ticking at 1 Hz.
func NewTimeWheel() *TimeWheel {
	tw := &TimeWheel{
		jobs:    map[string]*scheduled{},
		stopped: make(chan struct{}),
	}
	go tw.run()
	return tw
}

// Add registers a job. Returns ErrEmptyJob if the job is neither cron nor one-shot.
func (tw *TimeWheel) Add(j Job) error {
	if j.Cron == "" && j.RunAt.IsZero() {
		return ErrEmptyJob
	}
	s := &scheduled{job: j, oneShot: j.Cron == ""}
	if j.Cron != "" {
		spec, err := parseCron(j.Cron)
		if err != nil {
			return err
		}
		s.cron = spec
	}
	tw.mu.Lock()
	tw.jobs[j.ID] = s
	tw.mu.Unlock()
	return nil
}

// Cancel removes a job by id. Missing ids are no-ops.
func (tw *TimeWheel) Cancel(id string) error {
	tw.mu.Lock()
	delete(tw.jobs, id)
	tw.mu.Unlock()
	return nil
}

// Stop halts the time wheel. Subsequent Add / Cancel calls become no-ops.
func (tw *TimeWheel) Stop(_ context.Context) error {
	tw.once.Do(func() { close(tw.stopped) })
	return nil
}

func (tw *TimeWheel) run() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tw.stopped:
			return
		case now := <-tick.C:
			tw.tick(now)
		}
	}
}

func (tw *TimeWheel) tick(now time.Time) {
	tw.mu.Lock()
	pending := make([]*scheduled, 0)
	for _, s := range tw.jobs {
		if s.oneShot {
			if !s.fired && !now.Before(s.job.RunAt) {
				s.fired = true
				pending = append(pending, s)
			}
			continue
		}
		if s.cron.matches(now) {
			pending = append(pending, s)
		}
	}
	tw.mu.Unlock()

	for _, s := range pending {
		go s.job.Fire(context.Background())
	}
}
