package metastore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func OpenSQLite(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// modernc.org/sqlite is safe with a single connection; avoid lock contention.
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS datasets (
			project_id TEXT NOT NULL,
			name       TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (project_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS tables (
			project_id        TEXT NOT NULL,
			dataset           TEXT NOT NULL,
			name              TEXT NOT NULL,
			kind              TEXT NOT NULL,
			location          TEXT NOT NULL,
			format            TEXT NOT NULL,
			storage_class     TEXT NOT NULL,
			partition_columns TEXT NOT NULL,
			schema_json       TEXT NOT NULL,
			stats_json        TEXT NOT NULL,
			data_version      INTEGER NOT NULL,
			created_at        TEXT NOT NULL,
			updated_at        TEXT NOT NULL,
			PRIMARY KEY (project_id, dataset, name)
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

func (s *SQLiteStore) CreateDataset(ctx context.Context, ds *Dataset) error { panic("unimplemented") }
func (s *SQLiteStore) GetDataset(ctx context.Context, projectID, name string) (*Dataset, error) {
	panic("unimplemented")
}
func (s *SQLiteStore) ListDatasets(ctx context.Context, projectID string) ([]*Dataset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project_id, name, created_at FROM datasets WHERE project_id = ? ORDER BY name`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list datasets: %w", err)
	}
	defer rows.Close()
	out := []*Dataset{}
	for rows.Next() {
		var d Dataset
		var created string
		if err := rows.Scan(&d.ProjectID, &d.Name, &created); err != nil {
			return nil, err
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, &d)
	}
	return out, rows.Err()
}
func (s *SQLiteStore) CreateTable(ctx context.Context, t *Table) error { panic("unimplemented") }
func (s *SQLiteStore) GetTable(ctx context.Context, projectID, dataset, name string) (*Table, error) {
	panic("unimplemented")
}
func (s *SQLiteStore) ListTables(ctx context.Context, projectID, dataset string) ([]*Table, error) {
	panic("unimplemented")
}
func (s *SQLiteStore) DeleteTable(ctx context.Context, projectID, dataset, name string) error {
	panic("unimplemented")
}
func (s *SQLiteStore) BumpDataVersion(ctx context.Context, projectID, dataset, name string) (int64, error) {
	panic("unimplemented")
}

var _ Store = (*SQLiteStore)(nil)
