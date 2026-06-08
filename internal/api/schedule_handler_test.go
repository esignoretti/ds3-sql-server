package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// scheduleStoreStub is a minimal in-memory schedule store for handler tests.
type scheduleStoreStub struct {
	created []*metastore.Schedule
}

func (s *scheduleStoreStub) CreateSchedule(ctx context.Context, sch *metastore.Schedule) error {
	s.created = append(s.created, sch)
	return nil
}
func (s *scheduleStoreStub) UpdateSchedule(ctx context.Context, sch *metastore.Schedule) error {
	for i, c := range s.created {
		if c.ID == sch.ID {
			s.created[i] = sch
			return nil
		}
	}
	return metastore.ErrNotFound
}
func (s *scheduleStoreStub) ListSchedules(ctx context.Context, projectID string) ([]*metastore.Schedule, error) {
	return s.created, nil
}
func (s *scheduleStoreStub) GetSchedule(ctx context.Context, id string) (*metastore.Schedule, error) {
	for _, c := range s.created {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, metastore.ErrNotFound
}
func (s *scheduleStoreStub) DeleteSchedule(ctx context.Context, id, projectID string) error { return nil }

func TestScheduleHandler_CreateListDelete(t *testing.T) {
	stub := &scheduleStoreStub{}
	h := NewScheduleHandler(stub)

	body := `{"cron":"0 * * * *","sql":"CREATE TABLE d.t AS SELECT 1","into_table":"d.t"}`
	req := httptest.NewRequest("POST", "/schedules", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.CreateForProject(w, req, "p1", "alice@example.com")
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", w.Code, w.Body.String())
	}
	if len(stub.created) != 1 || stub.created[0].ProjectID != "p1" || stub.created[0].Owner != "alice@example.com" {
		t.Fatalf("schedule not created correctly: %+v", stub.created)
	}
	if stub.created[0].ID == "" || stub.created[0].NextRunAt.IsZero() {
		t.Fatalf("expected generated ID and computed NextRunAt: %+v", stub.created[0])
	}
	if stub.created[0].Type != "query" {
		t.Fatalf("expected default type 'query', got %q", stub.created[0].Type)
	}

	// Bad cron -> 400.
	req = httptest.NewRequest("POST", "/schedules", strings.NewReader(`{"cron":"nope","sql":"SELECT 1"}`))
	w = httptest.NewRecorder()
	h.CreateForProject(w, req, "p1", "alice@example.com")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad cron, got %d", w.Code)
	}

	// List.
	req = httptest.NewRequest("GET", "/schedules", nil)
	w = httptest.NewRecorder()
	h.ListForProject(w, req, "p1")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "0 * * * *") {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}

	// Create a convert schedule.
	body = `{"cron":"0 * * * *","type":"convert","source":"s3://bucket/logs/*.log","format":"text","post_action":"delete"}`
	req = httptest.NewRequest("POST", "/schedules", strings.NewReader(body))
	w = httptest.NewRecorder()
	h.CreateForProject(w, req, "p1", "alice@example.com")
	if w.Code != http.StatusCreated {
		t.Fatalf("create convert schedule status = %d, body=%s", w.Code, w.Body.String())
	}
	if len(stub.created) != 2 {
		t.Fatalf("expected 2 schedules, got %d", len(stub.created))
	}
	if stub.created[1].Type != "convert" || stub.created[1].Source != "s3://bucket/logs/*.log" || stub.created[1].PostAction != "delete" {
		t.Fatalf("convert schedule fields not set: %+v", stub.created[1])
	}
}
