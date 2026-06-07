package metastore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
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
		`CREATE TABLE IF NOT EXISTS jobs (
			id              TEXT PRIMARY KEY,
			project_id      TEXT NOT NULL,
			type            TEXT NOT NULL,
			sql             TEXT NOT NULL,
			status          TEXT NOT NULL,
			error           TEXT NOT NULL,
			row_count       INTEGER NOT NULL,
			bytes_scanned   INTEGER NOT NULL,
			result_location TEXT NOT NULL,
			created_at      TEXT NOT NULL,
			started_at      TEXT NOT NULL,
			finished_at     TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_project_created ON jobs(project_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS cache_index (
			key            TEXT PRIMARY KEY,
			project_id     TEXT NOT NULL,
			sql_norm       TEXT NOT NULL,
			table_versions TEXT NOT NULL,
			location       TEXT NOT NULL,
			size_bytes     INTEGER NOT NULL,
			created_at     TEXT NOT NULL,
			last_access_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS schedules (
			id          TEXT PRIMARY KEY,
			project_id  TEXT NOT NULL,
			cron        TEXT NOT NULL,
			sql         TEXT NOT NULL,
			into_table  TEXT NOT NULL,
			owner       TEXT NOT NULL,
			next_run_at TEXT NOT NULL,
			last_run_at TEXT NOT NULL,
			running     INTEGER NOT NULL,
			created_at  TEXT NOT NULL
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

func (s *SQLiteStore) CreateDataset(ctx context.Context, ds *Dataset) error {
	if ds.CreatedAt.IsZero() {
		ds.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO datasets (project_id, name, created_at) VALUES (?, ?, ?)`,
		ds.ProjectID, ds.Name, ds.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("create dataset: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetDataset(ctx context.Context, projectID, name string) (*Dataset, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT project_id, name, created_at FROM datasets WHERE project_id = ? AND name = ?`,
		projectID, name)
	var d Dataset
	var created string
	switch err := row.Scan(&d.ProjectID, &d.Name, &created); err {
	case nil:
		d.CreatedAt, _ = time.Parse(time.RFC3339, created)
		return &d, nil
	case sql.ErrNoRows:
		return nil, ErrNotFound
	default:
		return nil, fmt.Errorf("get dataset: %w", err)
	}
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
func (s *SQLiteStore) DeleteDataset(ctx context.Context, projectID, name string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM datasets WHERE project_id = ? AND name = ?`,
		projectID, name)
	if err != nil {
		return fmt.Errorf("delete dataset: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) CreateTable(ctx context.Context, t *Table) error {
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	if t.DataVersion == 0 {
		t.DataVersion = 1
	}
	parts, _ := json.Marshal(t.PartitionColumns)
	schema, _ := json.Marshal(t.Schema)
	stats, _ := json.Marshal(t.Stats)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tables (project_id, dataset, name, kind, location, format, storage_class,
			partition_columns, schema_json, stats_json, data_version, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ProjectID, t.Dataset, t.Name, t.Kind, t.Location, t.Format, t.StorageClass,
		string(parts), string(schema), string(stats), t.DataVersion,
		t.CreatedAt.Format(time.RFC3339), t.UpdatedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	return nil
}

func scanTable(row interface{ Scan(...any) error }) (*Table, error) {
	var t Table
	var parts, schema, stats, created, updated string
	err := row.Scan(&t.ProjectID, &t.Dataset, &t.Name, &t.Kind, &t.Location, &t.Format,
		&t.StorageClass, &parts, &schema, &stats, &t.DataVersion, &created, &updated)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(parts), &t.PartitionColumns)
	_ = json.Unmarshal([]byte(schema), &t.Schema)
	_ = json.Unmarshal([]byte(stats), &t.Stats)
	t.CreatedAt, _ = time.Parse(time.RFC3339, created)
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return &t, nil
}

const tableCols = `project_id, dataset, name, kind, location, format, storage_class,
	partition_columns, schema_json, stats_json, data_version, created_at, updated_at`

func (s *SQLiteStore) GetTable(ctx context.Context, projectID, dataset, name string) (*Table, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+tableCols+` FROM tables WHERE project_id = ? AND dataset = ? AND name = ?`,
		projectID, dataset, name)
	t, err := scanTable(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get table: %w", err)
	}
	return t, nil
}

func (s *SQLiteStore) ListTables(ctx context.Context, projectID, dataset string) ([]*Table, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+tableCols+` FROM tables WHERE project_id = ? AND dataset = ? ORDER BY name`,
		projectID, dataset)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()
	out := []*Table{}
	for rows.Next() {
		t, err := scanTable(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) DeleteTable(ctx context.Context, projectID, dataset, name string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM tables WHERE project_id = ? AND dataset = ? AND name = ?`,
		projectID, dataset, name)
	if err != nil {
		return fmt.Errorf("delete table: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) BumpDataVersion(ctx context.Context, projectID, dataset, name string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tables SET data_version = data_version + 1, updated_at = ?
		 WHERE project_id = ? AND dataset = ? AND name = ?`,
		time.Now().UTC().Format(time.RFC3339), projectID, dataset, name)
	if err != nil {
		return 0, fmt.Errorf("bump data version: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	t, err := s.GetTable(ctx, projectID, dataset, name)
	if err != nil {
		return 0, err
	}
	return t.DataVersion, nil
}

// ── Job helpers ────────────────────────────────────────────────

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func (s *SQLiteStore) CreateJob(ctx context.Context, j *JobRecord) error {
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (id, project_id, type, sql, status, error, row_count, bytes_scanned, result_location, created_at, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.ProjectID, j.Type, j.SQL, j.Status, j.Error, j.RowCount, j.BytesScanned, j.ResultLocation,
		fmtTime(j.CreatedAt), fmtTime(j.StartedAt), fmtTime(j.FinishedAt))
	if err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateJob(ctx context.Context, j *JobRecord) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, error = ?, row_count = ?, bytes_scanned = ?, result_location = ?, started_at = ?, finished_at = ?
		 WHERE id = ?`,
		j.Status, j.Error, j.RowCount, j.BytesScanned, j.ResultLocation,
		fmtTime(j.StartedAt), fmtTime(j.FinishedAt), j.ID)
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanJob(row interface{ Scan(...any) error }) (*JobRecord, error) {
	var j JobRecord
	var created, started, finished string
	err := row.Scan(&j.ID, &j.ProjectID, &j.Type, &j.SQL, &j.Status, &j.Error,
		&j.RowCount, &j.BytesScanned, &j.ResultLocation, &created, &started, &finished)
	if err != nil {
		return nil, err
	}
	j.CreatedAt = parseTime(created)
	j.StartedAt = parseTime(started)
	j.FinishedAt = parseTime(finished)
	return &j, nil
}

const jobCols = `id, project_id, type, sql, status, error, row_count, bytes_scanned, result_location, created_at, started_at, finished_at`

func (s *SQLiteStore) GetJob(ctx context.Context, id string) (*JobRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ?`, id)
	j, err := scanJob(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	return j, nil
}

func (s *SQLiteStore) ListJobs(ctx context.Context, projectID string, limit int) ([]*JobRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+jobCols+` FROM jobs WHERE project_id = ? ORDER BY created_at DESC LIMIT ?`,
		projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	out := []*JobRecord{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ── CacheIndex helpers ──────────────────────────────────────────

func (s *SQLiteStore) PutCacheEntry(ctx context.Context, e *CacheEntry) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if e.LastAccessAt.IsZero() {
		e.LastAccessAt = e.CreatedAt
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cache_index (key, project_id, sql_norm, table_versions, location, size_bytes, created_at, last_access_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		   project_id=excluded.project_id, sql_norm=excluded.sql_norm,
		   table_versions=excluded.table_versions, location=excluded.location,
		   size_bytes=excluded.size_bytes, last_access_at=excluded.last_access_at`,
		e.Key, e.ProjectID, e.SQLNorm, e.TableVersions, e.Location, e.SizeBytes,
		fmtTime(e.CreatedAt), fmtTime(e.LastAccessAt))
	if err != nil {
		return fmt.Errorf("put cache entry: %w", err)
	}
	return nil
}

