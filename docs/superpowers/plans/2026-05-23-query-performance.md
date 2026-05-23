# Query Performance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate per-query DuckDB setup overhead by adding a connection pool, and tune resource limits for better throughput.

**Architecture:** Replace the per-query `sql.Open("duckdb", "")` → LOAD extensions → configure → query → close pattern with a warm pool of pre-initialized DuckDB connections. The pool lives in `Engine`, connections are borrowed/returned via a buffered channel. `InferSchema` also uses the pool. Resource limits (memory, threads) become configurable with better defaults.

**Tech Stack:** Go 1.26, DuckDB via `github.com/marcboeker/go-duckdb`, `database/sql`

---

### Task 1: Add pool/config fields to Engine and update NewEngine

**Files:**
- Modify: `internal/query/engine.go:26-38`

- [ ] **Step 1: Update Engine struct and NewEngine signature**

Replace the existing `Engine` struct and `NewEngine` with pool-aware versions:

```go
type Engine struct {
	pool             chan *sql.DB
	maxRows          int
	maxExecutionSecs int
	maxResultBytes   int64
	memoryLimit      string
	threads          int
}

func NewEngine(maxRows, maxExecutionSecs int, maxResultBytes int64, poolSize, threads int, memoryLimit string) (*Engine, error) {
	if poolSize < 1 {
		poolSize = 1
	}
	if memoryLimit == "" {
		memoryLimit = "2GB"
	}

	pool := make(chan *sql.DB, poolSize)
	for i := 0; i < poolSize; i++ {
		db, err := sql.Open("duckdb", "")
		if err != nil {
			return nil, fmt.Errorf("open duckdb connection %d: %w", i, err)
		}
		if _, err := db.Exec("LOAD httpfs"); err != nil {
			return nil, fmt.Errorf("load httpfs on connection %d: %w", i, err)
		}
		if _, err := db.Exec("LOAD parquet"); err != nil {
			return nil, fmt.Errorf("load parquet on connection %d: %w", i, err)
		}
		pool <- db
	}

	return &Engine{
		pool:             pool,
		maxRows:          maxRows,
		maxExecutionSecs: maxExecutionSecs,
		maxResultBytes:   maxResultBytes,
		memoryLimit:      memoryLimit,
		threads:          threads,
	}, nil
}
```

- [ ] **Step 2: Add string import if not present**

Add `"fmt"` to the import block in `engine.go` if it's not already there (it is already present).

- [ ] **Step 3: Update the errorResult helper**

No change needed — it already returns `*Result` with `Error` and `ElapsedMs`.

- [ ] **Step 4: Verify it compiles**

```bash
go build ./internal/query/
```

Expected: compile error about `*sql.DB` not being comparable if chan type is wrong. Fix to `chan *sql.DB`.

---

### Task 2: Rewrite Engine.Query to borrow from pool

**Files:**
- Modify: `internal/query/engine.go:40-157`

- [ ] **Step 1: Replace the Query method body**

Replace the entire `func (e *Engine) Query` method. The new version borrows a connection from the pool, sets per-query config, executes SQL, collects rows, and returns the connection:

```go
func (e *Engine) Query(sqlStr string, accessKey, secretKey, rawEndpoint string) *Result {
	start := time.Now()

	useSSL := true
	endpoint := rawEndpoint
	if idx := strings.Index(endpoint, "://"); idx >= 0 {
		proto := endpoint[:idx]
		useSSL = proto == "https"
		endpoint = endpoint[idx+3:]
	}

	db := <-e.pool
	defer func() { e.pool <- db }()

	useSSLStr := "false"
	if useSSL {
		useSSLStr = "true"
	}

	// Set S3 credentials
	db.Exec("CREATE SECRET ds3_s3 (TYPE S3, KEY_ID '" + accessKey + "', SECRET '" + secretKey + "', ENDPOINT '" + endpoint + "', REGION 'us-east-1', USE_SSL " + useSSLStr + ", URL_STYLE 'path')")
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

	// Set memory limit
	memSQL := "SET memory_limit='" + e.memoryLimit + "'"
	if _, err := db.Exec(memSQL); err != nil {
		return errorResult("set memory_limit: "+err.Error(), start)
	}

	// Set threads (skip if 0 = DuckDB auto-detect)
	if e.threads > 0 {
		if _, err := db.Exec(fmt.Sprintf("SET threads=%d", e.threads)); err != nil {
			return errorResult("set threads: "+err.Error(), start)
		}
	}

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
		colInfos[i] = ColumnInfo{
			Name: columns[i],
			Type: columnTypes[i].DatabaseTypeName(),
		}
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

	elapsed := time.Since(start).Milliseconds()

	return &Result{
		Columns:   colInfos,
		Rows:      resultRows,
		RowCount:  rowCount,
		ElapsedMs: elapsed,
	}
}

func (e *Engine) PoolLen() int {
	return len(e.pool)
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/query/
```

