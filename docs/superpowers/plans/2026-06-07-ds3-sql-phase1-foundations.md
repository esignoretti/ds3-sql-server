# DS3 SQL Phase 1 (Foundations) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a managed catalog (datasets → external tables with stored schema/stats), a synchronous job API, and a `--role` flag so that `SELECT … FROM dataset.table` works end-to-end on a single node.

**Architecture:** Incremental in-place refactor (Approach A) of the existing Go binary. New packages — `metastore` (pluggable Store interface + embedded SQLite impl), `catalog` (dataset/table service: validation, schema inference, table→reader resolution), `job` (in-memory job manager + Executor seam + LocalExecutor) — sit alongside the existing `query`, `auth`, `s3`, `api` packages. The `query` engine gains a catalog-aware `QueryView` method that registers referenced tables as DuckDB views (the coordinator→worker "plan" seam). The `Executor` interface is the seam where Phase 2 will later swap in a remote worker.

**Tech Stack:** Go 1.26, DuckDB (`github.com/marcboeker/go-duckdb`, CGo), embedded SQLite (`modernc.org/sqlite`, pure Go), chi router, Cobra CLI. Module path: `github.com/esignoretti/ds3-sql-server`.

**Spec:** `docs/superpowers/specs/2026-06-07-ds3-sql-bigquery-refactor-design.md`

---

## File Structure

New packages and files (all under the repo root):

- `internal/metastore/store.go` — domain types (`Dataset`, `Table`, `Column`, `Stats`) + `Store` interface.
- `internal/metastore/sqlite.go` — `SQLiteStore` implementing `Store` (datasets + tables tables, JSON-encoded schema/stats/partitions).
- `internal/metastore/sqlite_test.go` — store CRUD tests against a temp-file DB.
- `internal/catalog/service.go` — `Service`: identifier validation, dataset/table CRUD wrapping the store, schema/stats inference via the query engine, `Resolve` (SQL → `[]query.ViewBinding`).
- `internal/catalog/service_test.go` — service tests using local CSV/Parquet files (no S3 needed).
- `internal/job/job.go` — `Job`, `Manager` (in-memory, synchronous), `Executor` interface, `ExecRequest`.
- `internal/job/local_executor.go` — `LocalExecutor` wiring `catalog.Service` + `query.Engine`.
- `internal/job/job_test.go` — manager test with a fake executor.
- `internal/job/local_executor_test.go` — end-to-end test (register table → query `dataset.table`) over local files.
- `internal/api/dataset_handler.go` — `DatasetHandler` (create/list).
- `internal/api/table_handler.go` — `TableHandler` (register/list/describe/drop).
- `internal/api/job_handler.go` — `JobHandler` (submit/get).
- `internal/api/dataset_handler_test.go`, `internal/api/table_handler_test.go`, `internal/api/job_handler_test.go` — httptest-based handler tests.

Modified files:

- `internal/query/engine.go` — extract `applyS3Creds` helper; add `ViewBinding` type + `QueryView` method.
- `internal/config/config.go` — add `Role` and `Metastore` config + env overrides.
- `cmd/ds3sql-server/main.go` — `--role` flag, init metastore/catalog/job, mount new routes.
- `cmd/ds3sql/datasets_cmd.go` (new), `cmd/ds3sql/tables_cmd.go` (new) — CLI commands.
- `cmd/ds3sql/query.go` — route `query` through `/jobs`.
- `go.mod` / `go.sum` — add `modernc.org/sqlite`.
- `docs/api.md`, `docs/cli.md`, `docs/architecture.md`, `docs/configuration.md`, `README.md` — document new surface.

**Conventions to follow (from existing code):**
- Handlers that need S3 credentials expose a `…WithCreds(w, r, accessKey, secretKey, endpoint)` method; `main.go` extracts creds from the session and calls it (mirrors `QueryHandler.QueryWithCreds`). This keeps handlers unit-testable without a fake auth context.
- Stores live behind small interfaces; SQLite/disk implementations mirror `report.DiskStore`.
- In-memory job tracking mirrors `convert.JobStore` (mutex + map).
- Errors returned to clients are JSON: `{"error":"…"}`.

---

## Task 1: Add SQLite dependency and metastore types + Store interface

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/metastore/store.go`
- Create: `internal/metastore/sqlite.go`
- Test: `internal/metastore/sqlite_test.go`

- [ ] **Step 1: Add the pure-Go SQLite driver**

Run:
```bash
cd "/Users/esignoretti/Documents/OpenCode/DS3-SQL Server"
go get modernc.org/sqlite@latest
```
Expected: `go.mod` gains a `modernc.org/sqlite` require line; `go.sum` updated.

- [ ] **Step 2: Write domain types and the Store interface**

Create `internal/metastore/store.go`:
```go
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
```

- [ ] **Step 3: Write the failing store test (compile target)**

Create `internal/metastore/sqlite_test.go`:
```go
package metastore

import (
	"context"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "meta.db")
	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenSQLite_CreatesSchema(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.ListDatasets(context.Background(), "proj-1"); err != nil {
		t.Fatalf("ListDatasets on empty store: %v", err)
	}
}
```

- [ ] **Step 4: Run the test to verify it fails to compile**

Run: `go test ./internal/metastore/ -run TestOpenSQLite_CreatesSchema -v`
Expected: FAIL — `undefined: OpenSQLite` / `SQLiteStore`.

- [ ] **Step 5: Implement the SQLite store skeleton with schema creation**

Create `internal/metastore/sqlite.go`:
```go
package metastore

