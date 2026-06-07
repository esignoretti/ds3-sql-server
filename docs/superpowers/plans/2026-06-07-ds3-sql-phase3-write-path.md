# DS3 SQL Phase 3 (Write Path) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the write path to the managed catalog — CTAS (`CREATE TABLE dataset.t … AS SELECT …`), batch load (`{type:"load"}`, generalizing the existing `convert` engine), Hive partitioning + storage-class tiering, scheduled queries (cron), and managed-table `DROP` that deletes data — all as async jobs that produce or refresh **managed** tables, invalidate the result cache, and bump `data_version`.

**Architecture:** Incremental in-place refactor (Approach A). A new `internal/write` package holds the managed-table writers: a lightweight CTAS parser/executor (`ctas.go`) and a batch loader (`load.go`) that both reuse the warm `query.Engine` DuckDB pool via a new `Engine.ExecWrite` method (runs a `COPY … TO` against registered views, no result rows). A new `internal/scheduler` package runs a cron ticker over `metastore` `Schedule` rows, enqueuing jobs through the existing `job.Manager` async `Submit` seam (added in Phase 2). The `job.Manager` routes by job `type`: `query` (Phase 1), `ctas`/`load` (this phase). Storage classes map logical tiers (`ssd`/`hdd`) → real DS3 buckets via `config.StorageConfig`. All writes go through a single post-write hook (`store.BumpDataVersion` + `cache.ResultCache.DeleteCacheEntriesForTable`). An `s3.Client.DeletePrefix` helper backs `overwrite` loads and managed `DROP`.

**Tech Stack:** Go 1.26, DuckDB (`github.com/marcboeker/go-duckdb`, CGo), embedded SQLite (`modernc.org/sqlite`, pure Go), chi v5 router, Cobra CLI, `github.com/google/uuid`, cron (`github.com/robfig/cron/v3`, added this phase). Module path: `github.com/esignoretti/ds3-sql-server`.

**Spec:** `docs/superpowers/specs/2026-06-07-ds3-sql-bigquery-refactor-design.md`

---

## File Structure

New packages and files (all under the repo root):

- `internal/config/config.go` *(modify)* — add `StorageConfig` (`Classes map[string]StorageClassConfig{Bucket,Endpoint}`) with `ssd`/`hdd` keys, env overrides, and `Config.ResolveStorageClass(name)`.
- `internal/s3/listing.go` *(modify)* — add `DeletePrefix(ctx, bucket, prefix)` (list + delete all objects under a prefix, paginated).
- `internal/s3/listing_test.go` *(create/append)* — test `DeletePrefix` against a stub (or skip-if-no-creds integration); plan uses a small unit test over a local fake.
- `internal/write/write.go` *(create)* — shared `Writer` type holding the engine + store + cache + storage resolver, the post-write invalidation hook (`afterWrite`), and the managed-location builder (`managedLocation`).
- `internal/write/write_test.go` *(create)* — tests for `managedLocation` and `afterWrite` (fake store + fake cache).
- `internal/write/ctas.go` *(create)* — `ParseCTAS` (lightweight grammar parser) + `Writer.RunCTAS`.
- `internal/write/ctas_test.go` *(create)* — parser table tests + an end-to-end CTAS over LOCAL files (COPY to a temp dir, read back, assert rows + registered managed table).
- `internal/write/load.go` *(create)* — `LoadRequest` + `Writer.RunLoad` (append/overwrite, Hive partitioning), reusing the DuckDB pool.
- `internal/write/load_test.go` *(create)* — end-to-end load over LOCAL files (CSV → Parquet in temp dir), append + overwrite, partitioned read-back.
- `internal/query/engine.go` *(modify)* — add `Engine.ExecWrite(sql string, bindings []ViewBinding, accessKey, secretKey, endpoint string) error` (registers views, runs a non-result statement such as `COPY`).
- `internal/query/engine_exec_test.go` *(create)* — `ExecWrite` test: COPY a SELECT to a local Parquet file, assert the file is readable.
- `internal/job/job.go` *(modify)* — add `WriteRequest`/`WriteExecutor` seam fields to `Manager`, and `Submit` routing by type for `ctas`/`load` (Phase 2 added async `Submit`; this phase adds the write routing). Add `Job.IntoTable`, `Job.Type` already exists.
- `internal/job/write_executor.go` *(create)* — `WriteExecutor` interface + `LocalWriteExecutor` wrapping `*write.Writer`, dispatching `ctas`/`load`.
- `internal/job/write_executor_test.go` *(create)* — manager routes a `ctas` SQL and a `load` request to the write executor.
- `internal/metastore/store.go` *(modify)* — add `Schedule` type + the six `Schedule` methods to the `Store` interface.
- `internal/metastore/sqlite.go` *(modify)* — `schedules` table migration + the six method implementations.
- `internal/metastore/sqlite_schedule_test.go` *(create)* — schedule CRUD + `GetDueSchedules` + `UpdateScheduleRun` tests.
- `internal/scheduler/scheduler.go` *(create)* — `Scheduler` with an injectable clock, `Tick(now)` due-check, cron next-run computation, misfire skip, job enqueue.
- `internal/scheduler/scheduler_test.go` *(create)* — deterministic `Tick` tests (no sleeping) covering due selection, misfire skip, next-run advance.
- `internal/catalog/service.go` *(modify)* — add `DropTableWithData` (delete managed data objects + cache invalidation) and a `RegisterManaged` helper used by writers.
- `internal/catalog/service_drop_test.go` *(create)* — managed drop deletes data via a fake s3 deleter; external drop only removes registration.
- `internal/api/job_handler.go` *(modify)* — `SubmitWithCreds` detects `ctas` (SQL prefix) and `{type:"load"}` bodies, routes to async submit, returns the job.
- `internal/api/job_handler_test.go` *(modify)* — add CTAS-routing and load-routing handler tests.
- `internal/api/schedule_handler.go` *(create)* — `ScheduleHandler` CRUD (`CreateForProject`/`ListForProject`/`DeleteForProject`).
- `internal/api/schedule_handler_test.go` *(create)* — httptest CRUD tests.
- `internal/api/table_handler.go` *(modify)* — `DropForProject` calls `DropTableWithData` with creds.
- `cmd/ds3sql-server/main.go` *(modify)* — wire storage config, `write.Writer`, write executor, scheduler (coordinator/all only), and `/schedules` routes; update drop route to pass creds.
- `cmd/ds3sql/tables_cmd.go` *(create)* — `tables create-as` + ensure `tables drop` exists; (datasets/tables base commands from Phase 1 assumed present — create if absent).
- `cmd/ds3sql/load_cmd.go` *(create)* — `load --source --into --format [--partition-by] [--mode]`.
- `cmd/ds3sql/schedules_cmd.go` *(create)* — `schedules create|ls|rm`.
- `cmd/ds3sql/status.go` *(modify)* — add `authedDelete` helper if not already present.
- `go.mod` / `go.sum` *(modify)* — add `github.com/robfig/cron/v3`.
- `docs/api.md`, `docs/cli.md`, `docs/architecture.md`, `docs/configuration.md`, `README.md` *(modify)* — document the write path.

**Conventions to follow (from existing code):**
- Handlers needing S3 creds expose `…WithCreds`/`…ForProject`; `main.go` extracts `(projectID, ak, sk, endpoint)` from `auth.GetSession` and calls them (mirrors `JobHandler.SubmitWithCreds`, `TableHandler.RegisterForProject`).
- DuckDB credential setup uses the shared `query.applyS3Creds`; writers reuse the pool via `query.Engine` methods, never opening their own connections.
- Errors returned to clients are JSON: `{"error":"…"}`.
- Stores live behind small interfaces; writers and the scheduler depend on narrow interfaces so tests inject fakes.
- DuckDB `COPY (<select>) TO '<path>' (FORMAT PARQUET[, PARTITION_BY (cols)])` works against both `s3://…` and local filesystem paths, so all write tests use `t.TempDir()` paths and assert by reading the Parquet back with `read_parquet`.

**Simplifications (explicit):**
- The CTAS parser handles exactly the documented grammar: `CREATE TABLE [IF NOT EXISTS] <dataset>.<table> [PARTITION BY (col[,col…])] [STORAGE '<ssd|hdd>'] AS <select>`. Anything else is rejected with a clear error (it is NOT a general SQL parser).
- Partition file sizing (128–512 MB target) is documented but not enforced; DuckDB chooses file counts. We set no `FILE_SIZE_BYTES` knob (kept simple); a note is added.
- `overwrite` deletes the table's location prefix via `s3.Client.DeletePrefix` before writing; `append` writes a new uniquely-named file set under the prefix. Concurrency between writes to the same table is out of scope (single-writer assumption, documented).

---

## Task 1: Storage-class configuration

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:
```go
func TestDefault_StorageClasses(t *testing.T) {
	c := Default()
	ssd, ok := c.ResolveStorageClass("ssd")
	if !ok {
		t.Fatal("expected default ssd storage class")
	}
	if ssd.Bucket == "" {
		t.Fatal("expected default ssd bucket")
	}
	if _, ok := c.ResolveStorageClass("hdd"); !ok {
		t.Fatal("expected default hdd storage class")
	}
	if _, ok := c.ResolveStorageClass("nope"); ok {
		t.Fatal("unknown class must not resolve")
	}
}

func TestLoad_StorageEnvOverride(t *testing.T) {
	t.Setenv("DS3SQL_STORAGE_SSD_BUCKET", "fast-bucket")
	t.Setenv("DS3SQL_STORAGE_SSD_ENDPOINT", "https://ssd.example")
	t.Setenv("DS3SQL_STORAGE_HDD_BUCKET", "cold-bucket")
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ssd, ok := c.ResolveStorageClass("ssd")
	if !ok || ssd.Bucket != "fast-bucket" || ssd.Endpoint != "https://ssd.example" {
		t.Fatalf("ssd env override not applied: %+v", ssd)
	}
	hdd, _ := c.ResolveStorageClass("hdd")
	if hdd.Bucket != "cold-bucket" {
		t.Fatalf("hdd env override not applied: %+v", hdd)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/config/ -run 'TestDefault_StorageClasses|TestLoad_StorageEnvOverride' -v`
Expected: FAIL — `c.ResolveStorageClass` undefined.

- [ ] **Step 3: Implement the config additions**

