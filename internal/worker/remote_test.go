package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/job"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// fakeResolver returns fixed bindings regardless of SQL.
type fakeResolver struct{ bindings []WireBinding }

func (f fakeResolver) ResolveWire(ctx context.Context, projectID, sql string) ([]WireBinding, error) {
	return f.bindings, nil
}

func TestRemoteExecutor_RoundTrip(t *testing.T) {
	// Stand-in worker: echoes a fixed result, asserting the secret arrived.
	var gotSecret string
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecret = r.Header.Get(SecretHeader)
		json.NewEncoder(w).Encode(query.Result{RowCount: 99})
	}))
	defer worker.Close()

	re := NewRemoteExecutor([]string{worker.URL}, "sekret", fakeResolver{bindings: []WireBinding{
		{Schema: "sales", Name: "orders", ReaderSQL: "read_parquet('s3://cold/o/*.parquet')", StorageClass: "hdd"},
	}})
	res := re.Execute(context.Background(), job.ExecRequest{SQL: "SELECT 1", ProjectID: "p1"})
	if res.Error != "" {
		t.Fatalf("execute error: %s", res.Error)
	}
	if res.RowCount != 99 {
		t.Fatalf("expected echoed result 99, got %d", res.RowCount)
	}
	if gotSecret != "sekret" {
		t.Fatalf("worker did not receive the shared secret, got %q", gotSecret)
	}
}

func TestRemoteExecutor_FallsBackWhenPreferredDown(t *testing.T) {
	// Healthy worker.
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(query.Result{RowCount: 7})
	}))
	defer good.Close()

	// Down worker: an address that is closed.
	down := "http://127.0.0.1:1"

	// Put the down worker first so the ring may prefer it; the executor must
	// fall back to the healthy one and still succeed.
	re := NewRemoteExecutor([]string{down, good.URL}, "s", fakeResolver{})
	res := re.Execute(context.Background(), job.ExecRequest{SQL: "SELECT 1", ProjectID: "p1"})
	if res.Error != "" {
		t.Fatalf("expected fallback success, got error: %s", res.Error)
	}
	if res.RowCount != 7 {
		t.Fatalf("expected result from healthy worker, got %d", res.RowCount)
	}
}

func TestRemoteExecutor_NoWorkers(t *testing.T) {
	re := NewRemoteExecutor(nil, "s", fakeResolver{})
	res := re.Execute(context.Background(), job.ExecRequest{SQL: "SELECT 1", ProjectID: "p1"})
	if res.Error == "" {
		t.Fatal("expected an error when no workers are configured")
	}
}
