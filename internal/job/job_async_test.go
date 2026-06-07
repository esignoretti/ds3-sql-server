package job

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/query"
)

type blockingExecutor struct {
	release chan struct{}
	started chan struct{}
	once    sync.Once
}

func (b *blockingExecutor) Execute(ctx context.Context, req ExecRequest) *query.Result {
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.release:
		return &query.Result{Columns: []query.ColumnInfo{{Name: "c"}}, Rows: [][]any{{int64(1)}}, RowCount: 1}
	case <-ctx.Done():
		return &query.Result{Error: "cancelled: " + ctx.Err().Error()}
	}
}

type recordingSink struct {
	mu    sync.Mutex
	saves int
}

func (s *recordingSink) Save(ctx context.Context, j *Job) error {
	s.mu.Lock()
	s.saves++
	s.mu.Unlock()
	return nil
}

func waitStatus(t *testing.T, m *Manager, id, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := m.Get(id); ok && j.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	j, _ := m.Get(id)
	t.Fatalf("job %s did not reach %q (last=%v)", id, want, j)
}

func TestSubmit_AsyncCompletes(t *testing.T) {
	be := &blockingExecutor{release: make(chan struct{}), started: make(chan struct{})}
	sink := &recordingSink{}
	m := NewManager(be)
	m.SetSink(sink)

	j := m.Submit(context.Background(), ExecRequest{SQL: "SELECT 1", ProjectID: "p1"})
	if j.Status != "queued" && j.Status != "running" {
		t.Fatalf("expected queued/running immediately, got %q", j.Status)
	}
	<-be.started
	waitStatus(t, m, j.ID, "running")
	close(be.release)
	waitStatus(t, m, j.ID, "done")

	got, _ := m.Get(j.ID)
	if got.Result == nil || got.Result.RowCount != 1 {
		t.Fatalf("unexpected result: %+v", got.Result)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.saves < 2 {
		t.Fatalf("expected at least 2 sink saves (create + terminal), got %d", sink.saves)
	}
}

func TestSubmit_Cancel(t *testing.T) {
	be := &blockingExecutor{release: make(chan struct{}), started: make(chan struct{})}
	m := NewManager(be)

	j := m.Submit(context.Background(), ExecRequest{SQL: "SELECT 1", ProjectID: "p1"})
	<-be.started
	waitStatus(t, m, j.ID, "running")

	if !m.Cancel(j.ID) {
		t.Fatal("Cancel returned false for a running job")
	}
	waitStatus(t, m, j.ID, "cancelled")

	if m.Cancel("nope") {
		t.Fatal("Cancel of unknown job must return false")
	}
}

func TestExecutorFunc_Satisfies(t *testing.T) {
	var e Executor = ExecutorFunc(func(ctx context.Context, req ExecRequest) *query.Result {
		return &query.Result{RowCount: 1}
	})
	if e.Execute(context.Background(), ExecRequest{}).RowCount != 1 {
		t.Fatal("ExecutorFunc did not invoke the closure")
	}
}

func TestList_ReturnsRecent(t *testing.T) {
	m := NewManager(execFunc(func(ctx context.Context, req ExecRequest) *query.Result {
		return &query.Result{RowCount: 0}
	}))
	m.Run(context.Background(), ExecRequest{SQL: "a", ProjectID: "p1"})
	m.Run(context.Background(), ExecRequest{SQL: "b", ProjectID: "p1"})
	m.Run(context.Background(), ExecRequest{SQL: "c", ProjectID: "p2"})

	list := m.List("p1", 10)
	if len(list) != 2 {
		t.Fatalf("expected 2 jobs for p1, got %d", len(list))
	}
}