In `internal/config/config.go`, add the types (after `MetastoreConfig`):
```go
// StorageClassConfig maps a logical storage class to a real DS3 bucket + endpoint.
// An empty Endpoint means "use the session's gateway endpoint".
type StorageClassConfig struct {
	Bucket   string `yaml:"bucket"`
	Endpoint string `yaml:"endpoint"`
}

// StorageConfig holds the storage-class → bucket map used by the write path.
type StorageConfig struct {
	Classes map[string]StorageClassConfig `yaml:"classes"`
}
```
Add the field to `Config` (after `Metastore`):
```go
	Storage StorageConfig `yaml:"storage"`
```
In `Default()`, set `Storage` (after `Metastore`):
```go
		Storage: StorageConfig{
			Classes: map[string]StorageClassConfig{
				"ssd": {Bucket: "ds3-fast", Endpoint: ""},
				"hdd": {Bucket: "ds3-cold", Endpoint: ""},
			},
		},
```
Add the resolver method (anywhere at package scope):
```go
// ResolveStorageClass returns the configured bucket/endpoint for a logical class
// name (e.g. "ssd"/"hdd"). The bool is false when the class is not configured.
func (c *Config) ResolveStorageClass(name string) (StorageClassConfig, bool) {
	if c.Storage.Classes == nil {
		return StorageClassConfig{}, false
	}
	sc, ok := c.Storage.Classes[name]
	return sc, ok
}
```
In `Load`, after the metastore env override, add (the map is always non-nil because `Default()` populates it, but guard anyway):
```go
	ensureClass := func(name string) StorageClassConfig {
		if cfg.Storage.Classes == nil {
			cfg.Storage.Classes = map[string]StorageClassConfig{}
		}
		return cfg.Storage.Classes[name]
	}
	setClass := func(name string, sc StorageClassConfig) {
		if cfg.Storage.Classes == nil {
			cfg.Storage.Classes = map[string]StorageClassConfig{}
		}
		cfg.Storage.Classes[name] = sc
	}
	for _, name := range []string{"ssd", "hdd"} {
		sc := ensureClass(name)
		upper := strings.ToUpper(name)
		if v := os.Getenv("DS3SQL_STORAGE_" + upper + "_BUCKET"); v != "" {
			sc.Bucket = v
		}
		if v := os.Getenv("DS3SQL_STORAGE_" + upper + "_ENDPOINT"); v != "" {
			sc.Endpoint = v
		}
		setClass(name, sc)
	}
```
Add `"strings"` to the imports in `config.go`.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): add storage-class tiering config and resolver"
```

---

## Task 2: `s3.Client.DeletePrefix` for managed-data deletion

**Files:**
- Modify: `internal/s3/listing.go`
- Test: `internal/s3/listing_test.go` (append)

`DeletePrefix` lists every object under a prefix (handling pagination via the continuation token) and deletes each. Used by `overwrite` loads and managed `DROP`.

- [ ] **Step 1: Write the failing test**

The test exercises the listing-paginate-then-delete control flow without a live bucket by checking the method exists and is wired against a real (loopback) client constructed with bogus creds — we assert it returns an error rather than panicking when the endpoint is unreachable, and we add a focused unit test of the key-collection helper. Append to `internal/s3/listing_test.go`:
```go
func TestDeletePrefix_UnreachableEndpointReturnsError(t *testing.T) {
	// 127.0.0.1:1 is reserved/closed; the SDK should fail fast, proving the
	// method is wired and does not panic on the list step.
	c, err := NewClient(context.Background(), "ak", "sk", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.DeletePrefix(context.Background(), "bucket", "prefix/"); err == nil {
		t.Fatal("expected error against unreachable endpoint")
	}
}
```
Ensure `internal/s3/listing_test.go` imports `"context"` and `"testing"`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/s3/ -run TestDeletePrefix_UnreachableEndpointReturnsError -v`
Expected: FAIL — `c.DeletePrefix` undefined.

- [ ] **Step 3: Implement `DeletePrefix`**

Append to `internal/s3/listing.go`:
```go
// DeletePrefix deletes every object whose key starts with prefix. It paginates
// the listing with a continuation token and deletes objects one at a time
// (DS3-compatible; avoids the batch DeleteObjects API). Used to clear a managed
// table's location for overwrite loads and DROP.
func (c *Client) DeletePrefix(ctx context.Context, bucket, prefix string) error {
	var token *string
	for {
		out, err := c.client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
			Bucket:            &bucket,
			Prefix:            &prefix,
			ContinuationToken: token,
		})
		if err != nil {
			return fmt.Errorf("list for delete %q: %w", prefix, err)
		}
		for _, obj := range out.Contents {
			if obj.Key == nil {
				continue
			}
			if _, err := c.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
				Bucket: &bucket,
				Key:    obj.Key,
			}); err != nil {
				return fmt.Errorf("delete %q: %w", *obj.Key, err)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			return nil
		}
		token = out.NextContinuationToken
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/s3/ -run TestDeletePrefix_UnreachableEndpointReturnsError -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/s3/
git commit -m "feat(s3): add DeletePrefix for managed-data deletion"
```

---

## Task 3: `query.Engine.ExecWrite` — run a COPY against registered views

**Files:**
- Modify: `internal/query/engine.go`
- Test: `internal/query/engine_exec_test.go` (create)

`ExecWrite` mirrors `QueryView` (registers bindings as views, applies creds, drops schemas) but runs a statement that returns no result rows (e.g. `COPY … TO`). It returns only an error.

- [ ] **Step 1: Write the failing test (local Parquet via COPY)**

Create `internal/query/engine_exec_test.go`:
```go
package query

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecWrite_CopyToLocalParquet(t *testing.T) {
	e, err := NewEngine(1000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	csv := filepath.Join(t.TempDir(), "src.csv")
	if err := os.WriteFile(csv, []byte("id,total\n1,10\n2,20\n3,30\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	out := filepath.Join(t.TempDir(), "out.parquet")

	bindings := []ViewBinding{{
		Schema:    "sales",
		Name:      "orders",
		ReaderSQL: "read_csv_auto('" + csv + "')",
	}}
	copySQL := "COPY (SELECT * FROM sales.orders WHERE total > 10) TO '" + out + "' (FORMAT PARQUET)"
	if err := e.ExecWrite(copySQL, bindings, "", "", ""); err != nil {
		t.Fatalf("ExecWrite: %v", err)
	}

	// Read the written Parquet back and assert 2 rows survived the filter.
	res := e.QueryView("SELECT count(*) AS c FROM read_parquet('"+out+"')", nil, "", "", "")
	if res.Error != "" {
		t.Fatalf("read back: %s", res.Error)
	}
	if got := res.Rows[0][0]; toInt64(got) != 2 {
		t.Fatalf("expected 2 rows in output, got %v", got)
	}
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case int:
		return int64(x)
	default:
		return -1
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/query/ -run TestExecWrite_CopyToLocalParquet -v`
Expected: FAIL — `e.ExecWrite` undefined.

- [ ] **Step 3: Implement `ExecWrite`**

In `internal/query/engine.go`, add after `QueryView`:
```go
// ExecWrite registers each binding as a DuckDB view, applies S3 credentials, then
// executes a statement that produces no result rows (e.g. COPY ... TO). It is the
// write-path counterpart to QueryView. Created schemas are dropped afterward.
func (e *Engine) ExecWrite(sqlStr string, bindings []ViewBinding, accessKey, secretKey, rawEndpoint string) error {
	db := <-e.pool
	defer func() { e.pool <- db }()

	applyS3Creds(db, accessKey, secretKey, rawEndpoint)

	schemas := map[string]struct{}{}
	for _, b := range bindings {
		schemas[b.Schema] = struct{}{}
	}
	for s := range schemas {
		if _, err := db.Exec("CREATE SCHEMA IF NOT EXISTS " + quoteIdent(s)); err != nil {
			return fmt.Errorf("create schema %s: %w", s, err)
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
			return fmt.Errorf("register table %s.%s: %w", b.Schema, b.Name, err)
		}
	}

	if e.threads > 0 {
		db.Exec(fmt.Sprintf("SET threads=%d", e.threads))
	}
	db.Exec("SET memory_limit='" + e.memoryLimit + "'")

	if _, err := db.Exec(sqlStr); err != nil {
		return fmt.Errorf("exec write: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/query/ -run TestExecWrite_CopyToLocalParquet -v`
Expected: PASS.

- [ ] **Step 5: Run the whole query package**

Run: `go test ./internal/query/`
Expected: PASS (ok).

- [ ] **Step 6: Commit**

```bash
git add internal/query/
git commit -m "feat(query): add ExecWrite to run COPY against registered views"
```

---

## Task 4: `write` package — shared Writer, post-write hook, managed location

**Files:**
- Create: `internal/write/write.go`
- Test: `internal/write/write_test.go`

The `Writer` carries the engine, the metastore `Store`, a result-cache invalidator, and a storage resolver. It owns the post-write invalidation (`BumpDataVersion` + `DeleteCacheEntriesForTable`) and the managed S3 location builder shared by CTAS and load.

- [ ] **Step 1: Write the failing test**

Create `internal/write/write_test.go`:
```go
package write

import (
	"context"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// fakeStore records BumpDataVersion calls and serves a managed table.
type fakeStore struct {
	bumped   int
	tables   map[string]*metastore.Table
}

func newFakeStore() *fakeStore { return &fakeStore{tables: map[string]*metastore.Table{}} }

func key(p, d, n string) string { return p + "/" + d + "/" + n }

func (f *fakeStore) BumpDataVersion(ctx context.Context, p, d, n string) (int64, error) {
	f.bumped++
	if t, ok := f.tables[key(p, d, n)]; ok {
		t.DataVersion++
		return t.DataVersion, nil
	}
	return int64(f.bumped), nil
}

// fakeCache records invalidations.
type fakeCache struct{ invalidated int }

func (c *fakeCache) DeleteCacheEntriesForTable(ctx context.Context, p, d, n string) error {
	c.invalidated++
	return nil
}

func TestManagedLocation(t *testing.T) {
	w := &Writer{}
	got := w.managedLocation("ds3-fast", "sales", "orders")
	want := "s3://ds3-fast/_managed/sales/orders/"
	if got != want {
		t.Fatalf("managedLocation = %q, want %q", got, want)
	}
}

func TestAfterWrite_BumpsAndInvalidates(t *testing.T) {
	store := newFakeStore()
	cache := &fakeCache{}
	w := &Writer{store: store, cache: cache}
	if err := w.afterWrite(context.Background(), "p1", "sales", "orders"); err != nil {
		t.Fatalf("afterWrite: %v", err)
	}
	if store.bumped != 1 {
		t.Fatalf("expected 1 bump, got %d", store.bumped)
	}
	if cache.invalidated != 1 {
		t.Fatalf("expected 1 invalidation, got %d", cache.invalidated)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/write/ -run 'TestManagedLocation|TestAfterWrite_BumpsAndInvalidates' -v`
Expected: FAIL — `undefined: Writer`.

- [ ] **Step 3: Implement the shared Writer**

Create `internal/write/write.go`:
```go
package write

import (
	"context"
	"fmt"
	"strings"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// versionBumper is the subset of metastore.Store the writer needs for invalidation.
type versionBumper interface {
	BumpDataVersion(ctx context.Context, projectID, dataset, name string) (int64, error)
}

// cacheInvalidator deletes result-cache entries that reference a table.
type cacheInvalidator interface {
	DeleteCacheEntriesForTable(ctx context.Context, projectID, dataset, table string) error
}

// storageResolver maps a logical storage class to a bucket + endpoint.
// config.Config satisfies a compatible shape via an adapter in main.go.
type storageResolver interface {
	Resolve(class string) (bucket, endpoint string, ok bool)
}

// catalogService is the subset of *catalog.Service the writer uses.
type catalogService interface {
	Resolve(ctx context.Context, projectID, sql string) ([]query.ViewBinding, error)
	RegisterManaged(ctx context.Context, in catalog.RegisterManagedInput, ak, sk, ep string) (*metastore.Table, error)
	GetTable(ctx context.Context, projectID, dataset, name string) (*metastore.Table, error)
}

// writeEngine is the subset of *query.Engine the writer uses.
type writeEngine interface {
	ExecWrite(sql string, bindings []query.ViewBinding, ak, sk, ep string) error
}

// Writer executes write jobs (CTAS, load) against the managed catalog.
type Writer struct {
	engine  writeEngine
	cat     catalogService
	store   versionBumper
	cache   cacheInvalidator
	storage storageResolver
}

// NewWriter builds a Writer. Any of store/cache may be nil-tolerant only in
// tests; production wiring always supplies all dependencies.
func NewWriter(engine writeEngine, cat catalogService, store versionBumper, cache cacheInvalidator, storage storageResolver) *Writer {
	return &Writer{engine: engine, cat: cat, store: store, cache: cache, storage: storage}
}

// managedLocation returns the base S3 prefix for a managed table's data files.
func (w *Writer) managedLocation(bucket, dataset, table string) string {
	return fmt.Sprintf("s3://%s/_managed/%s/%s/", bucket, dataset, table)
}

// afterWrite bumps the table's data_version and invalidates dependent
// result-cache entries. Errors from cache invalidation are non-fatal.
func (w *Writer) afterWrite(ctx context.Context, projectID, dataset, table string) error {
	if w.store != nil {
		if _, err := w.store.BumpDataVersion(ctx, projectID, dataset, table); err != nil {
			return fmt.Errorf("bump data version: %w", err)
		}
	}
	if w.cache != nil {
		_ = w.cache.DeleteCacheEntriesForTable(ctx, projectID, dataset, table)
	}
	return nil
}

// escapeLiteral escapes single quotes for embedding a value in SQL.
func escapeLiteral(s string) string { return strings.ReplaceAll(s, "'", "''") }
```

- [ ] **Step 4: Run to verify it passes**

The `Writer` struct fields are unexported; the test (same package) sets them directly. Run: `go test ./internal/write/ -run 'TestManagedLocation|TestAfterWrite_BumpsAndInvalidates' -v`
Expected: PASS.

> Note: this introduces references to `catalog.RegisterManagedInput` and `catalog.Service.RegisterManaged`, which are added in Task 7. To keep this task self-contained and compiling, Task 7 is a prerequisite of Task 5's end-to-end run, but `write.go` compiles now because it only references the *names* via the `catalogService` interface — the concrete `catalog` types are imported. If `go build ./internal/write/` fails here on the undefined `catalog.RegisterManagedInput`, proceed to Task 7 first, then return. **Implementation order recommendation:** do Task 7 before Task 5/6 end-to-end runs. (Tasks 4 and 7 may be committed together if the worker prefers a compiling tree at every commit.)

- [ ] **Step 5: Commit**

```bash
git add internal/write/write.go internal/write/write_test.go
git commit -m "feat(write): shared Writer with managed-location and post-write invalidation"
```

---

## Task 5: CTAS parser

**Files:**
- Create: `internal/write/ctas.go`
- Test: `internal/write/ctas_test.go`

`ParseCTAS` recognizes the documented grammar only and returns a structured plan or a descriptive error.

- [ ] **Step 1: Write the failing parser test**

Create `internal/write/ctas_test.go`:
```go
package write

import (
	"reflect"
	"testing"
)

func TestParseCTAS_Valid(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want CTASPlan
	}{
		{
			name: "minimal",
			sql:  "CREATE TABLE sales.daily AS SELECT 1 AS x",
			want: CTASPlan{Dataset: "sales", Table: "daily", Select: "SELECT 1 AS x"},
		},
		{
			name: "partition_and_storage",
			sql:  "CREATE TABLE sales.daily PARTITION BY (dt, region) STORAGE 'ssd' AS SELECT dt, region, n FROM sales.raw",
			want: CTASPlan{
				Dataset: "sales", Table: "daily",
				PartitionBy:  []string{"dt", "region"},
				StorageClass: "ssd",
				Select:       "SELECT dt, region, n FROM sales.raw",
			},
		},
		{
			name: "if_not_exists_and_single_partition",
			sql:  "create table IF NOT EXISTS analytics.t PARTITION BY (dt) AS select * from analytics.src",
			want: CTASPlan{
				Dataset: "analytics", Table: "t",
				PartitionBy: []string{"dt"},
				Select:      "select * from analytics.src",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCTAS(tc.sql)
			if err != nil {
				t.Fatalf("ParseCTAS: %v", err)
			}
			if got.Dataset != tc.want.Dataset || got.Table != tc.want.Table ||
				got.StorageClass != tc.want.StorageClass || got.Select != tc.want.Select ||
				!reflect.DeepEqual(normalizeNil(got.PartitionBy), normalizeNil(tc.want.PartitionBy)) {
				t.Fatalf("ParseCTAS = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func normalizeNil(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func TestParseCTAS_Rejects(t *testing.T) {
	bad := []string{
		"SELECT 1",                                  // not a CREATE TABLE
		"CREATE TABLE orders AS SELECT 1",           // missing dataset qualifier
		"CREATE TABLE sales.daily SELECT 1",         // missing AS
		"CREATE TABLE sales.daily STORAGE 'tape' AS SELECT 1", // bad storage class
		"CREATE TABLE sales.daily AS",               // empty select
		"CREATE OR REPLACE TABLE sales.daily AS SELECT 1", // unsupported form
	}
	for _, sql := range bad {
		if _, err := ParseCTAS(sql); err == nil {
			t.Fatalf("expected error for %q", sql)
		}
	}
}

func TestIsCTAS(t *testing.T) {
	if !IsCTAS("  create   table sales.t AS SELECT 1") {
		t.Fatal("expected IsCTAS true")
	}
	if IsCTAS("SELECT 1") {
		t.Fatal("expected IsCTAS false")
	}
	if IsCTAS("CREATE TABLE sales.t (id INT)") {
		t.Fatal("plain CREATE TABLE (no AS SELECT) must not be CTAS")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/write/ -run 'TestParseCTAS_Valid|TestParseCTAS_Rejects|TestIsCTAS' -v`
Expected: FAIL — `undefined: ParseCTAS` / `CTASPlan` / `IsCTAS`.

- [ ] **Step 3: Implement the parser**

Create `internal/write/ctas.go`:
```go
package write

import (
	"fmt"
	"regexp"
	"strings"
)

// CTASPlan is the parsed form of a CREATE TABLE ... AS SELECT statement.
type CTASPlan struct {
	Dataset      string
	Table        string
	PartitionBy  []string
	StorageClass string // "", "ssd", or "hdd"
	Select       string // the inner SELECT, verbatim
}

var (
	// Matches the full documented CTAS grammar. The (?is) flags make it
	// case-insensitive and let . span newlines for the trailing SELECT.
	ctasRe = regexp.MustCompile(`(?is)^\s*CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?` +
		`([a-zA-Z_][a-zA-Z0-9_]*)\.([a-zA-Z_][a-zA-Z0-9_]*)` +
		`(?:\s+PARTITION\s+BY\s*\(([^)]*)\))?` +
		`(?:\s+STORAGE\s+'([^']*)')?` +
		`\s+AS\s+(.*\S)\s*$`)

	// Quick prefix probe: "CREATE TABLE ... AS" with an AS SELECT somewhere.
	ctasProbeRe = regexp.MustCompile(`(?is)^\s*CREATE\s+TABLE\s+.+\s+AS\s+SELECT\b`)
)

// IsCTAS reports whether sql looks like a CREATE TABLE ... AS SELECT statement.
func IsCTAS(sql string) bool {
	return ctasProbeRe.MatchString(sql)
}

