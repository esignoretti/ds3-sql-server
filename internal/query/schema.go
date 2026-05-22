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

	db.Exec("INSTALL httpfs")
	db.Exec("INSTALL parquet")
	db.Exec("LOAD httpfs")
	db.Exec("LOAD parquet")

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

	schemaSQL := fmt.Sprintf("DESCRIBE SELECT * FROM read_parquet('%s')", path)

	rows, err := db.Query(schemaSQL)
	if err != nil {
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
