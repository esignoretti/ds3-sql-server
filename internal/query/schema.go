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
	Columns   []SchemaColumn `json:"columns"`
	ElapsedMs int64          `json:"elapsed_ms"`
	Error     string         `json:"error,omitempty"`
}

func (e *Engine) InferSchema(path, accessKey, secretKey, rawEndpoint string) *SchemaResult {
	start := time.Now()

	db := <-e.pool
	defer func() { e.pool <- db }()

	applyS3Creds(db, accessKey, secretKey, rawEndpoint)

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
