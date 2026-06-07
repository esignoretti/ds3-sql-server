# Architecture

DS3 SQL Server is a single Go binary that provides SQL querying of S3 data via DuckDB. It runs as a sidecar alongside the Cubbit DS3 Gateway.

## System Diagram

```
┌──────────────────────────────────────────────────────┐
│  HTTP Server (chi router)                             │
│  ├── Static files (/static)                           │
│  ├── Templates (HTML pages)                           │
│  └── API Handlers (/auth, /buckets, /query, /schema)  │
├──────────────────────────────────────────────────────┤
│  Services Layer                                       │
│  ├── Auth Service (Cubbit IAM challenge-response)     │
│  ├── DuckDB Engine (CGo, in-process)                  │
│  └── S3 Discovery (bucket/prefix listing)             │
├──────────────────────────────────────────────────────┤
│  DuckDB (in-memory per query)                         │
│  ├── httpfs extension (S3 access)                     │
│  └── parquet extension                                │
├──────────────────────────────────────────────────────┤
│  S3 Client (aws-sdk-go-v2 → DS3 Gateway endpoint)     │
└──────────────────────────────────────────────────────┘
```

## Key Design Decisions

### Stateless Per-Query DuckDB

Each SQL query creates a fresh in-memory DuckDB instance, configures it with the user's S3 credentials, executes the SQL, collects results, and discards the instance. No state persists between queries. This makes the server horizontally scalable — any number of replicas can serve any request.

### Sidecar Deployment

The server is designed to run in the same Kubernetes pod as the DS3 Gateway, communicating over localhost. This eliminates network latency and keeps S3 traffic within the pod. The server binds to localhost by default and is not exposed externally.

### Cubbit IAM Authentication

Authentication uses Cubbit's IAM challenge-response protocol with Ed25519 signatures. Per-project S3 credentials are automatically provisioned via Cubbit Keyvault, so users don't need to manage API keys manually.

### Three Access Interfaces

- **REST API**: JSON endpoints for programmatic access and integration
- **CLI**: Cobra-based terminal client (`ds3sql`) for scripting and terminal workflows
- **Web UI**: Server-rendered HTML with HTMX for interactive browsing without a build step

## Query Flow

1. User submits SQL via CLI, Web UI, or API call
2. Server validates JWT, extracts S3 credentials from session
3. Server creates in-memory DuckDB instance
4. DuckDB loads `httpfs` + `parquet` extensions
5. Server configures httpfs with DS3 Gateway endpoint + credentials
6. DuckDB executes SQL against S3 files
7. Server collects column names, types, and rows
8. DuckDB instance is discarded
9. Server returns JSON response

## Write Path (Phase 3)

Phase 3 extends DS3 SQL Server from read-only querying to a full write path: `CREATE TABLE … AS SELECT` (CTAS), batch load from source files, and cron-driven scheduling of both operations. Written data is stored as Parquet in storage-class-buckets (SSD/HDD tiers) and tracked in the metastore as *managed* tables.

### System Diagram (Write Path)

```
┌──────────────────────────────────────────────────────────────────┐
│  HTTP Server (chi router)                                         │
│  ├── /jobs          POST/GET/DELETE  — CTAS, load, query jobs     │
│  ├── /schedules     POST/GET/DELETE  — cron schedules             │
│  ├── /datasets/*/tables  CRUD        — table registry             │
├──────────────────────────────────────────────────────────────────┤
│  Write Packages                                                   │
│  ├── internal/write    — CTAS parser + executor, batch loader     │
│  ├── internal/scheduler — cron tick, misfire-skip, job enqueue    │
│  ├── internal/job      — async job manager (in-memory queue)      │
├──────────────────────────────────────────────────────────────────┤
│  Catalog (registry)                                               │
│  ├── internal/catalog — RegisterManaged, DropTableWithData        │
│  ├── internal/metastore — SQLite-based table/schedule registry    │
├──────────────────────────────────────────────────────────────────┤
│  DuckDB Engine                                                    │
│  ├── ExecWrite — COPY ... TO for Parquet output                   │
│  ├── QueryView  — catalog-aware SELECT with view bindings         │
├──────────────────────────────────────────────────────────────────┤
│  S3 Client (aws-sdk-go-v2 → DS3 Gateway / storage-class buckets)  │
└──────────────────────────────────────────────────────────────────┘
```

### Components

#### `internal/write` package

- **CTAS parser** (`ctas.go`): A deliberately strict regex-based parser for the supported CTAS grammar. Rejects anything outside `CREATE TABLE [IF NOT EXISTS] <ds>.<tbl> [PARTITION BY (...)] [STORAGE 'ssd'|'hdd'] AS SELECT ...`. Not a general SQL parser.
- **CTAS executor** (`ctas.go`): Resolves referenced catalog tables, builds a `COPY (SELECT ...) TO 's3://...' (FORMAT PARQUET, ...)` statement, executes via `query.Engine.ExecWrite`, then registers the result as a managed table via `catalog.Service.RegisterManaged`.
- **Batch loader** (`load.go`): Reads source files (CSV, TSV, JSON, Parquet) via DuckDB reader functions and writes Parquet to a storage-class bucket. Supports append and overwrite modes. Overwrite clears the managed prefix before writing.
- **Post-write invalidation** (`write.go`): After every write, bumps the table's `data_version` in the metastore and evicts dependent result-cache entries so subsequent queries see fresh data.

#### `query.Engine.ExecWrite`

