# DS3 SQL — "Simplified BigQuery" Refactor

**Status:** Approved design (architecture + phased roadmap)
**Date:** 2026-06-07
**Supersedes (extends):** [2026-05-22-ds3-sql-server-design.md](2026-05-22-ds3-sql-server-design.md)

## Summary

DS3 SQL Server today is a stateless, Athena-style sidecar: a single Go binary
running per-query DuckDB instances for `SELECT`-only queries against
Parquet/CSV/JSON/TSV files in Cubbit DS3 buckets, exposed via REST API, a Cobra
CLI (`ds3sql`), and an HTMX web UI.

This design evolves it into a **simplified, scalable BigQuery**: a managed
catalog of datasets and tables over DS3, an async job execution model with a
synchronous fast-path, a coordinator + elastic worker-pool topology, a write
path (CTAS, batch load, scheduled queries), and aggressive server-side
acceleration (result cache, local-SSD data cache, metadata cache) with explicit
storage tiering across fast-SSD and HDD DS3 buckets.

The guiding principle is the **80/20 rule**: the 20% of BigQuery's features that
serve 80% of users. Several "big-cost / low-frequency" features are explicitly
deferred (distributed single-query execution, materialized views, dry-run cost
estimation).

The migration strategy is an **incremental in-place refactor** of the existing
repository (Approach A): keep a working tool at every step, reuse existing
packages (`auth`, `s3`, `query`, `convert`, `analysis`, `report`, `column`,
`web`), and add new control-plane packages behind a `--role` flag.

## Goals

- A managed **catalog**: query `dataset.table`, not raw `read_parquet('s3://…')`.
- **Scale with load**: independently scalable, stateless worker pool fronted by a
  coordinator.
- **Server-side performance**: result cache, transparent local-SSD data cache for
  HDD-backed data, and an in-memory metadata cache driving partition pruning.
- **Storage tiering**: derived/hot data written to fast-SSD DS3 buckets; cold
  HDD data transparently accelerated.