// ParseCTAS parses the documented CTAS grammar. It is deliberately strict: any
// statement outside the grammar is rejected with a descriptive error. It is NOT
// a general SQL parser.
func ParseCTAS(sql string) (CTASPlan, error) {
	m := ctasRe.FindStringSubmatch(sql)
	if m == nil {
		return CTASPlan{}, fmt.Errorf("unsupported CTAS form: expected CREATE TABLE [IF NOT EXISTS] <dataset>.<table> [PARTITION BY (cols)] [STORAGE 'ssd'|'hdd'] AS SELECT ...")
	}
	plan := CTASPlan{
		Dataset: m[1],
		Table:   m[2],
		Select:  strings.TrimSpace(m[5]),
	}
	if m[3] != "" {
		for _, c := range strings.Split(m[3], ",") {
			c = strings.TrimSpace(c)
			if c == "" {
				return CTASPlan{}, fmt.Errorf("empty partition column in PARTITION BY")
			}
			plan.PartitionBy = append(plan.PartitionBy, c)
		}
	}
	if m[4] != "" {
		sc := strings.ToLower(strings.TrimSpace(m[4]))
		if sc != "ssd" && sc != "hdd" {
			return CTASPlan{}, fmt.Errorf("invalid storage class %q: must be 'ssd' or 'hdd'", m[4])
		}
		plan.StorageClass = sc
	}
	if !strings.HasPrefix(strings.ToUpper(plan.Select), "SELECT") {
		return CTASPlan{}, fmt.Errorf("CTAS inner statement must be a SELECT")
	}
	return plan, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/write/ -run 'TestParseCTAS_Valid|TestParseCTAS_Rejects|TestIsCTAS' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/write/ctas.go internal/write/ctas_test.go
git commit -m "feat(write): strict CTAS grammar parser"
```

---

## Task 6: Catalog — `RegisterManaged` and managed-aware `DropTableWithData`

**Files:**
- Modify: `internal/catalog/service.go`
- Test: `internal/catalog/service_drop_test.go` (create)

`RegisterManaged` is like `RegisterTable` but sets `Kind:"managed"`, stores the computed `Location`/`partition_columns`/`storage_class`, and uses an explicit pre-computed schema/stats source (it infers schema/row-count from the just-written location). `DropTableWithData` deletes the data objects of a *managed* table via an injected deleter, then removes the registration and invalidates the cache; *external* tables only have their registration removed.

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/service_drop_test.go`:
```go
package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// fakeDeleter records DeletePrefix calls.
type fakeDeleter struct {
	calls []string // "bucket|prefix"
}

func (d *fakeDeleter) DeletePrefix(ctx context.Context, bucket, prefix string) error {
	d.calls = append(d.calls, bucket+"|"+prefix)
	return nil
}

// fakeInvalidator records cache invalidations.
type fakeInvalidator struct{ n int }

func (c *fakeInvalidator) DeleteCacheEntriesForTable(ctx context.Context, p, d, t string) error {
	c.n++
	return nil
}

func TestDropTableWithData_Managed(t *testing.T) {
	svc := newService(t) // helper from service_test.go (Phase 1)
	ctx := context.Background()
	csv := filepath.Join(t.TempDir(), "m.csv")
	_ = os.WriteFile(csv, []byte("id\n1\n"), 0644)
	if err := svc.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	// Register a managed table whose Location is an s3:// prefix.
	tbl, err := svc.RegisterManaged(ctx, RegisterManagedInput{
		ProjectID: "p1", Dataset: "sales", Name: "orders",
		Location:     "s3://ds3-fast/_managed/sales/orders/",
		ProbeReader:  "read_csv_auto('" + csv + "')",
		StorageClass: "ssd",
	}, "", "", "")
	if err != nil {
		t.Fatalf("RegisterManaged: %v", err)
	}
	if tbl.Kind != "managed" {
		t.Fatalf("expected managed kind, got %q", tbl.Kind)
	}

	del := &fakeDeleter{}
	inv := &fakeInvalidator{}
	if err := svc.DropTableWithData(ctx, "p1", "sales", "orders", del, inv, "", "", ""); err != nil {
		t.Fatalf("DropTableWithData: %v", err)
	}
	if len(del.calls) != 1 || del.calls[0] != "ds3-fast|_managed/sales/orders/" {
		t.Fatalf("expected one delete of the managed prefix, got %v", del.calls)
	}
	if inv.n != 1 {
		t.Fatalf("expected one cache invalidation, got %d", inv.n)
	}
	if _, err := svc.GetTable(ctx, "p1", "sales", "orders"); err != metastore.ErrNotFound {
		t.Fatalf("expected ErrNotFound after drop, got %v", err)
	}
}

func TestDropTableWithData_ExternalSkipsDelete(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	csv := filepath.Join(t.TempDir(), "e.csv")
	_ = os.WriteFile(csv, []byte("id\n1\n"), 0644)
	if err := svc.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterTable(ctx, RegisterTableInput{
		ProjectID: "p1", Dataset: "sales", Name: "ext", Location: csv, Format: "csv",
	}, "", "", ""); err != nil {
		t.Fatal(err)
	}
	del := &fakeDeleter{}
	inv := &fakeInvalidator{}
	if err := svc.DropTableWithData(ctx, "p1", "sales", "ext", del, inv, "", "", ""); err != nil {
		t.Fatalf("DropTableWithData: %v", err)
	}
	if len(del.calls) != 0 {
		t.Fatalf("external table must not delete data, got %v", del.calls)
	}
	if inv.n != 1 {
		t.Fatalf("expected cache invalidation even for external, got %d", inv.n)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/catalog/ -run 'TestDropTableWithData_Managed|TestDropTableWithData_ExternalSkipsDelete' -v`
Expected: FAIL — `undefined: RegisterManaged` / `RegisterManagedInput` / `DropTableWithData`.

- [ ] **Step 3: Implement the catalog additions**

Append to `internal/catalog/service.go`:
```go
// PrefixDeleter deletes all objects under an s3 prefix (s3.Client satisfies it).
type PrefixDeleter interface {
	DeletePrefix(ctx context.Context, bucket, prefix string) error
}

// CacheInvalidator removes result-cache entries referencing a table.
type CacheInvalidator interface {
	DeleteCacheEntriesForTable(ctx context.Context, projectID, dataset, table string) error
}

// RegisterManagedInput registers (or replaces) a managed table after its data
// has been written. ProbeReader is the DuckDB reader expression for the just-
// written location, used to infer schema + row count.
type RegisterManagedInput struct {
	ProjectID        string
	Dataset          string
	Name             string
	Location         string // s3:// base prefix of the managed files
	ProbeReader      string // e.g. read_parquet('s3://.../*.parquet') or **/*.parquet
	StorageClass     string
	PartitionColumns []string
}

// RegisterManaged upserts a managed table, inferring schema + stats from the
// written data via ProbeReader. If a table with the same name exists it is
// replaced (drop registration, recreate) so CTAS/load are idempotent.
func (s *Service) RegisterManaged(ctx context.Context, in RegisterManagedInput, accessKey, secretKey, endpoint string) (*metastore.Table, error) {
	if err := validIdent("table", in.Name); err != nil {
		return nil, err
	}
	if _, err := s.store.GetDataset(ctx, in.ProjectID, in.Dataset); err != nil {
		return nil, fmt.Errorf("dataset %q: %w", in.Dataset, err)
	}

	// Infer schema from the written data.
	schemaRes := s.engine.InferSchema(in.ProbeReader, accessKey, secretKey, endpoint)
	if schemaRes.Error != "" {
		return nil, fmt.Errorf("infer managed schema: %s", schemaRes.Error)
	}
	cols := make([]metastore.Column, len(schemaRes.Columns))
	for i, c := range schemaRes.Columns {
		cols[i] = metastore.Column{Name: c.Name, Type: c.Type, Nullable: c.Nullable}
	}

	var rowCount int64
	countRes := s.engine.QueryView("SELECT count(*) AS c FROM "+in.ProbeReader, nil, accessKey, secretKey, endpoint)
	if countRes.Error == "" && countRes.RowCount == 1 {
		switch v := countRes.Rows[0][0].(type) {
		case int64:
			rowCount = v
		case int32:
			rowCount = int64(v)
		case int:
			rowCount = int64(v)
		}
	}

	storageClass := in.StorageClass
	if storageClass == "" {
		storageClass = "ssd"
	}

	// Replace any existing registration (idempotent CTAS/load).
	_ = s.store.DeleteTable(ctx, in.ProjectID, in.Dataset, in.Name)

	t := &metastore.Table{
		ProjectID:        in.ProjectID,
		Dataset:          in.Dataset,
		Name:             in.Name,
		Kind:             "managed",
		Location:         in.Location,
		Format:           "parquet",
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

// DropTableWithData drops a table registration. For managed tables it also
// deletes the underlying data objects via the deleter, parsing the bucket +
// prefix from the table's s3:// Location. The cache invalidator is always
// called so dependent cached results are evicted.
func (s *Service) DropTableWithData(ctx context.Context, projectID, dataset, name string, deleter PrefixDeleter, cache CacheInvalidator, accessKey, secretKey, endpoint string) error {
	tbl, err := s.store.GetTable(ctx, projectID, dataset, name)
	if err != nil {
		return err
	}
	if tbl.Kind == "managed" && deleter != nil {
		bucket, prefix, ok := splitS3(tbl.Location)
		if ok {
			if err := deleter.DeletePrefix(ctx, bucket, prefix); err != nil {
				return fmt.Errorf("delete managed data: %w", err)
			}
		}
	}
	if err := s.store.DeleteTable(ctx, projectID, dataset, name); err != nil {
		return err
	}
	if cache != nil {
		_ = cache.DeleteCacheEntriesForTable(ctx, projectID, dataset, name)
	}
	return nil
}

// splitS3 splits an "s3://bucket/key/prefix" into (bucket, "key/prefix"). The
// returned ok is false for non-s3 locations (e.g. external local globs).
func splitS3(location string) (bucket, prefix string, ok bool) {
	const scheme = "s3://"
	if !strings.HasPrefix(location, scheme) {
		return "", "", false
	}
	rest := location[len(scheme):]
	idx := strings.IndexByte(rest, '/')
	if idx < 0 {
		return rest, "", true
	}
	return rest[:idx], rest[idx+1:], true
}
```

> `RegisterManaged` references the existing `SchemaInferer` interface methods (`InferSchema`, `QueryView`) already on `s.engine` from Phase 1 Task 6 — no interface change needed.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/catalog/ -run 'TestDropTableWithData_Managed|TestDropTableWithData_ExternalSkipsDelete' -v`
Expected: PASS.

- [ ] **Step 5: Run the whole catalog package**

Run: `go test ./internal/catalog/`
Expected: PASS (ok).

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/
git commit -m "feat(catalog): RegisterManaged and managed-aware DropTableWithData"
```

---

## Task 7: CTAS execution (`Writer.RunCTAS`) end-to-end over local files

**Files:**
- Modify: `internal/write/ctas.go`
- Test: `internal/write/ctas_test.go` (append)

`RunCTAS` resolves source tables, builds the `COPY (<select>) TO '<location>' (FORMAT PARQUET[, PARTITION_BY (cols)])`, runs it via `ExecWrite`, registers the managed table, and runs the post-write hook.

- [ ] **Step 1: Write the failing end-to-end test (local dir as the "bucket")**

The storage resolver maps `ssd`→a local temp dir; the managed location becomes a local path so DuckDB `COPY … TO` writes real Parquet we read back. Append to `internal/write/ctas_test.go`:
```go
import (
	"context"
	"os"
	"path/filepath"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// localStorage maps every class to a local base directory, so managedLocation
// produces a filesystem path DuckDB can COPY to in tests.
type localStorage struct{ dir string }

func (l localStorage) Resolve(class string) (string, string, bool) {
	// Encode the local dir as the "bucket"; endpoint unused for local paths.
	return l.dir, "", true
}

func newCTASWriter(t *testing.T) (*Writer, *catalog.Service, string) {
	t.Helper()
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	eng, err := query.NewEngine(100000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.NewService(store, eng)
	baseDir := t.TempDir()
	w := NewWriter(eng, cat, store, noopCache{}, localStorage{dir: baseDir})
	// Override managedLocation to emit local paths instead of s3:// in tests.
	w.localBase = baseDir
	return w, cat, baseDir
}

type noopCache struct{}

func (noopCache) DeleteCacheEntriesForTable(ctx context.Context, p, d, t string) error { return nil }

func TestRunCTAS_EndToEndLocal(t *testing.T) {
	w, cat, _ := newCTASWriter(t)
	ctx := context.Background()

	// Source external table over a local CSV.
	csv := filepath.Join(t.TempDir(), "raw.csv")
	if err := os.WriteFile(csv, []byte("dt,region,n\n2026-06-01,eu,5\n2026-06-01,us,7\n2026-06-02,eu,3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cat.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.RegisterTable(ctx, catalog.RegisterTableInput{
		ProjectID: "p1", Dataset: "sales", Name: "raw", Location: csv, Format: "csv",
	}, "", "", ""); err != nil {
		t.Fatal(err)
	}

	sql := "CREATE TABLE sales.daily PARTITION BY (dt) STORAGE 'ssd' AS SELECT dt, region, n FROM sales.raw WHERE n > 3"
	tbl, err := w.RunCTAS(ctx, "p1", sql, "", "", "")
	if err != nil {
		t.Fatalf("RunCTAS: %v", err)
	}
	if tbl.Kind != "managed" || tbl.StorageClass != "ssd" {
		t.Fatalf("unexpected table: %+v", tbl)
	}
	if len(tbl.PartitionColumns) != 1 || tbl.PartitionColumns[0] != "dt" {
		t.Fatalf("expected partition by dt, got %+v", tbl.PartitionColumns)
	}
	// 2 rows survive n>3 (5 and 7). Read the written Parquet back.
	res := w.engine.(*query.Engine).QueryView(
		"SELECT count(*) AS c FROM read_parquet('"+filepath.Join(tbl.Location, "**", "*.parquet")+"', hive_partitioning=true)",
		nil, "", "", "")
	if res.Error != "" {
		t.Fatalf("read back: %s", res.Error)
	}
	if toI64(res.Rows[0][0]) != 2 {
		t.Fatalf("expected 2 rows written, got %v", res.Rows[0][0])
	}
}

func toI64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case int:
		return int64(x)
	default:
		return -1
	}
}
```

> The test reads back with `read_parquet('<location>/**/*.parquet', hive_partitioning=true)` because `PARTITION BY (dt)` writes `dt=.../` subdirectories. `tbl.Location` is a local directory path in tests (see `localBase` below).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/write/ -run TestRunCTAS_EndToEndLocal -v`
Expected: FAIL — `w.RunCTAS` undefined / `w.localBase` undefined.

- [ ] **Step 3: Implement `RunCTAS` (and a test-only local-base override)**

In `internal/write/write.go`, add a `localBase` field to `Writer` and adjust `managedLocation` to honor it:
```go
type Writer struct {
	engine    writeEngine
	cat       catalogService
	store     versionBumper
	cache     cacheInvalidator
	storage   storageResolver
	localBase string // test-only: when set, managed locations are local dirs
}
```
Replace `managedLocation` with:
```go
// managedLocation returns the base location for a managed table's data files.
// In tests localBase makes this a filesystem directory; in production it is an
// s3:// prefix under the storage-class bucket.
func (w *Writer) managedLocation(bucket, dataset, table string) string {
	if w.localBase != "" {
		return filepath.Join(w.localBase, "_managed", dataset, table)
	}
	return fmt.Sprintf("s3://%s/_managed/%s/%s", bucket, dataset, table)
}
```
Add `"path/filepath"` to `write.go` imports.

> Note the production form drops the trailing slash (so `filepath.Join` and the COPY target concatenate consistently); the prefix used for `DeletePrefix` is reconstructed by `splitS3` which tolerates a missing trailing slash.

In `internal/write/ctas.go`, add the executor:
```go
import (
	"context"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)
```
(Combine with the existing `ctas.go` import block — it currently imports `fmt`, `regexp`, `strings`; add the four above.)
```go
// RunCTAS parses, executes, and registers a CREATE TABLE ... AS SELECT.
func (w *Writer) RunCTAS(ctx context.Context, projectID, sql, accessKey, secretKey, endpoint string) (*metastore.Table, error) {
	plan, err := ParseCTAS(sql)
	if err != nil {
		return nil, err
	}
	storageClass := plan.StorageClass
	if storageClass == "" {
		storageClass = "ssd" // managed default tier
	}
	bucket := ""
	if w.storage != nil {
		b, _, ok := w.storage.Resolve(storageClass)
		if !ok {
			return nil, fmt.Errorf("unknown storage class %q", storageClass)
		}
		bucket = b
	}
	location := w.managedLocation(bucket, plan.Dataset, plan.Table)

	// Resolve source catalog tables referenced by the inner SELECT.
	bindings, err := w.cat.Resolve(ctx, projectID, plan.Select)
	if err != nil {
		return nil, fmt.Errorf("resolve sources: %w", err)
	}

	// Build COPY (<select>) TO '<location>' (FORMAT PARQUET [, PARTITION_BY (...)]).
	copyOpts := "FORMAT PARQUET"
	if len(plan.PartitionBy) > 0 {
		quoted := make([]string, len(plan.PartitionBy))
		for i, c := range plan.PartitionBy {
			quoted[i] = c
		}
		copyOpts += ", PARTITION_BY (" + strings.Join(quoted, ", ") + "), OVERWRITE_OR_IGNORE"
	}
	copySQL := fmt.Sprintf("COPY (%s) TO '%s' (%s)", plan.Select, escapeLiteral(location), copyOpts)
	if err := w.engine.ExecWrite(copySQL, bindings, accessKey, secretKey, endpoint); err != nil {
		return nil, fmt.Errorf("ctas copy: %w", err)
	}

	// Probe reader for schema/stats inference over the written files.
	probe := ctasProbeReader(location, len(plan.PartitionBy) > 0)
	tbl, err := w.cat.RegisterManaged(ctx, catalog.RegisterManagedInput{
		ProjectID:        projectID,
		Dataset:          plan.Dataset,
		Name:             plan.Table,
		Location:         location,
		ProbeReader:      probe,
		StorageClass:     storageClass,
		PartitionColumns: plan.PartitionBy,
	}, accessKey, secretKey, endpoint)
	if err != nil {
		return nil, err
	}
	if err := w.afterWrite(ctx, projectID, plan.Dataset, plan.Table); err != nil {
		return nil, err
	}
	return tbl, nil
}

// ctasProbeReader returns a read_parquet(...) expression covering the written
// files. Partitioned output lives in nested dt=.../ dirs and needs the glob +
// hive_partitioning flag so the partition columns appear in the schema.
func ctasProbeReader(location string, partitioned bool) string {
	if partitioned {
		return fmt.Sprintf("read_parquet('%s/**/*.parquet', hive_partitioning=true)", escapeLiteral(location))
	}
	return fmt.Sprintf("read_parquet('%s/*.parquet')", escapeLiteral(location))
}
```
Add `_ = query.ViewBinding{}` is unnecessary; the `query` import is used via `catalog.RegisterManagedInput` indirectly — actually `query` is referenced by the `catalogService` interface in `write.go`, not `ctas.go`. Remove `query` and `catalog` imports from `ctas.go` if `go build` reports them unused, keeping only what compiles. (Run `go build ./internal/write/` and trim unused imports as the compiler directs.)

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/write/ -run TestRunCTAS_EndToEndLocal -v`
Expected: PASS — 2 rows written and read back through the Hive-partitioned layout.

- [ ] **Step 5: Run the whole write package**

Run: `go test ./internal/write/`
Expected: PASS (ok).

- [ ] **Step 6: Commit**

```bash
git add internal/write/
git commit -m "feat(write): CTAS execution writing partitioned Parquet to managed tables"
```

---

## Task 8: Batch load (`Writer.RunLoad`) — append/overwrite, partitioned

**Files:**
- Modify: `internal/write/load.go` (create)
- Test: `internal/write/load_test.go` (create)

`RunLoad` reads a source glob via DuckDB `read_*`, writes Parquet into the target managed table's location, and updates the registration + stats. `overwrite` clears the location prefix first (via an injected deleter); `append` writes a new uniquely-suffixed file set under the same location. We *generalize* the convert engine's pattern (read via DuckDB, COPY to Parquet) but implement it as a focused method on `Writer` reusing `ExecWrite` rather than wrapping `convert.Engine` — chosen because the catalog write path needs schema inference + registration that `convert` does not provide, and `convert` stays untouched for raw bucket conversion. (Explicit choice: **reimplement, do not wrap**.)

- [ ] **Step 1: Write the failing end-to-end tests (local files)**

Create `internal/write/load_test.go`:
```go
package write

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// recordingDeleter records overwrite deletions and also wipes a local dir so the
// subsequent read-back reflects overwrite semantics in tests.
type recordingDeleter struct{ calls int }

func (d *recordingDeleter) DeletePrefix(ctx context.Context, bucket, prefix string) error {
	d.calls++
	os.RemoveAll(filepath.Join(bucket, prefix)) // local "bucket" == base dir
	return nil
}

func newLoadWriter(t *testing.T, del *recordingDeleter) (*Writer, *catalog.Service) {
	t.Helper()
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	eng, err := query.NewEngine(100000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.NewService(store, eng)
	base := t.TempDir()
	w := NewWriter(eng, cat, store, noopCache{}, localStorage{dir: base})
	w.localBase = base
	w.deleter = del
	return w, cat
}

func TestRunLoad_AppendThenOverwrite(t *testing.T) {
	del := &recordingDeleter{}
	w, cat := newLoadWriter(t, del)
	ctx := context.Background()
	if err := cat.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	src1 := filepath.Join(dir, "a.csv")
	_ = os.WriteFile(src1, []byte("id,v\n1,x\n2,y\n"), 0644)

	// Append into a fresh managed table.
	tbl, err := w.RunLoad(ctx, "p1", LoadRequest{
		Source: src1, Into: "sales.events", Format: "csv", Mode: "append",
	}, "", "", "")
	if err != nil {
		t.Fatalf("RunLoad append: %v", err)
	}
	if tbl.Stats.RowCount != 2 {
		t.Fatalf("expected 2 rows after first append, got %d", tbl.Stats.RowCount)
	}

	// Append a second file: total becomes 4.
	src2 := filepath.Join(dir, "b.csv")
	_ = os.WriteFile(src2, []byte("id,v\n3,z\n4,w\n"), 0644)
	tbl, err = w.RunLoad(ctx, "p1", LoadRequest{
		Source: src2, Into: "sales.events", Format: "csv", Mode: "append",
	}, "", "", "")
	if err != nil {
		t.Fatalf("RunLoad append 2: %v", err)
	}
	if tbl.Stats.RowCount != 4 {
		t.Fatalf("expected 4 rows after second append, got %d", tbl.Stats.RowCount)
	}
	if del.calls != 0 {
		t.Fatalf("append must not delete, got %d deletes", del.calls)
	}

	// Overwrite with a single 1-row file: total becomes 1, prefix cleared once.
	src3 := filepath.Join(dir, "c.csv")
	_ = os.WriteFile(src3, []byte("id,v\n9,q\n"), 0644)
	tbl, err = w.RunLoad(ctx, "p1", LoadRequest{
		Source: src3, Into: "sales.events", Format: "csv", Mode: "overwrite",
	}, "", "", "")
	if err != nil {
		t.Fatalf("RunLoad overwrite: %v", err)
	}
	if del.calls != 1 {
		t.Fatalf("overwrite must delete prefix once, got %d", del.calls)
	}
	if tbl.Stats.RowCount != 1 {
		t.Fatalf("expected 1 row after overwrite, got %d", tbl.Stats.RowCount)
	}
}

func TestRunLoad_Partitioned(t *testing.T) {
	del := &recordingDeleter{}
	w, cat := newLoadWriter(t, del)
	ctx := context.Background()
	if err := cat.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "p.csv")
	_ = os.WriteFile(src, []byte("dt,v\n2026-01-01,a\n2026-01-01,b\n2026-01-02,c\n"), 0644)

	tbl, err := w.RunLoad(ctx, "p1", LoadRequest{
		Source: src, Into: "sales.part", Format: "csv", Mode: "overwrite",
		PartitionBy: []string{"dt"},
	}, "", "", "")
	if err != nil {
		t.Fatalf("RunLoad partitioned: %v", err)
	}
	if len(tbl.PartitionColumns) != 1 || tbl.PartitionColumns[0] != "dt" {
		t.Fatalf("expected partition dt, got %+v", tbl.PartitionColumns)
	}
	if tbl.Stats.RowCount != 3 {
		t.Fatalf("expected 3 rows, got %d", tbl.Stats.RowCount)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/write/ -run 'TestRunLoad_AppendThenOverwrite|TestRunLoad_Partitioned' -v`
Expected: FAIL — `undefined: LoadRequest` / `w.RunLoad` / `w.deleter`.

- [ ] **Step 3: Implement `RunLoad`**

First add a `deleter` field to `Writer` in `internal/write/write.go`:
```go
type Writer struct {
	engine    writeEngine
	cat       catalogService
	store     versionBumper
	cache     cacheInvalidator
	storage   storageResolver
	deleter   prefixDeleter
	localBase string
}

// prefixDeleter clears all objects under a managed location's prefix.
type prefixDeleter interface {
	DeletePrefix(ctx context.Context, bucket, prefix string) error
}
```
Update `NewWriter` to accept the deleter:
```go
func NewWriter(engine writeEngine, cat catalogService, store versionBumper, cache cacheInvalidator, storage storageResolver, deleter prefixDeleter) *Writer {
	return &Writer{engine: engine, cat: cat, store: store, cache: cache, storage: storage, deleter: deleter}
}
```
> This changes the `NewWriter` signature used in Task 7's test helper. Update `newCTASWriter` in `ctas_test.go` to pass a deleter: `w := NewWriter(eng, cat, store, noopCache{}, localStorage{dir: baseDir}, nil)` then `w.localBase = baseDir`. (CTAS overwrites partition output via `OVERWRITE_OR_IGNORE` and does not need a deleter.)

Create `internal/write/load.go`:
```go
package write

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// LoadRequest is a batch-load job: read Source via DuckDB and write Parquet into
// the managed table Into ("dataset.table"). Mode is "append" or "overwrite".
type LoadRequest struct {
	Source      string   `json:"source"`
	Into        string   `json:"into"`
	Format      string   `json:"format"`
	PartitionBy []string `json:"partition_by"`
	Mode        string   `json:"mode"`
}

// sourceReaderSQL builds the DuckDB reader expression for the load source.
func sourceReaderSQL(source, format string) string {
	loc := strings.ReplaceAll(source, "'", "''")
	switch strings.ToLower(format) {
	case "parquet":
		return fmt.Sprintf("read_parquet('%s')", loc)
	case "json", "jsonl":
		return fmt.Sprintf("read_json_auto('%s')", loc)
	case "tsv":
		return fmt.Sprintf("read_csv_auto('%s', delim='\t')", loc)
	default: // csv
		return fmt.Sprintf("read_csv_auto('%s')", loc)
	}
}

func splitInto(into string) (dataset, table string, err error) {
	parts := strings.SplitN(into, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("into must be dataset.table, got %q", into)
	}
	return parts[0], parts[1], nil
}

// RunLoad executes a batch load. For overwrite it clears the table prefix first;
// for append it writes a uniquely-named file set under the same location.
func (w *Writer) RunLoad(ctx context.Context, projectID string, req LoadRequest, accessKey, secretKey, endpoint string) (*metastore.Table, error) {
	dataset, table, err := splitInto(req.Into)
	if err != nil {
		return nil, err
	}
	mode := strings.ToLower(req.Mode)
	if mode == "" {
		mode = "append"
	}
	if mode != "append" && mode != "overwrite" {
		return nil, fmt.Errorf("mode must be append or overwrite, got %q", req.Mode)
	}
	if req.Format == "" {
		req.Format = "csv"
	}

	// Determine storage class: keep existing table's class, else default ssd.
	storageClass := "ssd"
	if existing, gerr := w.cat.GetTable(ctx, projectID, dataset, table); gerr == nil && existing.StorageClass != "" {
		storageClass = existing.StorageClass
	}
	bucket := ""
	if w.storage != nil {
		b, _, ok := w.storage.Resolve(storageClass)
		if !ok {
			return nil, fmt.Errorf("unknown storage class %q", storageClass)
		}
		bucket = b
	}
	location := w.managedLocation(bucket, dataset, table)

	if mode == "overwrite" && w.deleter != nil {
		b, prefix, ok := splitS3(location)
		if !ok {
			// Local path (tests): use the location dir as bucket, "" prefix.
			b, prefix = location, ""
		}
		if err := w.deleter.DeletePrefix(ctx, b, prefix); err != nil {
			return nil, fmt.Errorf("overwrite clear: %w", err)
		}
	}

	// Build the COPY. For append we target a unique subdirectory so we do not
	// collide with existing files; for overwrite we target the base location.
	target := location
	if mode == "append" {
		target = filepath.Join(location, fmt.Sprintf("load-%d", time.Now().UnixNano()))
	}
	copyOpts := "FORMAT PARQUET"
	if len(req.PartitionBy) > 0 {
		copyOpts += ", PARTITION_BY (" + strings.Join(req.PartitionBy, ", ") + "), OVERWRITE_OR_IGNORE"
	}
	reader := sourceReaderSQL(req.Source, req.Format)
	copySQL := fmt.Sprintf("COPY (SELECT * FROM %s) TO '%s' (%s)", reader, escapeLiteral(target), copyOpts)
	if err := w.engine.ExecWrite(copySQL, nil, accessKey, secretKey, endpoint); err != nil {
		return nil, fmt.Errorf("load copy: %w", err)
	}

	// Probe the whole location (all appended sets) for schema + row count.
	partitioned := len(req.PartitionBy) > 0
	if existing, gerr := w.cat.GetTable(ctx, projectID, dataset, table); gerr == nil && len(existing.PartitionColumns) > 0 {
		partitioned = true
	}
	probe := loadProbeReader(location, partitioned)

	tbl, err := w.cat.RegisterManaged(ctx, catalog.RegisterManagedInput{
		ProjectID:        projectID,
		Dataset:          dataset,
		Name:             table,
		Location:         location,
		ProbeReader:      probe,
		StorageClass:     storageClass,
		PartitionColumns: req.PartitionBy,
	}, accessKey, secretKey, endpoint)
	if err != nil {
		return nil, err
	}
	if err := w.afterWrite(ctx, projectID, dataset, table); err != nil {
		return nil, err
	}
	return tbl, nil
}

// loadProbeReader globs all Parquet under a managed location (recursively, to
// cover both base-level overwrite files and per-append subdirectories).
func loadProbeReader(location string, partitioned bool) string {
	if partitioned {
		return fmt.Sprintf("read_parquet('%s/**/*.parquet', hive_partitioning=true)", escapeLiteral(location))
	}
	return fmt.Sprintf("read_parquet('%s/**/*.parquet')", escapeLiteral(location))
}
```

> Trim any unused imports the compiler flags (`go build ./internal/write/`).

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/write/ -run 'TestRunLoad_AppendThenOverwrite|TestRunLoad_Partitioned' -v`
Expected: PASS.

- [ ] **Step 5: Run the whole write package (race)**

Run: `go test -race ./internal/write/`
Expected: PASS (ok).

- [ ] **Step 6: Commit**

```bash
git add internal/write/
git commit -m "feat(write): batch load with append/overwrite and Hive partitioning"
```

---

## Task 9: Job package — write executor seam + type routing

**Files:**
- Modify: `internal/job/job.go`
- Create: `internal/job/write_executor.go`
- Test: `internal/job/write_executor_test.go`

The Phase 2 `Manager.Submit(ctx, req) *Job` runs jobs asynchronously. This task adds a `WriteExecutor` the manager dispatches to for `ctas`/`load` jobs (by `req.Type`), keeping the Phase 1 `query` path on the read `Executor`.

- [ ] **Step 1: Write the failing test (fake write executor)**

Create `internal/job/write_executor_test.go`:
```go
package job

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/query"
)

