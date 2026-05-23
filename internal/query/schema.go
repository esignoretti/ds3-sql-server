package query

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
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
