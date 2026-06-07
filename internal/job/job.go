package job

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// ExecRequest is a query/write execution request with the caller's S3
// credentials. Type selects the execution path ("query" default; "ctas"/"load"
// added in Phase 3).
type ExecRequest struct {
	Type      string       // "query" (default), "ctas", or "load"
	SQL       string
	ProjectID string
	AccessKey string
	SecretKey string
	Endpoint  string
	Load      *LoadRequest // set when Type == "load"
}

// LoadRequest mirrors write.LoadRequest at the job boundary so the job package
// does not import write (write imports job-free). main.go adapts between them.
type LoadRequest struct {
	Source      string   `json:"source"`
	Into        string   `json:"into"`
	Format      string   `json:"format"`
	PartitionBy []string `json:"partition_by"`
	Mode        string   `json:"mode"`
}

// Executor runs a query and returns its result. LocalExecutor runs in-process;
// Phase 2 will add a remote (worker-pool) implementation behind this same seam.
type Executor interface {
	Execute(ctx context.Context, req ExecRequest) *query.Result
}

// Job is a tracked unit of work.
type Job struct {
	ID        string        `json:"id"`
	ProjectID string        `json:"project_id"`
	Type      string        `json:"type"`
	SQL       string        `json:"sql"`
	Status    string        `json:"status"` // queued | running | done | failed | cancelled
	Result    *query.Result `json:"result,omitempty"`
	Error     string        `json:"error,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	IntoTable string        `json:"into_table,omitempty"`
}

type Manager struct {
	exec    Executor
	mu      sync.RWMutex
	jobs    map[string]*Job
	cancels map[string]context.CancelFunc
	sink    JobSink
	admit   *admission
	write   WriteExecutor
}

func NewManager(exec Executor) *Manager {
	return &Manager{
		exec:    exec,
		jobs:    make(map[string]*Job),
		cancels: make(map[string]context.CancelFunc),
	}
}

func (m *Manager) SetSink(s JobSink) { m.sink = s }

func (m *Manager) SetQuota(maxConcurrentPerProject int) {
	if maxConcurrentPerProject > 0 {
		m.admit = newAdmission(maxConcurrentPerProject)
	}
}

func (m *Manager) save(ctx context.Context, j *Job) {
	if m.sink != nil {
		_ = m.sink.Save(ctx, j)
	}
}

// Run executes the request synchronously (sync fast-path) and returns the job.
func (m *Manager) Run(ctx context.Context, req ExecRequest) *Job {
	typ := req.Type
	if typ == "" {
		typ = "query"
	}
	j := &Job{
		ID:        uuid.NewString(),
		ProjectID: req.ProjectID,
		Type:      typ,
		SQL:       req.SQL,
		Status:    "running",
		CreatedAt: time.Now().UTC(),
	}
	m.put(j)
	m.save(ctx, j)

	res := m.exec.Execute(ctx, req)

	m.mu.Lock()
	if res.Error != "" {
		j.Status = "failed"
		j.Error = res.Error
	} else {
		j.Status = "done"
		j.Result = res
	}
	m.jobs[j.ID] = j
	m.mu.Unlock()
	m.save(ctx, j)
	return j
}

// Submit runs the request asynchronously and returns immediately with a job in
// status "queued". Admission control (if enabled) may hold the job in "queued"
// until a per-project slot is free; otherwise it transitions to "running".
func (m *Manager) Submit(parent context.Context, req ExecRequest) *Job {
	typ := req.Type
	if typ == "" {
		typ = "query"
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &Job{
		ID:        uuid.NewString(),
		ProjectID: req.ProjectID,
		Type:      typ,
		SQL:       req.SQL,
		Status:    "queued",
		CreatedAt: time.Now().UTC(),
	}
	m.put(j)
	m.setCancel(j.ID, cancel)
	m.save(ctx, j)

	cp := *j
	go func() {
		defer m.clearCancel(j.ID)
		if m.admit != nil {
			if !m.admit.acquire(ctx, req.ProjectID) {
				m.save(ctx, m.updateStatus(j, "cancelled"))
				return
			}
			defer m.admit.release(req.ProjectID)
		}
		if ctx.Err() != nil {
			m.save(ctx, m.updateStatus(j, "cancelled"))
			return
		}
		m.save(ctx, m.updateStatus(j, "running"))

		switch typ {
		case "ctas":
			m.mu.RLock()
			we := m.write
			m.mu.RUnlock()
			if we == nil {
				m.mu.Lock()
				j.Status, j.Error = "failed", "write executor not configured"
				m.jobs[j.ID] = j
				m.mu.Unlock()
				m.save(ctx, j)
				return
			}
			into, err := we.RunCTAS(ctx, req.ProjectID, req.SQL, req.AccessKey, req.SecretKey, req.Endpoint)
			m.mu.Lock()
			if err != nil {
				j.Status, j.Error = "failed", err.Error()
			} else {
				j.Status, j.IntoTable = "done", into
			}
			m.jobs[j.ID] = j
			m.mu.Unlock()
			m.save(ctx, j)
		case "load":
			m.mu.RLock()
			we := m.write
			m.mu.RUnlock()
			if we == nil || req.Load == nil {
				m.mu.Lock()
				j.Status, j.Error = "failed", "load request or write executor missing"
				m.jobs[j.ID] = j
				m.mu.Unlock()
				m.save(ctx, j)
				return
			}
			into, err := we.RunLoad(ctx, req.ProjectID, *req.Load, req.AccessKey, req.SecretKey, req.Endpoint)
			m.mu.Lock()
			if err != nil {
				j.Status, j.Error = "failed", err.Error()
			} else {
				j.Status, j.IntoTable = "done", into
			}
			m.jobs[j.ID] = j
			m.mu.Unlock()
			m.save(ctx, j)
		default:
			res := m.exec.Execute(ctx, req)
			m.mu.Lock()
			switch {
			case ctx.Err() != nil:
				j.Status = "cancelled"
			case res.Error != "":
				j.Status = "failed"
				j.Error = res.Error
			default:
				j.Status = "done"
				j.Result = res
			}
			m.jobs[j.ID] = j
			m.mu.Unlock()
			m.save(ctx, j)
		}
	}()
	return &cp
}

func (m *Manager) updateStatus(j *Job, status string) *Job {
	m.mu.Lock()
	j.Status = status
	m.jobs[j.ID] = j
	m.mu.Unlock()
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
	if !ok {
		return nil, false
	}
	cp := *j
	return &cp, true
}

// Cancel cancels a running/queued job. Returns false if the job is unknown or
// already terminal.
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	cancel, ok := m.cancels[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// List returns the most recent jobs for a project from the in-memory map,
// newest first, capped at limit.
func (m *Manager) List(projectID string, limit int) []*Job {
	if limit <= 0 {
		limit = 100
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Job
	for _, j := range m.jobs {
		if j.ProjectID == projectID {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].CreatedAt.After(out[k].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (m *Manager) setCancel(id string, c context.CancelFunc) {
	m.mu.Lock()
	m.cancels[id] = c
	m.mu.Unlock()
}

func (m *Manager) clearCancel(id string) {
	m.mu.Lock()
	delete(m.cancels, id)
	m.mu.Unlock()
}

// Cleanup removes jobs older than maxAge (called periodically from main).
func (m *Manager) Cleanup(maxAge time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, j := range m.jobs {
		if now.Sub(j.CreatedAt) > maxAge {
			delete(m.cancels, id)
			delete(m.jobs, id)
		}
	}
}

// admission enforces a per-project max-concurrent limit with a FIFO queue and
// round-robin fairness across projects.
type admission struct {
	mu      sync.Mutex
	limit   int
	inUse   map[string]int
	waiters map[string][]chan struct{}
	order   []string
}

func newAdmission(limit int) *admission {
	return &admission{
		limit:   limit,
		inUse:   make(map[string]int),
		waiters: make(map[string][]chan struct{}),
	}
}

func (a *admission) acquire(ctx context.Context, project string) bool {
	a.mu.Lock()
	if a.inUse[project] < a.limit {
		a.inUse[project]++
		a.mu.Unlock()
		return true
	}
	ch := make(chan struct{})
	a.waiters[project] = append(a.waiters[project], ch)
	if len(a.waiters[project]) == 1 {
		a.order = append(a.order, project)
	}
	a.mu.Unlock()

	select {
	case <-ch:
		return true
	case <-ctx.Done():
		a.cancelWaiter(project, ch)
		return false
	}
}

func (a *admission) release(project string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inUse[project]--
	if a.inUse[project] < 0 {
		a.inUse[project] = 0
	}
	a.wakeNext()
}

func (a *admission) wakeNext() {
	for i := 0; i < len(a.order); i++ {
		p := a.order[0]
		a.order = a.order[1:]
		q := a.waiters[p]
		if len(q) == 0 {
			delete(a.waiters, p)
			continue
		}
		if a.inUse[p] >= a.limit {
			a.order = append(a.order, p)
			continue
		}
		ch := q[0]
		a.waiters[p] = q[1:]
		a.inUse[p]++
		if len(a.waiters[p]) > 0 {
			a.order = append(a.order, p)
		} else {
			delete(a.waiters, p)
		}
		close(ch)
		return
	}
}

// ExecutorFunc adapts a function to the Executor interface.
type ExecutorFunc func(ctx context.Context, req ExecRequest) *query.Result

func (f ExecutorFunc) Execute(ctx context.Context, req ExecRequest) *query.Result { return f(ctx, req) }

var _ Executor = (ExecutorFunc)(nil)

// WriteExecutor runs write jobs (CTAS/load) and returns the affected table ref.
type WriteExecutor interface {
	RunCTAS(ctx context.Context, projectID, sql, ak, sk, ep string) (string, error)
	RunLoad(ctx context.Context, projectID string, req LoadRequest, ak, sk, ep string) (string, error)
}

// SetWriteExecutor attaches the write executor used for ctas/load jobs.
func (m *Manager) SetWriteExecutor(we WriteExecutor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.write = we
}

func (a *admission) cancelWaiter(project string, ch chan struct{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	q := a.waiters[project]
	for i, c := range q {
		if c == ch {
			a.waiters[project] = append(q[:i], q[i+1:]...)
			break
		}
	}
	if len(a.waiters[project]) == 0 {
		delete(a.waiters, project)
	}
}