import (
	"database/sql"
	"fmt"

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
```

Add stub methods so the package compiles (real bodies land in Tasks 2–3):
```go
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
```
Add the needed imports to the top of `sqlite.go`: `"context"` and `"time"`.

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./internal/metastore/ -run TestOpenSQLite_CreatesSchema -v`
Expected: PASS.

- [ ] **Step 7: Verify the interface is satisfied**

Add to the bottom of `sqlite.go`:
```go
var _ Store = (*SQLiteStore)(nil)
```
Run: `go build ./internal/metastore/`
Expected: builds with no error.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/metastore/
git commit -m "feat(metastore): add Store interface and SQLite skeleton with schema"
```

---

## Task 2: Implement dataset CRUD in the SQLite store

**Files:**
- Modify: `internal/metastore/sqlite.go`
- Test: `internal/metastore/sqlite_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/metastore/sqlite_test.go`:
```go
func TestDatasetCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateDataset(ctx, &Dataset{ProjectID: "p1", Name: "sales"}); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	// Duplicate create must fail.
	if err := s.CreateDataset(ctx, &Dataset{ProjectID: "p1", Name: "sales"}); err == nil {
		t.Fatal("expected error on duplicate dataset")
	}
	// Same name under a different project is allowed.
	if err := s.CreateDataset(ctx, &Dataset{ProjectID: "p2", Name: "sales"}); err != nil {
		t.Fatalf("CreateDataset p2: %v", err)
	}

	got, err := s.GetDataset(ctx, "p1", "sales")
	if err != nil {
		t.Fatalf("GetDataset: %v", err)
	}
	if got.Name != "sales" || got.ProjectID != "p1" {
		t.Fatalf("unexpected dataset: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("CreatedAt not set")
	}

	if _, err := s.GetDataset(ctx, "p1", "missing"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	list, err := s.ListDatasets(ctx, "p1")
	if err != nil {
		t.Fatalf("ListDatasets: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 dataset for p1, got %d", len(list))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/metastore/ -run TestDatasetCRUD -v`
Expected: FAIL/panic — `unimplemented` in `CreateDataset`.

- [ ] **Step 3: Implement CreateDataset and GetDataset**

In `internal/metastore/sqlite.go`, replace the `CreateDataset` and `GetDataset` stubs:
```go
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
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/metastore/ -run TestDatasetCRUD -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/metastore/
git commit -m "feat(metastore): implement dataset CRUD"
```

---

## Task 3: Implement table CRUD + data-version bump in the SQLite store

**Files:**
- Modify: `internal/metastore/sqlite.go`
- Test: `internal/metastore/sqlite_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/metastore/sqlite_test.go`:
```go
func sampleTable() *Table {
	return &Table{
		ProjectID:        "p1",
		Dataset:          "sales",
		Name:             "orders",
		Kind:             "external",
		Location:         "s3://bucket/orders/*.parquet",
		Format:           "parquet",
		StorageClass:     "hdd",
		PartitionColumns: []string{"dt"},
		Schema:           []Column{{Name: "id", Type: "BIGINT", Nullable: false}},
		Stats:            Stats{RowCount: 42},
		DataVersion:      1,
	}
}

func TestTableCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateTable(ctx, sampleTable()); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if err := s.CreateTable(ctx, sampleTable()); err == nil {
		t.Fatal("expected error on duplicate table")
	}

	got, err := s.GetTable(ctx, "p1", "sales", "orders")
	if err != nil {
		t.Fatalf("GetTable: %v", err)
	}
	if got.Format != "parquet" || len(got.Schema) != 1 || got.Schema[0].Name != "id" {
		t.Fatalf("schema round-trip failed: %+v", got)
	}
	if len(got.PartitionColumns) != 1 || got.PartitionColumns[0] != "dt" {
		t.Fatalf("partition columns round-trip failed: %+v", got.PartitionColumns)
	}
	if got.Stats.RowCount != 42 {
		t.Fatalf("stats round-trip failed: %+v", got.Stats)
	}

	list, err := s.ListTables(ctx, "p1", "sales")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListTables: err=%v len=%d", err, len(list))
	}

	v, err := s.BumpDataVersion(ctx, "p1", "sales", "orders")
	if err != nil {
		t.Fatalf("BumpDataVersion: %v", err)
	}
	if v != 2 {
		t.Fatalf("expected version 2, got %d", v)
	}

	if err := s.DeleteTable(ctx, "p1", "sales", "orders"); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}
	if _, err := s.GetTable(ctx, "p1", "sales", "orders"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/metastore/ -run TestTableCRUD -v`
Expected: FAIL/panic — `unimplemented`.

- [ ] **Step 3: Implement table methods**

In `internal/metastore/sqlite.go`, add `"encoding/json"` to imports, then replace the four table stubs and `BumpDataVersion`:
```go
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
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/metastore/ -run TestTableCRUD -v`
Expected: PASS.

- [ ] **Step 5: Run the whole metastore package with race detector**

Run: `go test -race ./internal/metastore/`
Expected: PASS (ok).

- [ ] **Step 6: Commit**

```bash
git add internal/metastore/
git commit -m "feat(metastore): implement table CRUD and data-version bump"
```

---

## Task 4: Refactor query engine — extract `applyS3Creds` helper

**Files:**
- Modify: `internal/query/engine.go`
- Test: existing `internal/query/engine_test.go` (must stay green)

This is a pure refactor so `Query` and the new `QueryView` (Task 5) share credential setup (DRY).

- [ ] **Step 1: Add the helper**

In `internal/query/engine.go`, add this function (after `NewEngine`):
```go
// applyS3Creds configures a pooled DuckDB connection with S3 credentials for the
// given raw endpoint (which may include an http:// or https:// scheme).
func applyS3Creds(db *sql.DB, accessKey, secretKey, rawEndpoint string) {
	useSSL := true
	endpoint := rawEndpoint
	if idx := strings.Index(endpoint, "://"); idx >= 0 {
		useSSL = endpoint[:idx] == "https"
		endpoint = endpoint[idx+3:]
	}
	useSSLStr := "false"
	if useSSL {
		useSSLStr = "true"
	}
	db.Exec("CREATE OR REPLACE SECRET ds3_s3 (TYPE S3, KEY_ID '" + accessKey + "', SECRET '" + secretKey + "', ENDPOINT '" + endpoint + "', REGION 'us-east-1', USE_SSL " + useSSLStr + ", URL_STYLE 'path')")
	db.Exec("SET s3_access_key_id='" + accessKey + "'")
	db.Exec("SET s3_secret_access_key='" + secretKey + "'")
	db.Exec("SET s3_endpoint='" + endpoint + "'")
	db.Exec("SET s3_region='us-east-1'")
	db.Exec("SET s3_url_style='path'")
	if useSSL {
		db.Exec("SET s3_use_ssl=true")
	} else {
		db.Exec("SET s3_use_ssl=false")
	}
}
```

- [ ] **Step 2: Use the helper in `Query`**

In `Query`, replace the block from `useSSL := true` down through the closing of the `if useSSL { … } else { … }` credential `SET` statements (the lines ending at the `db.Exec("SET s3_use_ssl=false")` block, just before `// Set memory limit`) with:
```go
	db := <-e.pool
	defer func() { e.pool <- db }()

	applyS3Creds(db, accessKey, secretKey, rawEndpoint)
```
Remove the now-unused local `useSSL`, `endpoint`, `useSSLStr` declarations from `Query`.

- [ ] **Step 3: Use the helper in `InferSchema`**

In `internal/query/schema.go`, replace the equivalent credential-setup block (from `useSSL := true` through the `SET s3_use_ssl` block) with:
```go
	db := <-e.pool
	defer func() { e.pool <- db }()

	applyS3Creds(db, accessKey, secretKey, rawEndpoint)
```
Remove the now-unused `useSSL`, `endpoint`, `useSSLStr` locals and the `strings` import from `schema.go` if it becomes unused (run goimports/`go build` to confirm).

- [ ] **Step 4: Build and run existing query tests**

Run: `go test ./internal/query/`
Expected: PASS — behavior unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/query/
git commit -m "refactor(query): extract applyS3Creds helper shared by Query and InferSchema"
```

---

## Task 5: Add `ViewBinding` + catalog-aware `QueryView` to the query engine

**Files:**
- Modify: `internal/query/engine.go`
- Test: `internal/query/engine_test.go`

`QueryView` registers each referenced catalog table as a DuckDB view in a schema named after the dataset, runs the user SQL, then drops the schemas. This is the coordinator→worker "plan" seam: bindings are the resolved plan.

- [ ] **Step 1: Write the failing test (local file, no S3)**

Append to `internal/query/engine_test.go`:
```go
func TestQueryView_LocalFile(t *testing.T) {
	e, err := NewEngine(1000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	csv := filepath.Join(t.TempDir(), "people.csv")
	if err := os.WriteFile(csv, []byte("id,name\n1,alice\n2,bob\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	bindings := []ViewBinding{{
		Schema:    "sales",
		Name:      "people",
		ReaderSQL: "read_csv_auto('" + csv + "')",
	}}
	// Empty creds: local files don't need S3, applyS3Creds is harmless.
	res := e.QueryView("SELECT count(*) AS c FROM sales.people", bindings, "", "", "")
	if res.Error != "" {
		t.Fatalf("query error: %s", res.Error)
	}
	if res.RowCount != 1 {
		t.Fatalf("expected 1 row, got %d", res.RowCount)
	}
	if got := fmt.Sprintf("%v", res.Rows[0][0]); got != "2" {
		t.Fatalf("expected count 2, got %s", got)
	}
}
```
Ensure the test file imports `"os"`, `"path/filepath"`, and `"fmt"` (add any that are missing).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/query/ -run TestQueryView_LocalFile -v`
Expected: FAIL — `undefined: ViewBinding` / `QueryView`.

- [ ] **Step 3: Implement `ViewBinding` and `QueryView`**

In `internal/query/engine.go`, add:
```go
// ViewBinding maps a catalog table (schema.name) to the DuckDB reader expression
// that produces its rows, e.g. read_parquet('s3://bucket/path/*.parquet').
type ViewBinding struct {
	Schema    string
	Name      string
	ReaderSQL string
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// QueryView registers each binding as a DuckDB view, executes the user SQL, then
// drops the created schemas. Results are collected exactly like Query.
func (e *Engine) QueryView(sqlStr string, bindings []ViewBinding, accessKey, secretKey, rawEndpoint string) *Result {
	start := time.Now()

	db := <-e.pool
	defer func() { e.pool <- db }()

	applyS3Creds(db, accessKey, secretKey, rawEndpoint)

	schemas := map[string]struct{}{}
	for _, b := range bindings {
		schemas[b.Schema] = struct{}{}
	}
	for s := range schemas {
		if _, err := db.Exec("CREATE SCHEMA IF NOT EXISTS " + quoteIdent(s)); err != nil {
			return errorResult("create schema "+s+": "+err.Error(), start)
		}
	}
	defer func() {
		for s := range schemas {
			db.Exec("DROP SCHEMA IF EXISTS " + quoteIdent(s) + " CASCADE")
		}
	}()
	for _, b := range bindings {
		stmt := "CREATE OR REPLACE VIEW " + quoteIdent(b.Schema) + "." + quoteIdent(b.Name) +
			" AS SELECT * FROM " + b.ReaderSQL
		if _, err := db.Exec(stmt); err != nil {
			return errorResult("register table "+b.Schema+"."+b.Name+": "+err.Error(), start)
		}
	}

	if e.threads > 0 {
		db.Exec(fmt.Sprintf("SET threads=%d", e.threads))
	}
	db.Exec("SET memory_limit='" + e.memoryLimit + "'")

	return e.collectRows(db, sqlStr, start)
}
```

- [ ] **Step 4: Extract a shared `collectRows` from `Query` (DRY)**

In `internal/query/engine.go`, add:
```go
// collectRows executes sqlStr and gathers columns, types, and rows subject to
// the engine's row/byte limits.
func (e *Engine) collectRows(db *sql.DB, sqlStr string, start time.Time) *Result {
	rows, err := db.Query(sqlStr)
	if err != nil {
		return errorResult(err.Error(), start)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return errorResult("get columns: "+err.Error(), start)
	}
	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return errorResult("get types: "+err.Error(), start)
	}
	colInfos := make([]ColumnInfo, len(columns))
	for i := range columns {
		colInfos[i] = ColumnInfo{Name: columns[i], Type: columnTypes[i].DatabaseTypeName()}
	}

	var resultRows [][]any
	rowCount := 0
	totalBytes := int64(0)
	for rows.Next() {
		vals := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		resultRows = append(resultRows, vals)
		rowCount++
		for _, v := range vals {
			switch val := v.(type) {
			case string:
				totalBytes += int64(len(val))
			case []byte:
				totalBytes += int64(len(val))
			default:
				totalBytes += 8
			}
		}
		if rowCount >= e.maxRows || (e.maxResultBytes > 0 && totalBytes >= e.maxResultBytes) {
			break
		}
	}
	return &Result{Columns: colInfos, Rows: resultRows, RowCount: rowCount, ElapsedMs: time.Since(start).Milliseconds()}
}
```
Then in `Query`, replace everything from `rows, err := db.Query(sqlStr)` through the final `return &Result{…}` with:
```go
	return e.collectRows(db, sqlStr, start)
```
(Keep the memory-limit and threads `SET` statements that precede it in `Query`.)

- [ ] **Step 5: Run query tests (new + existing)**

Run: `go test ./internal/query/ -v`
Expected: PASS — `TestQueryView_LocalFile` and all pre-existing tests.

- [ ] **Step 6: Commit**

```bash
git add internal/query/
git commit -m "feat(query): add ViewBinding and catalog-aware QueryView"
```

---

## Task 6: Catalog service — datasets, table registration (schema+stats), describe/list/drop

**Files:**
- Create: `internal/catalog/service.go`
- Test: `internal/catalog/service_test.go`

The `Service` validates identifiers, wraps the store, and infers schema + row-count stats at registration via the query engine. It uses a small interface so tests can pass a fake engine.

- [ ] **Step 1: Write the failing test (local files)**

Create `internal/catalog/service_test.go`:
```go
package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

func newService(t *testing.T) *Service {
	t.Helper()
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	eng, err := query.NewEngine(1000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return NewService(store, eng)
}

func TestCreateDataset_Validation(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	if err := svc.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatalf("valid dataset rejected: %v", err)
	}
	if err := svc.CreateDataset(ctx, "p1", "bad name!"); err == nil {
		t.Fatal("expected validation error for bad dataset name")
	}
}

func TestRegisterTable_InfersSchemaAndStats(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	csv := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(csv, []byte("id,total\n1,10\n2,20\n3,30\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	if err := svc.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	tbl, err := svc.RegisterTable(ctx, RegisterTableInput{
		ProjectID: "p1", Dataset: "sales", Name: "orders",
		Location: csv, Format: "csv",
	}, "", "", "")
	if err != nil {
		t.Fatalf("RegisterTable: %v", err)
	}
	if len(tbl.Schema) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(tbl.Schema))
	}
	if tbl.Stats.RowCount != 3 {
		t.Fatalf("expected 3 rows, got %d", tbl.Stats.RowCount)
	}
	// Registering into a missing dataset must fail.
	if _, err := svc.RegisterTable(ctx, RegisterTableInput{
		ProjectID: "p1", Dataset: "missing", Name: "x", Location: csv, Format: "csv",
	}, "", "", ""); err == nil {
		t.Fatal("expected error registering into missing dataset")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/catalog/ -run TestCreateDataset_Validation -v`
Expected: FAIL — `undefined: NewService` etc.

- [ ] **Step 3: Implement the service**

Create `internal/catalog/service.go`:
```go
package catalog

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

var identRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func validIdent(kind, s string) error {
	if !identRe.MatchString(s) {
		return fmt.Errorf("invalid %s name %q: must match [a-zA-Z_][a-zA-Z0-9_]*", kind, s)
	}
	return nil
}

// SchemaInferer is the subset of *query.Engine the catalog needs (kept small for testing).
type SchemaInferer interface {
	InferSchema(path, accessKey, secretKey, endpoint string) *query.SchemaResult
	QueryView(sql string, bindings []query.ViewBinding, accessKey, secretKey, endpoint string) *query.Result
}

type Service struct {
	store  metastore.Store
	engine SchemaInferer
}

func NewService(store metastore.Store, engine SchemaInferer) *Service {
	return &Service{store: store, engine: engine}
}

func (s *Service) CreateDataset(ctx context.Context, projectID, name string) error {
	if err := validIdent("dataset", name); err != nil {
		return err
	}
	return s.store.CreateDataset(ctx, &metastore.Dataset{ProjectID: projectID, Name: name})
}

func (s *Service) ListDatasets(ctx context.Context, projectID string) ([]*metastore.Dataset, error) {
	return s.store.ListDatasets(ctx, projectID)
}

type RegisterTableInput struct {
	ProjectID        string
	Dataset          string
	Name             string
	Location         string
	Format           string // parquet | csv | tsv | json
	StorageClass     string // ssd | hdd (default hdd)
	PartitionColumns []string
}

// readerSQL builds the DuckDB reader expression for a location + format.
func readerSQL(location, format string) string {
	loc := strings.ReplaceAll(location, "'", "''")
	switch strings.ToLower(format) {
	case "parquet":
		return fmt.Sprintf("read_parquet('%s')", loc)
	case "json":
		return fmt.Sprintf("read_json_auto('%s')", loc)
	case "tsv":
		return fmt.Sprintf("read_csv_auto('%s', delim='\t')", loc)
	default: // csv
		return fmt.Sprintf("read_csv_auto('%s')", loc)
	}
}

func (s *Service) RegisterTable(ctx context.Context, in RegisterTableInput, accessKey, secretKey, endpoint string) (*metastore.Table, error) {
	if err := validIdent("table", in.Name); err != nil {
		return nil, err
	}
	if _, err := s.store.GetDataset(ctx, in.ProjectID, in.Dataset); err != nil {
		return nil, fmt.Errorf("dataset %q: %w", in.Dataset, err)
	}
	storageClass := in.StorageClass
	if storageClass == "" {
		storageClass = "hdd"
	}

	schemaRes := s.engine.InferSchema(in.Location, accessKey, secretKey, endpoint)
	if schemaRes.Error != "" {
		return nil, fmt.Errorf("infer schema: %s", schemaRes.Error)
	}
	cols := make([]metastore.Column, len(schemaRes.Columns))
	for i, c := range schemaRes.Columns {
		cols[i] = metastore.Column{Name: c.Name, Type: c.Type, Nullable: c.Nullable}
	}

	// Best-effort row-count stat.
	var rowCount int64
	countRes := s.engine.QueryView(
		"SELECT count(*) AS c FROM "+readerSQL(in.Location, in.Format),
		nil, accessKey, secretKey, endpoint)
	if countRes.Error == "" && countRes.RowCount == 1 {
		switch v := countRes.Rows[0][0].(type) {
		case int64:
			rowCount = v
		case int:
			rowCount = int64(v)
		}
	}

	t := &metastore.Table{
		ProjectID:        in.ProjectID,
		Dataset:          in.Dataset,
		Name:             in.Name,
		Kind:             "external",
		Location:         in.Location,
		Format:           strings.ToLower(in.Format),
		StorageClass:     storageClass,
		PartitionColumns: in.PartitionColumns,
		Schema:           cols,
		Stats:            metastore.Stats{RowCount: rowCount},
	}
	if err := s.store.CreateTable(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Service) GetTable(ctx context.Context, projectID, dataset, name string) (*metastore.Table, error) {
	return s.store.GetTable(ctx, projectID, dataset, name)
}

func (s *Service) ListTables(ctx context.Context, projectID, dataset string) ([]*metastore.Table, error) {
	return s.store.ListTables(ctx, projectID, dataset)
}

func (s *Service) DropTable(ctx context.Context, projectID, dataset, name string) error {
	return s.store.DeleteTable(ctx, projectID, dataset, name)
}
```

- [ ] **Step 4: Run both tests to verify they pass**

Run: `go test ./internal/catalog/ -run 'TestCreateDataset_Validation|TestRegisterTable_InfersSchemaAndStats' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/
git commit -m "feat(catalog): service for datasets and external table registration"
```

---

## Task 7: Catalog `Resolve` — turn SQL + project tables into view bindings

**Files:**
- Modify: `internal/catalog/service.go`
- Test: `internal/catalog/service_test.go`

`Resolve` lists the project's tables and, for each whose qualified name appears in the SQL, produces a `query.ViewBinding`. No S3 access — it uses stored `Location`/`Format`.

- [ ] **Step 1: Write the failing test**

Append to `internal/catalog/service_test.go`:
```go
func TestResolve_MatchesReferencedTables(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	csv := filepath.Join(t.TempDir(), "orders.csv")
	_ = os.WriteFile(csv, []byte("id,total\n1,10\n"), 0644)
	if err := svc.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterTable(ctx, RegisterTableInput{
		ProjectID: "p1", Dataset: "sales", Name: "orders", Location: csv, Format: "csv",
	}, "", "", ""); err != nil {
		t.Fatal(err)
	}

	bindings, err := svc.Resolve(ctx, "p1", "SELECT * FROM sales.orders WHERE total > 5")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].Schema != "sales" || bindings[0].Name != "orders" {
		t.Fatalf("unexpected binding: %+v", bindings[0])
	}

	// A query that references no catalog tables yields zero bindings.
	none, err := svc.Resolve(ctx, "p1", "SELECT 1")
	if err != nil {
		t.Fatalf("Resolve(no refs): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected 0 bindings, got %d", len(none))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/catalog/ -run TestResolve_MatchesReferencedTables -v`
Expected: FAIL — `svc.Resolve undefined`.

- [ ] **Step 3: Implement `Resolve`**

Append to `internal/catalog/service.go`:
```go
// Resolve scans sql for references to the project's catalog tables and returns a
// view binding for each one that appears. Matching is done on the qualified name
// `dataset.table` (and its double-quoted form) at identifier boundaries.
func (s *Service) Resolve(ctx context.Context, projectID, sql string) ([]query.ViewBinding, error) {
	datasets, err := s.store.ListDatasets(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var bindings []query.ViewBinding
	for _, ds := range datasets {
		tables, err := s.store.ListTables(ctx, projectID, ds.Name)
		if err != nil {
			return nil, err
		}
		for _, t := range tables {
			if referencesTable(sql, t.Dataset, t.Name) {
				bindings = append(bindings, query.ViewBinding{
					Schema:    t.Dataset,
					Name:      t.Name,
					ReaderSQL: readerSQL(t.Location, t.Format),
				})
			}
		}
	}
	return bindings, nil
}

func referencesTable(sql, dataset, name string) bool {
	patterns := []string{
		`(?i)\b` + regexp.QuoteMeta(dataset) + `\.` + regexp.QuoteMeta(name) + `\b`,
		`(?i)"` + regexp.QuoteMeta(dataset) + `"\."` + regexp.QuoteMeta(name) + `"`,
	}
	for _, p := range patterns {
		if regexp.MustCompile(p).MatchString(sql) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/catalog/ -run TestResolve_MatchesReferencedTables -v`
Expected: PASS.

- [ ] **Step 5: Run the whole catalog package**

Run: `go test ./internal/catalog/`
Expected: PASS (ok).

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/
git commit -m "feat(catalog): Resolve SQL references into view bindings"
```

---

## Task 8: Job package — Executor interface and synchronous Manager

**Files:**
- Create: `internal/job/job.go`
- Test: `internal/job/job_test.go`

- [ ] **Step 1: Write the failing test (fake executor)**

Create `internal/job/job_test.go`:
```go
package job

import (
	"context"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/query"
)

type fakeExecutor struct{ called bool }

func (f *fakeExecutor) Execute(ctx context.Context, req ExecRequest) *query.Result {
	f.called = true
	return &query.Result{
		Columns:  []query.ColumnInfo{{Name: "c", Type: "BIGINT"}},
		Rows:     [][]any{{int64(1)}},
		RowCount: 1,
	}
}

func TestManager_RunSync(t *testing.T) {
	fe := &fakeExecutor{}
	m := NewManager(fe)
	j := m.Run(context.Background(), ExecRequest{SQL: "SELECT 1", ProjectID: "p1"})
	if !fe.called {
		t.Fatal("executor not called")
	}
	if j.Status != "done" {
		t.Fatalf("expected status done, got %s", j.Status)
	}
	if j.Result == nil || j.Result.RowCount != 1 {
		t.Fatalf("unexpected result: %+v", j.Result)
	}
	if j.ID == "" {
		t.Fatal("job ID not set")
	}
	got, ok := m.Get(j.ID)
	if !ok || got.ID != j.ID {
		t.Fatalf("Get(%s) failed", j.ID)
	}
}

func TestManager_RunError(t *testing.T) {
	m := NewManager(execFunc(func(ctx context.Context, req ExecRequest) *query.Result {
		return &query.Result{Error: "boom"}
	}))
	j := m.Run(context.Background(), ExecRequest{SQL: "bad"})
	if j.Status != "failed" {
		t.Fatalf("expected status failed, got %s", j.Status)
	}
	if j.Error != "boom" {
		t.Fatalf("expected error boom, got %q", j.Error)
	}
}

type execFunc func(ctx context.Context, req ExecRequest) *query.Result

func (f execFunc) Execute(ctx context.Context, req ExecRequest) *query.Result { return f(ctx, req) }
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/job/ -run TestManager_RunSync -v`
Expected: FAIL — `undefined: NewManager` etc.

- [ ] **Step 3: Implement the job package**

Create `internal/job/job.go`:
```go
package job

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// ExecRequest is a query execution request with the caller's S3 credentials.
type ExecRequest struct {
	SQL       string
	ProjectID string
	AccessKey string
	SecretKey string
	Endpoint  string
}

// Executor runs a query and returns its result. LocalExecutor runs in-process;
// Phase 2 will add a remote (worker-pool) implementation behind this same seam.
type Executor interface {
	Execute(ctx context.Context, req ExecRequest) *query.Result
}

// Job is a tracked unit of work. Phase 1 supports only synchronous "query" jobs.
type Job struct {
	ID        string        `json:"id"`
	Type      string        `json:"type"`
	SQL       string        `json:"sql"`
	Status    string        `json:"status"` // queued | running | done | failed
	Result    *query.Result `json:"result,omitempty"`
	Error     string        `json:"error,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

type Manager struct {
	exec Executor
	mu   sync.RWMutex
	jobs map[string]*Job
}

func NewManager(exec Executor) *Manager {
	return &Manager{exec: exec, jobs: make(map[string]*Job)}
}

// Run executes the request synchronously (sync fast-path) and returns the job.
func (m *Manager) Run(ctx context.Context, req ExecRequest) *Job {
	j := &Job{
		ID:        uuid.NewString(),
		Type:      "query",
		SQL:       req.SQL,
		Status:    "running",
		CreatedAt: time.Now().UTC(),
	}
	m.put(j)

	res := m.exec.Execute(ctx, req)
	if res.Error != "" {
		j.Status = "failed"
		j.Error = res.Error
	} else {
		j.Status = "done"
		j.Result = res
	}
	m.put(j)
	return j
}

func (m *Manager) put(j *Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.ID] = j
}

func (m *Manager) Get(id string) (*Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	return j, ok
}

// Cleanup removes jobs older than maxAge (called periodically from main).
func (m *Manager) Cleanup(maxAge time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, j := range m.jobs {
		if now.Sub(j.CreatedAt) > maxAge {
			delete(m.jobs, id)
		}
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/job/ -run 'TestManager_RunSync|TestManager_RunError' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/job/
git commit -m "feat(job): synchronous job manager and Executor seam"
```

---

## Task 9: LocalExecutor — end-to-end `SELECT FROM dataset.table`

**Files:**
- Create: `internal/job/local_executor.go`
- Test: `internal/job/local_executor_test.go`

- [ ] **Step 1: Write the failing end-to-end test (local files)**

Create `internal/job/local_executor_test.go`:
```go
package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

func TestLocalExecutor_EndToEnd(t *testing.T) {
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	eng, err := query.NewEngine(1000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatal(err)
	}
	svc := catalog.NewService(store, eng)
	ctx := context.Background()

	csv := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(csv, []byte("id,total\n1,10\n2,20\n3,30\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := svc.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterTable(ctx, catalog.RegisterTableInput{
		ProjectID: "p1", Dataset: "sales", Name: "orders", Location: csv, Format: "csv",
	}, "", "", ""); err != nil {
		t.Fatal(err)
	}

	ex := NewLocalExecutor(svc, eng)
	res := ex.Execute(ctx, ExecRequest{
		SQL: "SELECT sum(total) AS s FROM sales.orders", ProjectID: "p1",
	})
	if res.Error != "" {
		t.Fatalf("execute error: %s", res.Error)
	}
	if got := fmt.Sprintf("%v", res.Rows[0][0]); got != "60" {
		t.Fatalf("expected sum 60, got %s", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/job/ -run TestLocalExecutor_EndToEnd -v`
Expected: FAIL — `undefined: NewLocalExecutor`.

- [ ] **Step 3: Implement LocalExecutor**

Create `internal/job/local_executor.go`:
```go
package job

import (
	"context"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// LocalExecutor resolves catalog table references and runs the query in-process.
type LocalExecutor struct {
	cat    *catalog.Service
	engine *query.Engine
}

func NewLocalExecutor(cat *catalog.Service, engine *query.Engine) *LocalExecutor {
	return &LocalExecutor{cat: cat, engine: engine}
}

func (l *LocalExecutor) Execute(ctx context.Context, req ExecRequest) *query.Result {
	bindings, err := l.cat.Resolve(ctx, req.ProjectID, req.SQL)
	if err != nil {
		return &query.Result{Error: "resolve tables: " + err.Error()}
	}
	return l.engine.QueryView(req.SQL, bindings, req.AccessKey, req.SecretKey, req.Endpoint)
}

var _ Executor = (*LocalExecutor)(nil)
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/job/ -run TestLocalExecutor_EndToEnd -v`
Expected: PASS.

- [ ] **Step 5: Run the whole job package with race detector**

Run: `go test -race ./internal/job/`
Expected: PASS (ok).

- [ ] **Step 6: Commit**

```bash
git add internal/job/
git commit -m "feat(job): LocalExecutor resolves catalog tables and runs queries"
```

---

## Task 10: Config — add `Role` and `Metastore` settings

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:
```go
package config

import (
	"os"
	"testing"
)

func TestDefault_RoleAndMetastore(t *testing.T) {
	c := Default()
	if c.Role != "all" {
		t.Fatalf("expected default role 'all', got %q", c.Role)
	}
	if c.Metastore.Path == "" {
		t.Fatal("expected a default metastore path")
	}
}

func TestLoad_MetastoreEnvOverride(t *testing.T) {
	t.Setenv("DS3SQL_METASTORE_PATH", "/tmp/custom-meta.db")
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Metastore.Path != "/tmp/custom-meta.db" {
		t.Fatalf("env override not applied: %q", c.Metastore.Path)
	}
	_ = os.Unsetenv("DS3SQL_METASTORE_PATH")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/config/ -run 'TestDefault_RoleAndMetastore|TestLoad_MetastoreEnvOverride' -v`
Expected: FAIL — `c.Role`/`c.Metastore` undefined.

- [ ] **Step 3: Implement config additions**

In `internal/config/config.go`:

Add fields to the `Config` struct (after `DS3GatewayURL`):
```go
	Role string `yaml:"role"`
```
Add a new section to the `Config` struct (after `RateLimit`):
```go
	Metastore MetastoreConfig `yaml:"metastore"`
```
Add the type:
```go
type MetastoreConfig struct {
	Path string `yaml:"path"`
}
```
In `Default()`, set `Role` and `Metastore`:
```go
	Role: "all",
	Metastore: MetastoreConfig{
		Path: defaultMetastorePath(),
	},
```
Add the helper:
```go
func defaultMetastorePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "metastore.db"
	}
	return home + "/.ds3sql/metastore.db"
}
```
In `Load`, add env overrides (with the others):
```go
	if v := os.Getenv("DS3SQL_ROLE"); v != "" {
		cfg.Role = v
	}
	if v := os.Getenv("DS3SQL_METASTORE_PATH"); v != "" {
		cfg.Metastore.Path = v
	}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): add role and metastore path settings"
```

---

## Task 11: API — dataset handlers (create/list)

**Files:**
- Create: `internal/api/dataset_handler.go`
- Test: `internal/api/dataset_handler_test.go`

Handlers take `projectID` as a parameter (extracted from the session in `main.go`), mirroring the existing `QueryWithCreds` convention so they're testable without an auth context.

- [ ] **Step 1: Write the failing test**

Create `internal/api/dataset_handler_test.go`:
```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

func newTestCatalog(t *testing.T) *catalog.Service {
	t.Helper()
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	eng, err := query.NewEngine(1000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatal(err)
	}
	return catalog.NewService(store, eng)
}

func TestDatasetHandler_CreateAndList(t *testing.T) {
	h := NewDatasetHandler(newTestCatalog(t))

	// Create
	req := httptest.NewRequest("POST", "/datasets", strings.NewReader(`{"name":"sales"}`))
	w := httptest.NewRecorder()
	h.CreateForProject(w, req, "p1")
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", w.Code, w.Body.String())
	}

	// List
	req = httptest.NewRequest("GET", "/datasets", nil)
	w = httptest.NewRecorder()
	h.ListForProject(w, req, "p1")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	var out struct {
		Datasets []struct {
			Name string `json:"name"`
		} `json:"datasets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Datasets) != 1 || out.Datasets[0].Name != "sales" {
		t.Fatalf("unexpected datasets: %+v", out.Datasets)
	}

	// Invalid name -> 400
	req = httptest.NewRequest("POST", "/datasets", strings.NewReader(`{"name":"bad name"}`))
	w = httptest.NewRecorder()
	h.CreateForProject(w, req, "p1")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad name, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestDatasetHandler_CreateAndList -v`
Expected: FAIL — `undefined: NewDatasetHandler`.

- [ ] **Step 3: Implement the handler**

Create `internal/api/dataset_handler.go`:
```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
)

type DatasetHandler struct {
	cat *catalog.Service
}

func NewDatasetHandler(cat *catalog.Service) *DatasetHandler {
	return &DatasetHandler{cat: cat}
}

func (h *DatasetHandler) CreateForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if err := h.cat.CreateDataset(r.Context(), projectID, req.Name); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"name": req.Name})
}