- **Write path**: CTAS, batch load (generalizing today's `convert`), scheduled
  queries.
- **Scriptable CLI** preserved and extended (`--json`, clean exit codes, async).
- **Operational simplicity by default**: embedded metadata store out of the box,
  external Postgres as an opt-in for larger deployments.

## Non-Goals (deferred)

- **Distributed single-query execution** (Dremel/Trino-style shuffle across
  workers). The coordinator↔worker boundary is designed as a clean seam so this
  can be added later without changing the client API. Until then, one query
  executes entirely on one worker.
- **Materialized views** (incremental refresh, staleness management).
- **Dry-run cost estimation** (bytes-to-scan preview). Feasible later atop the
  metadata cache; out of initial scope.
- **Fully automatic tiering / data migration** between SSD and HDD based on
  access patterns. Placement is a per-table hint; only the SSD *cache* is
  automatic.

## Key Decisions

| Area | Decision |
|------|----------|
| Execution scaling | Scale-out concurrency (1 query = 1 worker) with a clean seam for distributed execution later |
| Query surface | Full catalog: datasets → tables, Hive-style partitioning |
| Write path | CTAS + batch load + scheduled queries (materialized views deferred) |
| Caching | Result cache + local-SSD data cache + metadata cache (warm DuckDB pool retained) |
| Tiering | Per-table `storage_class` hint (`ssd`/`hdd`) + automatic SSD data cache |
| Execution model | Async jobs with a synchronous fast-path |
| Topology | Coordinator (HA-capable) + elastic stateless worker pool |
| Metadata store | Pluggable `MetadataStore`: embedded (SQLite) default, Postgres opt-in |
| Governance/UX | Query history, concurrency quotas/queue, catalog browser in Web UI |
| Migration | Incremental in-place refactor (Approach A); single binary, `--role` flag |

## Topology & Roles

One binary, two roles selected at runtime (`--role=coordinator|worker|all`),
sharing the existing packages. `--role=all` runs both in one process for dev and
small deployments, preserving today's single-process experience.

```
            ┌──────────────────────────────────────────────┐
   clients  │  COORDINATOR (1, or small HA set)             │
 CLI / API ─┼─▶ HTTP API (chi)                              │
  Web UI    │   ├─ Auth (Cubbit IAM)         ── carried over │
            │   ├─ Catalog service           ── NEW          │
            │   ├─ Job manager + queue/quotas ── NEW         │
            │   ├─ Planner (prune, routing)  ── NEW          │
            │   ├─ Result-cache index        ── NEW          │
            │   ├─ Scheduler (cron queries)  ── NEW          │
            │   └─ MetadataStore iface ─▶ embedded | Postgres│
            └───────────────┬──────────────────────────────┘
                            │ plan/fragment dispatch (gRPC or HTTP)
        ┌───────────────────┼───────────────────┐
        ▼                   ▼                   ▼
  ┌───────────┐       ┌───────────┐       ┌───────────┐
  │ WORKER    │  ...  │ WORKER    │  ...  │ WORKER    │   ← elastic pool
  │ DuckDB    │       │ DuckDB    │       │ DuckDB    │
  │ warm pool │       │ warm pool │       │ warm pool │
  │ local-SSD │       │ local-SSD │       │ local-SSD │
  │ data cache│       │ data cache│       │ data cache│
  └─────┬─────┘       └─────┬─────┘       └─────┬─────┘
        └──────────── S3 (DS3) ────────────────┘
            fast-SSD buckets  +  HDD buckets
```

- **Coordinator** — control plane only; holds no query data. Stateless with
  respect to the metadata store, so an HA set can share one Postgres backend.
  Responsibilities: authentication, catalog, planning (table resolution +
  partition pruning), result-cache lookup, admission control (quotas/queue),
  routing, job tracking/history, scheduling, serving the API and Web UI.
- **Worker** — data plane; a stateless DuckDB executor with a warm connection
  pool and a local-SSD data cache. Receives a resolved plan (tables → concrete
  S3 paths + credentials + SQL + per-job limits), executes, writes results to a
  results location, reports status and row/byte counts. Scales horizontally and
  independently of the coordinator.
- **Cache-locality routing** — the coordinator routes a job to a worker via
  consistent hashing over the pruned object/partition set, so a table's objects
  repeatedly land on the same worker and its SSD cache stays warm. Falls back to
  least-loaded when the preferred worker is saturated or absent.
- **Distributed-later seam** — the coordinator→worker protocol speaks "plan +
  fragment." Today one fragment = the whole query on one worker. Later, a plan
  can fan out into multiple fragments with an exchange/shuffle step without
  changing the client-facing API.

## Catalog & Metadata Model

Entities, persisted via the `MetadataStore` interface:

- **Dataset** — namespace owned by a Cubbit project; addressed as
  `project.dataset` (project scoping comes from IAM auth).
- **Table** — addressed as `dataset.table`, with:
  - `storage_class`: `ssd` | `hdd` — which configured bucket tier CTAS/load
    output is written to.
  - `kind`: `managed` (engine owns the files) | `external` (registers existing
    S3 data, read-only).
  - `location`: base S3 prefix (managed) or external glob (external).
  - `format`: `parquet` (managed default) | `csv` | `json` | `tsv` (external).
  - `schema`: columns + types, inferred on create and stored.
  - `partition_columns`: e.g. `[dt]` → Hive-style `dt=2026-06-07/` layout for
    pruning.
  - `stats`: row count, per-partition file list, per-column min/max/null counts.
    Drives partition pruning and result-cache scoping.
  - `data_version`: monotonic token bumped transactionally on any write. Part of
    the result-cache key, so writes auto-invalidate dependent cached results.

### MetadataStore interface (pluggable seam)

CRUD for `Datasets`, `Tables`, `Jobs`, `Schedules`, `CacheIndex`, plus a
transactional `data_version` bump. Implementations:

- **`embedded`** (default) — SQLite via `modernc.org/sqlite` (pure Go, avoids a
  second CGo dependency alongside go-duckdb), stored in a local directory, with
  an optional periodic snapshot to a DS3 bucket. Target: dev and small
  production. Effectively single-coordinator.
- **`postgres`** (opt-in via config) — same interface; supports an HA
  coordinator set and larger deployments.

Schemas, partition lists, and column stats are additionally cached in memory on
the coordinator (the **metadata cache**), refreshed on write or by TTL, so
planning and pruning rarely touch the store or S3.

## Job Model & Query Lifecycle

Every operation is a **job**: an ID, a `type` (`query` | `ctas` | `load` |
`scheduled`), a `status` (`queued` → `running` → `done` | `failed` |
`cancelled`), and a result pointer. Lifecycle:

1. Client submits SQL to the coordinator (`POST /jobs`).
2. **Auth** — resolve Cubbit project and S3 credentials (carried over).
3. **Result-cache check** — normalize the SQL, compute the key
   `(normalized_sql + referenced tables' data_versions)`. On hit, return the
   cached result location immediately; job marked `done (cached)`.
4. **Plan** — parse table refs (`dataset.table` → concrete S3 paths via the
   catalog), apply **partition pruning** from `WHERE` predicates + partition
   stats, attach credentials and per-job limits.
5. **Admission** — check the per-user/project **concurrency quota**. Under quota
   → dispatch; over → **queue** (FIFO per tenant, fair across tenants).
6. **Route** — consistent hash on the pruned object set (cache locality), else
   least-loaded worker.
7. **Execute** — worker runs DuckDB, streams the result to the results location
   (Parquet/Arrow on an SSD bucket), reports row/byte counts and status.
8. **Record** — coordinator writes the job to **query history** and updates the
   result-cache index.

**Synchronous fast-path** — `POST /jobs?wait=2s` (the default for
`ds3sql query`) blocks up to a threshold; if the job finishes within it, results
return inline, exactly like today's `POST /query`. Otherwise the call returns a
job ID and the client polls or streams (`GET /jobs/{id}`,
`GET /jobs/{id}/results?page=…`). `ctas`, `load`, and scheduled jobs are always
async.

**Cancellation & limits** — `DELETE /jobs/{id}` cancels. The existing
`max_rows`, `max_execution_seconds`, `max_result_bytes`, and `memory_limit`
become per-job settings enforced on the worker.

## Caching & Storage Tiering

Three cooperating layers:

- **Result cache (coordinator)** — index in the `MetadataStore`; payloads
  (result Parquet) on an SSD bucket. The key includes every referenced table's
  `data_version`, so any write to a table auto-invalidates dependent results
  with no manual purging. TTL plus LRU eviction by total size.
- **Local-SSD data cache (worker)** — caches Parquet objects / byte-ranges
  fetched from **HDD** buckets onto the worker's local SSD (configurable
  directory and size cap, LRU). Repeat scans of cold data hit SSD. SSD-bucket
  tables are already fast and bypass caching (or cache opportunistically).
  Consistent-hash routing keeps a table's objects on the same worker so the
  cache stays warm.
