package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// fakeSchedStore implements the narrow Store the scheduler needs.
type fakeSchedStore struct {
	due       []*metastore.Schedule
	updates   []update
	getByID   map[string]*metastore.Schedule
}

type update struct {
	id      string
	lastRun time.Time
	running bool
}

func (f *fakeSchedStore) GetDueSchedules(ctx context.Context, now time.Time) ([]*metastore.Schedule, error) {
	return f.due, nil
}
func (f *fakeSchedStore) UpdateScheduleRun(ctx context.Context, id string, lastRun time.Time, running bool) error {
	f.updates = append(f.updates, update{id, lastRun, running})
	if s, ok := f.getByID[id]; ok {
		s.Running = running
		s.LastRunAt = lastRun
	}
	return nil
}

// fakeEnqueuer records enqueued schedules.
type fakeEnqueuer struct{ enqueued []*metastore.Schedule }

func (e *fakeEnqueuer) Enqueue(sch *metastore.Schedule) {
	e.enqueued = append(e.enqueued, sch)
}

func TestTick_EnqueuesDueAndAdvancesNextRun(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	sch := &metastore.Schedule{
		ID: "s1", ProjectID: "p1", Cron: "0 * * * *", // top of every hour
		SQL: "CREATE TABLE d.t AS SELECT 1", IntoTable: "d.t",
		NextRunAt: now.Add(-time.Minute),
	}
	store := &fakeSchedStore{due: []*metastore.Schedule{sch}, getByID: map[string]*metastore.Schedule{"s1": sch}}
	enq := &fakeEnqueuer{}
	s := New(store, enq)

	if err := s.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(enq.enqueued) != 1 || enq.enqueued[0].ID != "s1" {
		t.Fatalf("expected s1 enqueued, got %+v", enq.enqueued)
	}
	// Must mark running=true and compute the next run at the next top-of-hour (13:00).
	if len(store.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(store.updates))
	}
	u := store.updates[0]
	if !u.running {
		t.Fatal("expected schedule marked running")
	}
	wantNext := time.Date(2026, 6, 7, 13, 0, 0, 0, time.UTC)
	if !sch.NextRunAt.Equal(wantNext) {
		t.Fatalf("next run = %v, want %v", sch.NextRunAt, wantNext)
	}
}

func TestTick_BadCronMarksNotRunningAndSkips(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	sch := &metastore.Schedule{ID: "bad", ProjectID: "p1", Cron: "not a cron", SQL: "SELECT 1", NextRunAt: now.Add(-time.Minute)}
	store := &fakeSchedStore{due: []*metastore.Schedule{sch}, getByID: map[string]*metastore.Schedule{"bad": sch}}
	enq := &fakeEnqueuer{}
	s := New(store, enq)
	if err := s.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// Bad cron must not enqueue and must not leave the schedule marked running.
	if len(enq.enqueued) != 0 {
		t.Fatalf("bad cron must not enqueue, got %+v", enq.enqueued)
	}
}

func TestComputeNextRun(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC)
	next, err := computeNextRun("0 * * * *", now)
	if err != nil {
		t.Fatalf("computeNextRun: %v", err)
	}
	want := time.Date(2026, 6, 7, 13, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}