func (h *DatasetHandler) ListForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	datasets, err := h.cat.ListDatasets(r.Context(), projectID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"datasets": datasets})
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run TestDatasetHandler_CreateAndList -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/dataset_handler.go internal/api/dataset_handler_test.go
git commit -m "feat(api): dataset create/list handlers"
```

---

## Task 12: API — table handlers (register/list/describe/drop)

**Files:**
- Create: `internal/api/table_handler.go`
- Test: `internal/api/table_handler_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/table_handler_test.go`:
```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestTableHandler_RegisterListDescribeDrop(t *testing.T) {
	cat := newTestCatalog(t)
	if err := cat.CreateDataset(context.Background(), "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	h := NewTableHandler(cat)

	csv := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(csv, []byte("id,total\n1,10\n2,20\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Register (uses creds params; empty creds are fine for local files).
	body := `{"name":"orders","location":"` + csv + `","format":"csv"}`
	req := httptest.NewRequest("POST", "/datasets/sales/tables", strings.NewReader(body))
	req = withURLParam(req, "dataset", "sales")
	w := httptest.NewRecorder()
	h.RegisterForProject(w, req, "p1", "", "", "")
	if w.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body=%s", w.Code, w.Body.String())
	}

	// List
	req = httptest.NewRequest("GET", "/datasets/sales/tables", nil)
	req = withURLParam(req, "dataset", "sales")
	w = httptest.NewRecorder()
	h.ListForProject(w, req, "p1")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "orders") {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}

	// Describe
	req = httptest.NewRequest("GET", "/datasets/sales/tables/orders", nil)
	req = withURLParam(req, "dataset", "sales")
	req = withURLParam(req, "table", "orders")
	w = httptest.NewRecorder()
	h.DescribeForProject(w, req, "p1")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "row_count") {
		t.Fatalf("describe failed: %d %s", w.Code, w.Body.String())
	}

	// Drop
	req = httptest.NewRequest("DELETE", "/datasets/sales/tables/orders", nil)
	req = withURLParam(req, "dataset", "sales")
	req = withURLParam(req, "table", "orders")
	w = httptest.NewRecorder()
	h.DropForProject(w, req, "p1")
	if w.Code != http.StatusNoContent {
		t.Fatalf("drop status = %d", w.Code)
	}
}

