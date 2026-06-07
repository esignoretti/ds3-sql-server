package query

import (
	"database/sql"
	"fmt"
	"time"
)

type SchemaColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

type SchemaResult struct {
	Columns    []SchemaColumn `json:"columns"`
	ElapsedMs  int64          `json:"elapsed_ms"`
	Error      string         `json:"error,omitempty"`
	Detected   string         `json:"detected,omitempty"` // actual reader format that worked
}

func (e *Engine) InferSchema(path, accessKey, secretKey, rawEndpoint string) *SchemaResult {
	start := time.Now()
	path = s3PathFromHTTPS(path)

	db := <-e.pool
	defer func() { e.pool <- db }()

	applyS3Creds(db, accessKey, secretKey, rawEndpoint)

	var rows *sql.Rows
	var lastErr error
	var detected string
	for _, reader := range []string{"read_parquet", "read_csv_auto", "read_json_auto"} {
		schemaSQL := fmt.Sprintf("DESCRIBE SELECT * FROM %s('%s')", reader, path)
		rows, lastErr = db.Query(schemaSQL)
		if lastErr == nil {
			switch reader {
			case "read_parquet":
				detected = "parquet"
			case "read_csv_auto":
				detected = "csv"
			case "read_json_auto":
				detected = "json"
			}
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
			colType    sql.NullString
			colNull    string
			colKey     sql.NullString
			colDefault sql.NullString
			colExtra   sql.NullString
		)
		if err := rows.Scan(&colName, &colType, &colNull, &colKey, &colDefault, &colExtra); err != nil {
			continue
		}
		columns = append(columns, SchemaColumn{
			Name:     colName,
			Type:     colType.String,
			Nullable: colNull == "YES",
		})
	}
	if err := rows.Err(); err != nil {
		return &SchemaResult{Error: err.Error(), ElapsedMs: time.Since(start).Milliseconds()}
	}

	return &SchemaResult{
		Columns:   columns,
		ElapsedMs: time.Since(start).Milliseconds(),
		Detected:  detected,
	}
}
