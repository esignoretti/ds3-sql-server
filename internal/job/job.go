package job

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// ExecRequest is a query execution request with the caller's S3 credentials.
type ExecRequest struct {
	SQL       string
	ProjectID string
	AccessKey string
	SecretKey string
	Endpoint  string
}

// Executor runs a query and returns its result. LocalExecutor runs in-process;
// Phase 2 will add a remote (worker-pool) implementation behind this same seam.
type Executor interface {
	Execute(ctx context.Context, req ExecRequest) *query.Result
}

// Job is a tracked unit of work. Phase 1 supports only synchronous "query" jobs.
type Job struct {
	ID        string        `json:"id"`
	Type      string        `json:"type"`
	SQL       string        `json:"sql"`
	Status    string        `json:"status"` // queued | running | done | failed
	Result    *query.Result `json:"result,omitempty"`
	Error     string        `json:"error,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

type Manager struct {
	exec Executor
	mu   sync.RWMutex
	jobs map[string]*Job
}

func NewManager(exec Executor) *Manager {
	return &Manager{exec: exec, jobs: make(map[string]*Job)}
}

// Run executes the request synchronously (sync fast-path) and returns the job.
func (m *Manager) Run(ctx context.Context, req ExecRequest) *Job {
	j := &Job{
		ID:        uuid.NewString(),
		Type:      "query",
		SQL:       req.SQL,
		Status:    "running",
		CreatedAt: time.Now().UTC(),
	}
	m.put(j)

	res := m.exec.Execute(ctx, req)
	if res.Error != "" {
		j.Status = "failed"
		j.Error = res.Error
	} else {
		j.Status = "done"
		j.Result = res
	}
	m.put(j)
	return j
}

func (m *Manager) put(j *Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.ID] = j
}

func (m *Manager) Get(id string) (*Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	return j, ok
}

// Cleanup removes jobs older than maxAge (called periodically from main).
func (m *Manager) Cleanup(maxAge time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, j := range m.jobs {
		if now.Sub(j.CreatedAt) > maxAge {
			delete(m.jobs, id)
		}
	}
}
