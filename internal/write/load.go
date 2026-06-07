package write

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/s3"
)

// LoadRequest is a batch-load job: read Source via DuckDB and write Parquet into
// the managed table Into ("dataset.table"). Mode is "append" or "overwrite".
type LoadRequest struct {
	Source      string   `json:"source"`
	Into        string   `json:"into"`
	Format      string   `json:"format"`
	PartitionBy []string `json:"partition_by"`
	Mode        string   `json:"mode"`
}

// sourceReaderSQL builds the DuckDB reader expression for the load source.
func sourceReaderSQL(source, format string) string {
	loc := strings.ReplaceAll(source, "'", "''")
	switch strings.ToLower(format) {
	case "parquet":
		return fmt.Sprintf("read_parquet('%s')", loc)
	case "json", "jsonl":
		return fmt.Sprintf("read_json_auto('%s')", loc)
	case "tsv":
		return fmt.Sprintf("read_csv_auto('%s', delim='\t')", loc)
	default: // csv
		return fmt.Sprintf("read_csv_auto('%s')", loc)
	}
}

func splitInto(into string) (dataset, table string, err error) {
	parts := strings.SplitN(into, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("into must be dataset.table, got %q", into)
	}
	return parts[0], parts[1], nil
}

// RunLoad executes a batch load. For overwrite it clears the table prefix first;
// for append it writes a uniquely-named file set under the same location.
func (w *Writer) RunLoad(ctx context.Context, projectID string, req LoadRequest, accessKey, secretKey, endpoint string) (*metastore.Table, error) {
	dataset, table, err := splitInto(req.Into)
	if err != nil {
		return nil, err
	}
	mode := strings.ToLower(req.Mode)
	if mode == "" {
		mode = "append"
	}
	if mode != "append" && mode != "overwrite" {
		return nil, fmt.Errorf("mode must be append or overwrite, got %q", req.Mode)
	}
	if req.Format == "" {
		req.Format = "csv"
	}

	// Determine storage class: keep existing table's class, else default ssd.
	storageClass := "ssd"
	if existing, gerr := w.cat.GetTable(ctx, projectID, dataset, table); gerr == nil && existing.StorageClass != "" {
		storageClass = existing.StorageClass
	}
	bucket := ""
	if w.storage != nil {
		b, _, ok := w.storage.Resolve(storageClass)
		if !ok {
			return nil, fmt.Errorf("unknown storage class %q", storageClass)
		}
		bucket = b
	}
	location := w.managedLocation(bucket, projectID, dataset, table)

	if mode == "overwrite" {
		deleter := w.deleter
		if deleter == nil {
			client, err := s3.NewClient(ctx, accessKey, secretKey, endpoint)
			if err != nil {
				return nil, fmt.Errorf("overwrite: create s3 client: %w", err)
			}
			deleter = client
		}
		b, prefix, ok := splitS3(location)
		if !ok {
			// Local path (tests): use the location dir as bucket, "" prefix.
			b, prefix = location, ""
		}
		if err := deleter.DeletePrefix(ctx, b, prefix); err != nil {
			return nil, fmt.Errorf("overwrite clear: %w", err)
		}
	}

	// Build the COPY. Both append and overwrite write to a uniquely-named
	// subdirectory so the probe reader can always glob location/*.
	target := filepath.Join(location, fmt.Sprintf("load-%d", time.Now().UnixNano()))
	if mode == "overwrite" {
		target = filepath.Join(location, fmt.Sprintf("overwrite-%d", time.Now().UnixNano()))
	}
	// Ensure the parent directory exists for local paths; DuckDB needs it but
	// creates the target file/directory itself.
	if !strings.HasPrefix(target, "s3://") {
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return nil, fmt.Errorf("create target parent dir: %w", err)
		}
	}
	copyOpts := "FORMAT PARQUET"
	if len(req.PartitionBy) > 0 {
		copyOpts += ", PARTITION_BY (" + strings.Join(req.PartitionBy, ", ") + "), OVERWRITE_OR_IGNORE"
	}
	reader := sourceReaderSQL(req.Source, req.Format)
	copySQL := fmt.Sprintf("COPY (SELECT * FROM %s) TO '%s' (%s)", reader, escapeLiteral(target), copyOpts)
	if err := w.engine.ExecWrite(copySQL, nil, accessKey, secretKey, endpoint); err != nil {
		return nil, fmt.Errorf("load copy: %w", err)
	}

	// Probe the whole location (all appended sets) for schema + row count.
	partitioned := len(req.PartitionBy) > 0
	if existing, gerr := w.cat.GetTable(ctx, projectID, dataset, table); gerr == nil && len(existing.PartitionColumns) > 0 {
		partitioned = true
	}
	probe := loadProbeReader(location, partitioned)

	tbl, err := w.cat.RegisterManaged(ctx, catalog.RegisterManagedInput{
		ProjectID:        projectID,
		Dataset:          dataset,
		Name:             table,
		Location:         location,
		ProbeReader:      probe,
		StorageClass:     storageClass,
		PartitionColumns: req.PartitionBy,
	}, accessKey, secretKey, endpoint)
	if err != nil {
		return nil, err
	}
	if err := w.afterWrite(ctx, projectID, dataset, table); err != nil {
		return nil, err
	}
	return tbl, nil
}

// loadProbeReader globs all Parquet under a managed location (recursively, to
// cover both base-level overwrite files and per-append subdirectories).
func loadProbeReader(location string, partitioned bool) string {
	if partitioned {
		return fmt.Sprintf("read_parquet('%s/**/*.parquet', hive_partitioning=true)", escapeLiteral(location))
	}
	// Non-partitioned COPY TO creates a single file without extension; the
	// ** recursive glob picks it up regardless of name.
	return fmt.Sprintf("read_parquet('%s/**')", escapeLiteral(location))
}
