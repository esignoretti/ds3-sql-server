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

// ── Datasets ──────────────────────────────────────────────────────

func (s *PostgresStore) CreateDataset(ctx context.Context, ds *Dataset) error {
	if ds.CreatedAt.IsZero() {
		ds.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO datasets (project_id, name, created_at) VALUES ($1, $2, $3)`,
		ds.ProjectID, ds.Name, ds.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("create dataset: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetDataset(ctx context.Context, projectID, name string) (*Dataset, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT project_id, name, created_at FROM datasets WHERE project_id = $1 AND name = $2`,
		projectID, name)
	var d Dataset
	switch err := row.Scan(&d.ProjectID, &d.Name, &d.CreatedAt); err {
	case nil:
		d.CreatedAt = d.CreatedAt.UTC()
		return &d, nil
	case sql.ErrNoRows:
		return nil, ErrNotFound
	default:
		return nil, fmt.Errorf("get dataset: %w", err)
	}
}

func (s *PostgresStore) ListDatasets(ctx context.Context, projectID string) ([]*Dataset, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT project_id, name, created_at FROM datasets WHERE project_id = $1 ORDER BY name`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list datasets: %w", err)
	}
	defer rows.Close()
	out := []*Dataset{}
	for rows.Next() {
		var d Dataset
		if err := rows.Scan(&d.ProjectID, &d.Name, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.CreatedAt = d.CreatedAt.UTC()
		out = append(out, &d)
	}
	return out, rows.Err()
}

// ── Tables ────────────────────────────────────────────────────────

const pgTableCols = `project_id, dataset, name, kind, location, format, storage_class,
	partition_columns, schema_json, stats_json, data_version, created_at, updated_at`

func (s *PostgresStore) CreateTable(ctx context.Context, t *Table) error {
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	if t.DataVersion == 0 {
		t.DataVersion = 1
	}
	parts := t.PartitionColumns
	if parts == nil {
		parts = []string{}
	}
	schema := t.Schema
	if schema == nil {
		schema = []Column{}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tables (`+pgTableCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		t.ProjectID, t.Dataset, t.Name, t.Kind, t.Location, t.Format, t.StorageClass,
		mustJSON(parts), mustJSON(schema), mustJSON(t.Stats), t.DataVersion,
		t.CreatedAt.UTC(), t.UpdatedAt.UTC())
	if err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	return nil
}

func scanPgTable(row interface{ Scan(...any) error }) (*Table, error) {
	var t Table
	var parts, schema, stats string
	err := row.Scan(&t.ProjectID, &t.Dataset, &t.Name, &t.Kind, &t.Location, &t.Format,
		&t.StorageClass, &parts, &schema, &stats, &t.DataVersion, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(parts), &t.PartitionColumns)
	_ = json.Unmarshal([]byte(schema), &t.Schema)
	_ = json.Unmarshal([]byte(stats), &t.Stats)
	t.CreatedAt = t.CreatedAt.UTC()
	t.UpdatedAt = t.UpdatedAt.UTC()
	return &t, nil
}

func (s *PostgresStore) GetTable(ctx context.Context, projectID, dataset, name string) (*Table, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+pgTableCols+` FROM tables WHERE project_id = $1 AND dataset = $2 AND name = $3`,
		projectID, dataset, name)
	t, err := scanPgTable(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get table: %w", err)
	}
	return t, nil
}

