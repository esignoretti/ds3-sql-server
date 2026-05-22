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

	exts := []string{"httpfs", "parquet"}
	for _, ext := range exts {
		if _, err := db.Exec(fmt.Sprintf("LOAD %s", ext)); err != nil {
			return errorResult(fmt.Sprintf("load extension %s: %v", ext, err), start)
		}
	}

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