Expected: success.

---

### Task 3: Rewrite Engine.InferSchema to borrow from pool

**Files:**
- Modify: `internal/query/schema.go:24-101`

- [ ] **Step 1: Replace the InferSchema method body**

Replace the method to borrow from pool instead of opening its own DuckDB instance:

```go
func (e *Engine) InferSchema(path, accessKey, secretKey, rawEndpoint string) *SchemaResult {
	start := time.Now()

	useSSL := true
	endpoint := rawEndpoint
	if idx := strings.Index(endpoint, "://"); idx >= 0 {
		proto := endpoint[:idx]
		useSSL = proto == "https"
		endpoint = endpoint[idx+3:]
	}

	db := <-e.pool
	defer func() { e.pool <- db }()

	useSSLStr := "false"
	if useSSL {
		useSSLStr = "true"
	}

	// Set S3 credentials
	db.Exec("CREATE SECRET ds3_s3 (TYPE S3, KEY_ID '" + accessKey + "', SECRET '" + secretKey + "', ENDPOINT '" + endpoint + "', REGION 'us-east-1', USE_SSL " + useSSLStr + ", URL_STYLE 'path')")
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

	var rows *sql.Rows
	var lastErr error
	for _, reader := range []string{"read_parquet", "read_csv_auto", "read_json_auto"} {
		schemaSQL := fmt.Sprintf("DESCRIBE SELECT * FROM %s('%s')", reader, path)
		rows, lastErr = db.Query(schemaSQL)
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return &SchemaResult{Error: lastErr.Error(), ElapsedMs: time.Since(start).Milliseconds()}
	}
	defer rows.Close()

	var columns []SchemaColumn
	for rows.Next() {
		var (
			colName    string
			colType    string
			colNull    string
			colKey     string
			colDefault *string
			colExtra   *string
		)
		if err := rows.Scan(&colName, &colType, &colNull, &colKey, &colDefault, &colExtra); err != nil {
			continue
		}
		columns = append(columns, SchemaColumn{
			Name:     colName,
			Type:     colType,
			Nullable: colNull == "YES",
		})
	}

	return &SchemaResult{
		Columns:   columns,
		ElapsedMs: time.Since(start).Milliseconds(),
	}
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/query/
```

Expected: success.

---

### Task 4: Update config – add PoolSize, Threads, MemoryLimit

**Files:**
- Modify: `internal/config/config.go:26-54`

- [ ] **Step 1: Add new fields to QueryConfig**

```go
type QueryConfig struct {
	MaxRows          int    `yaml:"max_rows"`
	MaxExecutionSecs int    `yaml:"max_execution_seconds"`
	MaxResultBytes   int64  `yaml:"max_result_bytes"`
	PoolSize         int    `yaml:"pool_size"`
	Threads          int    `yaml:"threads"`
	MemoryLimit      string `yaml:"memory_limit"`
}
```

- [ ] **Step 2: Update Default()**

```go
func Default() *Config {
	return &Config{
		ListenAddr:    ":8080",
		IAMURL:         "https://api.eu00wi.cubbit.services",
		DS3GatewayURL: "http://localhost:9000",
		Auth: AuthConfig{
			TokenExpiry:        24 * time.Hour,
			RefreshTokenExpiry: 720 * time.Hour,
		},
		Query: QueryConfig{
			MaxRows:          10000,
			MaxExecutionSecs: 60,
			MaxResultBytes:   104857600,
			PoolSize:         4,
			Threads:          0,
			MemoryLimit:      "2GB",
		},
		RateLimit: RateLimitConfig{
			QueriesPerMinute: 10,
		},
	}
}
```

- [ ] **Step 3: Add env var overrides**

Add after the `DS3SQL_DS3_GATEWAY_URL` block in `Load()`:

```go
if v := os.Getenv("DS3SQL_POOL_SIZE"); v != "" {
    if n, err := strconv.Atoi(v); err == nil {
        cfg.Query.PoolSize = n
    }
}
if v := os.Getenv("DS3SQL_THREADS"); v != "" {
    if n, err := strconv.Atoi(v); err == nil {
        cfg.Query.Threads = n
    }
}
if v := os.Getenv("DS3SQL_MEMORY_LIMIT"); v != "" {
    cfg.Query.MemoryLimit = v
}
```

Add `"strconv"` to the import block in `config.go`.

- [ ] **Step 4: Verify it compiles**