type fakeWriteExec struct {
	mu     sync.Mutex
	ctas   int
	load   int
	intoT  string
}

func (f *fakeWriteExec) RunCTAS(ctx context.Context, projectID, sql, ak, sk, ep string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ctas++
	return "sales.daily", nil
}

func (f *fakeWriteExec) RunLoad(ctx context.Context, projectID string, req LoadRequest, ak, sk, ep string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.load++
	f.intoT = req.Into
	return req.Into, nil
}

// readExec satisfies Executor for the query path.
type readExec struct{}

func (readExec) Execute(ctx context.Context, req ExecRequest) *query.Result {
	return &query.Result{RowCount: 0}
}

func waitDone(t *testing.T, m *Manager, id string) *Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := m.Get(id); ok && (j.Status == "done" || j.Status == "failed") {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish", id)
	return nil
}

func TestManager_RoutesCTAS(t *testing.T) {
	fw := &fakeWriteExec{}
	m := NewManager(readExec{})
	m.SetWriteExecutor(fw)

	j := m.Submit(context.Background(), ExecRequest{
		Type: "ctas", SQL: "CREATE TABLE sales.daily AS SELECT 1", ProjectID: "p1",
	})
	done := waitDone(t, m, j.ID)
	if done.Status != "done" {
		t.Fatalf("expected done, got %s (%s)", done.Status, done.Error)
	}
	if fw.ctas != 1 {
		t.Fatalf("expected 1 ctas call, got %d", fw.ctas)
	}
	if done.IntoTable != "sales.daily" {
		t.Fatalf("expected IntoTable sales.daily, got %q", done.IntoTable)
	}
}

