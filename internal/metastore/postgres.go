package metastore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresStore implements Store against PostgreSQL. It mirrors the SQLite schema
// using Postgres types (TIMESTAMPTZ, BIGINT, JSONB) and supports an HA coordinator
// set sharing one database.
type PostgresStore struct {
	db *sql.DB
}

// OpenPostgres opens a connection pool to the given DSN and runs migrations.
func OpenPostgres(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(8)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	s := &PostgresStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *PostgresStore) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS datasets (
			project_id TEXT NOT NULL,
			name       TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL,
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
			partition_columns JSONB NOT NULL,
			schema_json       JSONB NOT NULL,
			stats_json        JSONB NOT NULL,
			data_version      BIGINT NOT NULL,
			created_at        TIMESTAMPTZ NOT NULL,
			updated_at        TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (project_id, dataset, name)
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id              TEXT PRIMARY KEY,
			project_id      TEXT NOT NULL,
			type            TEXT NOT NULL,
			sql             TEXT NOT NULL,
			status          TEXT NOT NULL,
			error           TEXT NOT NULL DEFAULT '',
			row_count       BIGINT NOT NULL DEFAULT 0,
			bytes_scanned   BIGINT NOT NULL DEFAULT 0,
			result_location TEXT NOT NULL DEFAULT '',
			created_at      TIMESTAMPTZ NOT NULL,
			started_at      TIMESTAMPTZ,
			finished_at     TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS jobs_project_created ON jobs (project_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS cache_index (
			key            TEXT PRIMARY KEY,
			project_id     TEXT NOT NULL,
			sql_norm       TEXT NOT NULL,
			table_versions TEXT NOT NULL,
			location       TEXT NOT NULL,
			size_bytes     BIGINT NOT NULL DEFAULT 0,
			created_at     TIMESTAMPTZ NOT NULL,
			last_access_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS schedules (
			id          TEXT PRIMARY KEY,
			project_id  TEXT NOT NULL,
			cron        TEXT NOT NULL,
			sql         TEXT NOT NULL,
			into_table  TEXT NOT NULL DEFAULT '',
			owner       TEXT NOT NULL DEFAULT '',
			next_run_at TIMESTAMPTZ,
			last_run_at TIMESTAMPTZ,
			running     BOOLEAN NOT NULL DEFAULT FALSE,
			created_at  TIMESTAMPTZ NOT NULL
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

func (s *PostgresStore) Close() error { return s.db.Close() }

// nullTime converts a possibly-zero time to a value suitable for a nullable column.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func timeFromNull(nt sql.NullTime) time.Time {
	if nt.Valid {
		return nt.Time.UTC()
	}
	return time.Time{}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var _ Store = (*PostgresStore)(nil)

// ── Stub methods (replaced by Tasks 5–7) ──────────────────────────

func (s *PostgresStore) CreateDataset(ctx context.Context, ds *Dataset) error { panic("unimplemented") }
func (s *PostgresStore) GetDataset(ctx context.Context, projectID, name string) (*Dataset, error) { panic("unimplemented") }
func (s *PostgresStore) ListDatasets(ctx context.Context, projectID string) ([]*Dataset, error) { panic("unimplemented") }
func (s *PostgresStore) CreateTable(ctx context.Context, t *Table) error { panic("unimplemented") }
func (s *PostgresStore) GetTable(ctx context.Context, projectID, dataset, name string) (*Table, error) { panic("unimplemented") }
func (s *PostgresStore) ListTables(ctx context.Context, projectID, dataset string) ([]*Table, error) { panic("unimplemented") }
func (s *PostgresStore) DeleteTable(ctx context.Context, projectID, dataset, name string) error { panic("unimplemented") }
func (s *PostgresStore) BumpDataVersion(ctx context.Context, projectID, dataset, name string) (int64, error) { panic("unimplemented") }
func (s *PostgresStore) CreateJob(ctx context.Context, j *JobRecord) error { panic("unimplemented") }
func (s *PostgresStore) UpdateJob(ctx context.Context, j *JobRecord) error { panic("unimplemented") }
func (s *PostgresStore) GetJob(ctx context.Context, id string) (*JobRecord, error) { panic("unimplemented") }
func (s *PostgresStore) ListJobs(ctx context.Context, projectID string, limit int) ([]*JobRecord, error) { panic("unimplemented") }
func (s *PostgresStore) PutCacheEntry(ctx context.Context, e *CacheEntry) error { panic("unimplemented") }
func (s *PostgresStore) LookupCacheEntry(ctx context.Context, key string) (*CacheEntry, error) { panic("unimplemented") }
func (s *PostgresStore) DeleteCacheEntry(ctx context.Context, key string) error { panic("unimplemented") }
func (s *PostgresStore) ListCacheEntries(ctx context.Context) ([]*CacheEntry, error) { panic("unimplemented") }
func (s *PostgresStore) DeleteCacheEntriesForTable(ctx context.Context, projectID, dataset, table string) error { panic("unimplemented") }
func (s *PostgresStore) CreateSchedule(ctx context.Context, sc *Schedule) error { panic("unimplemented") }
func (s *PostgresStore) ListSchedules(ctx context.Context, projectID string) ([]*Schedule, error) { panic("unimplemented") }
func (s *PostgresStore) GetSchedule(ctx context.Context, id string) (*Schedule, error) { panic("unimplemented") }
func (s *PostgresStore) DeleteSchedule(ctx context.Context, id string) error { panic("unimplemented") }
func (s *PostgresStore) UpdateScheduleRun(ctx context.Context, id string, lastRun time.Time, running bool) error { panic("unimplemented") }
func (s *PostgresStore) GetDueSchedules(ctx context.Context, now time.Time) ([]*Schedule, error) { panic("unimplemented") }