```bash
go build ./internal/config/
```

Expected: success.

---

### Task 5: Wire new config in main.go

**Files:**
- Modify: `cmd/ds3sql-server/main.go:61-65`

- [ ] **Step 1: Update NewEngine call**

Replace:
```go
queryEngine := query.NewEngine(
    cfg.Query.MaxRows,
    cfg.Query.MaxExecutionSecs,
    cfg.Query.MaxResultBytes,
)
```

With:
```go
queryEngine, err := query.NewEngine(
    cfg.Query.MaxRows,
    cfg.Query.MaxExecutionSecs,
    cfg.Query.MaxResultBytes,
    cfg.Query.PoolSize,
    cfg.Query.Threads,
    cfg.Query.MemoryLimit,
)
if err != nil {
    log.Fatalf("failed to init query engine: %v", err)
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./cmd/ds3sql-server/
```

Expected: success.

---

### Task 6: Fix engine tests

**Files:**
- Modify: `internal/query/engine_test.go:1-25`

- [ ] **Step 1: Update test calls to NewEngine**

```go
package query

import (
	"testing"
)

func TestErrorOnInvalidSQL(t *testing.T) {
	engine, err := NewEngine(100, 10, 1024*1024, 1, 0, "128MB")
	if err != nil {
		t.Fatal(err)
	}
	result := engine.Query("SELECT BADSYNTAX", "", "", "")
	if result.Error == "" {
		t.Fatal("expected error for invalid SQL")
	}
	t.Logf("got expected error: %s", result.Error)
}

func TestTrimToMaxRows(t *testing.T) {
	engine, err := NewEngine(5, 10, 1024*1024, 1, 0, "128MB")
	if err != nil {
		t.Fatal(err)
	}
	result := engine.Query("SELECT * FROM range(100)", "", "", "")
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.RowCount > 5 {
		t.Fatalf("expected at most 5 rows, got %d", result.RowCount)
	}
	t.Logf("got %d rows (limited to 5)", result.RowCount)
}
```

- [ ] **Step 2: Run tests**

```bash
go test -v -race ./internal/query/
```

Expected: both tests pass.

---

### Task 7: Add pool health check to /health endpoint

**Files:**
- Modify: `cmd/ds3sql-server/main.go:69-72`

- [ ] **Step 1: Update health endpoint to check pool health**

Replace:
```go
r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    w.Write([]byte(`{"status":"ok"}`))
})
```

With:
```go
r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    poolOk := len(queryEngine.Pool) > 0
    if poolOk {
        w.Write([]byte(`{"status":"ok","pool_size":` + strconv.Itoa(len(queryEngine.Pool)) + `}`))
    } else {
        w.WriteHeader(http.StatusServiceUnavailable)
        w.Write([]byte(`{"status":"degraded","error":"query pool empty"}`))
    }
})
```

Also need to add `strconv` to imports (it's already imported in main.go).

And need to export PoolLen — add a method to Engine:

```go
// In engine.go, add:
func (e *Engine) PoolLen() int {
    return len(e.pool)
}
```

- [ ] **Step 2: Verify compile**

```bash
go build ./cmd/ds3sql-server/
```

Expected: success.

---

### Task 8: Update user-facing docs

**Files:**
- Modify: `docs/configuration.md:7-33`

- [ ] **Step 1: Add new config fields to YAML example**

Add `pool_size: 4`, `threads: 0`, `memory_limit: "2GB"` under `query:`:

```yaml
query:
  max_rows: 10000
  max_execution_seconds: 60
  max_result_bytes: 104857600
  pool_size: 4
  threads: 0
  memory_limit: "2GB"
```

- [ ] **Step 2: Add new env vars to table**

| `DS3SQL_POOL_SIZE` | `query.pool_size` | `4` |
| `DS3SQL_THREADS` | `query.threads` | `0` (auto) |
| `DS3SQL_MEMORY_LIMIT` | `query.memory_limit` | `2GB` |

### Task 9: Commit everything

- [ ] **Step 1: Stage and commit**

```bash
git add -A
git commit -m "perf: DuckDB connection pool with configurable memory/threads

- Add connection pool (default 4) to eliminate per-query setup overhead
- Engine.Query and Engine.InferSchema both borrow from the pool
- Memory limit raised to 2GB (configurable), threads set to auto (configurable)
- Pool health check in /health endpoint
- Config: pool_size, threads, memory_limit with env var overrides"
```

- [ ] **Step 2: Verify tests pass**

```bash
go test -v -race ./...
```

Expected: all tests pass.

- [ ] **Step 3: Build binaries**

```bash
make build
```

Expected: `ds3sql-server` and `ds3sql` binaries produced.
