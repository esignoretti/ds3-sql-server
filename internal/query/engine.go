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

func (e *Engine) Query(sqlStr string, accessKey, secretKey, rawEndpoint string) *Result {
	start := time.Now()

	db := <-e.pool
	defer func() { e.pool <- db }()

	applyS3Creds(db, accessKey, secretKey, rawEndpoint)

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