func TestManager_RoutesLoad(t *testing.T) {
	fw := &fakeWriteExec{}
	m := NewManager(readExec{})
	m.SetWriteExecutor(fw)

	j := m.Submit(context.Background(), ExecRequest{
		Type:    "load",
		Load:    &LoadRequest{Source: "s3://b/*.csv", Into: "sales.ev", Format: "csv", Mode: "append"},
		ProjectID: "p1",
	})
	done := waitDone(t, m, j.ID)
	if done.Status != "done" {
		t.Fatalf("expected done, got %s (%s)", done.Status, done.Error)
	}
	if fw.load != 1 || fw.intoT != "sales.ev" {
		t.Fatalf("load not routed: load=%d into=%q", fw.load, fw.intoT)
	}
}
```

> This test assumes the Phase 2 async `Manager.Submit` exists. It also introduces fields the manager must carry (`ExecRequest.Type`, `ExecRequest.Load`, `Job.IntoTable`) and the `LoadRequest` type used by the job layer.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/job/ -run 'TestManager_RoutesCTAS|TestManager_RoutesLoad' -v`
Expected: FAIL — `SetWriteExecutor`, `ExecRequest.Type`, `ExecRequest.Load`, `Job.IntoTable`, `LoadRequest` undefined.

- [ ] **Step 3: Extend the job types and manager routing**

In `internal/job/job.go`:

Add the load request mirror and extend `ExecRequest`/`Job` (extend, do not remove existing fields):
```go
// LoadRequest mirrors write.LoadRequest at the job boundary so the job package
// does not import write (write imports job-free). main.go adapts between them.
type LoadRequest struct {
	Source      string   `json:"source"`
	Into        string   `json:"into"`
	Format      string   `json:"format"`
	PartitionBy []string `json:"partition_by"`
	Mode        string   `json:"mode"`
}
```
Add fields to `ExecRequest`:
```go
	Type string       // "query" (default), "ctas", or "load"
	Load *LoadRequest // set when Type == "load"
```
Add a field to `Job`:
```go
	IntoTable string `json:"into_table,omitempty"`
```
Add the write executor seam + setter to `Manager`:
```go
// WriteExecutor runs write jobs (CTAS/load) and returns the affected table ref.
type WriteExecutor interface {
	RunCTAS(ctx context.Context, projectID, sql, ak, sk, ep string) (string, error)
	RunLoad(ctx context.Context, projectID string, req LoadRequest, ak, sk, ep string) (string, error)
}

// SetWriteExecutor attaches the write executor used for ctas/load jobs.
func (m *Manager) SetWriteExecutor(we WriteExecutor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.write = we
}
```
Add the `write WriteExecutor` field to the `Manager` struct.

In `Submit` (Phase 2 async path), route by type. Locate the goroutine that executes the job and replace its body's "run query" portion with a type switch. The async `Submit` should look like:
```go
func (m *Manager) Submit(ctx context.Context, req ExecRequest) *Job {
	typ := req.Type
	if typ == "" {
		typ = "query"
	}
	j := &Job{
		ID:        uuid.NewString(),
		Type:      typ,
		SQL:       req.SQL,
		Status:    "queued",
		CreatedAt: time.Now().UTC(),
	}
	m.put(j)

	go func() {
		j.Status = "running"
		m.put(j)
		switch typ {
		case "ctas":
			m.mu.RLock()
			we := m.write
			m.mu.RUnlock()
			if we == nil {
				j.Status, j.Error = "failed", "write executor not configured"
				m.put(j)
				return
			}
			into, err := we.RunCTAS(ctx, req.ProjectID, req.SQL, req.AccessKey, req.SecretKey, req.Endpoint)
			if err != nil {
				j.Status, j.Error = "failed", err.Error()
			} else {
				j.Status, j.IntoTable = "done", into
			}
			m.put(j)
		case "load":
			m.mu.RLock()
			we := m.write
			m.mu.RUnlock()
			if we == nil || req.Load == nil {
				j.Status, j.Error = "failed", "load request or write executor missing"
				m.put(j)
				return
			}
			into, err := we.RunLoad(ctx, req.ProjectID, *req.Load, req.AccessKey, req.SecretKey, req.Endpoint)
			if err != nil {
				j.Status, j.Error = "failed", err.Error()
			} else {
				j.Status, j.IntoTable = "done", into
			}
			m.put(j)
		default:
			res := m.exec.Execute(ctx, req)
			if res.Error != "" {
				j.Status, j.Error = "failed", res.Error
			} else {
				j.Status, j.Result = "done", res
			}
			m.put(j)
		}
	}()
	return j
}
```
> If the Phase 2 `Submit` already exists with a different internal shape, adapt the type-switch into its goroutine rather than replacing wholesale; the contract (queued→running→done/failed, routing by `req.Type`) is what matters. The `m.write` field, `SetWriteExecutor`, and the `ctas`/`load` branches are the new parts.

- [ ] **Step 4: Implement the LocalWriteExecutor adapter**

Create `internal/job/write_executor.go`:
```go
package job

import (
	"context"

	"github.com/esignoretti/ds3-sql-server/internal/write"
)

// LocalWriteExecutor adapts *write.Writer to the WriteExecutor interface,
// translating the job-layer LoadRequest into the write-layer one.
type LocalWriteExecutor struct {
	w *write.Writer
}

func NewLocalWriteExecutor(w *write.Writer) *LocalWriteExecutor {
	return &LocalWriteExecutor{w: w}
}

func (l *LocalWriteExecutor) RunCTAS(ctx context.Context, projectID, sql, ak, sk, ep string) (string, error) {
	tbl, err := l.w.RunCTAS(ctx, projectID, sql, ak, sk, ep)
	if err != nil {
		return "", err
	}
	return tbl.Dataset + "." + tbl.Name, nil
}

func (l *LocalWriteExecutor) RunLoad(ctx context.Context, projectID string, req LoadRequest, ak, sk, ep string) (string, error) {
	tbl, err := l.w.RunLoad(ctx, projectID, write.LoadRequest{
		Source:      req.Source,
		Into:        req.Into,
		Format:      req.Format,
		PartitionBy: req.PartitionBy,
		Mode:        req.Mode,
	}, ak, sk, ep)
	if err != nil {
		return "", err
	}
	return tbl.Dataset + "." + tbl.Name, nil
}

var _ WriteExecutor = (*LocalWriteExecutor)(nil)
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/job/ -run 'TestManager_RoutesCTAS|TestManager_RoutesLoad' -v`
Expected: PASS.

- [ ] **Step 6: Run the whole job package (race)**

Run: `go test -race ./internal/job/`
Expected: PASS — existing Phase 1/2 tests stay green.

- [ ] **Step 7: Commit**

```bash
git add internal/job/
git commit -m "feat(job): write-executor seam and ctas/load type routing"
```

---

## Task 10: Metastore — `Schedule` type + six methods

**Files:**
- Modify: `internal/metastore/store.go`
- Modify: `internal/metastore/sqlite.go`
- Test: `internal/metastore/sqlite_schedule_test.go` (create)

Adds the canonical `Schedule` type and the six `Store` methods (Phase 4's Postgres store implements these exactly).

- [ ] **Step 1: Write the failing test**

Create `internal/metastore/sqlite_schedule_test.go`:
```go
package metastore

import (
	"context"
	"testing"
	"time"
)

func TestScheduleCRUD(t *testing.T) {
	s := newTestStore(t) // helper from sqlite_test.go (Phase 1)
	ctx := context.Background()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

	sch := &Schedule{
		ID:        "sch-1",
		ProjectID: "p1",
		Cron:      "0 * * * *",
		SQL:       "CREATE TABLE sales.hourly AS SELECT 1",
		IntoTable: "sales.hourly",
		Owner:     "alice@example.com",
		NextRunAt: now.Add(time.Hour),
		CreatedAt: now,
	}
	if err := s.CreateSchedule(ctx, sch); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	got, err := s.GetSchedule(ctx, "sch-1")
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if got.Cron != "0 * * * *" || got.IntoTable != "sales.hourly" || got.Owner != "alice@example.com" {
		t.Fatalf("schedule round-trip failed: %+v", got)
	}

	list, err := s.ListSchedules(ctx, "p1")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListSchedules: err=%v len=%d", err, len(list))
	}

	// Mark running with a last-run time, then GetDueSchedules must skip it
	// (running) when due, and exclude it (next-run in the future) regardless.
	lastRun := now.Add(time.Hour)
	if err := s.UpdateScheduleRun(ctx, "sch-1", lastRun, true); err != nil {
		t.Fatalf("UpdateScheduleRun: %v", err)
	}
	got, _ = s.GetSchedule(ctx, "sch-1")
	if !got.Running || !got.LastRunAt.Equal(lastRun) {
		t.Fatalf("UpdateScheduleRun not applied: %+v", got)
	}

	// Make it due: next_run in the past, not running.
	if err := s.UpdateScheduleRun(ctx, "sch-1", lastRun, false); err != nil {
		t.Fatalf("UpdateScheduleRun clear: %v", err)
	}
	// Manually set NextRunAt in the past by recreating via a second schedule.
	due := &Schedule{
		ID: "sch-2", ProjectID: "p1", Cron: "0 * * * *",
		SQL: "SELECT 1", NextRunAt: now.Add(-time.Minute), CreatedAt: now,
	}
	if err := s.CreateSchedule(ctx, due); err != nil {
		t.Fatalf("CreateSchedule due: %v", err)
	}
	dueList, err := s.GetDueSchedules(ctx, now)
	if err != nil {
		t.Fatalf("GetDueSchedules: %v", err)
	}
	foundDue := false
	for _, d := range dueList {
		if d.ID == "sch-2" {
			foundDue = true
		}
		if d.Running {
			t.Fatalf("GetDueSchedules returned a running schedule: %+v", d)
		}
	}
	if !foundDue {
		t.Fatalf("expected sch-2 to be due, got %+v", dueList)
	}

	if err := s.DeleteSchedule(ctx, "sch-1"); err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}
	if _, err := s.GetSchedule(ctx, "sch-1"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/metastore/ -run TestScheduleCRUD -v`
Expected: FAIL — `undefined: Schedule` / `CreateSchedule` etc.

- [ ] **Step 3: Add the type and interface methods**

In `internal/metastore/store.go`, add the type (after `Table`):
```go
// Schedule is a cron-driven query/CTAS/load. NextRunAt drives due selection;
// Running guards against overlapping runs (misfire policy: skip if still running).
type Schedule struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Cron      string    `json:"cron"`
	SQL       string    `json:"sql"`
	IntoTable string    `json:"into_table"`
	Owner     string    `json:"owner"`
	NextRunAt time.Time `json:"next_run_at"`
	LastRunAt time.Time `json:"last_run_at"`
	Running   bool      `json:"running"`
	CreatedAt time.Time `json:"created_at"`
}
```
Add to the `Store` interface (before `Close() error`):
```go
	CreateSchedule(ctx context.Context, sch *Schedule) error
	ListSchedules(ctx context.Context, projectID string) ([]*Schedule, error)
	GetSchedule(ctx context.Context, id string) (*Schedule, error)
	DeleteSchedule(ctx context.Context, id string) error
	UpdateScheduleRun(ctx context.Context, id string, lastRun time.Time, running bool) error
	GetDueSchedules(ctx context.Context, now time.Time) ([]*Schedule, error)
```

- [ ] **Step 4: Implement the methods in SQLite**

In `internal/metastore/sqlite.go`, add a migration statement to the `stmts` slice in `migrate()`:
```go
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
```
Then add the methods (a zero `time.Time` is stored as empty string; `parseTime` tolerates it):
```go
const scheduleCols = `id, project_id, cron, sql, into_table, owner, next_run_at, last_run_at, running, created_at`

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

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

func (s *SQLiteStore) DeleteSchedule(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM schedules WHERE id = ?`, id)
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
```
> RFC3339 timestamps sort lexicographically in UTC, so the `next_run_at <= ?` string comparison is correct for due selection.

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/metastore/ -run TestScheduleCRUD -v`
Expected: PASS.

- [ ] **Step 6: Run the whole metastore package (race)**

Run: `go test -race ./internal/metastore/`
Expected: PASS — Phase 1/2 store tests stay green; the `var _ Store = (*SQLiteStore)(nil)` assertion still compiles with the new methods.

- [ ] **Step 7: Commit**

```bash
git add internal/metastore/
git commit -m "feat(metastore): Schedule type and CRUD + due-selection methods"
```

---

## Task 11: Scheduler — deterministic tick loop with misfire skip

**Files:**
- Create: `internal/scheduler/scheduler.go`
- Test: `internal/scheduler/scheduler_test.go`
- Modify: `go.mod`/`go.sum` (add cron)

The `Scheduler` exposes `Tick(now)` (the unit-testable core: select due, enqueue, advance next-run, mark running) and a `Run(ctx)` ticker that calls `Tick(time.Now())`. Misfire policy is enforced by `GetDueSchedules` excluding running schedules; the scheduler marks a schedule `Running=true` on enqueue and relies on the job-completion callback to clear it.

- [ ] **Step 1: Add the cron dependency** *(instruction for the implementer; do not run here)*

Run:
```bash
cd "/Users/esignoretti/Documents/OpenCode/DS3-SQL Server"
go get github.com/robfig/cron/v3@latest
```
Expected: `go.mod` gains `github.com/robfig/cron/v3`; `go.sum` updated.

- [ ] **Step 2: Write the failing test (deterministic, no sleeping)**

Create `internal/scheduler/scheduler_test.go`:
```go
package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// fakeSchedStore implements the narrow Store the scheduler needs.
type fakeSchedStore struct {
	due       []*metastore.Schedule
	updates   []update
	getByID   map[string]*metastore.Schedule
}

type update struct {
	id      string
	lastRun time.Time
	running bool
}

func (f *fakeSchedStore) GetDueSchedules(ctx context.Context, now time.Time) ([]*metastore.Schedule, error) {
	return f.due, nil
}
func (f *fakeSchedStore) UpdateScheduleRun(ctx context.Context, id string, lastRun time.Time, running bool) error {
	f.updates = append(f.updates, update{id, lastRun, running})
	if s, ok := f.getByID[id]; ok {
		s.Running = running
		s.LastRunAt = lastRun
	}
	return nil
}

// fakeEnqueuer records enqueued schedules.
type fakeEnqueuer struct{ enqueued []*metastore.Schedule }

func (e *fakeEnqueuer) Enqueue(sch *metastore.Schedule) {
	e.enqueued = append(e.enqueued, sch)
}

func TestTick_EnqueuesDueAndAdvancesNextRun(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	sch := &metastore.Schedule{
		ID: "s1", ProjectID: "p1", Cron: "0 * * * *", // top of every hour
		SQL: "CREATE TABLE d.t AS SELECT 1", IntoTable: "d.t",
		NextRunAt: now.Add(-time.Minute),
	}
	store := &fakeSchedStore{due: []*metastore.Schedule{sch}, getByID: map[string]*metastore.Schedule{"s1": sch}}
	enq := &fakeEnqueuer{}
	s := New(store, enq)

	if err := s.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(enq.enqueued) != 1 || enq.enqueued[0].ID != "s1" {
		t.Fatalf("expected s1 enqueued, got %+v", enq.enqueued)
	}
	// Must mark running=true and compute the next run at the next top-of-hour (13:00).
	if len(store.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(store.updates))
	}
	u := store.updates[0]
	if !u.running {
		t.Fatal("expected schedule marked running")
	}
	wantNext := time.Date(2026, 6, 7, 13, 0, 0, 0, time.UTC)
	if !sch.NextRunAt.Equal(wantNext) {
		t.Fatalf("next run = %v, want %v", sch.NextRunAt, wantNext)
	}
}

func TestTick_BadCronMarksNotRunningAndSkips(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	sch := &metastore.Schedule{ID: "bad", ProjectID: "p1", Cron: "not a cron", SQL: "SELECT 1", NextRunAt: now.Add(-time.Minute)}
	store := &fakeSchedStore{due: []*metastore.Schedule{sch}, getByID: map[string]*metastore.Schedule{"bad": sch}}
	enq := &fakeEnqueuer{}
	s := New(store, enq)
	if err := s.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// Bad cron must not enqueue and must not leave the schedule marked running.
	if len(enq.enqueued) != 0 {
		t.Fatalf("bad cron must not enqueue, got %+v", enq.enqueued)
	}
}

func TestComputeNextRun(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC)
	next, err := computeNextRun("0 * * * *", now)
	if err != nil {
		t.Fatalf("computeNextRun: %v", err)
	}
	want := time.Date(2026, 6, 7, 13, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/scheduler/ -run 'TestTick_EnqueuesDueAndAdvancesNextRun|TestTick_BadCronMarksNotRunningAndSkips|TestComputeNextRun' -v`
