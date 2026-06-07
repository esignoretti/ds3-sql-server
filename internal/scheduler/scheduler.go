package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// Store is the narrow metastore subset the scheduler depends on.
type Store interface {
	GetDueSchedules(ctx context.Context, now time.Time) ([]*metastore.Schedule, error)
	UpdateScheduleRun(ctx context.Context, id string, lastRun time.Time, running bool) error
}

// Enqueuer hands a due schedule to the job layer. Implementations submit an
// async job and arrange to clear the schedule's Running flag on completion.
type Enqueuer interface {
	Enqueue(sch *metastore.Schedule)
}

// Scheduler ticks over due schedules and enqueues jobs.
type Scheduler struct {
	store Store
	enq   Enqueuer
}

func New(store Store, enq Enqueuer) *Scheduler {
	return &Scheduler{store: store, enq: enq}
}

// cronParser accepts standard 5-field cron expressions.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// computeNextRun returns the next activation strictly after now for a cron spec.
func computeNextRun(spec string, now time.Time) (time.Time, error) {
	sched, err := cronParser.Parse(spec)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron %q: %w", spec, err)
	}
	return sched.Next(now), nil
}

// Tick selects due schedules, advances their next-run, marks them running, and
// enqueues each. Schedules already running are excluded by GetDueSchedules
// (the misfire/overlap skip). A bad cron expression is logged and skipped
// without enqueuing or marking running.
func (s *Scheduler) Tick(ctx context.Context, now time.Time) error {
	due, err := s.store.GetDueSchedules(ctx, now)
	if err != nil {
		return fmt.Errorf("get due schedules: %w", err)
	}
	for _, sch := range due {
		next, err := computeNextRun(sch.Cron, now)
		if err != nil {
			log.Printf("scheduler: skipping schedule %s: %v", sch.ID, err)
			continue
		}
		// Persist running=true with this run's timestamp so overlapping ticks
		// skip it until the job completes and clears the flag.
		if err := s.store.UpdateScheduleRun(ctx, sch.ID, now, true); err != nil {
			log.Printf("scheduler: mark running %s: %v", sch.ID, err)
			continue
		}
		sch.NextRunAt = next
		sch.LastRunAt = now
		sch.Running = true
		s.enq.Enqueue(sch)
	}
	return nil
}

// Run ticks on the given interval until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Tick(ctx, time.Now().UTC()); err != nil {
				log.Printf("scheduler tick: %v", err)
			}
		}
	}
}