// withURLParam injects a chi URL param into the request context for handler tests.
func withURLParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	if existing := chi.RouteContext(r.Context()); existing != nil {
		rctx = existing
	}
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestTableHandler_RegisterListDescribeDrop -v`
Expected: FAIL — `undefined: NewTableHandler`.

- [ ] **Step 3: Implement the handler**

Create `internal/api/table_handler.go`:
```go
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

type TableHandler struct {
	cat *catalog.Service
}

func NewTableHandler(cat *catalog.Service) *TableHandler {
	return &TableHandler{cat: cat}
}

func (h *TableHandler) RegisterForProject(w http.ResponseWriter, r *http.Request, projectID, accessKey, secretKey, endpoint string) {
	dataset := chi.URLParam(r, "dataset")
	var req struct {
		Name             string   `json:"name"`
		Location         string   `json:"location"`
		Format           string   `json:"format"`
		StorageClass     string   `json:"storage_class"`
		PartitionColumns []string `json:"partition_columns"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Location == "" {
		http.Error(w, `{"error":"name and location are required"}`, http.StatusBadRequest)
		return
	}
	if req.Format == "" {
		req.Format = "parquet"
	}
	tbl, err := h.cat.RegisterTable(r.Context(), catalog.RegisterTableInput{
		ProjectID:        projectID,
		Dataset:          dataset,
		Name:             req.Name,
		Location:         req.Location,
		Format:           req.Format,
		StorageClass:     req.StorageClass,
		PartitionColumns: req.PartitionColumns,
	}, accessKey, secretKey, endpoint)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(tbl)
}

func (h *TableHandler) ListForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	dataset := chi.URLParam(r, "dataset")
	tables, err := h.cat.ListTables(r.Context(), projectID, dataset)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"tables": tables})
}

func (h *TableHandler) DescribeForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	dataset := chi.URLParam(r, "dataset")
	name := chi.URLParam(r, "table")
	tbl, err := h.cat.GetTable(r.Context(), projectID, dataset, name)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, metastore.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, `{"error":"`+err.Error()+`"}`, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tbl)
}

func (h *TableHandler) DropForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	dataset := chi.URLParam(r, "dataset")
	name := chi.URLParam(r, "table")
	if err := h.cat.DropTable(r.Context(), projectID, dataset, name); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, metastore.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, `{"error":"`+err.Error()+`"}`, status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Note: `metastore.ErrNotFound` is a value (`errString`); `errors.Is` compares it by equality, which works because the same value is returned everywhere.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run TestTableHandler_RegisterListDescribeDrop -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/table_handler.go internal/api/table_handler_test.go
git commit -m "feat(api): table register/list/describe/drop handlers"
```

---

## Task 13: API — job handlers (submit/get)

**Files:**
- Create: `internal/api/job_handler.go`
- Test: `internal/api/job_handler_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/job_handler_test.go`:
```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/job"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

type stubExecutor struct{}

func (stubExecutor) Execute(ctx context.Context, req job.ExecRequest) *query.Result {
	return &query.Result{
		Columns:  []query.ColumnInfo{{Name: "n", Type: "INTEGER"}},
		Rows:     [][]any{{int64(1)}},
		RowCount: 1,
	}
}

func TestJobHandler_SubmitSyncAndGet(t *testing.T) {
	mgr := job.NewManager(stubExecutor{})
	h := NewJobHandler(mgr)

	req := httptest.NewRequest("POST", "/jobs", strings.NewReader(`{"sql":"SELECT 1"}`))
	w := httptest.NewRecorder()
	h.SubmitWithCreds(w, req, "p1", "ak", "sk", "http://localhost:9000")
	if w.Code != http.StatusOK {
		t.Fatalf("submit status = %d, body=%s", w.Code, w.Body.String())
	}
	var submitted job.Job
	if err := json.Unmarshal(w.Body.Bytes(), &submitted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if submitted.Status != "done" || submitted.Result.RowCount != 1 {
		t.Fatalf("unexpected job: %+v", submitted)
	}

	// Get by ID
	req = httptest.NewRequest("GET", "/jobs/"+submitted.ID, nil)
	req = withURLParam(req, "id", submitted.ID)
	w = httptest.NewRecorder()
	h.Get(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d", w.Code)
	}

	// Missing SQL -> 400
	req = httptest.NewRequest("POST", "/jobs", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	h.SubmitWithCreds(w, req, "p1", "ak", "sk", "http://localhost:9000")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing sql, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestJobHandler_SubmitSyncAndGet -v`
Expected: FAIL — `undefined: NewJobHandler`.

- [ ] **Step 3: Implement the handler**

Create `internal/api/job_handler.go`:
```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/esignoretti/ds3-sql-server/internal/job"
)

type JobHandler struct {
	mgr *job.Manager
}

func NewJobHandler(mgr *job.Manager) *JobHandler {
	return &JobHandler{mgr: mgr}
}

// SubmitWithCreds runs a query job synchronously (Phase 1 sync fast-path) and
// returns the completed job inline.
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
	j := h.mgr.Run(r.Context(), job.ExecRequest{
		SQL:       req.SQL,
		ProjectID: projectID,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Endpoint:  endpoint,
	})
	w.Header().Set("Content-Type", "application/json")
	if j.Status == "failed" {
		w.WriteHeader(http.StatusBadRequest)
	}
	json.NewEncoder(w).Encode(j)
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
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run TestJobHandler_SubmitSyncAndGet -v`
Expected: PASS.

- [ ] **Step 5: Run the whole api package**

Run: `go test ./internal/api/`
Expected: PASS (ok).

- [ ] **Step 6: Commit**

```bash
git add internal/api/job_handler.go internal/api/job_handler_test.go
git commit -m "feat(api): synchronous job submit/get handlers"
```

---

## Task 14: Server wiring — `--role` flag, metastore/catalog/job init, new routes

**Files:**
- Modify: `cmd/ds3sql-server/main.go`

- [ ] **Step 1: Add the `--role` flag and validate it**

In `cmd/ds3sql-server/main.go`, in `main()`, replace the flag block:
```go
	port := flag.Int("port", 0, "Listening port (overrides config)")
	flag.Parse()
```
with:
```go
	port := flag.Int("port", 0, "Listening port (overrides config)")
	role := flag.String("role", "", "Server role: coordinator | worker | all (overrides config)")
	flag.Parse()
```
After `cfg, err := config.Load("")` and its error check, add:
```go
	if *role != "" {
		cfg.Role = *role
	}
	switch cfg.Role {
	case "all", "coordinator", "worker":
		// ok
	default:
		log.Fatalf("invalid role %q: must be coordinator, worker, or all", cfg.Role)
	}
```

- [ ] **Step 2: Initialize the metastore, catalog service, and job manager**

After the query engine is created (`queryHandler := api.NewQueryHandler(queryEngine)` block), add:
```go
	// Metastore (embedded SQLite by default)
	if dir := filepath.Dir(cfg.Metastore.Path); dir != "" {
		os.MkdirAll(dir, 0755)
	}
	store, err := metastore.OpenSQLite(cfg.Metastore.Path)
	if err != nil {
		log.Fatalf("failed to open metastore: %v", err)
	}
	defer store.Close()

	catalogSvc := catalog.NewService(store, queryEngine)
	localExecutor := job.NewLocalExecutor(catalogSvc, queryEngine)
	jobManager := job.NewManager(localExecutor)

	datasetHandler := api.NewDatasetHandler(catalogSvc)
	tableHandler := api.NewTableHandler(catalogSvc)
	jobHandler := api.NewJobHandler(jobManager)
```
Add imports at the top: `"path/filepath"`, and the new internal packages:
```go
	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/job"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
```
(`"os"` and `"flag"` are already imported.)

- [ ] **Step 3: Add a job-manager cleanup goroutine**

Next to the existing convert cleanup goroutine, add:
```go
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			jobManager.Cleanup(30 * time.Minute)
		}
	}()
```

- [ ] **Step 4: Mount the new routes inside the protected, no-timeout group**

In the second protected group (`r.Group(func(r chi.Router) { … r.Use(auth.Middleware(sessionStore)) … })` that holds `/analyze`, `/convert`, etc.), add the dataset/table/job routes. Each resolves the project's credentials from the session the same way the existing `/query` route does. Add this helper just above that group (after `s3ClientForProject` is defined in the first group is out of scope — define a local cred resolver here):
```go
	// projectCreds returns (projectID, accessKey, secretKey, endpoint, ok) for the
	// selected project (?project=, or the first project if unset).
	projectCreds := func(r *http.Request) (string, string, string, string, bool) {
		session := auth.GetSession(r)
		projectID := r.URL.Query().Get("project")
		for _, p := range session.Projects {
			if projectID == "" || p.ProjectID == projectID {
				return p.ProjectID, p.AccessKey, p.SecretKey, session.GatewayEndpoint, true
			}
		}
		return "", "", "", "", false
	}
```
Then inside that protected group, add:
```go
		r.Post("/datasets", func(w http.ResponseWriter, r *http.Request) {
			pid, _, _, _, ok := projectCreds(r)
			if !ok {
				http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
				return
			}
			datasetHandler.CreateForProject(w, r, pid)
		})
		r.Get("/datasets", func(w http.ResponseWriter, r *http.Request) {
			pid, _, _, _, ok := projectCreds(r)
			if !ok {
				http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
				return
			}
			datasetHandler.ListForProject(w, r, pid)
		})
		r.Post("/datasets/{dataset}/tables", func(w http.ResponseWriter, r *http.Request) {
			pid, ak, sk, ep, ok := projectCreds(r)
			if !ok {
				http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
				return
			}
			tableHandler.RegisterForProject(w, r, pid, ak, sk, ep)
		})
		r.Get("/datasets/{dataset}/tables", func(w http.ResponseWriter, r *http.Request) {
			pid, _, _, _, ok := projectCreds(r)
			if !ok {
				http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
				return
			}
			tableHandler.ListForProject(w, r, pid)
		})
		r.Get("/datasets/{dataset}/tables/{table}", func(w http.ResponseWriter, r *http.Request) {
			pid, _, _, _, ok := projectCreds(r)
			if !ok {
				http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
				return
			}
			tableHandler.DescribeForProject(w, r, pid)
		})
		r.Delete("/datasets/{dataset}/tables/{table}", func(w http.ResponseWriter, r *http.Request) {
			pid, _, _, _, ok := projectCreds(r)
			if !ok {
				http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
				return
			}
			tableHandler.DropForProject(w, r, pid)
		})
		r.Post("/jobs", func(w http.ResponseWriter, r *http.Request) {
			pid, ak, sk, ep, ok := projectCreds(r)
			if !ok {
				http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
				return
			}
			jobHandler.SubmitWithCreds(w, r, pid, ak, sk, ep)
		})
		r.Get("/jobs/{id}", jobHandler.Get)
```

> Phase 1 note: all three roles (`all`, `coordinator`, `worker`) currently wire the same in-process server; the `Executor` interface is the seam where Phase 2 swaps a remote worker executor into the coordinator. The role is validated now so deployments can already set it.

- [ ] **Step 5: Build the server**

Run: `go build ./cmd/ds3sql-server/`
Expected: builds with no error.

- [ ] **Step 6: Build everything and run the full test suite**

Run:
```bash
go build ./...
go test ./...
```
Expected: all packages build; all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/ds3sql-server/main.go
git commit -m "feat(server): --role flag and wire catalog/job routes"
```

---

## Task 15: CLI — `datasets` command

**Files:**
- Create: `cmd/ds3sql/datasets_cmd.go`

- [ ] **Step 1: Implement the command**

Create `cmd/ds3sql/datasets_cmd.go`:
```go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	datasetsCmd.AddCommand(datasetsLsCmd)
	datasetsCmd.AddCommand(datasetsCreateCmd)
	datasetsCmd.PersistentFlags().StringP("project", "p", "", "Project ID (defaults to first project)")
	rootCmd.AddCommand(datasetsCmd)
}

var datasetsCmd = &cobra.Command{
	Use:   "datasets",
	Short: "Manage catalog datasets",
}

func projectQuery(cmd *cobra.Command) string {
	if p, _ := cmd.Flags().GetString("project"); p != "" {
		return "?project=" + p
	}
	return ""
}

var datasetsLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List datasets",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		data, err := authedGet(cfg, "/datasets"+projectQuery(cmd))
		if err != nil {
			return err
		}
		var out struct {
			Datasets []struct {
				Name string `json:"name"`
			} `json:"datasets"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		for _, d := range out.Datasets {
			fmt.Println(d.Name)
		}
		return nil
	},
}

var datasetsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a dataset",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]string{"name": args[0]})
		data, err := authedPost(cfg, "/datasets"+projectQuery(cmd), body)
		if err != nil {
			return err
		}
		var out struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &out)
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		fmt.Printf("dataset %q created\n", args[0])
		return nil
	},
}
```

- [ ] **Step 2: Build the CLI**

Run: `go build ./cmd/ds3sql/`
Expected: builds with no error.

- [ ] **Step 3: Verify the command is registered**

Run: `go run ./cmd/ds3sql/ datasets --help`
Expected: help text lists `ls` and `create` subcommands.

- [ ] **Step 4: Commit**

```bash
git add cmd/ds3sql/datasets_cmd.go
git commit -m "feat(cli): datasets ls/create commands"
```

---

## Task 16: CLI — `tables` command

**Files:**
- Create: `cmd/ds3sql/tables_cmd.go`

- [ ] **Step 1: Implement the command**

Create `cmd/ds3sql/tables_cmd.go`:
```go
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"os"

	"github.com/spf13/cobra"
)

func init() {
	tablesCmd.AddCommand(tablesLsCmd)
	tablesCmd.AddCommand(tablesRegisterCmd)
	tablesCmd.AddCommand(tablesDescribeCmd)
	tablesCmd.AddCommand(tablesDropCmd)
	tablesCmd.PersistentFlags().StringP("project", "p", "", "Project ID (defaults to first project)")
	tablesRegisterCmd.Flags().String("location", "", "S3 location or glob (required)")
	tablesRegisterCmd.Flags().String("format", "parquet", "File format: parquet | csv | tsv | json")
	tablesRegisterCmd.Flags().String("storage-class", "hdd", "Storage class: ssd | hdd")
	tablesRegisterCmd.Flags().StringSlice("partition-by", nil, "Partition columns")
	rootCmd.AddCommand(tablesCmd)
}

var tablesCmd = &cobra.Command{
	Use:   "tables",
	Short: "Manage catalog tables",
}

// splitRef splits "dataset.table" into its parts.
func splitRef(ref string) (string, string, error) {
	parts := strings.SplitN(ref, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected dataset.table, got %q", ref)
	}
	return parts[0], parts[1], nil
}

var tablesLsCmd = &cobra.Command{
	Use:   "ls <dataset>",
	Short: "List tables in a dataset",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		data, err := authedGet(cfg, "/datasets/"+args[0]+"/tables"+projectQuery(cmd))
		if err != nil {
			return err
		}
		var out struct {
			Tables []struct {
				Name   string `json:"name"`
				Format string `json:"format"`
			} `json:"tables"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		for _, t := range out.Tables {
			fmt.Printf("%s\t%s\n", t.Name, t.Format)
		}
		return nil
	},
}