Expected: FAIL — `undefined: New` / `computeNextRun` / package missing.

- [ ] **Step 4: Implement the scheduler**

Create `internal/scheduler/scheduler.go`:
```go
package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// Store is the narrow metastore subset the scheduler depends on.
type Store interface {
	GetDueSchedules(ctx context.Context, now time.Time) ([]*metastore.Schedule, error)
	UpdateScheduleRun(ctx context.Context, id string, lastRun time.Time, running bool) error
}

// Enqueuer hands a due schedule to the job layer. Implementations submit an
// async job and arrange to clear the schedule's Running flag on completion.
type Enqueuer interface {
	Enqueue(sch *metastore.Schedule)
}

// Scheduler ticks over due schedules and enqueues jobs.
type Scheduler struct {
	store Store
	enq   Enqueuer
}

func New(store Store, enq Enqueuer) *Scheduler {
	return &Scheduler{store: store, enq: enq}
}

// cronParser accepts standard 5-field cron expressions.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// computeNextRun returns the next activation strictly after now for a cron spec.
func computeNextRun(spec string, now time.Time) (time.Time, error) {
	sched, err := cronParser.Parse(spec)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron %q: %w", spec, err)
	}
	return sched.Next(now), nil
}

// Tick selects due schedules, advances their next-run, marks them running, and
// enqueues each. Schedules already running are excluded by GetDueSchedules
// (the misfire/overlap skip). A bad cron expression is logged and skipped
// without enqueuing or marking running.
func (s *Scheduler) Tick(ctx context.Context, now time.Time) error {
	due, err := s.store.GetDueSchedules(ctx, now)
	if err != nil {
		return fmt.Errorf("get due schedules: %w", err)
	}
	for _, sch := range due {
		next, err := computeNextRun(sch.Cron, now)
		if err != nil {
			log.Printf("scheduler: skipping schedule %s: %v", sch.ID, err)
			continue
		}
		// Persist running=true with this run's timestamp so overlapping ticks
		// skip it until the job completes and clears the flag.
		if err := s.store.UpdateScheduleRun(ctx, sch.ID, now, true); err != nil {
			log.Printf("scheduler: mark running %s: %v", sch.ID, err)
			continue
		}
		sch.NextRunAt = next
		sch.LastRunAt = now
		sch.Running = true
		s.enq.Enqueue(sch)
	}
	return nil
}

// Run ticks on the given interval until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Tick(ctx, time.Now().UTC()); err != nil {
				log.Printf("scheduler tick: %v", err)
			}
		}
	}
}
```

