# Query Performance — Design Document

**Date**: 2026-05-23
**Status**: Draft

## 1. Overview

Replace the current per-query DuckDB lifecycle (open → load extensions → configure → query → close) with a connection pool of pre-initialized DuckDB instances, and tune resource limits for better throughput.

## 2. Current Bottlenecks

| Bottleneck | Impact |
|---|---|
| Per-query `sql.Open("duckdb", "")` + `LOAD httpfs` + `LOAD parquet` | ~50–100ms overhead per query before SQL even starts |
| `SET threads=2` hardcoded | Under-utilizes CPU on any machine with >2 cores |
| `SET memory_limit='512MB'` | Constrains DuckDB's ability to cache data pages and hash tables |
| Row-by-row Go scanning with byte accounting | Adds overhead proportional to result set size |

## 3. Solution: Connection Pool

### Pool Architecture

The `Engine` holds a buffered channel of `*sql.DB` DuckDB connections, initialized at server startup:

```
1. NewEngine(poolSize=N)
   └─ Create N DuckDB instances via sql.Open("duckdb", "")
       └─ Each: LOAD httpfs, LOAD parquet
           └─ Push into pool (chan *sql.DB, size N)

2. Query(sql, creds)
   ├─ Borrow: db := <-pool (blocks if all busy)
   ├─ Configure: set S3 creds + memory_limit + threads on db
   ├─ Execute SQL, collect results
   └─ Return: pool <- db
```

### Pool Size

Configurable via `query.pool_size`, default `4`. This gives minimal memory overhead (DuckDB in-memory instances are lightweight until they execute queries) while allowing 4 concurrent queries without serialization.

Blocks if all connections are busy — natural backpressure without an external queue.

### Borrow Contract

On borrow, the engine sets per-query configuration on the connection:

- `CREATE SECRET ds3_s3 (TYPE S3, ...)` (DuckDB >= 0.10)
- `SET s3_access_key_id/secret/endpoint/region/use_ssl/url_style` (fallback)
- `SET memory_limit='${memoryLimit}'`
- `SET threads=${threads}`

No cleanup needed on return. DuckDB connections are stateless beyond session-level SETs, and the next borrow will overwrite them.

## 4. Resource Tuning

### Memory Limit

Raise from `512MB` to `2GB`. Configurable via `query.memory_limit` in server YAML.

Rationale: DS3 SQL Server is a sidecar to the DS3 Gateway. The Gateway is I/O-bound (S3 traffic). DuckDB benefits significantly from more memory for hash joins, aggregations, and Parquet metadata caching. 2GB is conservative for typical sidecar resource limits (512Mi–1Gi request, 2–4Gi limit).

### Thread Count

Remove the hardcoded `SET threads=2`. Default to `0` (DuckDB auto-detect — uses all available cores), configurable via `query.threads`. When `threads` is `0` or negative, the `SET threads` statement is skipped entirely and DuckDB uses its built-in default (all logical CPUs).

## 5. Config Changes

New fields in `config.go`:

```go
type QueryConfig struct {
    MaxRows          int   `yaml:"max_rows"`
    MaxExecutionSecs int   `yaml:"max_execution_seconds"`
    MaxResultBytes   int64 `yaml:"max_result_bytes"`
    PoolSize         int   `yaml:"pool_size"`       // new, default 4
    Threads          int   `yaml:"threads"`           // new, default 0 (auto)
    MemoryLimit      string `yaml:"memory_limit"`     // new, default "2GB"
}
```

Default config:

```yaml
query:
  max_rows: 10000
  max_execution_seconds: 60
  max_result_bytes: 104857600
  pool_size: 4
  threads: 0
  memory_limit: "2GB"
```

Env var overrides: `DS3SQL_MEMORY_LIMIT`, `DS3SQL_THREADS`, `DS3SQL_POOL_SIZE`.

## 6. Engine Refactor

### Before

```go
type Engine struct {
    maxRows          int
    maxExecutionSecs int
    maxResultBytes   int64
}

func (e *Engine) Query(sqlStr, accessKey, secretKey, rawEndpoint string) *Result {
    db, _ := sql.Open("duckdb", "")
    defer db.Close()
    db.Exec("LOAD httpfs")
    db.Exec("LOAD parquet")
    // set creds, memory, threads
    // run query
}
```

### After

```go
type Engine struct {
    pool             chan *sql.DB
    maxRows          int
    maxExecutionSecs int
    maxResultBytes   int64
    memoryLimit      string
    threads          int
}

func NewEngine(maxRows, maxExecutionSecs int, maxResultBytes int64, poolSize, threads int, memoryLimit string) *Engine

func (e *Engine) Query(sqlStr, accessKey, secretKey, rawEndpoint string) *Result {
    db := <-e.pool
    defer func() { e.pool <- db }()
    // set creds, memory, threads
    // run query
}
```

### Thread safety

The pool channel provides mutual exclusion. No `sync.Mutex` needed. Connections are not shared — each is used by exactly one goroutine at a time.

## 7. Initialization Flow (main.go)

```go
engine := query.NewEngine(
    cfg.Query.MaxRows,
    cfg.Query.MaxExecutionSecs,
    cfg.Query.MaxResultBytes,
    cfg.Query.PoolSize,
    cfg.Query.Threads,
    cfg.Query.MemoryLimit,
)
```

The constructor blocks until all pool connections are opened and have loaded extensions. If any fail, startup is aborted (fail-fast).

## 8. Error Handling

- **Pool creation failure**: If any of the `sql.Open` calls fail during `NewEngine`, return an error immediately — server fails to start with a clear message.
- **Query-time connection error**: If `db.Query` returns a connection-level error (e.g., "connection refused", "broken pipe"), the borrowed connection is closed and replaced with a fresh one before returning to the pool. If the replacement fails, the pool shrinks by one and a warning is logged.
- **Panic safety**: The `defer` that returns the connection to the pool runs even on panic. If the panic originated from DuckDB/CGo, the connection is considered poisoned and is closed/replaced rather than returned.
- **All connections exhausted**: If every connection in the pool has been replaced with a fresh one and the fresh ones also fail, the pool eventually empties and `<-pool` blocks forever. A startup health check (`GET /health`) verifies the pool has at least one available connection; if not, it returns 503.
- The `Query` method already returns SQL-level errors as structured JSON; this doesn't change.

## 9. Changes Required

| File | Change |
|---|---|
| `internal/config/config.go` | Add PoolSize, Threads, MemoryLimit to QueryConfig; update Default() |
| `internal/query/engine.go` | Add pool, NewEngine pool init, borrow/return in Query |
| `internal/query/engine_test.go` | Update NewEngine calls with new params |
| `cmd/ds3sql-server/main.go` | Pass new config fields to NewEngine |
| `docs/configuration.md` | Document new config fields |
| `docs/superpowers/specs/2026-05-22-ds3-sql-server-design.md` | Update config section |

## 10. Testing

- Existing tests should pass with minor NewEngine signature update
- Test pool reuses connections (run sequential queries, verify no new sql.Open calls)
- Test concurrent queries don't share connections
- Test blocking behavior (pool exhausted waits)
