package job

import (
	"context"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/query"
)

type fakeExecutor struct{ called bool }

func (f *fakeExecutor) Execute(ctx context.Context, req ExecRequest) *query.Result {
	f.called = true
	return &query.Result{
		Columns:  []query.ColumnInfo{{Name: "c", Type: "BIGINT"}},
		Rows:     [][]any{{int64(1)}},
		RowCount: 1,
	}
}

func TestManager_RunSync(t *testing.T) {
	fe := &fakeExecutor{}
	m := NewManager(fe)
	j := m.Run(context.Background(), ExecRequest{SQL: "SELECT 1", ProjectID: "p1"})
	if !fe.called {
		t.Fatal("executor not called")
	}
	if j.Status != "done" {
		t.Fatalf("expected status done, got %s", j.Status)
	}
	if j.Result == nil || j.Result.RowCount != 1 {
		t.Fatalf("unexpected result: %+v", j.Result)
	}
	if j.ID == "" {
		t.Fatal("job ID not set")
	}
	got, ok := m.Get(j.ID)
	if !ok || got.ID != j.ID {
		t.Fatalf("Get(%s) failed", j.ID)
	}
}

func TestManager_RunError(t *testing.T) {
	m := NewManager(execFunc(func(ctx context.Context, req ExecRequest) *query.Result {
		return &query.Result{Error: "boom"}
	}))
	j := m.Run(context.Background(), ExecRequest{SQL: "bad"})
	if j.Status != "failed" {
		t.Fatalf("expected status failed, got %s", j.Status)
	}
	if j.Error != "boom" {
		t.Fatalf("expected error boom, got %q", j.Error)
	}
}

type execFunc func(ctx context.Context, req ExecRequest) *query.Result

func (f execFunc) Execute(ctx context.Context, req ExecRequest) *query.Result { return f(ctx, req) }
