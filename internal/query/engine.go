package query

import (
	"database/sql"
	"fmt"
	"log"
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

func (e *Engine) Query(sqlStr string, accessKey, secretKey, rawEndpoint string) *Result {
	start := time.Now()

	useSSL := true
	endpoint := rawEndpoint
	if idx := strings.Index(endpoint, "://"); idx >= 0 {
		proto := endpoint[:idx]
		useSSL = proto == "https"
		endpoint = endpoint[idx+3:]
	}

	db, err := sql.Open("duckdb", "")
	if err != nil {
		return errorResult("open duckdb: "+err.Error(), start)
	}
	defer db.Close()

	exts := []string{"httpfs", "parquet"}
	for _, ext := range exts {
		if _, err := db.Exec(fmt.Sprintf("LOAD %s", ext)); err != nil {
			db.Exec(fmt.Sprintf("INSTALL %s", ext))
			if _, err2 := db.Exec(fmt.Sprintf("LOAD %s", ext)); err2 != nil {
				return errorResult(fmt.Sprintf("load extension %s: %v", ext, err2), start)
			}
		}
	}

	useSSLStr := "false"
	if useSSL {
		useSSLStr = "true"
	}

	log.Printf("DuckDB S3 config: endpoint=%s use_ssl=%s raw=%s", endpoint, useSSLStr, rawEndpoint)

	// Try CREATE SECRET (DuckDB >= 0.10); fallback to SET vars
	db.Exec("CREATE SECRET ds3_s3 (TYPE S3, KEY_ID '" + accessKey + "', SECRET '" + secretKey + "', ENDPOINT '" + endpoint + "', REGION 'us-east-1', USE_SSL " + useSSLStr + ", URL_STYLE 'path')")
	// DuckDB >= 0.8 fallback
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

	if _, err := db.Exec("SET memory_limit='512MB'; SET threads=2;"); err != nil {
		return errorResult("set config: "+err.Error(), start)
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