func (s *PostgresStore) ListTables(ctx context.Context, projectID, dataset string) ([]*Table, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+pgTableCols+` FROM tables WHERE project_id = $1 AND dataset = $2 ORDER BY name`,
		projectID, dataset)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()
	out := []*Table{}
	for rows.Next() {
		t, err := scanPgTable(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PostgresStore) DeleteTable(ctx context.Context, projectID, dataset, name string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM tables WHERE project_id = $1 AND dataset = $2 AND name = $3`,
		projectID, dataset, name)
	if err != nil {
		return fmt.Errorf("delete table: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) BumpDataVersion(ctx context.Context, projectID, dataset, name string) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx,
		`UPDATE tables SET data_version = data_version + 1, updated_at = $1
		 WHERE project_id = $2 AND dataset = $3 AND name = $4
		 RETURNING data_version`,
		time.Now().UTC(), projectID, dataset, name).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("bump data version: %w", err)
	}
	return v, nil
}

// ── Jobs ──────────────────────────────────────────────────────────

func (s *PostgresStore) CreateJob(ctx context.Context, j *JobRecord) error {
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (id, project_id, type, sql, status, error, row_count,
			bytes_scanned, result_location, created_at, started_at, finished_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		j.ID, j.ProjectID, j.Type, j.SQL, j.Status, j.Error, j.RowCount,
		j.BytesScanned, j.ResultLocation, j.CreatedAt.UTC(), nullTime(j.StartedAt), nullTime(j.FinishedAt))
	if err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	return nil
}

func (s *PostgresStore) UpdateJob(ctx context.Context, j *JobRecord) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status=$1, error=$2, row_count=$3, bytes_scanned=$4,
			result_location=$5, started_at=$6, finished_at=$7 WHERE id=$8`,
		j.Status, j.Error, j.RowCount, j.BytesScanned, j.ResultLocation,
		nullTime(j.StartedAt), nullTime(j.FinishedAt), j.ID)
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanPgJob(row interface{ Scan(...any) error }) (*JobRecord, error) {
	var j JobRecord
	var started, finished sql.NullTime
	err := row.Scan(&j.ID, &j.ProjectID, &j.Type, &j.SQL, &j.Status, &j.Error,
		&j.RowCount, &j.BytesScanned, &j.ResultLocation, &j.CreatedAt, &started, &finished)
	if err != nil {
		return nil, err
	}
	j.CreatedAt = j.CreatedAt.UTC()
	j.StartedAt = timeFromNull(started)
	j.FinishedAt = timeFromNull(finished)
	return &j, nil
}

const pgJobCols = `id, project_id, type, sql, status, error, row_count,
	bytes_scanned, result_location, created_at, started_at, finished_at`

func (s *PostgresStore) GetJob(ctx context.Context, id string) (*JobRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+pgJobCols+` FROM jobs WHERE id = $1`, id)
	j, err := scanPgJob(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	return j, nil
}

func (s *PostgresStore) ListJobs(ctx context.Context, projectID string, limit int) ([]*JobRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+pgJobCols+` FROM jobs WHERE project_id = $1 ORDER BY created_at DESC LIMIT $2`,
		projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	out := []*JobRecord{}
	for rows.Next() {
		j, err := scanPgJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ── Cache Index ────────────────────────────────────────────────────

func (s *PostgresStore) PutCacheEntry(ctx context.Context, e *CacheEntry) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if e.LastAccessAt.IsZero() {
		e.LastAccessAt = e.CreatedAt
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cache_index (key, project_id, sql_norm, table_versions, location, size_bytes, created_at, last_access_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (key) DO UPDATE SET
			project_id=EXCLUDED.project_id, sql_norm=EXCLUDED.sql_norm,
			table_versions=EXCLUDED.table_versions, location=EXCLUDED.location,
			size_bytes=EXCLUDED.size_bytes, last_access_at=EXCLUDED.last_access_at`,
		e.Key, e.ProjectID, e.SQLNorm, e.TableVersions, e.Location, e.SizeBytes,
		e.CreatedAt.UTC(), e.LastAccessAt.UTC())
	if err != nil {
		return fmt.Errorf("put cache entry: %w", err)
	}
	return nil
}

func scanPgCache(row interface{ Scan(...any) error }) (*CacheEntry, error) {
	var e CacheEntry
	err := row.Scan(&e.Key, &e.ProjectID, &e.SQLNorm, &e.TableVersions, &e.Location,
		&e.SizeBytes, &e.CreatedAt, &e.LastAccessAt)
	if err != nil {
		return nil, err
	}
	e.CreatedAt = e.CreatedAt.UTC()
	e.LastAccessAt = e.LastAccessAt.UTC()
	return &e, nil
}

const pgCacheCols = `key, project_id, sql_norm, table_versions, location, size_bytes, created_at, last_access_at`

func (s *PostgresStore) LookupCacheEntry(ctx context.Context, key string) (*CacheEntry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+pgCacheCols+` FROM cache_index WHERE key = $1`, key)
	e, err := scanPgCache(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lookup cache entry: %w", err)
	}
	return e, nil
}

func (s *PostgresStore) DeleteCacheEntry(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cache_index WHERE key = $1`, key)
	if err != nil {
		return fmt.Errorf("delete cache entry: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListCacheEntries(ctx context.Context) ([]*CacheEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pgCacheCols+` FROM cache_index ORDER BY last_access_at`)
	if err != nil {
		return nil, fmt.Errorf("list cache entries: %w", err)
	}
	defer rows.Close()
	out := []*CacheEntry{}
	for rows.Next() {
		e, err := scanPgCache(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PostgresStore) DeleteCacheEntriesForTable(ctx context.Context, projectID, dataset, table string) error {
	// table_versions is a JSON object mapping each referenced table's
	// fully-qualified name to its data_version. Match by JSON key.
	needle := `"` + projectID + "/" + dataset + "/" + table + `":`
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM cache_index WHERE project_id = $1 AND table_versions LIKE $2`,
		projectID, "%"+needle+"%")
	if err != nil {
		return fmt.Errorf("delete cache entries for table: %w", err)
	}
	return nil
}

// ── Schedules ──────────────────────────────────────────────────────

func (s *PostgresStore) CreateSchedule(ctx context.Context, sc *Schedule) error {
	if sc.CreatedAt.IsZero() {
		sc.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO schedules (id, project_id, cron, sql, into_table, owner, next_run_at, last_run_at, running, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		sc.ID, sc.ProjectID, sc.Cron, sc.SQL, sc.IntoTable, sc.Owner,
		nullTime(sc.NextRunAt), nullTime(sc.LastRunAt), sc.Running, sc.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("create schedule: %w", err)
	}
	return nil
}

func scanPgSchedule(row interface{ Scan(...any) error }) (*Schedule, error) {
	var sc Schedule
	var next, last sql.NullTime
	err := row.Scan(&sc.ID, &sc.ProjectID, &sc.Cron, &sc.SQL, &sc.IntoTable, &sc.Owner,
		&next, &last, &sc.Running, &sc.CreatedAt)
	if err != nil {
		return nil, err
	}
	sc.NextRunAt = timeFromNull(next)
	sc.LastRunAt = timeFromNull(last)
	sc.CreatedAt = sc.CreatedAt.UTC()
	return &sc, nil
}

const pgScheduleCols = `id, project_id, cron, sql, into_table, owner, next_run_at, last_run_at, running, created_at`

func (s *PostgresStore) ListSchedules(ctx context.Context, projectID string) ([]*Schedule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+pgScheduleCols+` FROM schedules WHERE project_id = $1 ORDER BY created_at`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()
	out := []*Schedule{}
	for rows.Next() {
		sc, err := scanPgSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetSchedule(ctx context.Context, id string) (*Schedule, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+pgScheduleCols+` FROM schedules WHERE id = $1`, id)
	sc, err := scanPgSchedule(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get schedule: %w", err)
	}
	return sc, nil
}

func (s *PostgresStore) DeleteSchedule(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM schedules WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) UpdateScheduleRun(ctx context.Context, id string, lastRun time.Time, running bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE schedules SET last_run_at = $1, running = $2 WHERE id = $3`,
		nullTime(lastRun), running, id)
	if err != nil {
		return fmt.Errorf("update schedule run: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) GetDueSchedules(ctx context.Context, now time.Time) ([]*Schedule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+pgScheduleCols+` FROM schedules
		 WHERE running = FALSE AND next_run_at IS NOT NULL AND next_run_at <= $1`,
		now.UTC())
	if err != nil {
		return nil, fmt.Errorf("get due schedules: %w", err)
	}
	defer rows.Close()
	out := []*Schedule{}
	for rows.Next() {
		sc, err := scanPgSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}
