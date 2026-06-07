package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/job"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

type stubExecutor struct{}

func (stubExecutor) Execute(ctx context.Context, req job.ExecRequest) *query.Result {
	return &query.Result{
		Columns:  []query.ColumnInfo{{Name: "n", Type: "INTEGER"}},
		Rows:     [][]any{{int64(1)}},
		RowCount: 1,
	}
}

func TestJobHandler_SubmitSyncAndGet(t *testing.T) {
	mgr := job.NewManager(stubExecutor{})
	h := NewJobHandler(mgr)

	req := httptest.NewRequest("POST", "/jobs", strings.NewReader(`{"sql":"SELECT 1"}`))
	w := httptest.NewRecorder()
	h.SubmitWithCreds(w, req, "p1", "ak", "sk", "http://localhost:9000")
	if w.Code != http.StatusOK {
		t.Fatalf("submit status = %d, body=%s", w.Code, w.Body.String())
	}
	var submitted job.Job
	if err := json.Unmarshal(w.Body.Bytes(), &submitted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if submitted.Status != "done" || submitted.Result.RowCount != 1 {
		t.Fatalf("unexpected job: %+v", submitted)
	}

	// Get by ID
	req = httptest.NewRequest("GET", "/jobs/"+submitted.ID, nil)
	req = withURLParam(req, "id", submitted.ID)
	w = httptest.NewRecorder()
	h.Get(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d", w.Code)
	}

	// Missing SQL -> 400
	req = httptest.NewRequest("POST", "/jobs", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	h.SubmitWithCreds(w, req, "p1", "ak", "sk", "http://localhost:9000")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing sql, got %d", w.Code)
	}
}
