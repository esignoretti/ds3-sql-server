# DS3 SQL Phase 2 (Scale-out & Caching) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the single-node Phase 1 foundation into a scale-out system: a coordinator that routes resolved query plans to a stateless worker pool via consistent hashing (cache locality), a result cache keyed on referenced-table data versions, a local-SSD read-through data cache on workers, async jobs with polling/cancellation and persisted query history, and per-project concurrency quotas with a fair FIFO queue.

**Architecture:** Incremental in-place refactor (Approach A). The Phase 1 `job.Executor` seam is exploited: the coordinator keeps `LocalExecutor` for `--role=all`, or swaps in a new `worker.RemoteExecutor` for `--role=coordinator`. A `--role=worker` node runs a small HTTP server (`internal/worker`) exposing `POST /internal/execute` (resolved SQL + view bindings + creds → `query.QueryView`), guarded by a shared-secret header; the worker wraps a local-SSD data cache (`internal/cache/data.go`) that copies HDD-class objects to local SSD and rewrites `ViewBinding.ReaderSQL`. The coordinator gains a result cache (`internal/cache/result.go`) that sits in front of whatever `Executor` is wired, keyed on `hash(normalizedSQL + sorted referenced-table data_versions)`, with the index in the metastore (new `CacheEntry` rows) and payloads (serialized `query.Result` JSON) on a configurable location (local dir in Phase 2/tests). The `job.Manager` keeps the synchronous fast-path (`Run`) and adds async `Submit` (queued→running→done/failed/cancelled), per-job `context.CancelFunc` tracking for cancellation, metastore persistence of every job (`CreateJob`/`UpdateJob`) for history, and admission control (per-project semaphore + FIFO queue, round-robin fair across projects). The metastore `Store` interface gains 9 methods (Jobs + CacheIndex) implemented in SQLite.

**Tech Stack:** Go 1.26, DuckDB (`github.com/marcboeker/go-duckdb`, CGo), embedded SQLite (`modernc.org/sqlite`, pure Go), chi v5 router, Cobra CLI, `github.com/google/uuid`. Module path: `github.com/esignoretti/ds3-sql-server`.

**Spec:** `docs/superpowers/specs/2026-06-07-ds3-sql-bigquery-refactor-design.md`

---

## File Structure

New packages and files (all under the repo root):

- `internal/metastore/store.go` *(modify)* — add `JobRecord` + `CacheEntry` types and 9 methods (Jobs + CacheIndex) to the `Store` interface.
- `internal/metastore/sqlite.go` *(modify)* — `jobs` + `cache_index` table migrations and the 9 method implementations.
- `internal/metastore/sqlite_jobs_test.go` *(create)* — JobRecord CRUD/list tests against a temp-file DB.
- `internal/metastore/sqlite_cache_test.go` *(create)* — CacheEntry put/lookup/delete/list + per-table invalidation tests.
- `internal/job/job.go` *(modify)* — add `ExecRequest.Type`, `Job.Status` value `cancelled`, async `Submit`, per-job cancel tracking, `Cancel`, `List`, metastore persistence hook (`JobSink`), admission control (semaphore + per-project FIFO queue), `Quota`.
- `internal/job/job_async_test.go` *(create)* — async `Submit`, polling, cancellation, history-persistence (fake sink) tests.
- `internal/job/quota_test.go` *(create)* — admission control: 3rd concurrent job for a project queues when limit is 2; fairness across projects.
- `internal/job/sink.go` *(create)* — `JobSink` interface + `MetastoreSink` adapter mapping `*Job`→`*metastore.JobRecord`.
- `internal/job/sink_test.go` *(create)* — `MetastoreSink` persists create + terminal update against a real SQLite store.
- `internal/cache/result.go` *(create)* — `ResultCache`: SQL normalization, cache-key hashing over referenced-table versions, payload read/write to a `Blobstore`, metastore index, TTL + total-size LRU eviction, `CachingExecutor` wrapper around a `job.Executor`.
- `internal/cache/result_test.go` *(create)* — normalization, key stability, hit/miss, version-bump invalidation, TTL + LRU eviction tests (local temp dir blobstore + real SQLite index).
- `internal/cache/data.go` *(create)* — `DataCache`: read-through whole-object cache (local-dir object store in tests), LRU + size cap keyed by bucket+key+etag, `RewriteBindings` to point ReaderSQL at local copies; SSD-class tables bypass.
- `internal/cache/data_test.go` *(create)* — copy-on-miss, hit-skips-copy, LRU eviction, SSD-bypass, ReaderSQL rewrite tests against a local-dir "object store".
- `internal/worker/server.go` *(create)* — `Server`: `POST /internal/execute` handler, shared-secret guard, runs `query.QueryView` (with optional data-cache rewrite), returns `query.Result`.
- `internal/worker/server_test.go` *(create)* — httptest: auth rejection, execute round-trip over local files.
- `internal/worker/remote.go` *(create)* — `RemoteExecutor` (implements `job.Executor`): consistent-hash worker selection over referenced object/table set, least-loaded fallback, posts to `/internal/execute`.
- `internal/worker/remote_test.go` *(create)* — consistent-hash selection determinism, `RemoteExecutor` against an `httptest.Server` worker, fallback when preferred is down.
- `internal/worker/hashring.go` *(create)* — `HashRing` consistent-hash ring over worker endpoints.
- `internal/worker/hashring_test.go` *(create)* — ring placement determinism + balanced distribution + node removal stability.
- `internal/api/job_handler.go` *(modify)* — `SubmitWithCreds` honors `?wait=<dur>` (sync fast-path inline, else 202 + job id); add `List` (history) and `Cancel`.
- `internal/api/job_handler_test.go` *(modify/create)* — wait fast-path, 202 async path, list, cancel tests.
- `internal/config/config.go` *(modify)* — add `ClusterConfig{Workers []string, SharedSecret string}`, `CacheConfig{ResultDir, ResultTTL, ResultMaxBytes, DataDir, DataMaxBytes}`, `Query.MaxConcurrentPerProject`; env overrides.
- `cmd/ds3sql-server/main.go` *(modify)* — wire by role: result cache + admission in front of `LocalExecutor`/`RemoteExecutor`; worker server for `--role=worker`; new job routes (`GET /jobs`, `DELETE /jobs/{id}`).
- `cmd/ds3sql/jobs_cmd.go` *(create)* — `jobs list|get|cancel|wait`.
- `cmd/ds3sql/query.go` *(modify)* — add `--async` flag.
- `cmd/ds3sql/status.go` *(modify)* — add `authedDelete` helper if not already present.
- `docs/api.md`, `docs/cli.md`, `docs/architecture.md`, `docs/configuration.md`, `README.md` *(modify)* — document scale-out, caching, async jobs, quotas.

**Conventions to follow (from existing code):**
- Handlers needing S3 creds expose `…WithCreds`/`…ForProject`; `main.go` extracts `(projectID, ak, sk, endpoint)` from `auth.GetSession` and calls them (mirrors `JobHandler.SubmitWithCreds`).
- DuckDB credential setup uses the shared `query.applyS3Creds`; workers run queries via `query.Engine.QueryView`, never opening their own connections.
- Errors returned to clients are JSON: `{"error":"…"}`.
- Stores live behind small interfaces; in-memory state uses mutex+map (mirrors `job.Manager`, `convert.JobStore`).
- SQLite stores in tests use a temp-file path (`filepath.Join(t.TempDir(), "m.db")`), never `:memory:`, and rely on `db.SetMaxOpenConns(1)` (already set by `OpenSQLite`).
- Tests avoid live S3: a local directory simulates the object store; DuckDB reads local files directly.

**Simplifications (explicit, Phase 2 only):**
- Worker list is **static** (config `cluster.workers`); dynamic discovery/health-gossip is deferred.
- Worker "load" for least-loaded fallback is an **in-process counter on the coordinator** (in-flight requests dispatched per worker), not a worker-reported metric; documented.
- Coordinator↔worker transport is **plain HTTP** with a shared-secret header (decision recorded per spec Open Question); gRPC deferred.
- Result-cache payloads are **JSON-serialized `query.Result`** on a local dir (or SSD-bucket prefix); Arrow/Parquet paginated results deferred.
- Data cache caches **whole objects** (not byte ranges); HDD-class tables only. SSD-class tables bypass the cache.

---

## Task 1: Metastore — `JobRecord` + Jobs table (CRUD + list)

**Files:**
- Modify: `internal/metastore/store.go`, `internal/metastore/sqlite.go`
- Test: `internal/metastore/sqlite_jobs_test.go` (create)

- [ ] **Step 1: Add the `JobRecord` type and Jobs methods to the Store interface**

In `internal/metastore/store.go`, add the type (after `Table`):
```go
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
```
In the `Store` interface, add (after `BumpDataVersion`):
```go
	CreateJob(ctx context.Context, j *JobRecord) error
	UpdateJob(ctx context.Context, j *JobRecord) error
	GetJob(ctx context.Context, id string) (*JobRecord, error)
	ListJobs(ctx context.Context, projectID string, limit int) ([]*JobRecord, error)
```

- [ ] **Step 2: Write the failing test**

Create `internal/metastore/sqlite_jobs_test.go`:
```go
package metastore

import (
	"context"
	"testing"
	"time"
)

func TestJobCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	j := &JobRecord{
		ID:        "j1",
		ProjectID: "p1",
		Type:      "query",
		SQL:       "SELECT 1",
		Status:    "running",
		CreatedAt: time.Now().UTC(),
		StartedAt: time.Now().UTC(),
	}
	if err := s.CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	j.Status = "done"
	j.RowCount = 5
	j.BytesScanned = 1024
	j.ResultLocation = "file:///tmp/r.json"
	j.FinishedAt = time.Now().UTC()
	if err := s.UpdateJob(ctx, j); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	got, err := s.GetJob(ctx, "j1")
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != "done" || got.RowCount != 5 || got.BytesScanned != 1024 {
		t.Fatalf("update not persisted: %+v", got)
	}
	if got.ResultLocation != "file:///tmp/r.json" {
		t.Fatalf("result location not persisted: %q", got.ResultLocation)
	}

	if _, err := s.GetJob(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// A second project's job must not leak into p1's history.
	if err := s.CreateJob(ctx, &JobRecord{ID: "j2", ProjectID: "p2", Type: "query", SQL: "SELECT 2", Status: "done", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateJob j2: %v", err)
	}
	list, err := s.ListJobs(ctx, "p1", 10)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(list) != 1 || list[0].ID != "j1" {
		t.Fatalf("expected only j1 for p1, got %+v", list)
	}

	// limit is honored.
	if err := s.CreateJob(ctx, &JobRecord{ID: "j3", ProjectID: "p1", Type: "query", SQL: "x", Status: "done", CreatedAt: time.Now().UTC().Add(time.Second)}); err != nil {
		t.Fatalf("CreateJob j3: %v", err)
	}
	limited, err := s.ListJobs(ctx, "p1", 1)
	if err != nil {
		t.Fatalf("ListJobs limited: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected 1 job with limit 1, got %d", len(limited))
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/metastore/ -run TestJobCRUD -v`
Expected: FAIL — `s.CreateJob` undefined.

- [ ] **Step 4: Add the `jobs` table migration**

In `internal/metastore/sqlite.go`, append to the `stmts` slice inside `migrate()`:
```go
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
```

- [ ] **Step 5: Implement the Jobs methods**

In `internal/metastore/sqlite.go`, add (the `time` and `sql` packages are already imported):
```go
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
```

- [ ] **Step 6: Run to verify it passes**

Run: `go test ./internal/metastore/ -run TestJobCRUD -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/metastore/
git commit -m "feat(metastore): add JobRecord type and Jobs CRUD/list"
```

---

## Task 2: Metastore — `CacheEntry` + CacheIndex table (put/lookup/delete/list + per-table invalidation)

**Files:**
- Modify: `internal/metastore/store.go`, `internal/metastore/sqlite.go`
- Test: `internal/metastore/sqlite_cache_test.go` (create)

The result-cache index lives in the metastore. `TableVersions` is a JSON string mapping a referenced table's fully-qualified name (`projectID/dataset/table`) → its `data_version` at cache time. `DeleteCacheEntriesForTable` invalidates every entry whose `TableVersions` JSON references the given table — the seam Phase 3 writes use to auto-invalidate on `BumpDataVersion`.

- [ ] **Step 1: Add the `CacheEntry` type and CacheIndex methods to the Store interface**

In `internal/metastore/store.go`, add the type (after `JobRecord`):
```go
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
```
In the `Store` interface, add (after `ListJobs`):
```go
	PutCacheEntry(ctx context.Context, e *CacheEntry) error
	LookupCacheEntry(ctx context.Context, key string) (*CacheEntry, error)
	DeleteCacheEntry(ctx context.Context, key string) error
	ListCacheEntries(ctx context.Context) ([]*CacheEntry, error)
	DeleteCacheEntriesForTable(ctx context.Context, projectID, dataset, table string) error
```

- [ ] **Step 2: Write the failing test**

