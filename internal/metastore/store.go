package metastore

import (
	"context"
	"time"
)

// Column is one column of a table's inferred schema.
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

// Stats holds lightweight table statistics.
type Stats struct {
	RowCount int64 `json:"row_count"`
}

// Dataset is a namespace owned by a Cubbit project.
type Dataset struct {
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Table is a catalog table. In Phase 1 Kind is always "external".
type Table struct {
	ProjectID        string    `json:"project_id"`
	Dataset          string    `json:"dataset"`
	Name             string    `json:"name"`
	Kind             string    `json:"kind"`
	Location         string    `json:"location"`
	Format           string    `json:"format"`
	StorageClass     string    `json:"storage_class"`
	PartitionColumns []string  `json:"partition_columns"`
	Schema           []Column  `json:"schema"`
	Stats            Stats     `json:"stats"`
	DataVersion      int64     `json:"data_version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// JobRecord is the persisted form of a job for query history.
type JobRecord struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	Type           string    `json:"type"`
	SQL            string    `json:"sql"`
	Status         string    `json:"status"`
	Error          string    `json:"error,omitempty"`
	RowCount       int64     `json:"row_count"`
	BytesScanned   int64     `json:"bytes_scanned"`
	ResultLocation string    `json:"result_location,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
}

// CacheEntry is one result-cache index row. TableVersions is a JSON object
// mapping each referenced table's fully-qualified name to its data_version at
// the time the result was cached; a write that bumps any of those versions
// invalidates this entry. Location points at the serialized payload.
type CacheEntry struct {
	Key           string    `json:"key"`
	ProjectID     string    `json:"project_id"`
	SQLNorm       string    `json:"sql_norm"`
	TableVersions string    `json:"table_versions"`
	Location      string    `json:"location"`
	SizeBytes     int64     `json:"size_bytes"`
	CreatedAt     time.Time `json:"created_at"`
	LastAccessAt  time.Time `json:"last_access_at"`
}

// Store is the pluggable metadata store. Phase 1 ships the embedded SQLite
// implementation; Phase 4 adds a Postgres implementation of this same interface.
type Store interface {
	CreateDataset(ctx context.Context, ds *Dataset) error
	GetDataset(ctx context.Context, projectID, name string) (*Dataset, error)
	ListDatasets(ctx context.Context, projectID string) ([]*Dataset, error)

	CreateTable(ctx context.Context, t *Table) error
	GetTable(ctx context.Context, projectID, dataset, name string) (*Table, error)
	ListTables(ctx context.Context, projectID, dataset string) ([]*Table, error)
	DeleteTable(ctx context.Context, projectID, dataset, name string) error
	BumpDataVersion(ctx context.Context, projectID, dataset, name string) (int64, error)

	// Jobs (query history)
	CreateJob(ctx context.Context, j *JobRecord) error
	UpdateJob(ctx context.Context, j *JobRecord) error
	GetJob(ctx context.Context, id string) (*JobRecord, error)
	ListJobs(ctx context.Context, projectID string, limit int) ([]*JobRecord, error)

	// CacheIndex (result-cache index)
	PutCacheEntry(ctx context.Context, e *CacheEntry) error
	LookupCacheEntry(ctx context.Context, key string) (*CacheEntry, error)
	DeleteCacheEntry(ctx context.Context, key string) error
	ListCacheEntries(ctx context.Context) ([]*CacheEntry, error)
	DeleteCacheEntriesForTable(ctx context.Context, projectID, dataset, table string) error

	Close() error
}

// ErrNotFound is returned when a dataset or table does not exist.
var ErrNotFound = errString("not found")

type errString string

func (e errString) Error() string { return string(e) }