- **Metadata cache (coordinator)** — schemas, partition lists, and column stats
  held in memory; refreshed on write or TTL. Powers planning/pruning without
  hitting S3 or the store per query.

The pre-warmed DuckDB connection pool (httpfs/parquet loaded) is retained and
becomes a per-worker resource.

**Storage tiering** — each table's `storage_class` (`ssd`/`hdd`) selects the
destination bucket for CTAS/load output. Configuration maps logical classes to
real DS3 buckets:

```yaml
storage:
  classes:
    ssd: { bucket: "ds3-fast", endpoint: "..." }   # SSD-backed DS3 bucket
    hdd: { bucket: "ds3-cold", endpoint: "..." }    # HDD-backed DS3 bucket
  data_cache:
    dir: "/var/cache/ds3sql"
    max_bytes: 53687091200   # 50 GiB local SSD
result_cache:
  bucket: "ds3-fast"
  ttl: 1h
  max_bytes: 10737418240
```

Net effect: hot/derived data is *written* to SSD buckets by class; cold HDD data
is *transparently accelerated* by the worker SSD cache; repeated queries skip
execution entirely via the result cache.

## Write Path

All three are jobs that produce or refresh managed tables, reusing the worker
DuckDB engine and generalizing today's `convert` package.

- **CTAS** — `CREATE TABLE dataset.t [PARTITION BY (dt)] [STORAGE 'ssd'] AS
  SELECT …`. The worker runs the SELECT and writes Parquet (Hive-partitioned if
  requested) to the table's storage-class bucket via DuckDB `COPY … TO`. The
  coordinator registers the table, infers the schema, computes stats, and bumps
  `data_version`. Partition files target roughly 128–512 MB for scan efficiency.
- **Batch load** — `POST /jobs {type:load, source:"s3://…/*.csv",
  into:"dataset.t", format, partition_by}`. Generalizes the existing `convert`
  engine (CSV/JSON/log/syslog → Parquet) into "load into a catalog table,"
  writing to the table's tier and updating stats/version. Supports append vs
  overwrite.
- **Scheduled queries** — a `Schedule` row `(cron, sql, optional into-table,
  owner)` in the `MetadataStore`. The coordinator's scheduler enqueues a job at
  each tick (same admission/quota path). Misfire policy: skip if the previous
  run of the same schedule is still running. CLI: `ds3sql schedules
  {create,list,rm}`.

`DROP TABLE` / `DELETE` removes the catalog entry and (for managed tables) the
data, invalidating dependent result-cache entries via `data_version`.

## Interfaces

### REST API (job-centric; old endpoints preserved)