var tablesRegisterCmd = &cobra.Command{
	Use:   "register <dataset.table>",
	Short: "Register an external table over existing S3 data",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		dataset, name, err := splitRef(args[0])
		if err != nil {
			return err
		}
		location, _ := cmd.Flags().GetString("location")
		if location == "" {
			return fmt.Errorf("--location is required")
		}
		format, _ := cmd.Flags().GetString("format")
		storageClass, _ := cmd.Flags().GetString("storage-class")
		partitionBy, _ := cmd.Flags().GetStringSlice("partition-by")
		body, _ := json.Marshal(map[string]any{
			"name":              name,
			"location":          location,
			"format":            format,
			"storage_class":     storageClass,
			"partition_columns": partitionBy,
		})
		data, err := authedPost(cfg, "/datasets/"+dataset+"/tables"+projectQuery(cmd), body)
		if err != nil {
			return err
		}
		var out struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &out)
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		fmt.Printf("table %s.%s registered\n", dataset, name)
		return nil
	},
}

var tablesDescribeCmd = &cobra.Command{
	Use:   "describe <dataset.table>",
	Short: "Show a table's schema and stats",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		dataset, name, err := splitRef(args[0])
		if err != nil {
			return err
		}
		data, err := authedGet(cfg, "/datasets/"+dataset+"/tables/"+name+projectQuery(cmd))
		if err != nil {
			return err
		}
		var out struct {
			Schema []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"schema"`
			Stats struct {
				RowCount int64 `json:"row_count"`
			} `json:"stats"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "COLUMN\tTYPE")
		for _, c := range out.Schema {
			fmt.Fprintf(w, "%s\t%s\n", c.Name, c.Type)
		}
		w.Flush()
		fmt.Printf("\nrows: %d\n", out.Stats.RowCount)
		return nil
	},
}