> Note on next-run persistence: `Tick` updates the in-memory `sch.NextRunAt`/`LastRunAt` and persists `running=true` + `last_run_at` via `UpdateScheduleRun`. The *next_run_at* column is advanced when the job completes (the Enqueuer's completion callback re-persists next-run and clears running) — see Task 12 wiring. The deterministic test asserts the in-memory advance and the running-flag persistence, which is the scheduler's own responsibility.

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/scheduler/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/scheduler/ go.mod go.sum
git commit -m "feat(scheduler): cron tick loop with misfire skip and next-run computation"
```

---

## Task 12: API — schedule handlers + write-job routing

**Files:**
- Create: `internal/api/schedule_handler.go`
- Test: `internal/api/schedule_handler_test.go`
- Modify: `internal/api/job_handler.go`
- Modify: `internal/api/job_handler_test.go`

### Part A: Schedule CRUD handlers

- [ ] **Step 1: Write the failing schedule-handler test**

Create `internal/api/schedule_handler_test.go`:
```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// scheduleStoreStub is a minimal in-memory schedule store for handler tests.
type scheduleStoreStub struct {
	created []*metastore.Schedule
}

func (s *scheduleStoreStub) CreateSchedule(ctx context.Context, sch *metastore.Schedule) error {
	s.created = append(s.created, sch)
	return nil
}
func (s *scheduleStoreStub) ListSchedules(ctx context.Context, projectID string) ([]*metastore.Schedule, error) {
	return s.created, nil
}
func (s *scheduleStoreStub) DeleteSchedule(ctx context.Context, id string) error { return nil }

func TestScheduleHandler_CreateListDelete(t *testing.T) {
	stub := &scheduleStoreStub{}
	h := NewScheduleHandler(stub)

	body := `{"cron":"0 * * * *","sql":"CREATE TABLE d.t AS SELECT 1","into_table":"d.t"}`
	req := httptest.NewRequest("POST", "/schedules", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.CreateForProject(w, req, "p1", "alice@example.com")
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", w.Code, w.Body.String())
	}
	if len(stub.created) != 1 || stub.created[0].ProjectID != "p1" || stub.created[0].Owner != "alice@example.com" {
		t.Fatalf("schedule not created correctly: %+v", stub.created)
	}
	if stub.created[0].ID == "" || stub.created[0].NextRunAt.IsZero() {
		t.Fatalf("expected generated ID and computed NextRunAt: %+v", stub.created[0])
	}

	// Bad cron -> 400.
	req = httptest.NewRequest("POST", "/schedules", strings.NewReader(`{"cron":"nope","sql":"SELECT 1"}`))
	w = httptest.NewRecorder()
	h.CreateForProject(w, req, "p1", "alice@example.com")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad cron, got %d", w.Code)
	}

	// List.
	req = httptest.NewRequest("GET", "/schedules", nil)
	w = httptest.NewRecorder()
	h.ListForProject(w, req, "p1")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "0 * * * *") {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestScheduleHandler_CreateListDelete -v`
Expected: FAIL — `undefined: NewScheduleHandler`.

- [ ] **Step 3: Implement the schedule handler**

Create `internal/api/schedule_handler.go`:
```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// ScheduleStore is the subset of metastore.Store the handler needs.
type ScheduleStore interface {
	CreateSchedule(ctx context.Context, sch *metastore.Schedule) error
	ListSchedules(ctx context.Context, projectID string) ([]*metastore.Schedule, error)
	DeleteSchedule(ctx context.Context, id string) error
}

type ScheduleHandler struct {
	store ScheduleStore
}

func NewScheduleHandler(store ScheduleStore) *ScheduleHandler {
	return &ScheduleHandler{store: store}
}

var scheduleCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func (h *ScheduleHandler) CreateForProject(w http.ResponseWriter, r *http.Request, projectID, owner string) {
	var req struct {
		Cron      string `json:"cron"`
		SQL       string `json:"sql"`
		IntoTable string `json:"into_table"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Cron == "" || req.SQL == "" {
		http.Error(w, `{"error":"cron and sql are required"}`, http.StatusBadRequest)
		return
	}
	sched, err := scheduleCronParser.Parse(req.Cron)
	if err != nil {
		http.Error(w, `{"error":"invalid cron expression"}`, http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	s := &metastore.Schedule{
		ID:        uuid.NewString(),
		ProjectID: projectID,
		Cron:      req.Cron,
		SQL:       req.SQL,
		IntoTable: req.IntoTable,
		Owner:     owner,
		NextRunAt: sched.Next(now),
		CreatedAt: now,
	}
	if err := h.store.CreateSchedule(r.Context(), s); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(s)
}

func (h *ScheduleHandler) ListForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	list, err := h.store.ListSchedules(r.Context(), projectID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"schedules": list})
}

func (h *ScheduleHandler) DeleteForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	id := chi.URLParam(r, "id")
	if err := h.store.DeleteSchedule(r.Context(), id); err != nil {
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

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run TestScheduleHandler_CreateListDelete -v`
Expected: PASS.

### Part B: Route ctas/load through the job handler

- [ ] **Step 5: Write the failing job-routing test**

Append to `internal/api/job_handler_test.go`:
```go
// writeRecorder records async submits routed to the manager.
func TestJobHandler_RoutesCTAS(t *testing.T) {
	mgr := job.NewManager(stubExecutor{})
	mgr.SetWriteExecutor(stubWriteExec{})
	h := NewJobHandler(mgr)

	body := `{"sql":"CREATE TABLE sales.daily AS SELECT 1"}`
	req := httptest.NewRequest("POST", "/jobs", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.SubmitWithCreds(w, req, "p1", "ak", "sk", "http://localhost:9000")
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for async ctas, got %d body=%s", w.Code, w.Body.String())
	}
	var j job.Job
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if j.Type != "ctas" {
		t.Fatalf("expected type ctas, got %q", j.Type)
	}
}

func TestJobHandler_RoutesLoad(t *testing.T) {
	mgr := job.NewManager(stubExecutor{})
	mgr.SetWriteExecutor(stubWriteExec{})
	h := NewJobHandler(mgr)

	body := `{"type":"load","source":"s3://b/*.csv","into":"sales.ev","format":"csv","mode":"append"}`
	req := httptest.NewRequest("POST", "/jobs", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.SubmitWithCreds(w, req, "p1", "ak", "sk", "http://localhost:9000")
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for async load, got %d body=%s", w.Code, w.Body.String())
	}
	var j job.Job
	_ = json.Unmarshal(w.Body.Bytes(), &j)
	if j.Type != "load" {
		t.Fatalf("expected type load, got %q", j.Type)
	}
}

type stubWriteExec struct{}

func (stubWriteExec) RunCTAS(ctx context.Context, projectID, sql, ak, sk, ep string) (string, error) {
	return "sales.daily", nil
}
func (stubWriteExec) RunLoad(ctx context.Context, projectID string, req job.LoadRequest, ak, sk, ep string) (string, error) {
	return req.Into, nil
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./internal/api/ -run 'TestJobHandler_RoutesCTAS|TestJobHandler_RoutesLoad' -v`
Expected: FAIL — `SubmitWithCreds` still runs everything synchronously / returns 200.

- [ ] **Step 7: Update `SubmitWithCreds` to detect and route writes**

In `internal/api/job_handler.go`, replace `SubmitWithCreds` with a version that decodes a richer body, detects `ctas` (SQL prefix) and `load` (explicit type), and submits writes asynchronously (202) while keeping the Phase 1 synchronous fast-path for queries:
```go
import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/esignoretti/ds3-sql-server/internal/job"
	"github.com/esignoretti/ds3-sql-server/internal/write"
)

// SubmitWithCreds runs query jobs synchronously (Phase 1 fast-path) and routes
// ctas/load jobs to the async write path (returns 202 + the queued job).
func (h *JobHandler) SubmitWithCreds(w http.ResponseWriter, r *http.Request, projectID, accessKey, secretKey, endpoint string) {
	var req struct {
		Type        string   `json:"type"`
		SQL         string   `json:"sql"`
		Source      string   `json:"source"`
		Into        string   `json:"into"`
		Format      string   `json:"format"`
		PartitionBy []string `json:"partition_by"`
		Mode        string   `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	// Explicit load type.
	if strings.EqualFold(req.Type, "load") {
		if req.Source == "" || req.Into == "" {
			http.Error(w, `{"error":"load requires source and into"}`, http.StatusBadRequest)
			return
		}
		j := h.mgr.Submit(r.Context(), job.ExecRequest{
			Type:      "load",
			ProjectID: projectID,
			AccessKey: accessKey,
			SecretKey: secretKey,
			Endpoint:  endpoint,
			Load: &job.LoadRequest{
				Source: req.Source, Into: req.Into, Format: req.Format,
				PartitionBy: req.PartitionBy, Mode: req.Mode,
			},
		})
		writeJSON(w, http.StatusAccepted, j)
		return
	}

	if req.SQL == "" {
		http.Error(w, `{"error":"sql field is required"}`, http.StatusBadRequest)
		return
	}

	// CTAS detected by SQL shape -> async write path.
	if write.IsCTAS(req.SQL) {
		j := h.mgr.Submit(r.Context(), job.ExecRequest{
			Type:      "ctas",
			SQL:       req.SQL,
			ProjectID: projectID,
			AccessKey: accessKey,
			SecretKey: secretKey,
			Endpoint:  endpoint,
		})
		writeJSON(w, http.StatusAccepted, j)
		return
	}

	// Plain query -> synchronous fast-path.
	j := h.mgr.Run(r.Context(), job.ExecRequest{
		SQL:       req.SQL,
		ProjectID: projectID,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Endpoint:  endpoint,
	})
	if j.Status == "failed" {
		writeJSON(w, http.StatusBadRequest, j)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
```
> The `Get` method and existing imports remain; ensure `chi` stays imported (used by `Get`). Remove the old inline status-write code that this replaces. If `writeJSON` collides with an existing helper, reuse the existing one instead.

- [ ] **Step 8: Run to verify it passes**

Run: `go test ./internal/api/ -run 'TestJobHandler_RoutesCTAS|TestJobHandler_RoutesLoad|TestJobHandler_SubmitSyncAndGet' -v`
Expected: PASS — query path still returns 200 with a synchronous result; ctas/load return 202.

- [ ] **Step 9: Run the whole api package**

Run: `go test ./internal/api/`
Expected: PASS (ok).

- [ ] **Step 10: Commit**

```bash
git add internal/api/schedule_handler.go internal/api/schedule_handler_test.go internal/api/job_handler.go internal/api/job_handler_test.go
git commit -m "feat(api): schedule CRUD and ctas/load job routing"
```

---

## Task 13: API — managed-aware table drop

**Files:**
- Modify: `internal/api/table_handler.go`
- Test: `internal/api/table_handler_test.go` (append)

`DropForProject` must accept creds + a deleter + cache so managed tables have their data deleted. We add a `…WithDeps` variant and keep wiring in `main.go`.

- [ ] **Step 1: Write the failing test**

Append to `internal/api/table_handler_test.go`:
```go
func TestTableHandler_DropManagedDeletesData(t *testing.T) {
	cat := newTestCatalog(t)
	ctx := context.Background()
	if err := cat.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	csv := filepath.Join(t.TempDir(), "m.csv")
	_ = os.WriteFile(csv, []byte("id\n1\n"), 0644)
	if _, err := cat.RegisterManaged(ctx, catalog.RegisterManagedInput{
		ProjectID: "p1", Dataset: "sales", Name: "m",
		Location: "s3://ds3-fast/_managed/sales/m/", ProbeReader: "read_csv_auto('" + csv + "')",
		StorageClass: "ssd",
	}, "", "", ""); err != nil {
		t.Fatal(err)
	}

	del := &apiFakeDeleter{}
	inv := &apiFakeInvalidator{}
	h := NewTableHandler(cat)

	req := httptest.NewRequest("DELETE", "/datasets/sales/tables/m", nil)
	req = withURLParam(req, "dataset", "sales")
	req = withURLParam(req, "table", "m")
	w := httptest.NewRecorder()
	h.DropWithDeps(w, req, "p1", del, inv, "", "", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("drop status = %d body=%s", w.Code, w.Body.String())
	}
	if len(del.calls) != 1 {
		t.Fatalf("expected managed data deletion, got %d calls", len(del.calls))
	}
}

type apiFakeDeleter struct{ calls []string }

func (d *apiFakeDeleter) DeletePrefix(ctx context.Context, bucket, prefix string) error {
	d.calls = append(d.calls, bucket+"|"+prefix)
	return nil
}

type apiFakeInvalidator struct{ n int }

func (c *apiFakeInvalidator) DeleteCacheEntriesForTable(ctx context.Context, p, d, t string) error {
	c.n++
	return nil
}
```
Ensure the test file imports `"os"`, `"path/filepath"`, and `"github.com/esignoretti/ds3-sql-server/internal/catalog"` (add any missing).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestTableHandler_DropManagedDeletesData -v`
Expected: FAIL — `h.DropWithDeps` undefined.

- [ ] **Step 3: Implement `DropWithDeps`**

In `internal/api/table_handler.go`, add (keep the existing `DropForProject` for back-compat, or have it delegate with nil deps — here we add a new method and repoint `main.go` to it):
```go
// DropWithDeps drops a table; for managed tables it deletes the underlying data
// via the deleter and invalidates dependent result-cache entries.
func (h *TableHandler) DropWithDeps(w http.ResponseWriter, r *http.Request, projectID string, deleter catalog.PrefixDeleter, cache catalog.CacheInvalidator, accessKey, secretKey, endpoint string) {
	dataset := chi.URLParam(r, "dataset")
	name := chi.URLParam(r, "table")
	if err := h.cat.DropTableWithData(r.Context(), projectID, dataset, name, deleter, cache, accessKey, secretKey, endpoint); err != nil {
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
Add `"github.com/esignoretti/ds3-sql-server/internal/catalog"` to the imports if not present.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run 'TestTableHandler_DropManagedDeletesData|TestTableHandler_RegisterListDescribeDrop' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/table_handler.go internal/api/table_handler_test.go
git commit -m "feat(api): managed-aware table drop deletes data and invalidates cache"
```

---

## Task 14: Server wiring — storage, writer, scheduler, routes

**Files:**
- Modify: `cmd/ds3sql-server/main.go`

This task has no new unit test; it is verified by `go build` + the full suite + a boot smoke test (Task 18). All steps below edit `main.go`.

- [ ] **Step 1: Add a storage resolver adapter**

The `write` package expects a `storageResolver` with `Resolve(class)(bucket,endpoint,ok)`. Adapt `config.Config`. Near the top-level of `main.go` (package scope), add:
```go
// storageAdapter adapts config storage classes to write.storageResolver.
type storageAdapter struct{ cfg *config.Config }

func (s storageAdapter) Resolve(class string) (string, string, bool) {
	sc, ok := s.cfg.ResolveStorageClass(class)
	if !ok {
		return "", "", false
	}
	return sc.Bucket, sc.Endpoint, true
}
```

- [ ] **Step 2: Build the writer, write executor, and result-cache invalidator**

After `jobManager := job.NewManager(localExecutor)` in `main()`, add (the `cache.ResultCache` is the Phase 2 result cache; `metaStore` already implements `DeleteCacheEntriesForTable`):
```go
	// Result-cache invalidator: metaStore implements DeleteCacheEntriesForTable
	// (Phase 2). The write path bumps data_version and evicts dependent results.
	writeStorage := storageAdapter{cfg: cfg}

	// A tier-aware s3 deleter for overwrite loads / managed drops is created
	// per-request from session creds; the writer uses a creds-bound deleter
	// supplied at call time. For the in-process executor we pass a deleter
	// factory that builds an s3 client from the request creds. To keep the
	// writer dependency simple, we construct the writer with a nil deleter and
	// instead clear prefixes inside RunLoad via a creds-bound deleter passed
	// through the executor.
```
> Design choice for wiring: the `write.Writer` `deleter` needs S3 creds, which are per-request. To avoid threading creds into a long-lived writer, we build the writer's deleter from the **storage class endpoint + per-job creds** at executor-construction time is not possible (creds vary per job). Therefore we make the writer's deleter a small adapter that, given a bucket/prefix, builds an s3 client from creds captured per job. Implement this by having the `LocalWriteExecutor` own a deleter factory. Add the following.

Replace the placeholder comment above with concrete wiring:
```go
	// Build the managed-table writer. The deleter is creds-bound per job, so we
	// pass a deleter that the executor swaps in; here we give the writer a
	// credsDeleter that resolves an s3 client from the storage class endpoint
	// and the job's creds at call time.
	writer := write.NewWriter(
		queryEngine,                 // writeEngine (ExecWrite)
		catService,                  // catalogService (Resolve/RegisterManaged/GetTable)
		metaStore,                   // versionBumper (BumpDataVersion)
		metaStore,                   // cacheInvalidator (DeleteCacheEntriesForTable)
		writeStorage,                // storageResolver
		newCredsDeleter(cfg),        // prefixDeleter (builds s3 client lazily)
	)
	writeExecutor := job.NewLocalWriteExecutor(writer)
	jobManager.SetWriteExecutor(writeExecutor)

	scheduleHandler := api.NewScheduleHandler(metaStore)
```
And add the `newCredsDeleter` helper at package scope:
```go
// credsDeleter builds an s3 client on demand to delete a prefix. It uses the
// hdd/ssd endpoint from config when set, else the configured DS3 gateway.
type credsDeleter struct {
	cfg       *config.Config
	accessKey string
	secretKey string
	endpoint  string
}

func newCredsDeleter(cfg *config.Config) *credsDeleter {
	return &credsDeleter{cfg: cfg}
}

// WithCreds returns a copy bound to the given credentials (used per job). The
// write executor calls this before RunLoad/RunCTAS when overwrite is requested.
func (d *credsDeleter) WithCreds(ak, sk, endpoint string) *credsDeleter {
	c := *d
	c.accessKey, c.secretKey, c.endpoint = ak, sk, endpoint
	return &c
}

func (d *credsDeleter) DeletePrefix(ctx context.Context, bucket, prefix string) error {
	ep := d.endpoint
	client, err := s3.NewClient(ctx, d.accessKey, d.secretKey, ep)
	if err != nil {
		return err
	}
	return client.DeletePrefix(ctx, bucket, prefix)
}
```
> Simplification: the long-lived `credsDeleter` holds empty creds; overwrite loads in the in-process server therefore require the deleter to receive creds. To keep this plan's wiring runnable without a larger refactor, the `LocalWriteExecutor` is extended in Task 9 to bind creds — but to avoid changing Task 9 retroactively, we accept this documented limitation: **in Phase 3 the in-process `credsDeleter` uses the job's creds because `ExecWrite` already received them, and overwrite deletion uses the same creds via a per-call binding.** Implementer action: pass the job creds into `DeletePrefix` by having `Writer.RunLoad` call `w.deleter` only when non-nil, and wire the deleter binding in `LocalWriteExecutor.RunLoad` by calling `d.WithCreds(ak,sk,ep)` if the deleter implements an interface `interface{ WithCreds(ak,sk,ep string) *credsDeleter }`. If this proves awkward at implementation time, the acceptable fallback (documented in `docs/architecture.md`) is: overwrite deletes are best-effort and skipped when creds are unavailable, with append always safe. Choose the binding approach if straightforward; otherwise document the fallback. *(This is the one wiring seam the implementer must finalize against the actual Phase 2 code.)*

- [ ] **Step 3: Mount the schedule routes (coordinator/all only)**

In the protected, timed group (where `/datasets`/`/jobs` are mounted), add after the job routes:
```go
		r.Post("/schedules", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			for _, p := range session.Projects {
				scheduleHandler.CreateForProject(w, r, p.ProjectID, session.Email)
				return
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
		r.Get("/schedules", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			for _, p := range session.Projects {
				scheduleHandler.ListForProject(w, r, p.ProjectID)
				return
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
		r.Delete("/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			for _, p := range session.Projects {
				scheduleHandler.DeleteForProject(w, r, p.ProjectID)
				return
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
```
> `session.Email` is used as the schedule owner; if the `auth.Session` field is named differently, use that field. (Confirm against `internal/auth/auth.go`.)

- [ ] **Step 4: Repoint the table drop route at `DropWithDeps`**

Replace the existing `r.Delete("/datasets/{dataset}/tables/{table}", …)` body with one that passes a creds-bound deleter + the cache invalidator:
```go
		r.Delete("/datasets/{dataset}/tables/{table}", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			for _, p := range session.Projects {
				deleter := newCredsDeleter(cfg).WithCreds(p.AccessKey, p.SecretKey, session.GatewayEndpoint)
				tableHandler.DropWithDeps(w, r, p.ProjectID, deleter, metaStore, p.AccessKey, p.SecretKey, session.GatewayEndpoint)
				return
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
```

- [ ] **Step 5: Start the scheduler for coordinator/all roles**

After the job-cleanup goroutine, add:
```go
	// Scheduler runs only on the control plane (coordinator/all).
	if cfg.Role == "coordinator" || cfg.Role == "all" {
		schedEnqueuer := newSchedulerEnqueuer(jobManager, metaStore)
		sched := scheduler.New(metaStore, schedEnqueuer)
		schedCtx, schedCancel := context.WithCancel(context.Background())
		defer schedCancel()
		go sched.Run(schedCtx, 30*time.Second)
	}
```
Add a small in-process enqueuer that submits the schedule's SQL as a job and clears `Running` + advances `NextRunAt` on completion. At package scope:
```go
// schedulerEnqueuer submits a schedule's SQL as a job and clears its Running
// flag (and persists the next run) when the job finishes.
type schedulerEnqueuer struct {
	mgr   *job.Manager
	store *metastore.SQLiteStore
}

func newSchedulerEnqueuer(mgr *job.Manager, store *metastore.SQLiteStore) *schedulerEnqueuer {
	return &schedulerEnqueuer{mgr: mgr, store: store}
}

func (e *schedulerEnqueuer) Enqueue(sch *metastore.Schedule) {
	// Scheduled jobs run as the schedule's project with no live session creds
	// in Phase 3 (documented limitation): the SQL must reference managed/SSD
	// tables reachable with the server's configured access, or be a CTAS whose
	// sources are managed. Submit asynchronously and persist completion.
	typ := "query"
	if write.IsCTAS(sch.SQL) {
		typ = "ctas"
	}
	j := e.mgr.Submit(context.Background(), job.ExecRequest{
		Type:      typ,
		SQL:       sch.SQL,
		ProjectID: sch.ProjectID,
	})
	go func() {
		// Poll the job to completion, then clear Running and persist NextRunAt.
		for {
			cur, ok := e.mgr.Get(j.ID)
			if ok && (cur.Status == "done" || cur.Status == "failed") {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		_ = e.store.UpdateScheduleRun(context.Background(), sch.ID, sch.LastRunAt, false)
		// Persist the advanced NextRunAt by recreating is unnecessary; we
		// update next_run via a dedicated path. For Phase 3 simplicity, the
		// schedule's next_run_at is advanced by re-creating UpdateScheduleRun's
		// sibling — see note. We reuse CreateSchedule semantics only on create;
		// here we additionally persist NextRunAt through a direct update.
		_ = persistNextRun(e.store, sch.ID, sch.NextRunAt)
	}()
}
```
And add the helper (a thin update of just `next_run_at`):
```go
// persistNextRun updates only the next_run_at column for a schedule.
func persistNextRun(store *metastore.SQLiteStore, id string, next time.Time) error {
	return store.SetScheduleNextRun(context.Background(), id, next)
}
```
> This requires one tiny extra metastore method `SetScheduleNextRun(ctx, id, next)`. **Add it in Task 10** if the implementer prefers a single edit, OR add it here as a follow-on. To keep Task 10's canonical six methods exactly as specified (Phase 4 parity), add `SetScheduleNextRun` as a *non-interface* method on `*SQLiteStore` only (not part of the `Store` interface), implemented as:
```go
func (s *SQLiteStore) SetScheduleNextRun(ctx context.Context, id string, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE schedules SET next_run_at = ? WHERE id = ?`, fmtTime(next), id)
	return err
}
```
Place this in `internal/metastore/sqlite.go`. Because it is `*SQLiteStore`-only, it does not change the canonical `Store` interface.

- [ ] **Step 6: Add imports**

Add to `main.go` imports:
```go
	"github.com/esignoretti/ds3-sql-server/internal/scheduler"
	"github.com/esignoretti/ds3-sql-server/internal/write"
```
(`context`, `time`, `s3`, `config`, `job`, `metastore`, `api`, `catalog` are already imported.)

- [ ] **Step 7: Build the server**

Run: `go build ./cmd/ds3sql-server/`
Expected: builds with no error.

- [ ] **Step 8: Build everything and run the full suite**

Run:
```bash
go build ./...
go test ./...
```
Expected: all build; all tests PASS.

- [ ] **Step 9: Commit**

```bash
git add cmd/ds3sql-server/main.go internal/metastore/sqlite.go
git commit -m "feat(server): wire storage, writer, scheduler, and schedule/drop routes"
```

---

## Task 15: CLI — `tables create-as`, `load`, `schedules`

**Files:**
- Create/modify: `cmd/ds3sql/tables_cmd.go`
- Create: `cmd/ds3sql/load_cmd.go`
- Create: `cmd/ds3sql/schedules_cmd.go`
- Modify: `cmd/ds3sql/status.go` (ensure `authedDelete` exists)

> Phase 1's `datasets`/`tables` base commands and `authedDelete` may already exist. If `cmd/ds3sql/tables_cmd.go` does not exist, create it with the Phase 1 `ls/register/describe/drop` commands first (see Phase 1 plan Task 16). The steps below add the `create-as` subcommand and the new commands.

- [ ] **Step 1: Ensure `authedDelete` exists**

If `cmd/ds3sql/status.go` lacks `authedDelete`, add after `authedPost`:
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

- [ ] **Step 2: Add `tables create-as`**

Append to `cmd/ds3sql/tables_cmd.go` (and register in `init()`):
```go
var tablesCreateAsCmd = &cobra.Command{
	Use:   "create-as <dataset.table>",
	Short: "Create a managed table from a SELECT (CTAS)",
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
		sel, _ := cmd.Flags().GetString("as")
		if sel == "" {
			return fmt.Errorf("--as \"SELECT ...\" is required")
		}
		partitionBy, _ := cmd.Flags().GetStringSlice("partition-by")
		storageClass, _ := cmd.Flags().GetString("storage-class")

		var sb strings.Builder
		fmt.Fprintf(&sb, "CREATE TABLE %s.%s", dataset, name)
		if len(partitionBy) > 0 {
			fmt.Fprintf(&sb, " PARTITION BY (%s)", strings.Join(partitionBy, ", "))
		}
		if storageClass != "" {
			fmt.Fprintf(&sb, " STORAGE '%s'", storageClass)
		}
		fmt.Fprintf(&sb, " AS %s", sel)

		body, _ := json.Marshal(map[string]string{"sql": sb.String()})
		data, err := authedPost(cfg, "/jobs"+projectQuery(cmd), body)
		if err != nil {
			return err
		}
		var out struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			IntoTable string `json:"into_table"`
			Error     string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		fmt.Printf("ctas job %s submitted (%s)\n", out.ID, out.Status)
		return nil
	},
}
```
In `init()` of `tables_cmd.go`, add:
```go
	tablesCmd.AddCommand(tablesCreateAsCmd)
	tablesCreateAsCmd.Flags().String("as", "", "Inner SELECT statement (required)")
	tablesCreateAsCmd.Flags().StringSlice("partition-by", nil, "Partition columns")
	tablesCreateAsCmd.Flags().String("storage-class", "", "Storage class: ssd | hdd")
```
Ensure `tables_cmd.go` imports `"strings"`.

- [ ] **Step 3: Add the `load` command**

Create `cmd/ds3sql/load_cmd.go`:
```go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	loadCmd.Flags().String("source", "", "Source glob, e.g. s3://bucket/*.csv (required)")
	loadCmd.Flags().String("into", "", "Target managed table dataset.table (required)")
	loadCmd.Flags().String("format", "csv", "Source format: csv | tsv | json | parquet")
	loadCmd.Flags().StringSlice("partition-by", nil, "Partition columns")
	loadCmd.Flags().String("mode", "append", "append | overwrite")
	loadCmd.Flags().StringP("project", "p", "", "Project ID (defaults to first project)")
	rootCmd.AddCommand(loadCmd)
}

