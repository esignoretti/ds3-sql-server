package convert

import (
	"sync"
	"time"
)

type FileResult struct {
	File      string `json:"file"`
	Status    string `json:"status"`
	Converted string `json:"converted,omitempty"`
	Error     string `json:"error,omitempty"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

type Job struct {
	mu        sync.Mutex
	ID        string       `json:"-"`
	Bucket    string       `json:"bucket"`
	Total     int          `json:"total"`
	Completed int          `json:"completed"`
	Status    string       `json:"status"`
	Results   []FileResult `json:"results"`
	CreatedAt time.Time    `json:"created_at"`
}

type JobStore struct {
	mu   sync.RWMutex
	jobs map[string]*Job
}

func NewJobStore() *JobStore {
	return &JobStore{
		jobs: make(map[string]*Job),
	}
}

func (s *JobStore) Set(id string, job *Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[id] = job
}

func (s *JobStore) Get(id string) (*Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	return job, ok
}

func (s *JobStore) Cleanup(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, job := range s.jobs {
		if now.Sub(job.CreatedAt) > maxAge {
			delete(s.jobs, id)
		}
	}
}