var tablesDropCmd = &cobra.Command{
	Use:   "drop <dataset.table>",
	Short: "Drop a table registration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		dataset, name, err := splitRef(args[0])
		if err != nil {
			return err
		}
		if err := authedDelete(cfg, "/datasets/"+dataset+"/tables/"+name+projectQuery(cmd)); err != nil {
			return err
		}
		fmt.Printf("table %s.%s dropped\n", dataset, name)
		return nil
	},
}
```

- [ ] **Step 2: Add the `authedDelete` helper**

In `cmd/ds3sql/status.go`, after `authedPost`, add:
```go
func authedDelete(cfg *CLIConfig, path string) error {
	req, _ := http.NewRequest("DELETE", serverURL(cfg)+path, nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
```
Ensure `cmd/ds3sql/status.go` imports `"fmt"` (add if missing).

- [ ] **Step 3: Build the CLI**

Run: `go build ./cmd/ds3sql/`
Expected: builds with no error.

- [ ] **Step 4: Verify the command is registered**

Run: `go run ./cmd/ds3sql/ tables register --help`
Expected: help text shows `--location`, `--format`, `--storage-class`, `--partition-by`.

- [ ] **Step 5: Commit**

```bash
git add cmd/ds3sql/tables_cmd.go cmd/ds3sql/status.go
git commit -m "feat(cli): tables register/ls/describe/drop commands"
```

---

## Task 17: CLI — route `query` through `/jobs`

**Files:**
- Modify: `cmd/ds3sql/query.go`

- [ ] **Step 1: Point the query command at `/jobs`**

In `cmd/ds3sql/query.go`, change the request path from `/query` to `/jobs` and unwrap the job envelope. Replace:
```go
		body, _ := json.Marshal(map[string]string{"sql": sql})

		path := "/query"
		if p, _ := cmd.Flags().GetString("project"); p != "" {
			path += "?project=" + p
		}
		data, err := authedPost(cfg, path, body)
		if err != nil {
			return err
		}

		var result struct {
			Columns   []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"columns"`
			Rows      [][]any `json:"rows"`
			RowCount  int     `json:"row_count"`
			ElapsedMs int64   `json:"elapsed_ms"`
			Error     string  `json:"error"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		if result.Error != "" {
			return fmt.Errorf("query error: %s", result.Error)
		}
```
with:
```go
		body, _ := json.Marshal(map[string]string{"sql": sql})

		path := "/jobs"
		if p, _ := cmd.Flags().GetString("project"); p != "" {
			path += "?project=" + p
		}
		data, err := authedPost(cfg, path, body)
		if err != nil {
			return err
		}

		// /jobs returns a Job envelope; the result is nested under "result".
		var jobResp struct {
			Status string `json:"status"`
			Error  string `json:"error"`
			Result *struct {
				Columns []struct {
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"columns"`
				Rows      [][]any `json:"rows"`
				RowCount  int     `json:"row_count"`
				ElapsedMs int64   `json:"elapsed_ms"`
			} `json:"result"`
		}
		if err := json.Unmarshal(data, &jobResp); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if jobResp.Error != "" {
			return fmt.Errorf("query error: %s", jobResp.Error)
		}
		if jobResp.Result == nil {
			return fmt.Errorf("query returned no result")
		}
		result := *jobResp.Result
```
The remaining rendering code (which references `result.Columns`, `result.Rows`, `result.RowCount`, `result.ElapsedMs`) works unchanged because `result` now has the same field names.

For the `--json` branch, the printed JSON is now the job envelope. That is acceptable and documented; the rendering path below it is unaffected.

- [ ] **Step 2: Build the CLI**

Run: `go build ./cmd/ds3sql/`
Expected: builds with no error.

- [ ] **Step 3: Build everything and run all tests**

Run:
```bash
go build ./...
go test ./...
```
Expected: all build; all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/ds3sql/query.go
git commit -m "feat(cli): route query through job API (catalog-aware)"
```

---

## Task 18: Manual end-to-end verification + docs + final commit

**Files:**
- Modify: `docs/api.md`, `docs/cli.md`, `docs/architecture.md`, `docs/configuration.md`, `README.md`

- [ ] **Step 1: Full build, vet, and test**

Run:
```bash
go build ./...
go vet ./...
go test -race ./...
```
Expected: no build errors, no vet errors, all tests PASS.

- [ ] **Step 2: Manual end-to-end smoke test against local files**

This confirms `SELECT FROM dataset.table` end-to-end without needing S3, by registering a `file://`-style local path as an external table. Run:
```bash
cd "/Users/esignoretti/Documents/OpenCode/DS3-SQL Server"
cat > /tmp/ds3sql_e2e_test.go <<'EOF'
package main
EOF
rm /tmp/ds3sql_e2e_test.go
# Verify the binaries build and the server starts/stops cleanly:
go build -o /tmp/ds3sql-server ./cmd/ds3sql-server/
DS3SQL_METASTORE_PATH=/tmp/ds3sql-e2e-meta.db DS3SQL_ROLE=all /tmp/ds3sql-server --port 18080 &
SERVER_PID=$!
sleep 1
curl -s http://localhost:18080/health
kill $SERVER_PID
rm -f /tmp/ds3sql-e2e-meta.db
```
Expected: `/health` returns `{"status":"ok","pool_size":...}`; server shuts down on kill.

> The authenticated catalog/query path requires a live Cubbit IAM login and DS3 buckets, so full credentialed E2E is exercised by the automated `internal/job` `LocalExecutor` test (Task 9), which already proves `SELECT FROM dataset.table` resolves and executes over local files. This step only verifies the wired server boots and serves health.

- [ ] **Step 3: Update `docs/api.md`**

Add a "Datasets & Tables" section documenting:
- `POST /datasets` — body `{"name":"sales"}`, returns 201 `{"name":"sales"}`.
- `GET /datasets` — returns `{"datasets":[{"project_id","name","created_at"}]}`.
- `POST /datasets/{dataset}/tables` — body `{"name","location","format","storage_class","partition_columns"}`, returns 201 with the full table (schema + stats).
- `GET /datasets/{dataset}/tables` — returns `{"tables":[…]}`.
- `GET /datasets/{dataset}/tables/{table}` — returns the table (schema + stats), 404 if absent.
- `DELETE /datasets/{dataset}/tables/{table}` — 204 on success.

Add a "Jobs" section:
- `POST /jobs` — body `{"sql":"…"}`. Runs synchronously (Phase 1 fast-path). Returns a Job `{"id","type":"query","sql","status":"done|failed","result":{columns,rows,row_count,elapsed_ms},"error"}`.
- `GET /jobs/{id}` — returns the Job, 404 if unknown.
- Note: `POST /query` remains as a raw (non-catalog) endpoint for back-compat.

- [ ] **Step 4: Update `docs/cli.md`**

Document the new commands:
- `ds3sql datasets ls` / `ds3sql datasets create <name>`
- `ds3sql tables ls <dataset>`
- `ds3sql tables register <dataset.table> --location <s3://…> --format <parquet|csv|tsv|json> [--storage-class ssd|hdd] [--partition-by col1,col2]`
- `ds3sql tables describe <dataset.table>`
- `ds3sql tables drop <dataset.table>`
- Note that `ds3sql query "SELECT … FROM dataset.table"` now resolves catalog tables (routed through `/jobs`).

- [ ] **Step 5: Update `docs/architecture.md`**

Add a "Catalog & Roles (Phase 1)" subsection: the `metastore` Store interface (embedded SQLite default), the `catalog` service (datasets, external tables, schema/stats, `Resolve`), the `job` manager + `Executor` seam + `LocalExecutor`, the `query.QueryView` view-registration mechanism, and the `--role` flag (currently all roles wire the in-process server; the `Executor` interface is the Phase 2 remote-worker seam).

- [ ] **Step 6: Update `docs/configuration.md` and `README.md`**

In `docs/configuration.md`, document the new config + env vars:
- `role` (`DS3SQL_ROLE`) — `coordinator | worker | all`, default `all`.
- `metastore.path` (`DS3SQL_METASTORE_PATH`) — default `~/.ds3sql/metastore.db`.

In `README.md`, add a short "Catalog" subsection to Quick Start showing:
```bash
ds3sql datasets create sales
ds3sql tables register sales.orders --location 's3://my-bucket/orders/*.parquet' --format parquet
ds3sql query "SELECT count(*) FROM sales.orders"
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
git commit -m "docs: document catalog, jobs, roles for Phase 1"
```

---

## Self-Review

**Spec coverage (Phase 1 scope):**
- `metastore` interface + embedded SQLite → Tasks 1–3. ✓
- `catalog` (datasets/tables incl. external, schema/stats) → Tasks 6–7. ✓
- Job manager with synchronous fast-path only → Tasks 8–9, 13. ✓ (async/polling/history are Phase 2 — correctly excluded.)
- `--role=coordinator|worker|all` flag → Task 14 (flag + validation + Executor seam). ✓
- `SELECT FROM dataset.table` end-to-end on one node → proven by Task 9 (LocalExecutor) and wired in Task 14; CLI in Task 17. ✓
- Pluggable store seam (Postgres later) → `metastore.Store` interface (Task 1). ✓
- Distributed-later seam → `query.ViewBinding`/`QueryView` plan + `job.Executor` interface. ✓

**Type consistency check:**
- `metastore.Store` methods (Task 1) match the SQLite implementation signatures (Tasks 2–3) and the `catalog.Service` calls (Tasks 6–7). ✓
- `query.ViewBinding{Schema,Name,ReaderSQL}` (Task 5) is produced by `catalog.Resolve` (Task 7) and consumed by `job.LocalExecutor` (Task 9). ✓
- `job.ExecRequest{SQL,ProjectID,AccessKey,SecretKey,Endpoint}` (Task 8) is built identically in `JobHandler.SubmitWithCreds` (Task 13) and `Manager.Run` (Task 8). ✓
- `catalog.SchemaInferer` interface (Task 6) is satisfied by `*query.Engine` (`InferSchema` exists in `schema.go`; `QueryView` added in Task 5). ✓
- Handler `…ForProject` / `…WithCreds` convention matches the existing `QueryHandler.QueryWithCreds` pattern and the `main.go` session-extraction style (Task 14). ✓
- `authedGet`/`authedPost` exist (`status.go`); `authedDelete` added in Task 16. ✓

**Placeholder scan:** No TBD/TODO/"handle errors appropriately"/"similar to" placeholders; every code step contains complete code. ✓

**Note on deferred-but-defined:** `metastore.BumpDataVersion` is defined and tested in Phase 1 though first *used* by writes in Phase 3 — included because `data_version` is a stored column and the method documents the invalidation seam; it is covered by `TestTableCRUD`, so it is not dead/untested code.
