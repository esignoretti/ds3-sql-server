package job

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/query"
)

type gateExecutor struct {
	mu      sync.Mutex
	running int32
	maxSeen int32
	release chan struct{}
}

func (g *gateExecutor) Execute(ctx context.Context, req ExecRequest) *query.Result {
	n := atomic.AddInt32(&g.running, 1)
	g.mu.Lock()
	if n > g.maxSeen {
		g.maxSeen = n
	}
	g.mu.Unlock()
	<-g.release
	atomic.AddInt32(&g.running, -1)
	return &query.Result{RowCount: 1}
}

func (g *gateExecutor) MaxSeen() int32 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.maxSeen
}

func TestAdmission_ThirdJobQueuesWhenLimitTwo(t *testing.T) {
	g := &gateExecutor{release: make(chan struct{})}
	m := NewManager(g)
	m.SetQuota(2)

	for i := 0; i < 3; i++ {
		m.Submit(context.Background(), ExecRequest{SQL: "q", ProjectID: "p1"})
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&g.running) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&g.running); got > 2 {
		t.Fatalf("limit breached: %d concurrent (max allowed 2)", got)
	}

	// Wait for all goroutines to finish releasing
	close(g.release)
	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(&g.running) != 0 {
		t.Fatalf("expected all goroutines to finish, running=%d", atomic.LoadInt32(&g.running))
	}
	if g.MaxSeen() > 2 {
		t.Fatalf("max concurrency exceeded limit: %d", g.MaxSeen())
	}
}

func TestAdmission_OtherProjectNotBlocked(t *testing.T) {
	g := &gateExecutor{release: make(chan struct{})}
	m := NewManager(g)
	m.SetQuota(1)

	m.Submit(context.Background(), ExecRequest{SQL: "q", ProjectID: "p1"})
	m.Submit(context.Background(), ExecRequest{SQL: "q", ProjectID: "p1"})
	m.Submit(context.Background(), ExecRequest{SQL: "q", ProjectID: "p2"})

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&g.running) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt32(&g.running) < 2 {
		t.Fatalf("expected p1 and p2 both running (2), got %d", atomic.LoadInt32(&g.running))
	}
	close(g.release)
	time.Sleep(50 * time.Millisecond)
}