Create `internal/metastore/sqlite_cache_test.go`:
```go
package metastore

import (
	"context"
	"testing"
	"time"
)

func TestCacheEntryCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e := &CacheEntry{
		Key:           "k1",
		ProjectID:     "p1",
		SQLNorm:       "select count(*) from sales.orders",
		TableVersions: `{"p1/sales/orders":3}`,
		Location:      "/tmp/cache/k1.json",
		SizeBytes:     128,
		CreatedAt:     time.Now().UTC(),
		LastAccessAt:  time.Now().UTC(),
	}
	if err := s.PutCacheEntry(ctx, e); err != nil {
		t.Fatalf("PutCacheEntry: %v", err)
	}

	// Put is an upsert: updating LastAccessAt must not error.
	e.LastAccessAt = time.Now().UTC().Add(time.Minute)
	if err := s.PutCacheEntry(ctx, e); err != nil {
		t.Fatalf("PutCacheEntry upsert: %v", err)
	}

	got, err := s.LookupCacheEntry(ctx, "k1")
	if err != nil {
		t.Fatalf("LookupCacheEntry: %v", err)
	}
	if got.Location != "/tmp/cache/k1.json" || got.SizeBytes != 128 {
		t.Fatalf("round-trip failed: %+v", got)
	}

	if _, err := s.LookupCacheEntry(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	list, err := s.ListCacheEntries(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListCacheEntries: err=%v len=%d", err, len(list))
	}

	if err := s.DeleteCacheEntry(ctx, "k1"); err != nil {
		t.Fatalf("DeleteCacheEntry: %v", err)
	}
	if _, err := s.LookupCacheEntry(ctx, "k1"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteCacheEntriesForTable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Two entries reference sales.orders; one references sales.other.
	put := func(key, versions string) {
		if err := s.PutCacheEntry(ctx, &CacheEntry{
			Key: key, ProjectID: "p1", SQLNorm: "x", TableVersions: versions,
			Location: "/tmp/" + key, SizeBytes: 1, CreatedAt: time.Now().UTC(), LastAccessAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}
	put("a", `{"p1/sales/orders":1}`)
	put("b", `{"p1/sales/orders":2,"p1/sales/lines":1}`)
	put("c", `{"p1/sales/other":1}`)

	if err := s.DeleteCacheEntriesForTable(ctx, "p1", "sales", "orders"); err != nil {
		t.Fatalf("DeleteCacheEntriesForTable: %v", err)
	}

	if _, err := s.LookupCacheEntry(ctx, "a"); err != ErrNotFound {
		t.Fatalf("entry a should be gone")
	}
	if _, err := s.LookupCacheEntry(ctx, "b"); err != ErrNotFound {
		t.Fatalf("entry b should be gone")
	}
	if _, err := s.LookupCacheEntry(ctx, "c"); err != nil {
		t.Fatalf("entry c must survive: %v", err)
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/metastore/ -run 'TestCacheEntryCRUD|TestDeleteCacheEntriesForTable' -v`
Expected: FAIL — `s.PutCacheEntry` undefined.

- [ ] **Step 4: Add the `cache_index` table migration**

In `internal/metastore/sqlite.go`, append to the `stmts` slice in `migrate()`:
```go
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
```

- [ ] **Step 5: Implement the CacheIndex methods**

In `internal/metastore/sqlite.go`, add:
```go
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
```
Add `"strings"` to the imports in `sqlite.go` if not already present.

- [ ] **Step 6: Run to verify it passes**

Run: `go test ./internal/metastore/ -run 'TestCacheEntryCRUD|TestDeleteCacheEntriesForTable' -v`
Expected: PASS.

- [ ] **Step 7: Run the whole metastore package with the race detector**

Run: `go test -race ./internal/metastore/`
Expected: PASS (ok).

- [ ] **Step 8: Commit**

```bash
git add internal/metastore/
git commit -m "feat(metastore): add CacheEntry index with per-table invalidation"
```

---

## Task 3: Job persistence sink (`MetastoreSink`)

**Files:**
- Create: `internal/job/sink.go`
- Test: `internal/job/sink_test.go`

The `Manager` records every job to query history through a narrow `JobSink` interface so tests can inject a fake and `main.go` wires the metastore. `MetastoreSink` translates a `*Job` into a `*metastore.JobRecord` (create on first sight, update on terminal/state transitions).

- [ ] **Step 1: Write the failing test**

Create `internal/job/sink_test.go`:
```go
package job

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

func TestMetastoreSink_PersistsLifecycle(t *testing.T) {
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	sink := NewMetastoreSink(store)
	ctx := context.Background()

	j := &Job{ID: "j1", ProjectID: "p1", Type: "query", SQL: "SELECT 1", Status: "queued"}
	if err := sink.Save(ctx, j); err != nil {
		t.Fatalf("Save (create): %v", err)
	}

	// Transition to done with a result; Save must update the existing row.
	j.Status = "done"
	j.Result = &query.Result{RowCount: 7}
	if err := sink.Save(ctx, j); err != nil {
		t.Fatalf("Save (update): %v", err)
	}

	rec, err := store.GetJob(ctx, "j1")
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if rec.Status != "done" || rec.RowCount != 7 || rec.ProjectID != "p1" {
		t.Fatalf("unexpected persisted record: %+v", rec)
	}

	list, err := store.ListJobs(ctx, "p1", 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListJobs: err=%v len=%d", err, len(list))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/job/ -run TestMetastoreSink_PersistsLifecycle -v`
Expected: FAIL — `undefined: NewMetastoreSink` / `Job.ProjectID`.

> `Job.ProjectID` is added in Task 4 Step 3. Implementing the sink first will not compile until `Job` carries `ProjectID`; if `go build ./internal/job/` fails on `j.ProjectID` here, do Task 4 Step 3 (the struct field additions) before running this test. The two tasks are committed in order; the field belongs to the Job type so it is added there.

- [ ] **Step 3: Implement the sink**

Create `internal/job/sink.go`:
```go
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

// jobStore is the subset of metastore.Store the sink needs.
type jobStore interface {
	CreateJob(ctx context.Context, j *metastore.JobRecord) error
	UpdateJob(ctx context.Context, j *metastore.JobRecord) error
}

// MetastoreSink persists jobs to the metastore. It tracks which job IDs it has
// already created so subsequent Saves issue an UPDATE.
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
```

- [ ] **Step 4: Run to verify it passes** (after Task 4 Step 3 adds `Job.ProjectID`)

Run: `go test ./internal/job/ -run TestMetastoreSink_PersistsLifecycle -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/job/sink.go internal/job/sink_test.go
git commit -m "feat(job): MetastoreSink persists jobs to query history"
```

---

## Task 4: Async job model — `Submit`, polling, cancellation, history

**Files:**
- Modify: `internal/job/job.go`
- Test: `internal/job/job_async_test.go` (create)

Add async `Submit` (returns immediately, runs in a goroutine; `queued`→`running`→`done`/`failed`/`cancelled`), per-job `context.CancelFunc` for cancellation, a `List` over the in-memory map plus the persisted history, and a `JobSink` hook so every transition is recorded. The synchronous `Run` (Phase 1 fast-path) is retained and now also persists.

- [ ] **Step 1: Write the failing test**

Create `internal/job/job_async_test.go`:
```go
package job

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// blockingExecutor blocks until released or the context is cancelled.
type blockingExecutor struct {
	release  chan struct{}
	started  chan struct{}
	once     sync.Once
}

func (b *blockingExecutor) Execute(ctx context.Context, req ExecRequest) *query.Result {
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.release:
		return &query.Result{Columns: []query.ColumnInfo{{Name: "c"}}, Rows: [][]any{{int64(1)}}, RowCount: 1}
	case <-ctx.Done():
		return &query.Result{Error: "cancelled: " + ctx.Err().Error()}
	}
}

// recordingSink counts Save calls.
type recordingSink struct {
	mu    sync.Mutex
	saves int
}

func (s *recordingSink) Save(ctx context.Context, j *Job) error {
	s.mu.Lock()
	s.saves++
	s.mu.Unlock()
	return nil
}

func waitStatus(t *testing.T, m *Manager, id, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := m.Get(id); ok && j.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	j, _ := m.Get(id)
	t.Fatalf("job %s did not reach %q (last=%v)", id, want, j)
}

func TestSubmit_AsyncCompletes(t *testing.T) {
	be := &blockingExecutor{release: make(chan struct{}), started: make(chan struct{})}
	sink := &recordingSink{}
	m := NewManager(be)
	m.SetSink(sink)

	j := m.Submit(context.Background(), ExecRequest{SQL: "SELECT 1", ProjectID: "p1"})
	if j.Status != "queued" && j.Status != "running" {
		t.Fatalf("expected queued/running immediately, got %q", j.Status)
	}
	<-be.started
	waitStatus(t, m, j.ID, "running")
	close(be.release)
	waitStatus(t, m, j.ID, "done")

	got, _ := m.Get(j.ID)
	if got.Result == nil || got.Result.RowCount != 1 {
		t.Fatalf("unexpected result: %+v", got.Result)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.saves < 2 {
		t.Fatalf("expected at least 2 sink saves (create + terminal), got %d", sink.saves)
	}
}

func TestSubmit_Cancel(t *testing.T) {
	be := &blockingExecutor{release: make(chan struct{}), started: make(chan struct{})}
	m := NewManager(be)

	j := m.Submit(context.Background(), ExecRequest{SQL: "SELECT 1", ProjectID: "p1"})
	<-be.started
	waitStatus(t, m, j.ID, "running")

	if !m.Cancel(j.ID) {
		t.Fatal("Cancel returned false for a running job")
	}
	waitStatus(t, m, j.ID, "cancelled")

	if m.Cancel("nope") {
		t.Fatal("Cancel of unknown job must return false")
	}
}

func TestList_ReturnsRecent(t *testing.T) {
	m := NewManager(execFunc(func(ctx context.Context, req ExecRequest) *query.Result {
		return &query.Result{RowCount: 0}
	}))
	m.Run(context.Background(), ExecRequest{SQL: "a", ProjectID: "p1"})
	m.Run(context.Background(), ExecRequest{SQL: "b", ProjectID: "p1"})
	m.Run(context.Background(), ExecRequest{SQL: "c", ProjectID: "p2"})

	list := m.List("p1", 10)
	if len(list) != 2 {
		t.Fatalf("expected 2 jobs for p1, got %d", len(list))
	}
}
```
> `execFunc` is defined in the Phase 1 `job_test.go`; both files are in package `job`, so it is shared.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/job/ -run 'TestSubmit_AsyncCompletes|TestSubmit_Cancel|TestList_ReturnsRecent' -v`
Expected: FAIL — `m.Submit` / `m.Cancel` / `m.List` / `m.SetSink` undefined.

- [ ] **Step 3: Extend `ExecRequest` and `Job`, add async state to `Manager`**

In `internal/job/job.go`, replace the `ExecRequest` struct with:
```go
// ExecRequest is a query/write execution request with the caller's S3
// credentials. Type selects the execution path ("query" default; "ctas"/"load"
// added in Phase 3).
type ExecRequest struct {
	Type      string
	SQL       string
	ProjectID string
	AccessKey string
	SecretKey string
	Endpoint  string
}
```
Replace the `Job` struct with (adds `ProjectID`; documents `cancelled`):
```go
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
}
```
Replace the `Manager` struct and `NewManager` with:
```go
type Manager struct {
	exec    Executor
	mu      sync.RWMutex
	jobs    map[string]*Job
	cancels map[string]context.CancelFunc
	sink    JobSink
	admit   *admission // nil = no admission control (unbounded)
}

func NewManager(exec Executor) *Manager {
	return &Manager{
		exec:    exec,
		jobs:    make(map[string]*Job),
		cancels: make(map[string]context.CancelFunc),
	}
}

// SetSink installs a persistence sink; every job transition is recorded.
func (m *Manager) SetSink(s JobSink) { m.sink = s }

// SetQuota enables admission control with the given per-project limit.
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
```
Update `Run` to set `ProjectID` and persist:
```go
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
	if res.Error != "" {
		j.Status = "failed"
		j.Error = res.Error
	} else {
		j.Status = "done"
		j.Result = res
	}
	m.put(j)
	m.save(ctx, j)
	return j
}
```
Add `Submit`, `Cancel`, and `List`:
```go
// Submit runs the request asynchronously and returns immediately with a job in
// status "queued". Admission control (if enabled) may hold the job in "queued"
// until a per-project slot is free; otherwise it transitions to "running".
func (m *Manager) Submit(parent context.Context, req ExecRequest) *Job {
	typ := req.Type
	if typ == "" {
		typ = "query"
	}
	// Detach from the request context so the job outlives the HTTP handler;
	// cancellation is driven explicitly via Cancel.
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

	go func() {
		defer m.clearCancel(j.ID)
		// Admission: block until a slot is free (respecting cancellation).
		if m.admit != nil {
			if !m.admit.acquire(ctx, req.ProjectID) {
				j.Status = "cancelled"
				m.put(j)
				m.save(ctx, j)
				return
			}
			defer m.admit.release(req.ProjectID)
		}
		// If cancelled while queued, stop here.
		if ctx.Err() != nil {
			j.Status = "cancelled"
			m.put(j)
			m.save(ctx, j)
			return
		}
		j.Status = "running"
		m.put(j)
		m.save(ctx, j)

		res := m.exec.Execute(ctx, req)
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
		m.put(j)
		m.save(ctx, j)
	}()
	return j
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
// newest first, capped at limit. (Persisted history is read via the metastore
// in the API layer; this serves live in-process jobs.)
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
```
Add `"sort"` to the imports in `job.go`.

- [ ] **Step 4: Add the admission control type (stub for this task; tested in Task 5)**

Append to `internal/job/job.go`:
```go
// admission enforces a per-project max-concurrent limit with a FIFO queue and
// round-robin fairness across projects. acquire blocks until a slot is free or
// ctx is cancelled (returns false on cancellation).
type admission struct {
	mu      sync.Mutex
	limit   int
	inUse   map[string]int        // project -> running count
	waiters map[string][]chan struct{} // project -> FIFO of waiters
	order   []string              // round-robin order of projects with waiters
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
		return true // slot handed off by release()
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

// wakeNext hands a freed slot to the next waiter, scanning projects
// round-robin so no single project starves others.
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
			// Project at capacity; rotate it to the back and keep scanning.
			a.order = append(a.order, p)
			continue
		}
		ch := q[0]
		a.waiters[p] = q[1:]
		a.inUse[p]++
		if len(a.waiters[p]) > 0 {
			a.order = append(a.order, p) // still has waiters
		} else {
			delete(a.waiters, p)
		}
		close(ch)
		return
	}
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
```

- [ ] **Step 5: Run to verify the async tests pass**

Run: `go test ./internal/job/ -run 'TestSubmit_AsyncCompletes|TestSubmit_Cancel|TestList_ReturnsRecent|TestMetastoreSink_PersistsLifecycle' -race -v`
Expected: PASS.

- [ ] **Step 6: Run the whole job package (Phase 1 tests stay green)**

Run: `go test -race ./internal/job/`
Expected: PASS (ok) — `TestManager_RunSync`/`TestManager_RunError` from Phase 1 still pass.

- [ ] **Step 7: Commit**

```bash
git add internal/job/
git commit -m "feat(job): async Submit, cancellation, List, and history persistence"
```

---

## Task 5: Concurrency quota / fair queue (admission control test)

**Files:**
- Test: `internal/job/quota_test.go` (create)

The `admission` logic landed in Task 4; this task pins its behavior with a focused test proving a 3rd job for a project queues when the limit is 2, and that a different project is not blocked by a saturated one.

- [ ] **Step 1: Write the failing test**

Create `internal/job/quota_test.go`:
```go
package job

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// gateExecutor signals each start and blocks until a per-call release.
type gateExecutor struct {
	mu       sync.Mutex
	running  int32
	maxSeen  int32
	release  chan struct{}
}

