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

	Close() error
}

// ErrNotFound is returned when a dataset or table does not exist.
var ErrNotFound = errString("not found")

type errString string

func (e errString) Error() string { return string(e) }