A counterpart to `QueryView` that executes statements producing no result rows (e.g. `COPY ... TO`). It registers catalog tables as DuckDB views, applies S3 credentials, and executes. Created schemas are dropped after completion.

#### Storage-Class Tiering

Managed tables are written to either the `ssd` or `hdd` storage class. Each class maps to a distinct DS3 bucket and optional endpoint in `config.StorageConfig`:

```yaml
storage:
  classes:
    ssd:
      bucket: ds3-fast
      endpoint: ""
    hdd:
      bucket: ds3-cold
      endpoint: ""
```

The endpoint can target different DS3 Gateway instances (e.g. an NVMe-backed gateway for SSD and a HDD-backed gateway for cold storage). An empty endpoint means "use the session's gateway endpoint."

#### `internal/scheduler` package

- **Cron tick**: Polls `GetDueSchedules` every 30 seconds (on coordinator or all roles).
- **Misfire skip**: Uses a `running` flag per schedule. If a schedule is still running when its next tick fires, the tick is skipped entirely — no overlapping runs are allowed.
- **Enqueue**: Hands each due schedule to a `schedulerEnqueuer` that submits a job via `job.Manager` and starts a goroutine to poll for completion. When the job finishes (done or failed), the `running` flag is cleared and `next_run_at` is advanced.

#### `internal/job` package

In-memory async job manager. Jobs can be `query`, `ctas`, or `load`. Plain queries default to synchronous fast-path; CTAS and load always return `202 Accepted` with a queued job object. The caller polls `GET /jobs/{id}` for completion.

### Managed vs External Tables

| Aspect | External | Managed |
|--------|----------|---------|
| **Created by** | `POST /datasets/{ds}/tables` (register) | CTAS, load |
| **Data location** | User-specified S3 path | Storage-class bucket under `_managed/{dataset}/{table}/` |
| **Format** | Any (parquet, csv, tsv, json) | Always Parquet |
| **DROP semantics** | Registration only; source data preserved | Underlying Parquet files are deleted |
| **Storage class** | User-specified hint | Determined by CTAS/load options |

### Documented Simplifications

1. **Strict CTAS grammar**: Only the documented syntax is accepted. No support for `WITH`, `UNION`, sub-queries in the `FROM` clause of the outer CTAS, or arbitrary DDL.
2. **Partition-file sizing not enforced**: DuckDB manages file sizes; the system does not coalesce or repartition small files.
3. **Single-writer assumption**: No distributed locking or optimistic concurrency control on table writes. Concurrent CTAS/load operations to the same table may interleave.
4. **Scheduled-job credential limitation**: Scheduled jobs use the project's S3 credentials from the session at schedule-creation time. Credential rotation requires re-creating schedules.

## Component Overview

| Component | Responsibility |
|-----------|---------------|
| Auth Service | IAM challenge-response, JWT management, token refresh, credential reconciliation |
| DuckDB Engine | Per-query DuckDB lifecycle, S3 credential injection, SQL execution, schema inference |
| S3 Discovery | Bucket and prefix listing via aws-sdk-go-v2 |
| Web UI | Login, browse, and query pages via html/template + HTMX |
| REST API | JSON endpoints for all functionality |

## Phase 4: Product Polish

Phase 4 adds a richer Web UI, an optional Postgres-backed metastore for high-availability deployments, and query-time partition pruning to reduce I/O on partitioned managed tables.

### Web Catalog Browser

The primary left-navigation in the Web UI is now the **catalog browser** (datasets → tables → columns), backed by the `/datasets…` JSON API and a server-rendered `/ui/catalog` fragment (`internal/api/catalog_fragment_handler.go`). The interactive tree is driven by `internal/web/static/catalog.js`, which fetches datasets and tables on demand and seeds the query editor when a table is clicked.

Raw bucket browsing (listing buckets → objects) is demoted to the secondary **Buckets** tab. The query tab includes a **jobs/history panel** (`internal/web/static/jobs.js`) that calls `GET /jobs` to display recent query, CTAS, and load jobs, and allows clicking a job to restore its SQL into the editor.

### Postgres Metastore

`internal/metastore/postgres.go` implements the full `Store` interface (datasets, tables, jobs, cache_index, schedules) against PostgreSQL. It mirrors the SQLite schema using Postgres-native types (`TIMESTAMPTZ` for timestamps, `BIGINT` for integer counters, `JSONB` for partition/schema/stats payloads) and uses `pgx/v5/stdlib` (via `database/sql`) for connection pooling.

The Postgres backend is selected at startup by setting `metastore.driver` to `postgres` and providing a DSN via `metastore.dsn`. A shared `testStoreConformance` suite (defined in `internal/metastore/conformance_test.go`) runs against both the SQLite and Postgres backends, ensuring identical behaviour. The Postgres conformance test skips when `DS3SQL_TEST_POSTGRES_DSN` is unset.

### Partition Pruning

`internal/planner/prune.go` is a pure predicate analyzer that parses `WHERE` clauses from an incoming SQL string and compares them against a table's stored partition values. The catalog layer's `ResolvePruned` method (in `internal/catalog/service.go`) calls `planner.Prune` to build a `ReaderSQL` expression that reads from only the matching partitions.

**Supported predicates:**
- `=`, `IN`, `>`, `>=`, `<`, `<=` comparisons on partition columns
- Multiple predicates combined with `AND`

**Unsupported forms** (fall back to scanning all partitions — correctness-preserving):
- `OR` / `NOT` at the top level
- Expressions or function calls on partition columns
- Non-partition column predicates (pruning is purely partition-column-based)