func scanCacheEntry(row interface{ Scan(...any) error }) (*CacheEntry, error) {
	var e CacheEntry
	var created, accessed string
	err := row.Scan(&e.Key, &e.ProjectID, &e.SQLNorm, &e.TableVersions, &e.Location, &e.SizeBytes, &created, &accessed)
	if err != nil {
		return nil, err
	}
	e.CreatedAt = parseTime(created)
	e.LastAccessAt = parseTime(accessed)
	return &e, nil
}

const cacheCols = `key, project_id, sql_norm, table_versions, location, size_bytes, created_at, last_access_at`

func (s *SQLiteStore) LookupCacheEntry(ctx context.Context, key string) (*CacheEntry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+cacheCols+` FROM cache_index WHERE key = ?`, key)
	e, err := scanCacheEntry(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lookup cache entry: %w", err)
	}
	return e, nil
}

func (s *SQLiteStore) DeleteCacheEntry(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM cache_index WHERE key = ?`, key); err != nil {
		return fmt.Errorf("delete cache entry: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListCacheEntries(ctx context.Context) ([]*CacheEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cacheCols+` FROM cache_index ORDER BY last_access_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list cache entries: %w", err)
	}
	defer rows.Close()
	out := []*CacheEntry{}
	for rows.Next() {
		e, err := scanCacheEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteCacheEntriesForTable removes every cache entry whose TableVersions JSON
// references the given fully-qualified table. The match is a substring probe on
// the JSON key `"projectID/dataset/table":` — adequate because keys are exact
// fully-qualified names with no embedded quotes (validated identifiers).
func (s *SQLiteStore) DeleteCacheEntriesForTable(ctx context.Context, projectID, dataset, table string) error {
	needle := `"` + projectID + "/" + dataset + "/" + table + `":`
	// LIKE with an escaped pattern; '%' and '_' cannot appear in validated FQNs,
	// but escape defensively.
	pattern := "%" + likeEscape(needle) + "%"
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM cache_index WHERE table_versions LIKE ? ESCAPE '\'`, pattern)
	if err != nil {
		return fmt.Errorf("delete cache entries for table: %w", err)
	}
	return nil
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// ── Schedule helpers ──────────────────────────────────────────────

const scheduleCols = `id, project_id, cron, sql, into_table, owner, next_run_at, last_run_at, running, created_at`

func scanSchedule(row interface{ Scan(...any) error }) (*Schedule, error) {
	var sch Schedule
	var next, last, created string
	var running int
	if err := row.Scan(&sch.ID, &sch.ProjectID, &sch.Cron, &sch.SQL, &sch.IntoTable,
		&sch.Owner, &next, &last, &running, &created); err != nil {
		return nil, err
	}
	sch.NextRunAt = parseTime(next)
	sch.LastRunAt = parseTime(last)
	sch.CreatedAt = parseTime(created)
	sch.Running = running != 0
	return &sch, nil
}

func (s *SQLiteStore) CreateSchedule(ctx context.Context, sch *Schedule) error {
	if sch.CreatedAt.IsZero() {
		sch.CreatedAt = time.Now().UTC()
	}
	running := 0
	if sch.Running {
		running = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO schedules (`+scheduleCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sch.ID, sch.ProjectID, sch.Cron, sch.SQL, sch.IntoTable, sch.Owner,
		fmtTime(sch.NextRunAt), fmtTime(sch.LastRunAt), running, fmtTime(sch.CreatedAt))
	if err != nil {
		return fmt.Errorf("create schedule: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetSchedule(ctx context.Context, id string) (*Schedule, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+scheduleCols+` FROM schedules WHERE id = ?`, id)
	sch, err := scanSchedule(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get schedule: %w", err)
	}
	return sch, nil
}

func (s *SQLiteStore) ListSchedules(ctx context.Context, projectID string) ([]*Schedule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+scheduleCols+` FROM schedules WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()
	out := []*Schedule{}
	for rows.Next() {
		sch, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sch)
	}
	return out, rows.Err()
}

// SetScheduleNextRun updates only the next_run_at column. Not part of Store
// interface — used by the scheduler enqueuer to persist advanced next-run after
// a scheduled job completes.
func (s *SQLiteStore) SetScheduleNextRun(ctx context.Context, id string, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE schedules SET next_run_at = ? WHERE id = ?`, fmtTime(next), id)
	return err
}

func (s *SQLiteStore) DeleteSchedule(ctx context.Context, id, projectID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM schedules WHERE id = ? AND project_id = ?`, id, projectID)
	if err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) UpdateScheduleRun(ctx context.Context, id string, lastRun time.Time, running bool) error {
	r := 0
	if running {
		r = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE schedules SET last_run_at = ?, running = ? WHERE id = ?`,
		fmtTime(lastRun), r, id)
	if err != nil {
		return fmt.Errorf("update schedule run: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetDueSchedules returns schedules whose next_run_at is at or before now and
// that are not currently running (the misfire/overlap guard).
func (s *SQLiteStore) GetDueSchedules(ctx context.Context, now time.Time) ([]*Schedule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+scheduleCols+` FROM schedules WHERE running = 0 AND next_run_at != '' AND next_run_at <= ? ORDER BY next_run_at`,
		fmtTime(now))
	if err != nil {
		return nil, fmt.Errorf("get due schedules: %w", err)
	}
	defer rows.Close()
	out := []*Schedule{}
	for rows.Next() {
		sch, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sch)
	}
	return out, rows.Err()
}

var _ Store = (*SQLiteStore)(nil)
