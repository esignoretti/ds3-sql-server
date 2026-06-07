package query

import (
	"database/sql"
	"fmt"
	"strings"
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
			drainPool(pool)
			return nil, fmt.Errorf("open duckdb connection %d: %w", i, err)
		}
		if _, err := db.Exec("LOAD httpfs"); err != nil {
			db.Close()
			drainPool(pool)
			return nil, fmt.Errorf("load httpfs on connection %d: %w", i, err)
		}
		if _, err := db.Exec("LOAD parquet"); err != nil {
			db.Close()
			drainPool(pool)
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

// escapeSQL quotes a string literal by doubling single quotes, preventing
// SQL injection via DuckDB string literals.
func escapeSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func applyS3Creds(db *sql.DB, accessKey, secretKey, rawEndpoint string) error {
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
	ak := escapeSQL(accessKey)
	sk := escapeSQL(secretKey)
	ep := escapeSQL(endpoint)
	queries := []string{
		"CREATE OR REPLACE SECRET ds3_s3 (TYPE S3, KEY_ID '" + ak + "', SECRET '" + sk + "', ENDPOINT '" + ep + "', REGION 'us-east-1', USE_SSL " + useSSLStr + ", URL_STYLE 'path')",
		"SET s3_access_key_id='" + ak + "'",
		"SET s3_secret_access_key='" + sk + "'",
		"SET s3_endpoint='" + ep + "'",
		"SET s3_region='us-east-1'",
		"SET s3_url_style='path'",
	}
	if useSSL {
		queries = append(queries, "SET s3_use_ssl=true")
	} else {
		queries = append(queries, "SET s3_use_ssl=false")
	}
	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("apply s3 creds: %w", err)
		}
	}
	return nil
}

func (e *Engine) Query(sqlStr string, accessKey, secretKey, rawEndpoint string) *Result {
	start := time.Now()

	db := <-e.pool
	defer func() { e.pool <- db }()

	if err := applyS3Creds(db, accessKey, secretKey, rawEndpoint); err != nil {
		return errorResult(err.Error(), start)
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

	return e.collectRows(db, sqlStr, start)
}

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

// ExecWrite registers each binding as a DuckDB view, applies S3 credentials, then
// executes a statement that produces no result rows (e.g. COPY ... TO). It is the
// write-path counterpart to QueryView. Created schemas are dropped afterward.
func (e *Engine) ExecWrite(sqlStr string, bindings []ViewBinding, accessKey, secretKey, rawEndpoint string) error {
	db := <-e.pool
	defer func() { e.pool <- db }()

	if err := applyS3Creds(db, accessKey, secretKey, rawEndpoint); err != nil {
		return err
	}

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

// QueryView registers each binding as a DuckDB view, executes the user SQL, then
// drops the created schemas. Results are collected exactly like Query.
func (e *Engine) QueryView(sqlStr string, bindings []ViewBinding, accessKey, secretKey, rawEndpoint string) *Result {
	start := time.Now()

	db := <-e.pool
	defer func() { e.pool <- db }()

	if err := applyS3Creds(db, accessKey, secretKey, rawEndpoint); err != nil {
		return errorResult(err.Error(), start)
	}

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

func (e *Engine) PoolLen() int {
	return len(e.pool)
}

func (e *Engine) Pool() chan *sql.DB {
	return e.pool
}

func errorResult(msg string, start time.Time) *Result {
	return &Result{
		Error:     msg,
		ElapsedMs: time.Since(start).Milliseconds(),
	}
}

func drainPool(pool chan *sql.DB) {
	for {
		select {
		case db := <-pool:
			db.Close()
		default:
			return
		}
	}
}
