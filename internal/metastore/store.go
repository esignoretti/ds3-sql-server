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
	// Partitions is the per-partition file/location list used for pruning. It is
	// optional and omitted from JSON when empty, so pre-Phase-4 stats payloads
	// (which have no "partitions" key) round-trip unchanged.
	Partitions []Partition `json:"partitions,omitempty"`
}

// Partition describes one Hive-style partition of a table: the partition-column
// values that select it, the reader location for its files, a row-count estimate,
// and optional per-column min/max bounds for range pruning.
type Partition struct {
	Values   map[string]string `json:"values"`
	Location string            `json:"location"`
	RowCount int64             `json:"row_count"`
	Min      map[string]string `json:"min,omitempty"`
	Max      map[string]string `json:"max,omitempty"`
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
	DeleteDataset(ctx context.Context, projectID, name string) error

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

	// Schedule
	CreateSchedule(ctx context.Context, sch *Schedule) error
	UpdateSchedule(ctx context.Context, sch *Schedule) error
	ListSchedules(ctx context.Context, projectID string) ([]*Schedule, error)
	GetSchedule(ctx context.Context, id string) (*Schedule, error)
	DeleteSchedule(ctx context.Context, id, projectID string) error
	UpdateScheduleRun(ctx context.Context, id string, lastRun time.Time, running bool) error
	GetDueSchedules(ctx context.Context, now time.Time) ([]*Schedule, error)
	SetScheduleNextRun(ctx context.Context, id string, next time.Time) error

	Close() error
}

// Schedule is a cron-driven query/CTAS/load/convert. NextRunAt drives due selection;
// Running guards against overlapping runs (misfire policy: skip if still running).
// For "convert" schedules, Source/Format configure the input and PostAction/Move* control
// what happens to the source files after successful conversion.
type Schedule struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Cron        string    `json:"cron"`
	SQL         string    `json:"sql"`
	IntoTable   string    `json:"into_table"`
	Owner       string    `json:"owner"`
	Type        string    `json:"type"`                   // "query"|"ctas"|"load"|"convert"
	Source      string    `json:"source"`                 // s3 path/glob for convert source
	Format      string    `json:"format"`                 // source format
	PostAction  string    `json:"post_action"`            // "" | "delete" | "move"
	MoveBucket  string    `json:"move_bucket"`            // target bucket for move
	MovePrefix  string    `json:"move_prefix"`            // target prefix for move
	NextRunAt   time.Time `json:"next_run_at"`
	LastRunAt   time.Time `json:"last_run_at"`
	Running     bool      `json:"running"`
	CreatedAt   time.Time `json:"created_at"`
}

// ErrNotFound is returned when a dataset or table does not exist.
var ErrNotFound = errString("not found")

type errString string

func (e errString) Error() string { return string(e) }