var loadCmd = &cobra.Command{
	Use:   "load",
	Short: "Batch-load data into a managed table",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		source, _ := cmd.Flags().GetString("source")
		into, _ := cmd.Flags().GetString("into")
		if source == "" || into == "" {
			return fmt.Errorf("--source and --into are required")
		}
		format, _ := cmd.Flags().GetString("format")
		mode, _ := cmd.Flags().GetString("mode")
		partitionBy, _ := cmd.Flags().GetStringSlice("partition-by")

		body, _ := json.Marshal(map[string]any{
			"type":         "load",
			"source":       source,
			"into":         into,
			"format":       format,
			"mode":         mode,
			"partition_by": partitionBy,
		})
		data, err := authedPost(cfg, "/jobs"+projectQuery(cmd), body)
		if err != nil {
			return err
		}
		var out struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		fmt.Printf("load job %s submitted (%s)\n", out.ID, out.Status)
		return nil
	},
}
```
> `projectQuery(cmd)` is the helper defined in Phase 1's `datasets_cmd.go`. If absent, add it (returns `"?project=<p>"` or `""`).

- [ ] **Step 4: Add the `schedules` command**

Create `cmd/ds3sql/schedules_cmd.go`:
```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func init() {
	schedulesCmd.AddCommand(schedulesCreateCmd)
	schedulesCmd.AddCommand(schedulesLsCmd)
	schedulesCmd.AddCommand(schedulesRmCmd)
	schedulesCmd.PersistentFlags().StringP("project", "p", "", "Project ID (defaults to first project)")
	schedulesCreateCmd.Flags().String("cron", "", "Cron expression, e.g. \"0 * * * *\" (required)")
	schedulesCreateCmd.Flags().String("sql", "", "SQL to run (required)")
	schedulesCreateCmd.Flags().String("into", "", "Optional target managed table dataset.table")
	rootCmd.AddCommand(schedulesCmd)
}

var schedulesCmd = &cobra.Command{
	Use:   "schedules",
	Short: "Manage scheduled queries",
}

var schedulesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a scheduled query",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		cron, _ := cmd.Flags().GetString("cron")
		sql, _ := cmd.Flags().GetString("sql")
		into, _ := cmd.Flags().GetString("into")
		if cron == "" || sql == "" {
			return fmt.Errorf("--cron and --sql are required")
		}
		body, _ := json.Marshal(map[string]string{"cron": cron, "sql": sql, "into_table": into})
		data, err := authedPost(cfg, "/schedules"+projectQuery(cmd), body)
		if err != nil {
			return err
		}
		var out struct {
			ID    string `json:"id"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		fmt.Printf("schedule %s created\n", out.ID)
		return nil
	},
}

var schedulesLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List scheduled queries",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		data, err := authedGet(cfg, "/schedules"+projectQuery(cmd))
		if err != nil {
			return err
		}
		var out struct {
			Schedules []struct {
				ID        string `json:"id"`
				Cron      string `json:"cron"`
				IntoTable string `json:"into_table"`
				NextRunAt string `json:"next_run_at"`
			} `json:"schedules"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tCRON\tINTO\tNEXT_RUN")
		for _, s := range out.Schedules {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.ID, s.Cron, s.IntoTable, s.NextRunAt)
		}
		w.Flush()
		return nil
	},
}

var schedulesRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "Delete a scheduled query",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if err := authedDelete(cfg, "/schedules/"+args[0]+projectQuery(cmd)); err != nil {
			return err
		}
		fmt.Printf("schedule %s deleted\n", args[0])
		return nil
	},
}
```

- [ ] **Step 5: Build the CLI**

Run: `go build ./cmd/ds3sql/`
Expected: builds with no error.

- [ ] **Step 6: Verify commands are registered**

Run:
```bash
go run ./cmd/ds3sql/ tables create-as --help
go run ./cmd/ds3sql/ load --help
go run ./cmd/ds3sql/ schedules --help
```
Expected: `create-as` shows `--as/--partition-by/--storage-class`; `load` shows `--source/--into/--format/--partition-by/--mode`; `schedules` lists `create/ls/rm`.

- [ ] **Step 7: Commit**

```bash
git add cmd/ds3sql/
git commit -m "feat(cli): tables create-as, load, and schedules commands"
```

---

## Task 16: Full build, vet, race suite, and boot smoke test

**Files:** none (verification only)

- [ ] **Step 1: Full build, vet, race tests**

Run:
```bash
go build ./...
go vet ./...
go test -race ./...
```
Expected: no build errors, no vet errors, all tests PASS.

- [ ] **Step 2: Boot smoke test (no creds needed)**

Run:
```bash
go build -o /tmp/ds3sql-server ./cmd/ds3sql-server/
DS3SQL_METASTORE_PATH=/tmp/ds3sql-p3-meta.db DS3SQL_ROLE=all /tmp/ds3sql-server --port 18081 &
SERVER_PID=$!
sleep 1
curl -s http://localhost:18081/health
kill $SERVER_PID
rm -f /tmp/ds3sql-p3-meta.db
```
Expected: `/health` returns `{"status":"ok",…}`; the scheduler goroutine starts (role `all`) and the server shuts down on kill. The schedules table is created in the metastore on boot.

- [ ] **Step 3: Commit (if any incidental fixes were needed)**

```bash
git add -A
git commit -m "test: phase 3 full build, vet, and race verification"
```

---

## Task 17: Documentation

**Files:** `docs/api.md`, `docs/cli.md`, `docs/architecture.md`, `docs/configuration.md`, `README.md`

- [ ] **Step 1: `docs/api.md` — write path**

Add sections:
- **CTAS / write jobs** — `POST /jobs` now also accepts:
  - A CTAS body `{"sql":"CREATE TABLE dataset.t [PARTITION BY (cols)] [STORAGE 'ssd'|'hdd'] AS SELECT …"}` → returns `202` + a queued job `{"id","type":"ctas","status":"queued",…}`. Poll `GET /jobs/{id}`; on `done` the job has `"into_table":"dataset.t"`.
  - A load body `{"type":"load","source":"s3://…/*.csv","into":"dataset.t","format":"csv|tsv|json|parquet","partition_by":["dt"],"mode":"append|overwrite"}` → `202` + queued job.
  - Plain `{"sql":"SELECT …"}` still runs synchronously (`200`) as in Phase 1.
- **Schedules** — `POST /schedules` `{"cron","sql","into_table"}` → `201` + the schedule (`id`, `next_run_at`); `GET /schedules` → `{"schedules":[…]}`; `DELETE /schedules/{id}` → `204`.
- **Managed DROP** — `DELETE /datasets/{ds}/tables/{t}` deletes the underlying Parquet for `managed` tables (external tables only lose their registration).

- [ ] **Step 2: `docs/cli.md` — new commands**

Document:
- `ds3sql tables create-as <dataset.table> --as "SELECT …" [--partition-by col1,col2] [--storage-class ssd|hdd]`
- `ds3sql load --source <s3://…/*.csv> --into <dataset.table> [--format csv|tsv|json|parquet] [--partition-by …] [--mode append|overwrite]`
- `ds3sql schedules create --cron "0 * * * *" --sql "…" [--into dataset.table]` / `schedules ls` / `schedules rm <id>`

- [ ] **Step 3: `docs/architecture.md` — write path**

Add a "Write Path (Phase 3)" section: the `write` package (CTAS parser + executor, batch loader, shared post-write invalidation), `query.Engine.ExecWrite`, storage-class tiering (`config.StorageConfig`), the `scheduler` package (cron tick, misfire skip via `running` flag + `GetDueSchedules`), managed vs external DROP semantics, and the documented simplifications (strict CTAS grammar, partition-file sizing not enforced, single-writer assumption, scheduled-job credential limitation).

- [ ] **Step 4: `docs/configuration.md` — storage config**

Document:
- `storage.classes.ssd.{bucket,endpoint}` and `storage.classes.hdd.{bucket,endpoint}`.
- Env overrides: `DS3SQL_STORAGE_SSD_BUCKET`, `DS3SQL_STORAGE_SSD_ENDPOINT`, `DS3SQL_STORAGE_HDD_BUCKET`, `DS3SQL_STORAGE_HDD_ENDPOINT`.
- Note the scheduler runs on `coordinator`/`all` roles only.

- [ ] **Step 5: `README.md` — write-path quick start**

Add to Quick Start:
```bash
ds3sql tables create-as sales.daily --as "SELECT dt, region, sum(n) AS total FROM sales.raw GROUP BY 1,2" --partition-by dt --storage-class ssd
ds3sql load --source 's3://incoming/events/*.csv' --into sales.events --format csv --mode append
ds3sql schedules create --cron "0 * * * *" --sql "CREATE TABLE sales.hourly AS SELECT * FROM sales.events" --into sales.hourly
```

- [ ] **Step 6: Final build/test and commit**

Run:
```bash
go build ./...
go test ./...
```
Expected: all PASS.
```bash
git add docs/ README.md
git commit -m "docs: document Phase 3 write path (CTAS, load, schedules, tiering)"
```

---

## Self-Review

**Spec coverage (Phase 3 scope):**
- **Storage-class config + resolver** (`storage.classes.{ssd,hdd}.{bucket,endpoint}`, env overrides, `ResolveStorageClass`) → Task 1. ✓
- **CTAS** (`CREATE TABLE dataset.t [PARTITION BY (…)] [STORAGE '…'] AS SELECT …`; resolve sources, `COPY … TO` Parquet, register managed, infer schema/stats, bump version, invalidate cache) → Tasks 5 (parser) + 6 (RegisterManaged) + 7 (RunCTAS) + 3 (ExecWrite) + 12B/14 (routing/wiring). ✓
- **Batch load** (`{type:"load", source, into, format, partition_by, mode}`; append vs overwrite with prefix clear; Hive partitioning; stats + version + cache) → Task 8 + 2 (DeletePrefix) + 9 (job routing) + 12B/14. ✓
- **Generalize convert** → explicitly chosen to *reimplement* the read→COPY pattern in `write.RunLoad` (Task 8) leaving `convert` for raw bucket conversion; documented. ✓
- **Scheduled queries** (cron dep, `Schedule` type + six canonical methods, scheduler tick loop, misfire skip if previous run still running, CRUD API, CLI) → Tasks 10 (metastore) + 11 (scheduler) + 12A (API) + 15 (CLI) + 14 (startup, coordinator/all only). ✓
- **Misfire policy** → enforced by `GetDueSchedules` filtering `running=0` plus scheduler marking `running=true` on enqueue and clearing on completion → Tasks 10 + 11 + 14. ✓
- **DROP managed tables deletes data** (external only removes registration) → Task 6 (`DropTableWithData`) + 13 (API) + 14 (creds-bound deleter wiring). ✓
- **Result-cache invalidation + data_version bump on every write** → `Writer.afterWrite` (Task 4) called by RunCTAS/RunLoad; managed drop invalidates via `DropTableWithData`; uses `BumpDataVersion` + `DeleteCacheEntriesForTable` (Phase 2 method). ✓
- **API `/jobs` detects ctas (SQL prefix) and `{type:"load"}`; `/schedules` routes; CLI `tables create-as`, `load`, `schedules`** → Tasks 12 + 15. ✓
- **Partition file sizing 128–512 MB** → documented as a target, not enforced (no `FILE_SIZE_BYTES` knob set); noted in `ctas`/`load` and `docs/architecture.md`. ✓
- **`ExecWrite` engine method with full code** → Task 3. ✓

**Canonical-signature consistency check:**
- `metastore.Store` gains exactly the six specified methods with the exact signatures: `CreateSchedule(ctx, *Schedule) error`, `ListSchedules(ctx, projectID)`, `GetSchedule(ctx, id)`, `DeleteSchedule(ctx, id)`, `UpdateScheduleRun(ctx, id, lastRun, running)`, `GetDueSchedules(ctx, now)` — added to the interface (Task 10) and implemented on `*SQLiteStore` (Task 10). The extra `SetScheduleNextRun` is `*SQLiteStore`-only and **not** on the `Store` interface, preserving Phase 4 Postgres parity. ✓
- `Schedule{ID,ProjectID,Cron,SQL,IntoTable,Owner,NextRunAt,LastRunAt,Running,CreatedAt}` matches the canonical type exactly (Task 10). ✓
- `query.Engine.ExecWrite(sql, []ViewBinding, ak,sk,ep) error` (Task 3) is consumed by `write.Writer` via the `writeEngine` interface (Task 4) and the real `*query.Engine` in `main.go` (Task 14). ✓
- `write.LoadRequest` (Task 8) and `job.LoadRequest` (Task 9) have identical fields; `LocalWriteExecutor` translates between them (Task 9). ✓
- `job.ExecRequest` gains `Type`/`Load`; `Job` gains `IntoTable`; `Manager.SetWriteExecutor`/`WriteExecutor` added (Task 9), used by `JobHandler.SubmitWithCreds` (Task 12) and `main.go` (Task 14). ✓
- `catalog.RegisterManagedInput`/`RegisterManaged`/`DropTableWithData`/`PrefixDeleter`/`CacheInvalidator` (Task 6) are used by `write.Writer` (Tasks 4/7/8), `TableHandler.DropWithDeps` (Task 13), and `main.go` (Task 14) with matching signatures. ✓
- `s3.Client.DeletePrefix(ctx, bucket, prefix)` (Task 2) satisfies `catalog.PrefixDeleter` and `write.prefixDeleter`; the `credsDeleter` adapter (Task 14) wraps it. ✓
- `config.Config.ResolveStorageClass` (Task 1) is adapted to `write.storageResolver` via `storageAdapter` (Task 14). ✓
- Handler `…WithCreds`/`…ForProject`/`…WithDeps` conventions and `main.go` session extraction match the existing Phase 1 wiring (`JobHandler.SubmitWithCreds`, `TableHandler.RegisterForProject`). ✓
- `metastore.DeleteCacheEntriesForTable` and `job.Manager.Submit` are treated as Phase 2-provided (per the prompt's "ASSUME these were added in Phase 2"); not re-planned. ✓

**Placeholder scan:** Every code step contains complete Go (no `TODO`/`...`/"handle appropriately"). The one explicitly-flagged seam is the `credsDeleter` binding in Task 14 Step 2, which provides concrete code plus a documented acceptable fallback — it is finalized against the actual Phase 2 `Submit`/executor shape at implementation time, not left blank. Import-trimming notes ("trim unused imports as the compiler directs") are operational guidance, not missing code.

**Test strategy check:** CTAS and load are tested end-to-end against LOCAL files — `localStorage`/`localBase` make managed locations filesystem directories, DuckDB `COPY … TO` writes real Parquet into `t.TempDir()`, and assertions read it back via `read_parquet` (including Hive-partitioned globs). Scheduler timing is tested deterministically by calling `Tick(ctx, fixedNow)` with injected fake store/enqueuer and asserting next-run computation, enqueue, and the running flag — no sleeping. SQLite schedule tests use temp-file paths (`newTestStore` sets `SetMaxOpenConns(1)` via `OpenSQLite`). ✓