func (g *gateExecutor) Execute(ctx context.Context, req ExecRequest) *query.Result {
	n := atomic.AddInt32(&g.running, 1)
	g.mu.Lock()
	if n > g.maxSeen {
		g.maxSeen = n
	}
	g.mu.Unlock()
	<-g.release
	atomic.AddInt32(&g.running, -1)
	return &query.Result{RowCount: 1}
}

func TestAdmission_ThirdJobQueuesWhenLimitTwo(t *testing.T) {
	g := &gateExecutor{release: make(chan struct{})}
	m := NewManager(g)
	m.SetQuota(2)

	for i := 0; i < 3; i++ {
		m.Submit(context.Background(), ExecRequest{SQL: "q", ProjectID: "p1"})
	}

	// Give the goroutines time to acquire slots; at most 2 may be running.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&g.running) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // settle
	if got := atomic.LoadInt32(&g.running); got > 2 {
		t.Fatalf("limit breached: %d concurrent (max allowed 2)", got)
	}

	// Release all; the queued 3rd must then complete.
	close(g.release)
	time.Sleep(100 * time.Millisecond)
	if g.maxSeen > 2 {
		t.Fatalf("max concurrency exceeded limit: %d", g.maxSeen)
	}
}

func TestAdmission_OtherProjectNotBlocked(t *testing.T) {
	g := &gateExecutor{release: make(chan struct{})}
	m := NewManager(g)
	m.SetQuota(1)

	// p1 saturates its single slot.
	m.Submit(context.Background(), ExecRequest{SQL: "q", ProjectID: "p1"})
	m.Submit(context.Background(), ExecRequest{SQL: "q", ProjectID: "p1"}) // queues

	// p2 should still get to run despite p1 being saturated.
	m.Submit(context.Background(), ExecRequest{SQL: "q", ProjectID: "p2"})

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&g.running) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt32(&g.running) < 2 {
		t.Fatalf("expected p1 and p2 both running (2), got %d", atomic.LoadInt32(&g.running))
	}
	close(g.release)
	time.Sleep(50 * time.Millisecond)
}
```

- [ ] **Step 2: Run to verify it passes**

Run: `go test ./internal/job/ -run 'TestAdmission_ThirdJobQueuesWhenLimitTwo|TestAdmission_OtherProjectNotBlocked' -race -v`
Expected: PASS (the `admission` implementation from Task 4 satisfies both).

- [ ] **Step 3: Commit**

```bash
git add internal/job/quota_test.go
git commit -m "test(job): admission control queues over-quota jobs, stays fair across projects"
```

---

## Task 6: Result cache — normalization, key, blobstore, hit/miss

**Files:**
- Create: `internal/cache/result.go`
- Test: `internal/cache/result_test.go`

`ResultCache` computes a key from the normalized SQL plus the sorted referenced-table data versions, stores the serialized `query.Result` via a `Blobstore` (local dir in Phase 2/tests), and indexes it in the metastore. It exposes `Get`/`Put` and a `CachingExecutor` that wraps a `job.Executor`.

- [ ] **Step 1: Write the failing test**

Create `internal/cache/result_test.go`:
```go
package cache

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

func newResultCache(t *testing.T) (*ResultCache, *metastore.SQLiteStore) {
	t.Helper()
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	bs := NewDirBlobstore(t.TempDir())
	rc := NewResultCache(store, bs, ResultCacheOpts{TTL: time.Hour, MaxBytes: 1 << 20})
	return rc, store
}

