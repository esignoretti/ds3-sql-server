package job

import (
	"context"
	"sync"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// JobSink persists job state for query history. Save is called on creation and
// on every status transition; it must be idempotent (create-once, then update).
type JobSink interface {
	Save(ctx context.Context, j *Job) error
}

type jobStore interface {
	CreateJob(ctx context.Context, j *metastore.JobRecord) error
	UpdateJob(ctx context.Context, j *metastore.JobRecord) error
}

type MetastoreSink struct {
	store   jobStore
	mu      sync.Mutex
	created map[string]struct{}
}

func NewMetastoreSink(store jobStore) *MetastoreSink {
	return &MetastoreSink{store: store, created: make(map[string]struct{})}
}

func (s *MetastoreSink) Save(ctx context.Context, j *Job) error {
	rec := toRecord(j)
	s.mu.Lock()
	_, seen := s.created[j.ID]
	if !seen {
		s.created[j.ID] = struct{}{}
	}
	s.mu.Unlock()
	if !seen {
		if err := s.store.CreateJob(ctx, rec); err != nil {
			return err
		}
		return nil
	}
	return s.store.UpdateJob(ctx, rec)
}

func toRecord(j *Job) *metastore.JobRecord {
	rec := &metastore.JobRecord{
		ID:        j.ID,
		ProjectID: j.ProjectID,
		Type:      j.Type,
		SQL:       j.SQL,
		Status:    j.Status,
		Error:     j.Error,
		CreatedAt: j.CreatedAt,
	}
	if j.Result != nil {
		rec.RowCount = int64(j.Result.RowCount)
	}
	switch j.Status {
	case "running":
		rec.StartedAt = time.Now().UTC()
	case "done", "failed", "cancelled":
		rec.FinishedAt = time.Now().UTC()
	}
	return rec
}

var _ JobSink = (*MetastoreSink)(nil)
