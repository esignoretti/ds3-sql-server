# DS3 SQL Server — Phase 4: DuckDB Query Engine

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the DuckDB query engine that reads Parquet and CSV files directly from DS3 S3. Each query creates an ephemeral in-memory DuckDB instance, configures httpfs with DS3 credentials, executes the SQL, and returns structured results.

**Architecture:** Thin wrapper around `github.com/marcboeker/go-duckdb`. Per-query lifecycle: open DuckDB → load extensions → set S3 config → execute → collect results → close. No persistent state between queries.

**Tech Stack:** Go 1.22+, DuckDB (CGo bindings), `github.com/marcboeker/go-duckdb`

---

### Task 1: DuckDB engine wrapper

**Files:**
- Create: `DS3-SQL Server/internal/query/engine.go`
- Create: `DS3-SQL Server/internal/query/engine_test.go`
- Create: `DS3-SQL Server/internal/query/schema.go`

- [ ] **Step 1: Write the query engine**

`DS3-SQL Server/internal/query/engine.go`:

```go
package query

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

type ColumnInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type Result struct {
	Columns   []ColumnInfo `json:"columns"`
	Rows      [][]any      `json:"rows"`
	RowCount  int          `json:"row_count"`
	ElapsedMs int64        `json:"elapsed_ms"`
	Error     string       `json:"error,omitempty"`
}

type Engine struct {
	maxRows          int
	maxExecutionSecs int
	maxResultBytes   int64
}

func NewEngine(maxRows int, maxExecutionSecs int, maxResultBytes int64) *Engine {
	return &Engine{
		maxRows:          maxRows,
		maxExecutionSecs: maxExecutionSecs,
		maxResultBytes:   maxResultBytes,
	}
}

func (e *Engine) Query(sqlStr string, accessKey, secretKey, endpoint string) *Result {
	start := time.Now()

	db, err := sql.Open("duckdb", "")
	if err != nil {
		return errorResult("open duckdb: "+err.Error(), start)
	}
	defer db.Close()

	// Load extensions
	exts := []string{"httpfs", "parquet"}
	for _, ext := range exts {
		if _, err := db.Exec(fmt.Sprintf("LOAD %s", ext)); err != nil {
			return errorResult(fmt.Sprintf("load extension %s: %v", ext, err), start)
		}
	}

	// Configure S3 via DuckDB secrets (DuckDB 0.10+ syntax)
	s3Config := fmt.Sprintf(`
		CREATE SECRET ds3_s3 (
			TYPE S3,
			KEY_ID '%s',
			SECRET '%s',
			ENDPOINT '%s',
			REGION 'us-east-1',
			USE_SSL false,
			URL_STYLE 'path'
		)
	`, accessKey, secretKey, endpoint)

	if _, err := db.Exec(s3Config); err != nil {
		// Fallback for older DuckDB: set httpfs env vars
		fallback := fmt.Sprintf(`
			SET s3_access_key_id='%s';
			SET s3_secret_access_key='%s';
			SET s3_endpoint='%s';
			SET s3_region='us-east-1';
			SET s3_url_style='path';
			SET s3_use_ssl=false;
		`, accessKey, secretKey, endpoint)
		if _, err2 := db.Exec(fallback); err2 != nil {
			return errorResult(fmt.Sprintf("configure s3: %v (secret: %v)", err, err2), start)
		}
	}

	// Set query timeout
	timeoutSQL := fmt.Sprintf("SET memory_limit='512MB'; SET threads=2;")
	if _, err := db.Exec(timeoutSQL); err != nil {
		return errorResult("set config: "+err.Error(), start)
	}

	// Execute query
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

		// Estimate bytes for limit check
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

func errorResult(msg string, start time.Time) *Result {
	return &Result{
		Error:     msg,
		ElapsedMs: time.Since(start).Milliseconds(),
	}
}
```

- [ ] **Step 2: Write schema inference**

`DS3-SQL Server/internal/query/schema.go`:

```go
package query

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

type SchemaColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

type SchemaResult struct {
	Columns   []SchemaColumn `json:"columns"`
	ElapsedMs int64          `json:"elapsed_ms"`
	Error     string         `json:"error,omitempty"`
}

func (e *Engine) InferSchema(path, accessKey, secretKey, endpoint string) *SchemaResult {
	start := time.Now()

	db, err := sql.Open("duckdb", "")
	if err != nil {
		return &SchemaResult{Error: "open duckdb: " + err.Error(), ElapsedMs: time.Since(start).Milliseconds()}
	}
	defer db.Close()

	db.Exec("LOAD httpfs")
	db.Exec("LOAD parquet")

	// Configure S3
	s3Cfg := fmt.Sprintf(`
		CREATE SECRET ds3_s3 (
			TYPE S3, KEY_ID '%s', SECRET '%s',
			ENDPOINT '%s', REGION 'us-east-1',
			USE_SSL false, URL_STYLE 'path'
		)
	`, accessKey, secretKey, endpoint)
	if _, err := db.Exec(s3Cfg); err != nil {
		db.Exec(fmt.Sprintf("SET s3_access_key_id='%s'; SET s3_secret_access_key='%s'; SET s3_endpoint='%s'; SET s3_region='us-east-1'; SET s3_url_style='path'; SET s3_use_ssl=false", accessKey, secretKey, endpoint))
	}

	// Use DuckDB's DESCRIBE via a SELECT with LIMIT 0 to infer schema
	schemaSQL := fmt.Sprintf("DESCRIBE SELECT * FROM read_parquet('%s')", path)

	rows, err := db.Query(schemaSQL)
	if err != nil {
		// Try CSV if parquet fails
		schemaSQL = fmt.Sprintf("DESCRIBE SELECT * FROM read_csv_auto('%s')", path)
		rows, err = db.Query(schemaSQL)
		if err != nil {
			return &SchemaResult{Error: err.Error(), ElapsedMs: time.Since(start).Milliseconds()}
		}
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

- [ ] **Step 3: Add dependency**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go get github.com/marcboeker/go-duckdb
```

Expected: `go: added github.com/marcboeker/go-duckdb vX.Y.Z`

- [ ] **Step 4: Write a test (requires DuckDB C lib installed)**

`DS3-SQL Server/internal/query/engine_test.go`:

```go
package query

import (
	"testing"
)

func TestErrorOnInvalidSQL(t *testing.T) {
	engine := NewEngine(100, 10, 1024*1024)
	result := engine.Query("SELECT BADSYNTAX", "", "", "")
	if result.Error == "" {
		t.Fatal("expected error for invalid SQL")
	}
	t.Logf("got expected error: %s", result.Error)
}

func TestTrimToMaxRows(t *testing.T) {
	engine := NewEngine(5, 10, 1024*1024)
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

- [ ] **Step 5: Run test**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go test ./internal/query/ -v
```

Expected: PASS (may need CGo and DuckDB library)

- [ ] **Step 6: Commit**

```bash
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server add internal/query/ && \
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server commit -m "feat: DuckDB query engine with httpfs S3 support"
```