- `POST /jobs`, `GET /jobs`, `GET /jobs/{id}`, `GET /jobs/{id}/results`,
  `DELETE /jobs/{id}`
- `GET/POST /datasets`, `GET/POST /datasets/{ds}/tables`,
  `GET /datasets/{ds}/tables/{t}` (schema/stats/partitions)
- `GET/POST/DELETE /schedules`
- Carried over: `/auth/*`, `/buckets*` (raw browsing retained), `/schema`,
  `/health`
- Back-compat shims: existing `POST /query` and `POST /convert` become thin
  wrappers over the job API.

### CLI (`ds3sql`) — scriptability is first-class

Every command keeps `--json` and clean exit codes.

- `query "SQL"` → submit + wait (sync fast-path); `--async` returns a job ID;
  `-f file.sql`; `--format json|csv|table`; `--max-rows`
- `jobs [list|get|cancel|wait] <id>`
- `datasets [ls|create|rm]`
- `tables [ls|describe|create-as|drop]`
- `load …`
- `schedules [create|ls|rm]`
- Carried over: `login`, `logout`, `status`, `buckets`, `ls`, `schema`
- New `--coordinator` (host/port) flag replaces `--host/--port` (kept as
  aliases).

### Web UI (HTMX, no build step — carried forward)

- Left navigation becomes a **catalog browser** (datasets → tables → schema);
  raw bucket browsing demoted to a secondary tab.
- The query tab gains a **jobs/history panel** and async status display.
- `analysis` and `report` tabs carry over and now bind to catalog tables.

## Package Layout & Migration (Approach A)

```
cmd/ds3sql-server/   --role flag; wires coordinator or worker (or all)
cmd/ds3sql/          CLI (extended)

internal/
  auth/        ── carried over (challenge, session, middleware)
  s3/          ── carried over (+ tier-aware client, byte-range fetch)
  query/       ── carried over: becomes the WORKER executor
  convert/     ── generalized → load/CTAS writers
  analysis/    ── carried over (binds to catalog tables)
  report/      ── carried over
  column/      ── carried over

  catalog/     ── NEW: datasets/tables/partitions/stats
  metastore/   ── NEW: MetadataStore iface + embedded(SQLite) + postgres
  job/         ── NEW: job manager, queue, quotas, history
  planner/     ── NEW: table resolution, partition pruning, routing
  cache/       ── NEW: result-cache index + worker data cache
  scheduler/   ── NEW: cron → jobs
  worker/      ── NEW: worker server (wraps query engine + data cache)
  coordinator/ ── NEW: control-plane wiring
  api/         ── extended: job-centric handlers; old handlers → shims
  web/         ── carried over; catalog browser + jobs panel
```

Existing tests stay green throughout. Each new package lands behind the role
flag so a single-process `--role=all` dev mode keeps the local experience
identical to today.

## Phased Roadmap

Each phase ships a working tool and becomes its own spec → plan → implementation
cycle. This document is the architecture spec + roadmap; **Phase 1 is the next
`writing-plans` target.**

1. **Foundations** — `metastore` interface + embedded SQLite; `catalog`
   (datasets/tables, external tables, schema/stats); `job` manager with the
   **synchronous fast-path only**; role flag (`coordinator`/`worker`/`all`).
   Outcome: `SELECT FROM dataset.table` works end-to-end on one node, replacing
   raw-path-only querying.
2. **Scale-out & caching** — worker pool + coordinator routing; consistent-hash
   cache locality; **result cache** + **local-SSD data cache** + metadata cache;
   async jobs + polling; concurrency quotas/queue; query history.
3. **Write path** — CTAS; batch load (generalize `convert`); partitioning +
   storage-class tiering; scheduled queries.
4. **Product polish** — catalog browser + jobs panel in the Web UI; Postgres
   metastore option; partition-pruning refinements. (Deferred beyond scope:
   dry-run cost estimate, materialized views, distributed single-query
   execution.)

## Open Questions / Risks

- **go-duckdb + SQLite CGo** — using a pure-Go SQLite driver (`modernc.org/sqlite`)
  avoids a second CGo toolchain alongside go-duckdb; confirm during Phase 1.
- **Worker→results streaming format** — Parquet vs Arrow IPC for the results
  location; Arrow may be better for paginated fetch. Decide in Phase 2.
- **Coordinator↔worker transport** — gRPC vs plain HTTP for plan dispatch;
  HTTP keeps the dependency surface small, gRPC streams status better. Decide at
  the start of Phase 2.
- **Credential propagation to workers** — per-job S3 credentials must reach
  workers securely (short-lived, not logged); reuse the IAM/Keyvault flow.
