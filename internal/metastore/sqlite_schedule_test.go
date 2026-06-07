package metastore

import (
	"context"
	"testing"
	"time"
)

func TestScheduleCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

	sch := &Schedule{
		ID:        "sch-1",
		ProjectID: "p1",
		Cron:      "0 * * * *",
		SQL:       "CREATE TABLE sales.hourly AS SELECT 1",
		IntoTable: "sales.hourly",
		Owner:     "alice@example.com",
		NextRunAt: now.Add(time.Hour),
		CreatedAt: now,
	}
	if err := s.CreateSchedule(ctx, sch); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	got, err := s.GetSchedule(ctx, "sch-1")
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if got.Cron != "0 * * * *" || got.IntoTable != "sales.hourly" || got.Owner != "alice@example.com" {
		t.Fatalf("schedule round-trip failed: %+v", got)
	}

	list, err := s.ListSchedules(ctx, "p1")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListSchedules: err=%v len=%d", err, len(list))
	}

	// Mark running with a last-run time, then GetDueSchedules must skip it
	// (running) when due, and exclude it (next-run in the future) regardless.
	lastRun := now.Add(time.Hour)
	if err := s.UpdateScheduleRun(ctx, "sch-1", lastRun, true); err != nil {
		t.Fatalf("UpdateScheduleRun: %v", err)
	}
	got, _ = s.GetSchedule(ctx, "sch-1")
	if !got.Running || !got.LastRunAt.Equal(lastRun) {
		t.Fatalf("UpdateScheduleRun not applied: %+v", got)
	}

	// Make it due: next_run in the past, not running.
	if err := s.UpdateScheduleRun(ctx, "sch-1", lastRun, false); err != nil {
		t.Fatalf("UpdateScheduleRun clear: %v", err)
	}
	// Manually set NextRunAt in the past by recreating via a second schedule.
	due := &Schedule{
		ID: "sch-2", ProjectID: "p1", Cron: "0 * * * *",
		SQL: "SELECT 1", NextRunAt: now.Add(-time.Minute), CreatedAt: now,
	}
	if err := s.CreateSchedule(ctx, due); err != nil {
		t.Fatalf("CreateSchedule due: %v", err)
	}
	dueList, err := s.GetDueSchedules(ctx, now)
	if err != nil {
		t.Fatalf("GetDueSchedules: %v", err)
	}
	foundDue := false
	for _, d := range dueList {
		if d.ID == "sch-2" {
			foundDue = true
		}
		if d.Running {
			t.Fatalf("GetDueSchedules returned a running schedule: %+v", d)
		}
	}
	if !foundDue {
		t.Fatalf("expected sch-2 to be due, got %+v", dueList)
	}

	if err := s.DeleteSchedule(ctx, "sch-1"); err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}
	if _, err := s.GetSchedule(ctx, "sch-1"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
