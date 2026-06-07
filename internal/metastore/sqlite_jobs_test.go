package metastore

import (
	"context"
	"testing"
	"time"
)

func TestJobCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	j := &JobRecord{
		ID:        "j1",
		ProjectID: "p1",
		Type:      "query",
		SQL:       "SELECT 1",
		Status:    "running",
		CreatedAt: time.Now().UTC(),
		StartedAt: time.Now().UTC(),
	}
	if err := s.CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	j.Status = "done"
	j.RowCount = 5
	j.BytesScanned = 1024
	j.ResultLocation = "file:///tmp/r.json"
	j.FinishedAt = time.Now().UTC()
	if err := s.UpdateJob(ctx, j); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	got, err := s.GetJob(ctx, "j1")
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != "done" || got.RowCount != 5 || got.BytesScanned != 1024 {
		t.Fatalf("update not persisted: %+v", got)
	}
	if got.ResultLocation != "file:///tmp/r.json" {
		t.Fatalf("result location not persisted: %q", got.ResultLocation)
	}

	if _, err := s.GetJob(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// A second project's job must not leak into p1's history.
	if err := s.CreateJob(ctx, &JobRecord{ID: "j2", ProjectID: "p2", Type: "query", SQL: "SELECT 2", Status: "done", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateJob j2: %v", err)
	}
	list, err := s.ListJobs(ctx, "p1", 10)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(list) != 1 || list[0].ID != "j1" {
		t.Fatalf("expected only j1 for p1, got %+v", list)
	}

	// limit is honored.
	if err := s.CreateJob(ctx, &JobRecord{ID: "j3", ProjectID: "p1", Type: "query", SQL: "x", Status: "done", CreatedAt: time.Now().UTC().Add(time.Second)}); err != nil {
		t.Fatalf("CreateJob j3: %v", err)
	}
	limited, err := s.ListJobs(ctx, "p1", 1)
	if err != nil {
		t.Fatalf("ListJobs limited: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected 1 job with limit 1, got %d", len(limited))
	}
}