func TestNormalizeSQL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT  *   FROM t", "select * from t"},
		{"\tselect *\nfrom t\n", "select * from t"},
		{"Select * From T", "select * from t"},
	}
	for _, c := range cases {
		if got := NormalizeSQL(c.in); got != c.want {
			t.Fatalf("NormalizeSQL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCacheKey_StableAndVersionSensitive(t *testing.T) {
	v1 := map[string]int64{"p1/sales/orders": 3, "p1/sales/lines": 1}
	v2 := map[string]int64{"p1/sales/lines": 1, "p1/sales/orders": 3} // different map order
	k1 := CacheKey("p1", "SELECT * FROM sales.orders", v1)
	k2 := CacheKey("p1", "select  *  from sales.orders", v2)
	if k1 != k2 {
		t.Fatalf("key should be stable across map order and SQL whitespace/case: %s vs %s", k1, k2)
	}
	// Bumping a version changes the key.
	v3 := map[string]int64{"p1/sales/orders": 4, "p1/sales/lines": 1}
	if CacheKey("p1", "SELECT * FROM sales.orders", v3) == k1 {
		t.Fatal("version bump must change the cache key")
	}
}

func TestResultCache_PutGet(t *testing.T) {
	rc, _ := newResultCache(t)
	ctx := context.Background()
	versions := map[string]int64{"p1/sales/orders": 1}
	res := &query.Result{
		Columns:  []query.ColumnInfo{{Name: "c", Type: "BIGINT"}},
		Rows:     [][]any{{float64(2)}},
		RowCount: 1,
	}

	if got, ok := rc.Get(ctx, "p1", "SELECT count(*) FROM sales.orders", versions); ok {
		t.Fatalf("expected miss on empty cache, got %+v", got)
	}
	if err := rc.Put(ctx, "p1", "SELECT count(*) FROM sales.orders", versions, res); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := rc.Get(ctx, "p1", "SELECT count(*) FROM sales.orders", versions)
	if !ok {
		t.Fatal("expected hit after Put")
	}
	if got.RowCount != 1 || len(got.Columns) != 1 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// A version bump invalidates the lookup (miss).
	bumped := map[string]int64{"p1/sales/orders": 2}
	if _, ok := rc.Get(ctx, "p1", "SELECT count(*) FROM sales.orders", bumped); ok {
		t.Fatal("expected miss after version bump")
	}
}

func TestResultCache_PerTableInvalidation(t *testing.T) {
	rc, store := newResultCache(t)
	ctx := context.Background()
	versions := map[string]int64{"p1/sales/orders": 1}
	res := &query.Result{RowCount: 0}
	if err := rc.Put(ctx, "p1", "SELECT 1 FROM sales.orders", versions, res); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteCacheEntriesForTable(ctx, "p1", "sales", "orders"); err != nil {
		t.Fatal(err)
	}
	if _, ok := rc.Get(ctx, "p1", "SELECT 1 FROM sales.orders", versions); ok {
		t.Fatal("entry should be invalidated after DeleteCacheEntriesForTable")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cache/ -run 'TestNormalizeSQL|TestCacheKey_StableAndVersionSensitive|TestResultCache_PutGet|TestResultCache_PerTableInvalidation' -v`
Expected: FAIL — `undefined: ResultCache` etc.

- [ ] **Step 3: Implement the result cache**

Create `internal/cache/result.go`:
```go
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// NormalizeSQL produces a stable cache-normalization of a SQL string: trim,
// lowercase, and collapse all runs of whitespace to a single space. This is a
// deliberately simple normalization (it does not parse SQL); two textually
// equivalent queries that differ only in case/whitespace share a cache entry.
func NormalizeSQL(sql string) string {
	return strings.ToLower(strings.Join(strings.Fields(sql), " "))
}

// CacheKey hashes the normalized SQL together with the project and the sorted
// referenced-table data versions. Any version change yields a new key, which is
// how writes auto-invalidate dependent cached results.
func CacheKey(projectID, sql string, versions map[string]int64) string {
	fqns := make([]string, 0, len(versions))
	for fqn := range versions {
		fqns = append(fqns, fqn)
	}
	sort.Strings(fqns)
	h := sha256.New()
	h.Write([]byte(projectID))
	h.Write([]byte{0})
	h.Write([]byte(NormalizeSQL(sql)))
	for _, fqn := range fqns {
		h.Write([]byte{0})
		h.Write([]byte(fqn))
		h.Write([]byte{'='})
		h.Write([]byte(strconv.FormatInt(versions[fqn], 10)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Blobstore stores and retrieves serialized result payloads by key.
type Blobstore interface {
	Write(key string, data []byte) (location string, err error)
	Read(location string) ([]byte, error)
	Delete(location string) error
}

// DirBlobstore is a filesystem-backed Blobstore (a local dir or an SSD-bucket
// mount). Phase 2 uses this; an s3:// blobstore can implement the same iface.
type DirBlobstore struct{ dir string }

func NewDirBlobstore(dir string) *DirBlobstore { return &DirBlobstore{dir: dir} }

func (b *DirBlobstore) Write(key string, data []byte) (string, error) {
	if err := os.MkdirAll(b.dir, 0755); err != nil {
		return "", err
	}
	p := filepath.Join(b.dir, key+".json")
	if err := os.WriteFile(p, data, 0644); err != nil {
		return "", err
	}
	return p, nil
}

func (b *DirBlobstore) Read(location string) ([]byte, error) { return os.ReadFile(location) }
func (b *DirBlobstore) Delete(location string) error {
	err := os.Remove(location)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// cacheStore is the subset of metastore.Store the result cache needs.
type cacheStore interface {
	PutCacheEntry(ctx context.Context, e *metastore.CacheEntry) error
	LookupCacheEntry(ctx context.Context, key string) (*metastore.CacheEntry, error)
	DeleteCacheEntry(ctx context.Context, key string) error
	ListCacheEntries(ctx context.Context) ([]*metastore.CacheEntry, error)
}

type ResultCacheOpts struct {
	TTL      time.Duration
	MaxBytes int64
}

// ResultCache indexes cached query results in the metastore and stores payloads
// in a Blobstore. Eviction is TTL + total-size LRU.
type ResultCache struct {
	store cacheStore
	blobs Blobstore
	opts  ResultCacheOpts
}

func NewResultCache(store cacheStore, blobs Blobstore, opts ResultCacheOpts) *ResultCache {
	return &ResultCache{store: store, blobs: blobs, opts: opts}
}

// Get returns a cached result on hit. Misses (including TTL-expired entries)
// return ok=false. On hit, LastAccessAt is refreshed (LRU bookkeeping).
func (c *ResultCache) Get(ctx context.Context, projectID, sql string, versions map[string]int64) (*query.Result, bool) {
	key := CacheKey(projectID, sql, versions)
	e, err := c.store.LookupCacheEntry(ctx, key)
	if err != nil {
		return nil, false
	}
	if c.opts.TTL > 0 && time.Since(e.CreatedAt) > c.opts.TTL {
		_ = c.store.DeleteCacheEntry(ctx, key)
		_ = c.blobs.Delete(e.Location)
		return nil, false
	}
	data, err := c.blobs.Read(e.Location)
	if err != nil {
		_ = c.store.DeleteCacheEntry(ctx, key)
		return nil, false
	}
	var res query.Result
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, false
	}
	e.LastAccessAt = time.Now().UTC()
	_ = c.store.PutCacheEntry(ctx, e)
	return &res, true
}

// Put serializes the result, stores it, and indexes it. After insertion it runs
// total-size LRU eviction.
func (c *ResultCache) Put(ctx context.Context, projectID, sql string, versions map[string]int64, res *query.Result) error {
	key := CacheKey(projectID, sql, versions)
	data, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	loc, err := c.blobs.Write(key, data)
	if err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	tv, _ := json.Marshal(versions)
	now := time.Now().UTC()
	if err := c.store.PutCacheEntry(ctx, &metastore.CacheEntry{
		Key:           key,
		ProjectID:     projectID,
		SQLNorm:       NormalizeSQL(sql),
		TableVersions: string(tv),
		Location:      loc,
		SizeBytes:     int64(len(data)),
		CreatedAt:     now,
		LastAccessAt:  now,
	}); err != nil {
		return fmt.Errorf("index entry: %w", err)
	}
	return c.evictLRU(ctx)
}

// evictLRU removes least-recently-accessed entries until total size is under the
// cap. ListCacheEntries returns rows ordered by last_access_at ASC (oldest
// first), so we delete from the front while over budget.
func (c *ResultCache) evictLRU(ctx context.Context) error {
	if c.opts.MaxBytes <= 0 {
		return nil
	}
	entries, err := c.store.ListCacheEntries(ctx)
	if err != nil {
		return err
	}
	var total int64
	for _, e := range entries {
		total += e.SizeBytes
	}
	for _, e := range entries {
		if total <= c.opts.MaxBytes {
			break
		}
		_ = c.blobs.Delete(e.Location)
		_ = c.store.DeleteCacheEntry(ctx, e.Key)
		total -= e.SizeBytes
	}
	return nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/cache/ -run 'TestNormalizeSQL|TestCacheKey_StableAndVersionSensitive|TestResultCache_PutGet|TestResultCache_PerTableInvalidation' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cache/result.go internal/cache/result_test.go
git commit -m "feat(cache): result cache with version-keyed invalidation and LRU eviction"
```

---

## Task 7: Result cache — TTL + LRU eviction tests, and `CachingExecutor`

**Files:**
- Modify: `internal/cache/result.go`
- Test: `internal/cache/result_test.go` (append)

`CachingExecutor` wraps a `job.Executor` and a `catalog`-derived version source so the cache sits in front of execution: on `Execute` it computes referenced-table versions, checks the cache (hit → return cached result), else runs the wrapped executor and stores the result.

- [ ] **Step 1: Write the failing tests (TTL, LRU, caching executor)**

Append to `internal/cache/result_test.go`:
```go
import (
	// (add alongside existing imports)
	"context"
	"sync/atomic"
)

func TestResultCache_TTLExpiry(t *testing.T) {
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	rc := NewResultCache(store, NewDirBlobstore(t.TempDir()), ResultCacheOpts{TTL: time.Nanosecond, MaxBytes: 1 << 20})
	ctx := context.Background()
	versions := map[string]int64{"p1/sales/orders": 1}
	if err := rc.Put(ctx, "p1", "SELECT 1", versions, &query.Result{RowCount: 0}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, ok := rc.Get(ctx, "p1", "SELECT 1", versions); ok {
		t.Fatal("expected TTL-expired miss")
	}
}

func TestResultCache_LRUEviction(t *testing.T) {
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	// Tiny cap forces eviction after a couple of entries.
	rc := NewResultCache(store, NewDirBlobstore(t.TempDir()), ResultCacheOpts{TTL: time.Hour, MaxBytes: 200})
	ctx := context.Background()
	big := make([]any, 20)
	for i := range big {
		big[i] = "0123456789"
	}
	for i := 0; i < 5; i++ {
		v := map[string]int64{"p1/t/x": int64(i)}
		if err := rc.Put(ctx, "p1", "SELECT "+string(rune('a'+i)), v, &query.Result{Rows: [][]any{big}, RowCount: 1}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // ensure distinct last_access ordering
	}
	entries, err := store.ListCacheEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, e := range entries {
		total += e.SizeBytes
	}
	if total > 200 {
		t.Fatalf("LRU did not keep cache under cap: total=%d", total)
	}
	if len(entries) == 0 {
		t.Fatal("eviction removed everything; expected the most-recent entry to survive")
	}
}

// fakeExec counts executions and returns a fixed result.
type fakeExec struct{ calls int32 }

func (f *fakeExec) Execute(ctx context.Context, projectID, sql, ak, sk, ep string, versions map[string]int64) *query.Result {
	atomic.AddInt32(&f.calls, 1)
	return &query.Result{RowCount: 42}
}

func TestCachingExecutor_HitSkipsExecution(t *testing.T) {
	rc, _ := newResultCache(t)
	ctx := context.Background()
	fe := &fakeExec{}
	versions := map[string]int64{"p1/sales/orders": 1}
	vs := func(ctx context.Context, projectID, sql string) (map[string]int64, error) { return versions, nil }
	ce := NewCachingExecutor(rc, fe.Execute, vs)

	r1 := ce.Run(ctx, "p1", "SELECT * FROM sales.orders", "", "", "")
	if r1.RowCount != 42 {
		t.Fatalf("first run result: %+v", r1)
	}
	r2 := ce.Run(ctx, "p1", "SELECT * FROM sales.orders", "", "", "")
	if r2.RowCount != 42 {
		t.Fatalf("second run result: %+v", r2)
	}
	if atomic.LoadInt32(&fe.calls) != 1 {
		t.Fatalf("expected 1 underlying execution (2nd served from cache), got %d", fe.calls)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cache/ -run 'TestResultCache_TTLExpiry|TestResultCache_LRUEviction|TestCachingExecutor_HitSkipsExecution' -v`
Expected: FAIL — `undefined: NewCachingExecutor` (TTL/LRU tests should already pass given Task 6; the build fails because of the missing type, so run after Step 3).

- [ ] **Step 3: Implement `CachingExecutor`**

Append to `internal/cache/result.go`:
```go
// RawExec runs a query without caching, given the resolved referenced-table
// versions. It is the function the cache wraps (the coordinator supplies one
// backed by job.Executor + catalog).
type RawExec func(ctx context.Context, projectID, sql, accessKey, secretKey, endpoint string, versions map[string]int64) *query.Result

// VersionSource returns the referenced-table data versions for a SQL string,
// keyed by fully-qualified name (projectID/dataset/table).
type VersionSource func(ctx context.Context, projectID, sql string) (map[string]int64, error)

// CachingExecutor places the result cache in front of a RawExec. On a cache hit
// it returns the stored result; on a miss it executes and stores it.
type CachingExecutor struct {
	cache    *ResultCache
	exec     RawExec
	versions VersionSource
}

func NewCachingExecutor(cache *ResultCache, exec RawExec, versions VersionSource) *CachingExecutor {
	return &CachingExecutor{cache: cache, exec: exec, versions: versions}
}

// Run executes with caching. Errors from version resolution or cache storage are
// non-fatal: on any cache-path failure it falls back to a direct execution.
func (c *CachingExecutor) Run(ctx context.Context, projectID, sql, accessKey, secretKey, endpoint string) *query.Result {
	versions, err := c.versions(ctx, projectID, sql)
	if err != nil {
		return c.exec(ctx, projectID, sql, accessKey, secretKey, endpoint, nil)
	}
	if hit, ok := c.cache.Get(ctx, projectID, sql, versions); ok {
		return hit
	}
	res := c.exec(ctx, projectID, sql, accessKey, secretKey, endpoint, versions)
	if res.Error == "" {
		_ = c.cache.Put(ctx, projectID, sql, versions, res)
	}
	return res
}
```

- [ ] **Step 4: Run to verify all cache tests pass**

Run: `go test ./internal/cache/ -v`
Expected: PASS (TTL, LRU, hit/miss, invalidation, caching executor).

- [ ] **Step 5: Commit**

```bash
git add internal/cache/result.go internal/cache/result_test.go
git commit -m "feat(cache): CachingExecutor front-of-executor wrapper; TTL/LRU tests"
```

---

## Task 8: Local-SSD data cache (`internal/cache/data.go`)

**Files:**
- Create: `internal/cache/data.go`
- Test: `internal/cache/data_test.go`

A read-through whole-object cache for HDD-class objects. The "object store" is abstracted behind an `ObjectStore` interface (a local-dir implementation simulates S3 in tests). On a cache miss the worker copies the object to its local SSD dir (keyed by `bucket/key/etag`), under an LRU size cap; it then rewrites `ViewBinding.ReaderSQL` to read the local copy. SSD-class tables bypass the cache.

- [ ] **Step 1: Write the failing test**

Create `internal/cache/data_test.go`:
```go
package cache

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dirObjectStore simulates an object store backed by a local directory:
// "bucket/key" maps to <root>/bucket/key.
type dirObjectStore struct{ root string }

func (s *dirObjectStore) Get(ctx context.Context, bucket, key string) ([]byte, string, error) {
	p := filepath.Join(s.root, bucket, key)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	// Use size as a cheap stand-in etag for the test.
	return data, etagOf(data), nil
}

func writeObject(t *testing.T, root, bucket, key, content string) {
	t.Helper()
	p := filepath.Join(root, bucket, key)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDataCache_CopiesOnMissAndHits(t *testing.T) {
	srcRoot := t.TempDir()
	writeObject(t, srcRoot, "cold", "data/a.parquet", "PARQUET-A")
	os := &dirObjectStore{root: srcRoot}
	dc := NewDataCache(os, t.TempDir(), 1<<20)
	ctx := context.Background()

	local1, hit1, err := dc.Ensure(ctx, "cold", "data/a.parquet")
	if err != nil {
		t.Fatalf("Ensure miss: %v", err)
	}
	if hit1 {
		t.Fatal("first Ensure should be a miss")
	}
	if b, _ := readFile(local1); b != "PARQUET-A" {
		t.Fatalf("local copy content = %q", b)
	}

	local2, hit2, err := dc.Ensure(ctx, "cold", "data/a.parquet")
	if err != nil {
		t.Fatalf("Ensure hit: %v", err)
	}
	if !hit2 {
		t.Fatal("second Ensure should be a hit")
	}
	if local1 != local2 {
		t.Fatalf("cache path changed between calls: %q vs %q", local1, local2)
	}
}

func TestDataCache_LRUEviction(t *testing.T) {
	srcRoot := t.TempDir()
	writeObject(t, srcRoot, "cold", "a", "AAAAAAAAAA")
	writeObject(t, srcRoot, "cold", "b", "BBBBBBBBBB")
	writeObject(t, srcRoot, "cold", "c", "CCCCCCCCCC")
	store := &dirObjectStore{root: srcRoot}
	dc := NewDataCache(store, t.TempDir(), 25) // ~25 bytes: holds 2 of the 10-byte objects
	ctx := context.Background()

	_, _, _ = dc.Ensure(ctx, "cold", "a")
	_, _, _ = dc.Ensure(ctx, "cold", "b")
	_, _, _ = dc.Ensure(ctx, "cold", "c") // should evict "a"

	if dc.TotalBytes() > 25 {
		t.Fatalf("cache over cap: %d", dc.TotalBytes())
	}
}

func TestDataCache_RewriteBindings_HDDOnly(t *testing.T) {
	srcRoot := t.TempDir()
	writeObject(t, srcRoot, "cold", "orders.parquet", "DATA")
	store := &dirObjectStore{root: srcRoot}
	dc := NewDataCache(store, t.TempDir(), 1<<20)
	ctx := context.Background()

	bindings := []Binding{
		{Schema: "sales", Name: "orders", ReaderSQL: "read_parquet('s3://cold/orders.parquet')", StorageClass: "hdd", Objects: []ObjectRef{{Bucket: "cold", Key: "orders.parquet"}}},
		{Schema: "sales", Name: "fast", ReaderSQL: "read_parquet('s3://fast/f.parquet')", StorageClass: "ssd", Objects: []ObjectRef{{Bucket: "fast", Key: "f.parquet"}}},
	}
	out, err := dc.RewriteBindings(ctx, bindings)
	if err != nil {
		t.Fatalf("RewriteBindings: %v", err)
	}
	// HDD binding now points at a local file path.
	if strings.Contains(out[0].ReaderSQL, "s3://cold") {
		t.Fatalf("hdd binding not rewritten: %q", out[0].ReaderSQL)
	}
	if !strings.Contains(out[0].ReaderSQL, "read_parquet('") {
		t.Fatalf("hdd binding lost reader wrapper: %q", out[0].ReaderSQL)
	}
	// SSD binding is unchanged (bypass).
	if out[1].ReaderSQL != "read_parquet('s3://fast/f.parquet')" {
		t.Fatalf("ssd binding should bypass cache, got %q", out[1].ReaderSQL)
	}
}

// helpers
func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cache/ -run 'TestDataCache_CopiesOnMissAndHits|TestDataCache_LRUEviction|TestDataCache_RewriteBindings_HDDOnly' -v`
Expected: FAIL — `undefined: NewDataCache` etc.

- [ ] **Step 3: Implement the data cache**

Create `internal/cache/data.go`:
```go
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ObjectStore fetches whole objects from the backing store (S3/DS3 in
// production; a local-dir fake in tests). Get returns the bytes and an etag.
type ObjectStore interface {
	Get(ctx context.Context, bucket, key string) (data []byte, etag string, err error)
}

// ObjectRef identifies one object backing a table binding.
type ObjectRef struct {
	Bucket string
	Key    string
}

// Binding is the worker-side view of a table to execute over. It mirrors
// query.ViewBinding plus the storage class and the concrete objects so the data
// cache can decide whether to localize the data.
type Binding struct {
	Schema       string
	Name         string
	ReaderSQL    string
	StorageClass string
	Objects      []ObjectRef
}

// etagOf is a content-hash etag used by the local-dir test object store and as a
// fallback when the store does not provide one.
func etagOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

type cacheItem struct {
	path     string
	size     int64
	accessed time.Time
}

// DataCache copies HDD-class objects onto local SSD (read-through), evicting LRU
// when over the size cap, and rewrites reader SQL to point at the local copies.
type DataCache struct {
	store   ObjectStore
	dir     string
	maxBytes int64

	mu    sync.Mutex
	items map[string]*cacheItem // cacheKey -> item
	total int64
}

func NewDataCache(store ObjectStore, dir string, maxBytes int64) *DataCache {
	return &DataCache{
		store:    store,
		dir:      dir,
		maxBytes: maxBytes,
		items:    make(map[string]*cacheItem),
	}
}

func cacheKeyFor(bucket, key, etag string) string {
	sum := sha256.Sum256([]byte(bucket + "/" + key + "@" + etag))
	return hex.EncodeToString(sum[:])
}

// Ensure makes sure the object is present on local SSD and returns its local
// path. The bool reports whether it was already cached (a hit).
func (c *DataCache) Ensure(ctx context.Context, bucket, key string) (string, bool, error) {
	data, etag, err := c.store.Get(ctx, bucket, key)
	if err != nil {
		return "", false, fmt.Errorf("fetch %s/%s: %w", bucket, key, err)
	}
	ck := cacheKeyFor(bucket, key, etag)

	c.mu.Lock()
	if it, ok := c.items[ck]; ok {
		it.accessed = time.Now()
		path := it.path
		c.mu.Unlock()
		return path, true, nil
	}
	c.mu.Unlock()

	if err := os.MkdirAll(c.dir, 0755); err != nil {
		return "", false, err
	}
	path := filepath.Join(c.dir, ck+filepath.Ext(key))
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", false, err
	}

	c.mu.Lock()
	c.items[ck] = &cacheItem{path: path, size: int64(len(data)), accessed: time.Now()}
	c.total += int64(len(data))
	c.evictLocked()
	c.mu.Unlock()
	return path, false, nil
}

// evictLocked removes least-recently-used items until total <= maxBytes. The
// caller must hold c.mu.
func (c *DataCache) evictLocked() {
	if c.maxBytes <= 0 || c.total <= c.maxBytes {
		return
	}
	type kv struct {
		key string
		it  *cacheItem
	}
	all := make([]kv, 0, len(c.items))
	for k, it := range c.items {
		all = append(all, kv{k, it})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].it.accessed.Before(all[j].it.accessed) })
	for _, e := range all {
		if c.total <= c.maxBytes {
			break
		}
		_ = os.Remove(e.it.path)
		c.total -= e.it.size
		delete(c.items, e.key)
	}
}

// TotalBytes reports the current cache footprint (for tests/metrics).
func (c *DataCache) TotalBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// RewriteBindings localizes HDD-class bindings' objects and rewrites their
// ReaderSQL to read the local copies; SSD-class bindings pass through unchanged.
// When a binding has a single object, the reader points at its local file; with
// multiple objects it points at a brace-expanded list. The reader function is
// preserved (read_parquet/read_csv_auto/...) by replacing only the path list.
func (c *DataCache) RewriteBindings(ctx context.Context, bindings []Binding) ([]Binding, error) {
	out := make([]Binding, len(bindings))
	for i, b := range bindings {
		out[i] = b
		if strings.ToLower(b.StorageClass) == "ssd" || len(b.Objects) == 0 {
			continue // SSD tables are already fast: bypass the cache.
		}
		localPaths := make([]string, 0, len(b.Objects))
		for _, obj := range b.Objects {
			p, _, err := c.Ensure(ctx, obj.Bucket, obj.Key)
			if err != nil {
				return nil, err
			}
			localPaths = append(localPaths, p)
		}
		out[i].ReaderSQL = rewriteReader(b.ReaderSQL, localPaths)
	}
	return out, nil
}

// rewriteReader keeps the leading reader function (up to the first single-quoted
// path argument) and substitutes the local path list. For a single file:
//   read_parquet('local')      ; for many:  read_parquet(['l1','l2'])
func rewriteReader(reader string, localPaths []string) string {
	open := strings.IndexByte(reader, '(')
	if open < 0 {
		return reader
	}
	fn := reader[:open] // e.g. "read_parquet"
	// Preserve any trailing options after the path argument (e.g. ", delim='\t'")
	// by locating the closing paren of the original call.
	rest := ""
	if close := strings.LastIndexByte(reader, ')'); close > open {
		// Find the end of the first quoted path to capture trailing options.
		firstQuote := strings.IndexByte(reader[open:], '\'')
		if firstQuote >= 0 {
			afterPathQuote := strings.IndexByte(reader[open+firstQuote+1:], '\'')
			if afterPathQuote >= 0 {
				tail := reader[open+firstQuote+1+afterPathQuote+1 : close]
				rest = strings.TrimSpace(tail)
			}
		}
	}
	var pathArg string
	if len(localPaths) == 1 {
		pathArg = "'" + escapePath(localPaths[0]) + "'"
	} else {
		quoted := make([]string, len(localPaths))
		for i, p := range localPaths {
			quoted[i] = "'" + escapePath(p) + "'"
		}
		pathArg = "[" + strings.Join(quoted, ", ") + "]"
	}
	if rest != "" {
		return fn + "(" + pathArg + rest + ")"
	}
	return fn + "(" + pathArg + ")"
}

func escapePath(s string) string { return strings.ReplaceAll(s, "'", "''") }
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/cache/ -run 'TestDataCache_CopiesOnMissAndHits|TestDataCache_LRUEviction|TestDataCache_RewriteBindings_HDDOnly' -v`
Expected: PASS.

- [ ] **Step 5: Run the whole cache package with the race detector**

Run: `go test -race ./internal/cache/`
Expected: PASS (ok).

- [ ] **Step 6: Commit**

```bash
git add internal/cache/data.go internal/cache/data_test.go
git commit -m "feat(cache): local-SSD read-through data cache with binding rewrite (HDD-only)"
```

---

## Task 9: Worker — consistent-hash ring

**Files:**
- Create: `internal/worker/hashring.go`
- Test: `internal/worker/hashring_test.go`

A consistent-hash ring over worker endpoints with virtual nodes, used to route a job to a worker based on its referenced object/table set (cache locality).

- [ ] **Step 1: Write the failing test**

Create `internal/worker/hashring_test.go`:
```go
package worker

import "testing"

func TestHashRing_Deterministic(t *testing.T) {
	r := NewHashRing([]string{"http://w1", "http://w2", "http://w3"}, 50)
	a := r.Get("sales/orders")
	b := r.Get("sales/orders")
	if a != b {
		t.Fatalf("ring not deterministic: %q vs %q", a, b)
	}
	if a == "" {
		t.Fatal("expected a node")
	}
}

func TestHashRing_DistributesAcrossNodes(t *testing.T) {
	nodes := []string{"http://w1", "http://w2", "http://w3"}
	r := NewHashRing(nodes, 100)
	seen := map[string]bool{}
	for i := 0; i < 300; i++ {
		seen[r.Get(string(rune('a'+i%26))+string(rune('0'+i%10))+string(rune(i)))] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected keys to spread across nodes, only saw %d", len(seen))
	}
}

func TestHashRing_StableOnNodeRemoval(t *testing.T) {
	full := NewHashRing([]string{"http://w1", "http://w2", "http://w3"}, 100)
	reduced := NewHashRing([]string{"http://w1", "http://w2"}, 100)
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	moved := 0
	for _, k := range keys {
		if full.Get(k) != reduced.Get(k) {
			moved++
		}
	}
	// Only keys that hashed to w3 should move; not all keys.
	if moved == len(keys) {
		t.Fatalf("removal remapped every key; consistent hashing should localize churn")
	}
}

func TestHashRing_EmptyReturnsEmpty(t *testing.T) {
	r := NewHashRing(nil, 50)
	if r.Get("x") != "" {
		t.Fatal("empty ring must return empty string")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/worker/ -run 'TestHashRing' -v`
Expected: FAIL — `undefined: NewHashRing`.

- [ ] **Step 3: Implement the ring**

Create `internal/worker/hashring.go`:
```go
package worker

import (
	"hash/crc32"
	"sort"
	"strconv"
)

// HashRing is a consistent-hash ring over worker endpoints with virtual nodes.
type HashRing struct {
	hashes []uint32          // sorted vnode hashes
	owner  map[uint32]string // vnode hash -> endpoint
}

// NewHashRing builds a ring over endpoints with `replicas` virtual nodes each.
func NewHashRing(endpoints []string, replicas int) *HashRing {
	if replicas < 1 {
		replicas = 1
	}
	r := &HashRing{owner: make(map[uint32]string)}
	for _, ep := range endpoints {
		for i := 0; i < replicas; i++ {
			h := crc32.ChecksumIEEE([]byte(ep + "#" + strconv.Itoa(i)))
			r.hashes = append(r.hashes, h)
			r.owner[h] = ep
		}
	}
	sort.Slice(r.hashes, func(i, j int) bool { return r.hashes[i] < r.hashes[j] })
	return r
}

// Get returns the endpoint owning the given key (the first vnode clockwise from
// the key's hash). Returns "" for an empty ring.
func (r *HashRing) Get(key string) string {
	if len(r.hashes) == 0 {
		return ""
	}
	h := crc32.ChecksumIEEE([]byte(key))
	idx := sort.Search(len(r.hashes), func(i int) bool { return r.hashes[i] >= h })
	if idx == len(r.hashes) {
		idx = 0 // wrap around
	}
	return r.owner[r.hashes[idx]]
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/worker/ -run 'TestHashRing' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/hashring.go internal/worker/hashring_test.go
git commit -m "feat(worker): consistent-hash ring for cache-locality routing"
```

---

## Task 10: Worker server — `POST /internal/execute` with shared-secret guard

**Files:**
- Create: `internal/worker/server.go`
- Test: `internal/worker/server_test.go`

The worker server accepts a resolved request (SQL + view bindings + creds), runs `query.QueryView`, and returns the `query.Result`. A shared-secret header guards the endpoint. The wire types are shared with `RemoteExecutor` (Task 11).

- [ ] **Step 1: Write the failing test**

Create `internal/worker/server_test.go`:
```go
package worker

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/query"
)

func newWorkerServer(t *testing.T, secret string) *Server {
	t.Helper()
	eng, err := query.NewEngine(1000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(eng, secret, nil)
}

func TestWorkerServer_RejectsBadSecret(t *testing.T) {
	s := newWorkerServer(t, "topsecret")
	srv := httptest.NewServer(http.HandlerFunc(s.Execute))
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL, bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-DS3SQL-Worker-Secret", "wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestWorkerServer_ExecutesOverLocalFile(t *testing.T) {
	s := newWorkerServer(t, "topsecret")
	srv := httptest.NewServer(http.HandlerFunc(s.Execute))
	defer srv.Close()

	csv := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(csv, []byte("id,total\n1,10\n2,20\n3,30\n"), 0644); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(ExecuteRequest{
		SQL: "SELECT sum(total) AS s FROM sales.orders",
		Bindings: []WireBinding{{
			Schema: "sales", Name: "orders",
			ReaderSQL: "read_csv_auto('" + csv + "')", StorageClass: "ssd",
		}},
	})
	req, _ := http.NewRequest("POST", srv.URL, bytes.NewReader(body))
	req.Header.Set("X-DS3SQL-Worker-Secret", "topsecret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var res query.Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Fatalf("execute error: %s", res.Error)
	}
	if res.RowCount != 1 {
		t.Fatalf("expected 1 row, got %d", res.RowCount)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/worker/ -run 'TestWorkerServer' -v`
Expected: FAIL — `undefined: NewServer` / `ExecuteRequest` / `WireBinding`.

- [ ] **Step 3: Implement the wire types and server**

Create `internal/worker/server.go`:
```go
package worker

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/cache"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// SecretHeader carries the shared secret guarding worker endpoints.
const SecretHeader = "X-DS3SQL-Worker-Secret"

// WireBinding is the JSON-serializable form of a resolved table binding sent
// from coordinator to worker. Objects + StorageClass let the worker localize
// HDD data via its SSD cache before executing.
type WireBinding struct {
	Schema       string            `json:"schema"`
	Name         string            `json:"name"`
	ReaderSQL    string            `json:"reader_sql"`
	StorageClass string            `json:"storage_class"`
	Objects      []cache.ObjectRef `json:"objects,omitempty"`
}

// ExecuteRequest is the resolved plan dispatched to a worker.
type ExecuteRequest struct {
	SQL       string        `json:"sql"`
	Bindings  []WireBinding `json:"bindings"`
	AccessKey string        `json:"access_key"`
	SecretKey string        `json:"secret_key"`
	Endpoint  string        `json:"endpoint"`
}

// Server is the worker data-plane HTTP server.
type Server struct {
	engine *query.Engine
	secret string
	data   *cache.DataCache // optional local-SSD data cache (nil disables)
}

func NewServer(engine *query.Engine, secret string, data *cache.DataCache) *Server {
	return &Server{engine: engine, secret: secret, data: data}
}

// Execute handles POST /internal/execute. It validates the shared secret,
// optionally localizes HDD objects via the data cache, then runs QueryView.
func (s *Server) Execute(w http.ResponseWriter, r *http.Request) {
	if s.secret == "" || r.Header.Get(SecretHeader) != s.secret {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var req ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	bindings := s.resolveBindings(r.Context(), req)
	res := s.engine.QueryView(req.SQL, bindings, req.AccessKey, req.SecretKey, req.Endpoint)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// resolveBindings converts wire bindings to query.ViewBinding, rewriting HDD
// readers through the SSD data cache when one is configured.
func (s *Server) resolveBindings(ctx context.Context, req ExecuteRequest) []query.ViewBinding {
	if s.data != nil {
		cb := make([]cache.Binding, len(req.Bindings))
		for i, b := range req.Bindings {
			cb[i] = cache.Binding{
				Schema: b.Schema, Name: b.Name, ReaderSQL: b.ReaderSQL,
				StorageClass: b.StorageClass, Objects: b.Objects,
			}
		}
		if rewritten, err := s.data.RewriteBindings(ctx, cb); err == nil {
			out := make([]query.ViewBinding, len(rewritten))
			for i, b := range rewritten {
				out[i] = query.ViewBinding{Schema: b.Schema, Name: b.Name, ReaderSQL: b.ReaderSQL}
			}
			return out
		}
		// On cache failure, fall through to the original readers.
	}
	out := make([]query.ViewBinding, len(req.Bindings))
	for i, b := range req.Bindings {
		out[i] = query.ViewBinding{Schema: b.Schema, Name: b.Name, ReaderSQL: b.ReaderSQL}
	}
	return out
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/worker/ -run 'TestWorkerServer' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/server.go internal/worker/server_test.go
git commit -m "feat(worker): /internal/execute server with shared-secret guard and SSD-cache rewrite"
```

---

## Task 11: Worker — `RemoteExecutor` (coordinator side)

**Files:**
- Create: `internal/worker/remote.go`
- Test: `internal/worker/remote_test.go`

`RemoteExecutor` implements `job.Executor`. It resolves the SQL to bindings via a catalog resolver, picks a worker by consistent-hashing the referenced object/table set, POSTs the `ExecuteRequest` to `/internal/execute`, and on connection failure falls back to the least-loaded (fewest in-flight) worker.

- [ ] **Step 1: Write the failing test**

Create `internal/worker/remote_test.go`:
```go
package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/job"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// fakeResolver returns fixed bindings regardless of SQL.
type fakeResolver struct{ bindings []WireBinding }

func (f fakeResolver) ResolveWire(ctx context.Context, projectID, sql string) ([]WireBinding, error) {
	return f.bindings, nil
}

func TestRemoteExecutor_RoundTrip(t *testing.T) {
	// Stand-in worker: echoes a fixed result, asserting the secret arrived.
	var gotSecret string
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecret = r.Header.Get(SecretHeader)
		json.NewEncoder(w).Encode(query.Result{RowCount: 99})
	}))
	defer worker.Close()

	re := NewRemoteExecutor([]string{worker.URL}, "sekret", fakeResolver{bindings: []WireBinding{
		{Schema: "sales", Name: "orders", ReaderSQL: "read_parquet('s3://cold/o/*.parquet')", StorageClass: "hdd"},
	}})
	res := re.Execute(context.Background(), job.ExecRequest{SQL: "SELECT 1", ProjectID: "p1"})
	if res.Error != "" {
		t.Fatalf("execute error: %s", res.Error)
	}
	if res.RowCount != 99 {
		t.Fatalf("expected echoed result 99, got %d", res.RowCount)
	}
	if gotSecret != "sekret" {
		t.Fatalf("worker did not receive the shared secret, got %q", gotSecret)
	}
}

func TestRemoteExecutor_FallsBackWhenPreferredDown(t *testing.T) {
	// Healthy worker.
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(query.Result{RowCount: 7})
	}))
	defer good.Close()

	// Down worker: an address that is closed.
	down := "http://127.0.0.1:1"

	// Put the down worker first so the ring may prefer it; the executor must
	// fall back to the healthy one and still succeed.
	re := NewRemoteExecutor([]string{down, good.URL}, "s", fakeResolver{})
	res := re.Execute(context.Background(), job.ExecRequest{SQL: "SELECT 1", ProjectID: "p1"})
	if res.Error != "" {
		t.Fatalf("expected fallback success, got error: %s", res.Error)
	}
	if res.RowCount != 7 {
		t.Fatalf("expected result from healthy worker, got %d", res.RowCount)
	}
}

func TestRemoteExecutor_NoWorkers(t *testing.T) {
	re := NewRemoteExecutor(nil, "s", fakeResolver{})
	res := re.Execute(context.Background(), job.ExecRequest{SQL: "SELECT 1", ProjectID: "p1"})
	if res.Error == "" {
		t.Fatal("expected an error when no workers are configured")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/worker/ -run 'TestRemoteExecutor' -v`
Expected: FAIL — `undefined: NewRemoteExecutor`.

- [ ] **Step 3: Implement `RemoteExecutor`**

Create `internal/worker/remote.go`:
```go
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/job"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// WireResolver resolves a SQL string to worker-bound bindings (with objects +
// storage class for cache-locality routing). The coordinator's catalog adapter
// implements this.
type WireResolver interface {
	ResolveWire(ctx context.Context, projectID, sql string) ([]WireBinding, error)
}

// RemoteExecutor dispatches resolved plans to a static pool of workers, choosing
// a worker by consistent-hashing the referenced object/table set (cache
// locality) and falling back to the least-loaded worker on failure.
type RemoteExecutor struct {
	endpoints []string
	secret    string
	resolver  WireResolver
	ring      *HashRing
	client    *http.Client

	mu     sync.Mutex
	inFlight map[string]int // endpoint -> in-flight count (for least-loaded fallback)
}

func NewRemoteExecutor(endpoints []string, secret string, resolver WireResolver) *RemoteExecutor {
	return &RemoteExecutor{
		endpoints: endpoints,
		secret:    secret,
		resolver:  resolver,
		ring:      NewHashRing(endpoints, 100),
		client:    &http.Client{Timeout: 5 * time.Minute},
		inFlight:  make(map[string]int),
	}
}

// routeKey builds the consistent-hash key from the referenced object/table set
// so a table's data repeatedly lands on the same worker (warm SSD cache).
func routeKey(bindings []WireBinding) string {
	var parts []string
	for _, b := range bindings {
		if len(b.Objects) > 0 {
			for _, o := range b.Objects {
				parts = append(parts, o.Bucket+"/"+o.Key)
			}
		} else {
			parts = append(parts, b.Schema+"."+b.Name)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// orderedTargets returns the worker endpoints to try, preferred first (ring
// owner), then the remaining workers ordered least-loaded first.
func (e *RemoteExecutor) orderedTargets(bindings []WireBinding) []string {
	if len(e.endpoints) == 0 {
		return nil
	}
	preferred := e.ring.Get(routeKey(bindings))

	e.mu.Lock()
	rest := make([]string, 0, len(e.endpoints))
	for _, ep := range e.endpoints {
		if ep != preferred {
			rest = append(rest, ep)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return e.inFlight[rest[i]] < e.inFlight[rest[j]] })
	e.mu.Unlock()

	if preferred == "" {
		return rest
	}
	return append([]string{preferred}, rest...)
}

func (e *RemoteExecutor) Execute(ctx context.Context, req job.ExecRequest) *query.Result {
	bindings, err := e.resolver.ResolveWire(ctx, req.ProjectID, req.SQL)
	if err != nil {
		return &query.Result{Error: "resolve tables: " + err.Error()}
	}
	targets := e.orderedTargets(bindings)
	if len(targets) == 0 {
		return &query.Result{Error: "no workers configured"}
	}

	payload, err := json.Marshal(ExecuteRequest{
		SQL:       req.SQL,
		Bindings:  bindings,
		AccessKey: req.AccessKey,
		SecretKey: req.SecretKey,
		Endpoint:  req.Endpoint,
	})
	if err != nil {
		return &query.Result{Error: "marshal request: " + err.Error()}
	}

	var lastErr error
	for _, target := range targets {
		res, err := e.post(ctx, target, payload)
		if err != nil {
			lastErr = err
			continue // try the next worker (fallback)
		}
		return res
	}
	return &query.Result{Error: fmt.Sprintf("all workers failed: %v", lastErr)}
}

func (e *RemoteExecutor) post(ctx context.Context, endpoint string, payload []byte) (*query.Result, error) {
	e.mu.Lock()
	e.inFlight[endpoint]++
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.inFlight[endpoint]--
		e.mu.Unlock()
	}()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(endpoint, "/")+"/internal/execute", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(SecretHeader, e.secret)

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("worker %s returned %d", endpoint, resp.StatusCode)
	}
	var res query.Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

var _ job.Executor = (*RemoteExecutor)(nil)
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/worker/ -run 'TestRemoteExecutor' -v`
Expected: PASS.

- [ ] **Step 5: Run the whole worker package with the race detector**

Run: `go test -race ./internal/worker/`
Expected: PASS (ok).

- [ ] **Step 6: Commit**

```bash
git add internal/worker/remote.go internal/worker/remote_test.go
git commit -m "feat(worker): RemoteExecutor with consistent-hash routing and least-loaded fallback"
```

---

## Task 12: API — async job submit (`?wait=`), list, cancel

**Files:**
- Modify: `internal/api/job_handler.go`
- Test: `internal/api/job_handler_test.go` (modify/append)

`SubmitWithCreds` honors `?wait=<dur>`: if the job finishes within the wait window it returns the completed job inline (200); otherwise it returns the job (still queued/running) with 202. Add `ListForProject` (history) and `Cancel`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/api/job_handler_test.go` (the Phase 1 `stubExecutor`, `withURLParam`, and `TestJobHandler_SubmitSyncAndGet` remain):
```go
import (
	// add alongside existing imports
	"context"
	"time"
)

// slowExecutor blocks for `delay` before returning, to exercise the 202 path.
type slowExecutor struct{ delay time.Duration }

func (s slowExecutor) Execute(ctx context.Context, req job.ExecRequest) *query.Result {
	select {
	case <-time.After(s.delay):
		return &query.Result{RowCount: 1}
	case <-ctx.Done():
		return &query.Result{Error: "cancelled"}
	}
}

func TestJobHandler_WaitFastPathReturnsInline(t *testing.T) {
	mgr := job.NewManager(slowExecutor{delay: 1 * time.Millisecond})
	h := NewJobHandler(mgr)

	req := httptest.NewRequest("POST", "/jobs?wait=2s", strings.NewReader(`{"sql":"SELECT 1"}`))
	w := httptest.NewRecorder()
	h.SubmitWithCreds(w, req, "p1", "ak", "sk", "http://localhost:9000")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 inline, got %d body=%s", w.Code, w.Body.String())
	}
	var j job.Job
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if j.Status != "done" {
		t.Fatalf("expected done within wait, got %q", j.Status)
	}
}

func TestJobHandler_WaitTimeoutReturns202(t *testing.T) {
	mgr := job.NewManager(slowExecutor{delay: 500 * time.Millisecond})
	h := NewJobHandler(mgr)

	req := httptest.NewRequest("POST", "/jobs?wait=10ms", strings.NewReader(`{"sql":"SELECT 1"}`))
	w := httptest.NewRecorder()
	h.SubmitWithCreds(w, req, "p1", "ak", "sk", "http://localhost:9000")
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 on wait timeout, got %d", w.Code)
	}
	var j job.Job
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if j.ID == "" {
		t.Fatal("expected a job id for polling")
	}
	if j.Status == "done" {
		t.Fatal("job should not be done yet")
	}
}

func TestJobHandler_ListAndCancel(t *testing.T) {
	mgr := job.NewManager(slowExecutor{delay: 200 * time.Millisecond})
	h := NewJobHandler(mgr)

	// Submit one async job.
	req := httptest.NewRequest("POST", "/jobs?wait=1ms", strings.NewReader(`{"sql":"SELECT 1"}`))
	w := httptest.NewRecorder()
	h.SubmitWithCreds(w, req, "p1", "ak", "sk", "http://localhost:9000")
	var submitted job.Job
	_ = json.Unmarshal(w.Body.Bytes(), &submitted)

	// List.
	lreq := httptest.NewRequest("GET", "/jobs", nil)
	lw := httptest.NewRecorder()
	h.ListForProject(lw, lreq, "p1")
	if lw.Code != http.StatusOK || !strings.Contains(lw.Body.String(), submitted.ID) {
		t.Fatalf("list missing job: %d %s", lw.Code, lw.Body.String())
	}

	// Cancel.
	creq := httptest.NewRequest("DELETE", "/jobs/"+submitted.ID, nil)
	creq = withURLParam(creq, "id", submitted.ID)
	cw := httptest.NewRecorder()
	h.Cancel(cw, creq)
	if cw.Code != http.StatusOK && cw.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d", cw.Code)
	}

	// Cancel unknown -> 404.
	creq2 := httptest.NewRequest("DELETE", "/jobs/nope", nil)
	creq2 = withURLParam(creq2, "id", "nope")
	cw2 := httptest.NewRecorder()
	h.Cancel(cw2, creq2)
	if cw2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 cancelling unknown job, got %d", cw2.Code)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run 'TestJobHandler_WaitFastPathReturnsInline|TestJobHandler_WaitTimeoutReturns202|TestJobHandler_ListAndCancel' -v`
Expected: FAIL — `h.ListForProject` / `h.Cancel` undefined; `SubmitWithCreds` does not honor `wait`.

- [ ] **Step 3: Rewrite `SubmitWithCreds` and add `ListForProject`/`Cancel`**

Replace the body of `internal/api/job_handler.go` with:
```go
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/esignoretti/ds3-sql-server/internal/job"
)

type JobHandler struct {
	mgr *job.Manager
}

func NewJobHandler(mgr *job.Manager) *JobHandler {
	return &JobHandler{mgr: mgr}
}

// SubmitWithCreds submits a job. With ?wait=<dur> it blocks up to that duration
// (default 2s) for a synchronous fast-path: if the job finishes in time it is
// returned inline (200); otherwise the job id is returned with 202 for polling.
func (h *JobHandler) SubmitWithCreds(w http.ResponseWriter, r *http.Request, projectID, accessKey, secretKey, endpoint string) {
	var req struct {
		SQL string `json:"sql"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.SQL == "" {
		http.Error(w, `{"error":"sql field is required"}`, http.StatusBadRequest)
		return
	}

	wait := 2 * time.Second
	if v := r.URL.Query().Get("wait"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			wait = d
		}
	}

	j := h.mgr.Submit(r.Context(), job.ExecRequest{
		SQL:       req.SQL,
		ProjectID: projectID,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Endpoint:  endpoint,
	})

	// Poll for completion up to the wait window.
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if cur, ok := h.mgr.Get(j.ID); ok && isTerminal(cur.Status) {
			j = cur
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	w.Header().Set("Content-Type", "application/json")
	if cur, ok := h.mgr.Get(j.ID); ok {
		j = cur
	}
	if !isTerminal(j.Status) {
		w.WriteHeader(http.StatusAccepted)
	} else if j.Status == "failed" {
		w.WriteHeader(http.StatusBadRequest)
	}
	json.NewEncoder(w).Encode(j)
}

func isTerminal(status string) bool {
	return status == "done" || status == "failed" || status == "cancelled"
}

func (h *JobHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	j, ok := h.mgr.Get(id)
	if !ok {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(j)
}

// ListForProject returns recent jobs for the project (query history).
func (h *JobHandler) ListForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	limit := 100
	jobs := h.mgr.List(projectID, limit)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"jobs": jobs})
}

// Cancel cancels a running/queued job.
func (h *JobHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !h.mgr.Cancel(id) {
		http.Error(w, `{"error":"job not found or already finished"}`, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
```

> Phase 3 replaces `SubmitWithCreds` again to add `ctas`/`load` write routing; its self-review notes this. The `?wait=` fast-path and the `Submit`/`Get`/`List`/`Cancel` shape established here are the contract Phase 3 builds on.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run 'TestJobHandler' -v`
Expected: PASS (the Phase 1 `TestJobHandler_SubmitSyncAndGet` still passes: with the default 2s wait the stub job completes inline → 200).

- [ ] **Step 5: Run the whole api package**

Run: `go test ./internal/api/`
Expected: PASS (ok).

- [ ] **Step 6: Commit**

```bash
git add internal/api/job_handler.go internal/api/job_handler_test.go
git commit -m "feat(api): async job submit with wait fast-path, list history, cancel"
```

---

## Task 13: Config — cluster, cache, and quota settings

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go` (create the file if absent, package `config`):
```go
func TestDefault_Phase2Sections(t *testing.T) {
	c := Default()
	if c.Query.MaxConcurrentPerProject <= 0 {
		t.Fatal("expected a positive default max_concurrent_per_project")
	}
	if c.Cache.ResultTTL <= 0 {
		t.Fatal("expected a default result cache TTL")
	}
	if c.Cache.ResultDir == "" || c.Cache.DataDir == "" {
		t.Fatal("expected default cache dirs")
	}
}

func TestLoad_ClusterEnvOverrides(t *testing.T) {
	t.Setenv("DS3SQL_CLUSTER_WORKERS", "http://w1:8080,http://w2:8080")
	t.Setenv("DS3SQL_CLUSTER_SHARED_SECRET", "sekret")
	t.Setenv("DS3SQL_MAX_CONCURRENT_PER_PROJECT", "5")
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Cluster.Workers) != 2 || c.Cluster.Workers[0] != "http://w1:8080" {
		t.Fatalf("workers env override not applied: %+v", c.Cluster.Workers)
	}
	if c.Cluster.SharedSecret != "sekret" {
		t.Fatalf("shared secret env override not applied: %q", c.Cluster.SharedSecret)
	}
	if c.Query.MaxConcurrentPerProject != 5 {
		t.Fatalf("max_concurrent env override not applied: %d", c.Query.MaxConcurrentPerProject)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/config/ -run 'TestDefault_Phase2Sections|TestLoad_ClusterEnvOverrides' -v`
Expected: FAIL — `c.Cluster` / `c.Cache` / `Query.MaxConcurrentPerProject` undefined.

- [ ] **Step 3: Implement the config additions**

In `internal/config/config.go`, add the field to `QueryConfig` (after `MemoryLimit`):
```go
	MaxConcurrentPerProject int `yaml:"max_concurrent_per_project"`
```
Add the new types (after `MetastoreConfig`):
```go
// ClusterConfig configures the static worker pool and coordinator↔worker auth.
type ClusterConfig struct {
	Workers      []string `yaml:"workers"`       // worker base URLs, e.g. http://w1:8080
	SharedSecret string   `yaml:"shared_secret"` // guards /internal/execute
}

// CacheConfig configures the result cache and the worker local-SSD data cache.
type CacheConfig struct {
	ResultDir      string        `yaml:"result_dir"`       // payload dir (or SSD-bucket mount)
	ResultTTL      time.Duration `yaml:"result_ttl"`
	ResultMaxBytes int64         `yaml:"result_max_bytes"`
	DataDir        string        `yaml:"data_dir"`         // worker SSD cache dir
	DataMaxBytes   int64         `yaml:"data_max_bytes"`
}
```
Add fields to `Config` (after `Metastore`):
```go
	Cluster ClusterConfig `yaml:"cluster"`
	Cache   CacheConfig   `yaml:"cache"`
```
In `Default()`, set the new defaults. Add to `QueryConfig` literal:
```go
			MaxConcurrentPerProject: 4,
```
Add after the `Metastore:` line in the returned `&Config{...}`:
```go
		Cluster: ClusterConfig{
			Workers:      nil,
			SharedSecret: "",
		},
		Cache: CacheConfig{
			ResultDir:      defaultCacheDir("results"),
			ResultTTL:      1 * time.Hour,
			ResultMaxBytes: 10 << 30, // 10 GiB
			DataDir:        defaultCacheDir("data"),
			DataMaxBytes:   50 << 30, // 50 GiB
		},
```
Add the helper:
```go
func defaultCacheDir(sub string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("cache", sub)
	}
	return filepath.Join(home, ".ds3sql", "cache", sub)
}
```
Add `"path/filepath"` and `"strings"` to the imports.
In `Load`, after the metastore env override, add:
```go
	if v := os.Getenv("DS3SQL_CLUSTER_WORKERS"); v != "" {
		parts := strings.Split(v, ",")
		workers := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				workers = append(workers, p)
			}
		}
		cfg.Cluster.Workers = workers
	}
	if v := os.Getenv("DS3SQL_CLUSTER_SHARED_SECRET"); v != "" {
		cfg.Cluster.SharedSecret = v
	}
	if v := os.Getenv("DS3SQL_MAX_CONCURRENT_PER_PROJECT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Query.MaxConcurrentPerProject = n
		}
	}
	if v := os.Getenv("DS3SQL_CACHE_RESULT_DIR"); v != "" {
		cfg.Cache.ResultDir = v
	}
	if v := os.Getenv("DS3SQL_CACHE_DATA_DIR"); v != "" {
		cfg.Cache.DataDir = v
	}
```
(`strconv` is already imported.)

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): add cluster, cache, and per-project concurrency settings"
```

---

## Task 14: Server wiring — role-based executor, result cache, admission, worker server, new routes

**Files:**
- Modify: `cmd/ds3sql-server/main.go`

This task wires everything by role. `all` keeps `LocalExecutor`; `coordinator` uses `RemoteExecutor`; `worker` runs the worker server. The result cache + admission control front the executor on coordinator/all. New job routes: `GET /jobs`, `DELETE /jobs/{id}`.

- [ ] **Step 1: Build a catalog→wire-binding adapter for `RemoteExecutor` and the version source for the cache**

In `cmd/ds3sql-server/main.go`, after `catService := catalog.NewService(metaStore, queryEngine)`, add a small adapter type at file scope (above `main`, or in a new helper block). Add this function to the file:
```go
// wireResolver adapts catalog.Service.Resolve to worker.WireResolver by mapping
// query.ViewBinding to worker.WireBinding. Phase 2 carries no per-object lists
// from the catalog yet (partition pruning is Phase 4); StorageClass/Objects are
// best-effort from the table registration, so routing uses schema.name keys.
type wireResolver struct{ cat *catalog.Service }

func (w wireResolver) ResolveWire(ctx context.Context, projectID, sql string) ([]worker.WireBinding, error) {
	bindings, err := w.cat.Resolve(ctx, projectID, sql)
	if err != nil {
		return nil, err
	}
	out := make([]worker.WireBinding, len(bindings))
	for i, b := range bindings {
		sc := "hdd"
		if t, err := w.cat.GetTable(ctx, projectID, b.Schema, b.Name); err == nil {
			if t.StorageClass != "" {
				sc = t.StorageClass
			}
		}
		out[i] = worker.WireBinding{
			Schema:    b.Schema,
			Name:      b.Name,
			ReaderSQL: b.ReaderSQL,
			StorageClass: sc,
		}
	}
	return out, nil
}
```
Add imports to `main.go`:
```go
	"path/filepath"
	"github.com/esignoretti/ds3-sql-server/internal/cache"
	"github.com/esignoretti/ds3-sql-server/internal/worker"
```

- [ ] **Step 2: Choose the executor by role and front it with the result cache + admission**

Replace the Phase 1 wiring block:
```go
	catService := catalog.NewService(metaStore, queryEngine)
	localExecutor := job.NewLocalExecutor(catService, queryEngine)
	jobManager := job.NewManager(localExecutor)
```
with:
```go
	catService := catalog.NewService(metaStore, queryEngine)

	// Select the base executor by role.
	var baseExecutor job.Executor
	switch cfg.Role {
	case "coordinator":
		if len(cfg.Cluster.Workers) == 0 {
			log.Fatalf("role=coordinator requires cluster.workers to be configured")
		}
		baseExecutor = worker.NewRemoteExecutor(cfg.Cluster.Workers, cfg.Cluster.SharedSecret, wireResolver{cat: catService})
	default: // "all" and "worker" both run queries in-process for their own API
		baseExecutor = job.NewLocalExecutor(catService, queryEngine)
	}

	// Result cache fronts the executor (coordinator + all).
	resultCache := cache.NewResultCache(
		metaStore,
		cache.NewDirBlobstore(cfg.Cache.ResultDir),
		cache.ResultCacheOpts{TTL: cfg.Cache.ResultTTL, MaxBytes: cfg.Cache.ResultMaxBytes},
	)
	versionSource := func(ctx context.Context, projectID, sql string) (map[string]int64, error) {
		bindings, err := catService.Resolve(ctx, projectID, sql)
		if err != nil {
			return nil, err
		}
		versions := make(map[string]int64, len(bindings))
		for _, b := range bindings {
			t, err := catService.GetTable(ctx, projectID, b.Schema, b.Name)
			if err != nil {
				return nil, err
			}
			versions[projectID+"/"+b.Schema+"/"+b.Name] = t.DataVersion
		}
		return versions, nil
	}
	rawExec := func(ctx context.Context, projectID, sql, ak, sk, ep string, _ map[string]int64) *query.Result {
		return baseExecutor.Execute(ctx, job.ExecRequest{
			SQL: sql, ProjectID: projectID, AccessKey: ak, SecretKey: sk, Endpoint: ep,
		})
	}
	caching := cache.NewCachingExecutor(resultCache, rawExec, versionSource)

	// cachingAdapter makes the result cache satisfy job.Executor so the manager
	// front-runs the cache before dispatching to the base executor.
	cachingExecutor := job.ExecutorFunc(func(ctx context.Context, req job.ExecRequest) *query.Result {
		return caching.Run(ctx, req.ProjectID, req.SQL, req.AccessKey, req.SecretKey, req.Endpoint)
	})

	jobManager := job.NewManager(cachingExecutor)
	jobManager.SetSink(job.NewMetastoreSink(metaStore))
	jobManager.SetQuota(cfg.Query.MaxConcurrentPerProject)
```

- [ ] **Step 3: Add the `ExecutorFunc` adapter to the job package**

This small adapter (analogous to `http.HandlerFunc`) lets a closure satisfy `job.Executor`. Append to `internal/job/job.go`:
```go
// ExecutorFunc adapts a function to the Executor interface.
type ExecutorFunc func(ctx context.Context, req ExecRequest) *query.Result

func (f ExecutorFunc) Execute(ctx context.Context, req ExecRequest) *query.Result { return f(ctx, req) }

var _ Executor = (ExecutorFunc)(nil)
```
Commit this with the package; it does not need a dedicated test (covered by the wiring + Task 4 tests), but add a one-line assertion test to `job_async_test.go` to keep coverage explicit:
```go
func TestExecutorFunc_Satisfies(t *testing.T) {
	var e Executor = ExecutorFunc(func(ctx context.Context, req ExecRequest) *query.Result {
		return &query.Result{RowCount: 1}
	})
	if e.Execute(context.Background(), ExecRequest{}).RowCount != 1 {
		t.Fatal("ExecutorFunc did not invoke the closure")
	}
}
```

- [ ] **Step 4: Run the worker server when `--role=worker`**

After the handlers are constructed and before the protected route groups, add:
```go
	// Worker data-plane server (role=worker): exposes /internal/execute guarded
	// by the shared secret, fronted by a local-SSD data cache.
	if cfg.Role == "worker" {
		var dataCache *cache.DataCache
		if cfg.Cache.DataDir != "" && cfg.Cache.DataMaxBytes > 0 {
			// Phase 2 note: the worker's ObjectStore is supplied per-request via
			// the binding creds. A production S3-backed ObjectStore is wired here
			// in Phase 4 alongside partition pruning; for now the data cache is
			// constructed only when an object store is available, so we leave it
			// nil and the worker reads s3:// readers directly via DuckDB httpfs.
			dataCache = nil
		}
		workerSrv := worker.NewServer(queryEngine, cfg.Cluster.SharedSecret, dataCache)
		r.Group(func(r chi.Router) {
			r.Post("/internal/execute", workerSrv.Execute)
		})
	}
```

> Phase 2 simplification (documented): the worker SSD data cache requires an `ObjectStore` bound to live S3 credentials. Because credentials arrive per-request and Phase 2 does not yet thread an S3 `ObjectStore` into the worker, the data cache is constructed but left disabled here; DuckDB's httpfs reads `s3://` readers directly. The cache logic is fully unit-tested (Task 8) and the wiring seam (`worker.NewServer(..., dataCache)`) is in place. Phase 4 supplies the S3-backed `ObjectStore`.

- [ ] **Step 5: Add the new job routes**

In the first protected group (with timeout), the `POST /jobs` route already exists from Phase 1. Add `GET /jobs` next to it:
```go
		r.Get("/jobs", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			for _, p := range session.Projects {
				jobHandler.ListForProject(w, r, p.ProjectID)
				return
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
```
In the second protected group (no timeout), `GET /jobs/{id}` already exists from Phase 1. Add the cancel route:
```go
		r.Delete("/jobs/{id}", jobHandler.Cancel)
```

- [ ] **Step 6: Add the metastore dir creation (defensive) before opening it**

The Phase 1 code opens `metastore.OpenSQLite(cfg.Metastore.Path)` directly. Ensure the parent dir exists by adding, just before that call:
```go
	if dir := filepath.Dir(cfg.Metastore.Path); dir != "" {
		os.MkdirAll(dir, 0755)
	}
```

- [ ] **Step 7: Build the server and run the job package**

Run:
```bash
go build ./cmd/ds3sql-server/
go test ./internal/job/ -run TestExecutorFunc_Satisfies -v
```
Expected: builds with no error; the adapter test passes.

- [ ] **Step 8: Build everything and run the full suite**

Run:
```bash
go build ./...
go test ./...
```
Expected: all packages build; all tests PASS.

- [ ] **Step 9: Commit**

```bash
git add cmd/ds3sql-server/main.go internal/job/job.go internal/job/job_async_test.go
git commit -m "feat(server): role-based executor wiring, result cache, admission, worker server, job routes"
```

---

## Task 15: CLI — `jobs` command and `query --async`

**Files:**
- Create: `cmd/ds3sql/jobs_cmd.go`
- Modify: `cmd/ds3sql/query.go`, `cmd/ds3sql/status.go`

- [ ] **Step 1: Ensure `authedDelete` exists**

In `cmd/ds3sql/status.go`, if `authedDelete` is not already present, add it after `authedPost`:
```go
func authedDelete(cfg *CLIConfig, path string) ([]byte, int, error) {
	req, _ := http.NewRequest("DELETE", serverURL(cfg)+path, nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode, nil
}
```
> If a Phase 1/3 `authedDelete` with a different signature already exists, keep that one and adapt the calls in Step 2; do not define a duplicate.

- [ ] **Step 2: Implement the `jobs` command**

Create `cmd/ds3sql/jobs_cmd.go`:
```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	jobsCmd.AddCommand(jobsListCmd)
	jobsCmd.AddCommand(jobsGetCmd)
	jobsCmd.AddCommand(jobsCancelCmd)
	jobsCmd.AddCommand(jobsWaitCmd)
	jobsCmd.PersistentFlags().StringP("project", "p", "", "Project ID (defaults to first project)")
	rootCmd.AddCommand(jobsCmd)
}

var jobsCmd = &cobra.Command{
	Use:   "jobs",
	Short: "Manage and inspect query jobs",
}

type jobEnvelope struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	SQL       string `json:"sql"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	CreatedAt string `json:"created_at"`
	Result    *struct {
		RowCount  int   `json:"row_count"`
		ElapsedMs int64 `json:"elapsed_ms"`
	} `json:"result"`
}

func jobsProjectQuery(cmd *cobra.Command) string {
	if p, _ := cmd.Flags().GetString("project"); p != "" {
		return "?project=" + p
	}
	return ""
}

var jobsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent jobs (query history)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		data, err := authedGet(cfg, "/jobs"+jobsProjectQuery(cmd))
		if err != nil {
			return err
		}
		var out struct {
			Jobs  []jobEnvelope `json:"jobs"`
			Error string        `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tTYPE\tSTATUS\tCREATED")
		for _, j := range out.Jobs {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", j.ID, j.Type, j.Status, j.CreatedAt)
		}
		w.Flush()
		return nil
	},
}

var jobsGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show a job's status and result summary",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		data, err := authedGet(cfg, "/jobs/"+args[0])
		if err != nil {
			return err
		}
		var j jobEnvelope
		if err := json.Unmarshal(data, &j); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if j.ID == "" {
			return fmt.Errorf("job not found")
		}
		fmt.Printf("ID:      %s\nType:    %s\nStatus:  %s\n", j.ID, j.Type, j.Status)
		if j.Error != "" {
			fmt.Printf("Error:   %s\n", j.Error)
		}
		if j.Result != nil {
			fmt.Printf("Rows:    %d\nElapsed: %dms\n", j.Result.RowCount, j.Result.ElapsedMs)
		}
		return nil
	},
}

var jobsCancelCmd = &cobra.Command{
	Use:   "cancel <id>",
	Short: "Cancel a running or queued job",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		_, code, err := authedDelete(cfg, "/jobs/"+args[0])
		if err != nil {
			return err
		}
		if code == 404 {
			return fmt.Errorf("job not found or already finished")
		}
		fmt.Printf("job %s cancelled\n", args[0])
		return nil
	},
}

var jobsWaitCmd = &cobra.Command{
	Use:   "wait <id>",
	Short: "Poll a job until it reaches a terminal state",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		for {
			data, err := authedGet(cfg, "/jobs/"+args[0])
			if err != nil {
				return err
			}
			var j jobEnvelope
			if err := json.Unmarshal(data, &j); err != nil {
				return fmt.Errorf("parse response: %w", err)
			}
			if j.ID == "" {
				return fmt.Errorf("job not found")
			}
			switch j.Status {
			case "done":
				fmt.Printf("job %s done (%d rows)\n", j.ID, resultRows(j))
				return nil
			case "failed":
				return fmt.Errorf("job %s failed: %s", j.ID, j.Error)
			case "cancelled":
				return fmt.Errorf("job %s cancelled", j.ID)
			}
			time.Sleep(500 * time.Millisecond)
		}
	},
}

func resultRows(j jobEnvelope) int {
	if j.Result != nil {
		return j.Result.RowCount
	}
	return 0
}
```

- [ ] **Step 3: Add `--async` to the query command**

In `cmd/ds3sql/query.go`, add the flag in `init()`:
```go
	queryCmd.Flags().Bool("async", false, "Submit without waiting; print the job ID")
```
Then, where the query command builds its request path (Phase 1 routes through `/jobs`), make the wait window depend on `--async`. Replace the path/post block with one that sets `?wait=0s` when async and unwraps the job envelope. Concretely, change the path construction to:
```go
		async, _ := cmd.Flags().GetBool("async")
		path := "/jobs"
		sep := "?"
		if p, _ := cmd.Flags().GetString("project"); p != "" {
			path += sep + "project=" + p
			sep = "&"
		}
		if async {
			path += sep + "wait=0s"
		}
		data, err := authedPost(cfg, path, body)
		if err != nil {
			return err
		}
		if async {
			var j struct {
				ID    string `json:"id"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(data, &j); err != nil {
				return fmt.Errorf("parse response: %w", err)
			}
			if j.Error != "" {
				return fmt.Errorf("%s", j.Error)
			}
			fmt.Printf("job submitted: %s\n", j.ID)
			return nil
		}
```
> The existing synchronous rendering path below (which unwraps the `result` envelope from `/jobs`) is unchanged. If the Phase 1 `query.go` still points at `/query`, update it to `/jobs` and unwrap the job envelope as documented in the Phase 1 plan (Task 17); the `--async` branch above sits in front of that rendering.

- [ ] **Step 4: Build the CLI**

Run: `go build ./cmd/ds3sql/`
Expected: builds with no error.

- [ ] **Step 5: Verify the commands register**

Run:
```bash
go run ./cmd/ds3sql/ jobs --help
go run ./cmd/ds3sql/ query --help
```
Expected: `jobs` lists `list`/`get`/`cancel`/`wait`; `query` shows `--async`.

- [ ] **Step 6: Commit**

```bash
git add cmd/ds3sql/jobs_cmd.go cmd/ds3sql/query.go cmd/ds3sql/status.go
git commit -m "feat(cli): jobs list/get/cancel/wait and query --async"
```

---

## Task 16: Full verification + docs + final commit

**Files:**
- Modify: `docs/api.md`, `docs/cli.md`, `docs/architecture.md`, `docs/configuration.md`, `README.md`

- [ ] **Step 1: Full build, vet, and race-test**

Run:
```bash
go build ./...
go vet ./...
go test -race ./...
```
Expected: no build errors, no vet errors, all tests PASS.

- [ ] **Step 2: Manual smoke test — coordinator + worker locally**

This boots a worker and a coordinator pointed at it and checks `/health` on both. The credentialed execute path requires a live Cubbit IAM login + DS3 buckets, so the resolved-plan round-trip is covered by the automated `internal/worker` tests; this step verifies role wiring boots.
```bash
cd "/Users/esignoretti/Documents/OpenCode/DS3-SQL Server"
go build -o /tmp/ds3sql-server ./cmd/ds3sql-server/

# Worker
DS3SQL_ROLE=worker DS3SQL_CLUSTER_SHARED_SECRET=s \
  DS3SQL_METASTORE_PATH=/tmp/ds3-w-meta.db /tmp/ds3sql-server --port 18091 &
W=$!
# Coordinator pointed at the worker
DS3SQL_ROLE=coordinator DS3SQL_CLUSTER_WORKERS=http://localhost:18091 \
  DS3SQL_CLUSTER_SHARED_SECRET=s \
  DS3SQL_METASTORE_PATH=/tmp/ds3-c-meta.db /tmp/ds3sql-server --port 18090 &
C=$!
sleep 1
curl -s http://localhost:18090/health
curl -s http://localhost:18091/health
kill $W $C
rm -f /tmp/ds3-w-meta.db /tmp/ds3-c-meta.db
```
Expected: both `/health` return `{"status":"ok",...}`; processes stop on kill.

- [ ] **Step 3: Update `docs/api.md`**

Document the async job surface:
- `POST /jobs?wait=<dur>` — submits a job. Within `wait` (default `2s`) a finished job is returned inline (200); otherwise the job (queued/running) is returned with 202 for polling.
- `GET /jobs` — recent jobs for the project: `{"jobs":[{id,project_id,type,sql,status,created_at,...}]}` (query history).
- `GET /jobs/{id}` — the job envelope (status, result, error).
- `DELETE /jobs/{id}` — cancels a running/queued job (202; 404 if unknown/finished).
- Note the result cache: repeated identical queries against unchanged tables return cached results; any write that bumps a referenced table's `data_version` invalidates them.
- Worker internal endpoint (not client-facing): `POST /internal/execute` guarded by `X-DS3SQL-Worker-Secret`.

- [ ] **Step 4: Update `docs/cli.md`**

Document:
- `ds3sql jobs list` / `jobs get <id>` / `jobs cancel <id>` / `jobs wait <id>`.
- `ds3sql query --async "SELECT …"` returns a job ID immediately (uses `?wait=0s`).

- [ ] **Step 5: Update `docs/architecture.md`**

Add a "Scale-out & Caching (Phase 2)" subsection covering: the coordinator/worker split via `--role`; `worker.RemoteExecutor` consistent-hash routing (cache locality) with least-loaded fallback over a **static** worker list; the `worker` HTTP data-plane (`/internal/execute`, shared-secret); the result cache (metastore `CacheEntry` index + blob payloads, version-keyed invalidation, TTL + LRU); the worker local-SSD data cache (HDD-only, LRU, SSD bypass; ObjectStore wired in Phase 4); async jobs (`Submit`, cancel via `context.CancelFunc`, persisted history via `MetastoreSink`); per-project concurrency quota + fair FIFO queue. Note the documented simplifications (static workers, in-process load counter, HTTP transport, JSON result payloads, whole-object caching).

- [ ] **Step 6: Update `docs/configuration.md` and `README.md`**

In `docs/configuration.md`, document the new config + env vars:
- `query.max_concurrent_per_project` (`DS3SQL_MAX_CONCURRENT_PER_PROJECT`), default 4.
- `cluster.workers` (`DS3SQL_CLUSTER_WORKERS`, comma-separated) and `cluster.shared_secret` (`DS3SQL_CLUSTER_SHARED_SECRET`).
- `cache.result_dir`/`result_ttl`/`result_max_bytes` (`DS3SQL_CACHE_RESULT_DIR`) and `cache.data_dir`/`data_max_bytes` (`DS3SQL_CACHE_DATA_DIR`).
- Roles: `coordinator` requires `cluster.workers`; `worker` serves `/internal/execute`; `all` runs everything in-process.

In `README.md`, add a "Scale-out" subsection to Quick Start:
```bash
# Worker
DS3SQL_ROLE=worker DS3SQL_CLUSTER_SHARED_SECRET=s ds3sql-server --port 8091
# Coordinator
DS3SQL_ROLE=coordinator DS3SQL_CLUSTER_WORKERS=http://localhost:8091 \
  DS3SQL_CLUSTER_SHARED_SECRET=s ds3sql-server --port 8090
# Async query
ds3sql query --async "SELECT count(*) FROM sales.orders"
ds3sql jobs wait <id>
```

- [ ] **Step 7: Final build/test and commit**

Run:
```bash
go build ./...
go test ./...
```
Expected: all PASS.

```bash
git add docs/ README.md
git commit -m "docs: document scale-out, caching, async jobs, and quotas for Phase 2"
```

---

## Self-Review

**Spec coverage (Phase 2 scope):**
- Worker pool + coordinator routing; consistent-hash cache locality → `internal/worker` `HashRing` (Task 9), `RemoteExecutor` (Task 11), `Server` (Task 10), role wiring (Task 14). ✓
- Result cache (index in metastore, payloads on SSD/dir, version-keyed auto-invalidation, TTL + LRU) → `metastore.CacheEntry` (Task 2) + `cache.ResultCache`/`CachingExecutor` (Tasks 6–7), fronted in `main.go` (Task 14). ✓
- Local-SSD data cache (read-through, LRU, SSD bypass, reader rewrite) → `cache.DataCache` (Task 8), wired into `worker.Server` (Task 10); ObjectStore deferred to Phase 4 (documented in Task 14 Step 4). ✓
- Metadata cache → the catalog `Resolve`/`GetTable` already serve schema/version from the metastore (SQLite, effectively the metadata store); an in-memory TTL layer is a Phase 4 refinement (spec lists it under Phase 4 "partition-pruning refinements"); the cache-locality + result-cache 80/20 is delivered here. Noted as a deliberate scoping. ✓
- Async jobs + polling → `job.Submit`/`Get` (Task 4), API `?wait=` fast-path + `GET /jobs/{id}` (Task 12). ✓
- Concurrency quotas/queue (fair across tenants) → `job.admission` (Task 4) + tests proving the limit-2 queueing and cross-project fairness (Task 5). ✓
- Query history → `metastore.JobRecord` CRUD (Task 1) + `MetastoreSink` (Task 3) + `GET /jobs` list (Tasks 12, 14). ✓
- Cancellation → `job.Cancel` via per-job `context.CancelFunc` (Task 4) + `DELETE /jobs/{id}` (Tasks 12, 14). ✓
- Coordinator↔worker transport decision (Open Question) → resolved to plain HTTP + shared-secret header; recorded in the plan header and architecture docs. ✓
- Credential propagation to workers → carried in `ExecuteRequest` (creds fields), guarded by the shared secret; not logged. ✓

**Type-consistency check (against the canonical Phase 2 contract and sibling phases):**
- `metastore.Store` gains exactly the 9 methods listed (`CreateJob/UpdateJob/GetJob/ListJobs`, `PutCacheEntry/LookupCacheEntry/DeleteCacheEntry/ListCacheEntries/DeleteCacheEntriesForTable`) with the stated signatures (Tasks 1–2); `SQLiteStore` implements them; `var _ Store = (*SQLiteStore)(nil)` from Phase 1 still compiles. Phase 4's `PostgresStore` stub list (lines cited in its plan) matches these names/signatures. ✓
- `metastore.JobRecord{ID,ProjectID,Type,SQL,Status,Error,RowCount,BytesScanned,ResultLocation,CreatedAt,StartedAt,FinishedAt}` and `metastore.CacheEntry{Key,ProjectID,SQLNorm,TableVersions,Location,SizeBytes,CreatedAt,LastAccessAt}` match the contract and Phase 3/4 references exactly. ✓
- `TableVersions` is a JSON string mapping `projectID/dataset/table` → `data_version`; `CacheKey` (Task 6) builds the same FQN keys and `DeleteCacheEntriesForTable` (Task 2) matches on the same FQN substring — consistent end to end. ✓
- `job.Manager.Submit(ctx, req) *Job` exists with the queued→running→done/failed/cancelled lifecycle assumed by Phase 3 (which adds a `ctas`/`load` type switch in the same goroutine); `ExecRequest.Type` and the `cancelled` status are added here so Phase 3 compiles against them. ✓
- `cache.ResultCache` with `DeleteCacheEntriesForTable`-based invalidation is the exact mechanism Phase 3's `Writer.afterWrite` relies on (Phase 3 calls `store.BumpDataVersion` + the metastore's `DeleteCacheEntriesForTable`); both surfaces exist after this phase. ✓
- `job.Executor` seam unchanged in shape: `LocalExecutor` (Phase 1), `RemoteExecutor` (Task 11), and `ExecutorFunc` (Task 14) all satisfy `Execute(ctx, ExecRequest) *query.Result`. ✓
- API handler conventions (`SubmitWithCreds`/`ListForProject`/`Get`/`Cancel`) match the Phase 1 `…WithCreds`/`…ForProject` style and the `main.go` session-extraction pattern; Phase 3 re-extends `SubmitWithCreds` on top of the `?wait=` contract established here. ✓
- `worker.WireBinding` carries `Objects []cache.ObjectRef` + `StorageClass`, consumed by both `Server.resolveBindings` (Task 10) and `RemoteExecutor.routeKey` (Task 11); `query.ViewBinding{Schema,Name,ReaderSQL}` is produced from it unchanged. ✓
- Config additions (`Cluster`, `Cache`, `Query.MaxConcurrentPerProject`) are referenced only by `main.go` (Task 14) with matching field names. ✓

**Placeholder scan:** No `TBD`/`TODO`/"add error handling"/"similar to Task N" placeholders; every code step contains complete Go. The two explicitly-flagged seams are (a) the worker `ObjectStore` left `nil` in Task 14 Step 4 — full code is present, the cache is unit-tested in Task 8, and the deferral to Phase 4 (which supplies the S3-backed `ObjectStore`) is documented per the spec's Phase 4 scope; and (b) the `authedDelete` signature reconciliation in Task 15 Step 1, which provides complete code plus a note to reuse any pre-existing helper rather than duplicate it. Both are finalized against the actual tree at implementation time, not left blank.

**Note on cross-phase ordering:** Task 3 (`MetastoreSink`) references `Job.ProjectID`, added in Task 4 Step 3; the plan flags this and instructs adding the struct field first if the compiler complains. This is the only intra-phase forward reference and is explicitly handled.
