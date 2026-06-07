package job

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/query"
)

type fakeWriteExec struct {
	mu     sync.Mutex
	ctas   int
	load   int
	intoT  string
}

func (f *fakeWriteExec) RunCTAS(ctx context.Context, projectID, sql, ak, sk, ep string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ctas++
	return "sales.daily", nil
}

func (f *fakeWriteExec) RunLoad(ctx context.Context, projectID string, req LoadRequest, ak, sk, ep string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.load++
	f.intoT = req.Into
	return req.Into, nil
}

// readExec satisfies Executor for the query path.
type readExec struct{}

func (readExec) Execute(ctx context.Context, req ExecRequest) *query.Result {
	return &query.Result{RowCount: 0}
}

func waitDone(t *testing.T, m *Manager, id string) *Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := m.Get(id); ok && (j.Status == "done" || j.Status == "failed") {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish", id)
	return nil
}

func TestManager_RoutesCTAS(t *testing.T) {
	fw := &fakeWriteExec{}
	m := NewManager(readExec{})
	m.SetWriteExecutor(fw)

	j := m.Submit(context.Background(), ExecRequest{
		Type: "ctas", SQL: "CREATE TABLE sales.daily AS SELECT 1", ProjectID: "p1",
	})
	done := waitDone(t, m, j.ID)
	if done.Status != "done" {
		t.Fatalf("expected done, got %s (%s)", done.Status, done.Error)
	}
	if fw.ctas != 1 {
		t.Fatalf("expected 1 ctas call, got %d", fw.ctas)
	}
	if done.IntoTable != "sales.daily" {
		t.Fatalf("expected IntoTable sales.daily, got %q", done.IntoTable)
	}
}

func TestManager_RoutesLoad(t *testing.T) {
	fw := &fakeWriteExec{}
	m := NewManager(readExec{})
	m.SetWriteExecutor(fw)

	j := m.Submit(context.Background(), ExecRequest{
		Type:      "load",
		Load:      &LoadRequest{Source: "s3://b/*.csv", Into: "sales.ev", Format: "csv", Mode: "append"},
		ProjectID: "p1",
	})
	done := waitDone(t, m, j.ID)
	if done.Status != "done" {
		t.Fatalf("expected done, got %s (%s)", done.Status, done.Error)
	}
	if fw.load != 1 || fw.intoT != "sales.ev" {
		t.Fatalf("load not routed: load=%d into=%q", fw.load, fw.intoT)
	}
}
