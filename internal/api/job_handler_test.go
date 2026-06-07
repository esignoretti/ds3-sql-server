package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// slowExecutor blocks for `delay` before returning, to exercise the 202 path.
type slowExecutor struct{ delay time.Duration }

func (s slowExecutor) Execute(ctx context.Context, req job.ExecRequest) *query.Result {
	select {
	case <-time.After(s.delay):
		return &query.Result{RowCount: 1}
	case <-ctx.Done():
		return &query.Result{Error: "cancelled"}
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

func TestJobHandler_WaitFastPathReturnsInline(t *testing.T) {
	mgr := job.NewManager(slowExecutor{delay: 1 * time.Millisecond})
	h := NewJobHandler(mgr)

	req := httptest.NewRequest("POST", "/jobs?wait=2s", strings.NewReader(`{"sql":"SELECT 1"}`))
	w := httptest.NewRecorder()
	h.SubmitWithCreds(w, req, "p1", "ak", "sk", "http://localhost:9000")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 inline, got %d body=%s", w.Code, w.Body.String())
	}
	var j job.Job
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if j.Status != "done" {
		t.Fatalf("expected done within wait, got %q", j.Status)
	}
}

func TestJobHandler_WaitTimeoutReturns202(t *testing.T) {
	mgr := job.NewManager(slowExecutor{delay: 500 * time.Millisecond})
	h := NewJobHandler(mgr)

	req := httptest.NewRequest("POST", "/jobs?wait=10ms", strings.NewReader(`{"sql":"SELECT 1"}`))
	w := httptest.NewRecorder()
	h.SubmitWithCreds(w, req, "p1", "ak", "sk", "http://localhost:9000")
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 on wait timeout, got %d", w.Code)
	}
	var j job.Job
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if j.ID == "" {
		t.Fatal("expected a job id for polling")
	}
	if j.Status == "done" {
		t.Fatal("job should not be done yet")
	}
}

func TestJobHandler_RoutesCTAS(t *testing.T) {
	mgr := job.NewManager(stubExecutor{})
	mgr.SetWriteExecutor(stubWriteExec{})
	h := NewJobHandler(mgr)

	body := `{"sql":"CREATE TABLE sales.daily AS SELECT 1"}`
	req := httptest.NewRequest("POST", "/jobs", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.SubmitWithCreds(w, req, "p1", "ak", "sk", "http://localhost:9000")
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for async ctas, got %d body=%s", w.Code, w.Body.String())
	}
	var j job.Job
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if j.Type != "ctas" {
		t.Fatalf("expected type ctas, got %q", j.Type)
	}
}

func TestJobHandler_RoutesLoad(t *testing.T) {
	mgr := job.NewManager(stubExecutor{})
	mgr.SetWriteExecutor(stubWriteExec{})
	h := NewJobHandler(mgr)

	body := `{"type":"load","source":"s3://b/*.csv","into":"sales.ev","format":"csv","mode":"append"}`
	req := httptest.NewRequest("POST", "/jobs", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.SubmitWithCreds(w, req, "p1", "ak", "sk", "http://localhost:9000")
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for async load, got %d body=%s", w.Code, w.Body.String())
	}
	var j job.Job
	_ = json.Unmarshal(w.Body.Bytes(), &j)
	if j.Type != "load" {
		t.Fatalf("expected type load, got %q", j.Type)
	}
}

type stubWriteExec struct{}

func (stubWriteExec) RunCTAS(ctx context.Context, projectID, sql, ak, sk, ep string) (string, error) {
	return "sales.daily", nil
}
func (stubWriteExec) RunLoad(ctx context.Context, projectID string, req job.LoadRequest, ak, sk, ep string) (string, error) {
	return req.Into, nil
}

func TestJobHandler_ListAndCancel(t *testing.T) {
	mgr := job.NewManager(slowExecutor{delay: 200 * time.Millisecond})
	h := NewJobHandler(mgr)

	// Submit one async job.
	req := httptest.NewRequest("POST", "/jobs?wait=1ms", strings.NewReader(`{"sql":"SELECT 1"}`))
	w := httptest.NewRecorder()
	h.SubmitWithCreds(w, req, "p1", "ak", "sk", "http://localhost:9000")
	var submitted job.Job
	_ = json.Unmarshal(w.Body.Bytes(), &submitted)

	// List.
	lreq := httptest.NewRequest("GET", "/jobs", nil)
	lw := httptest.NewRecorder()
	h.ListForProject(lw, lreq, "p1")
	if lw.Code != http.StatusOK || !strings.Contains(lw.Body.String(), submitted.ID) {
		t.Fatalf("list missing job: %d %s", lw.Code, lw.Body.String())
	}

	// Cancel.
	creq := httptest.NewRequest("DELETE", "/jobs/"+submitted.ID, nil)
	creq = withURLParam(creq, "id", submitted.ID)
	cw := httptest.NewRecorder()
	h.Cancel(cw, creq)
	if cw.Code != http.StatusOK && cw.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d", cw.Code)
	}

	// Cancel unknown -> 404.
	creq2 := httptest.NewRequest("DELETE", "/jobs/nope", nil)
	creq2 = withURLParam(creq2, "id", "nope")
	cw2 := httptest.NewRecorder()
	h.Cancel(cw2, creq2)
	if cw2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 cancelling unknown job, got %d", cw2.Code)
	}
}
