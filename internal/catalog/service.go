package catalog

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/planner"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

var identRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func validIdent(kind, s string) error {
	if !identRe.MatchString(s) {
		return fmt.Errorf("invalid %s name %q: must match [a-zA-Z_][a-zA-Z0-9_]*", kind, s)
	}
	return nil
}

// SchemaInferer is the subset of *query.Engine the catalog needs (kept small for testing).
type SchemaInferer interface {
	InferSchema(path, accessKey, secretKey, endpoint string) *query.SchemaResult
	QueryView(sql string, bindings []query.ViewBinding, accessKey, secretKey, endpoint string) *query.Result
}

type Service struct {
	store  metastore.Store
	engine SchemaInferer
}

func NewService(store metastore.Store, engine SchemaInferer) *Service {
	return &Service{store: store, engine: engine}
}

func (s *Service) CreateDataset(ctx context.Context, projectID, name string) error {
	if err := validIdent("dataset", name); err != nil {
		return err
	}
	return s.store.CreateDataset(ctx, &metastore.Dataset{ProjectID: projectID, Name: name})
}

func (s *Service) ListDatasets(ctx context.Context, projectID string) ([]*metastore.Dataset, error) {
	return s.store.ListDatasets(ctx, projectID)
}

type RegisterTableInput struct {
	ProjectID        string
	Dataset          string
	Name             string
	Location         string
	Format           string // parquet | csv | tsv | json
	StorageClass     string // ssd | hdd (default hdd)
	PartitionColumns []string
}

// s3PathFromHTTPS rewrites an https S3 virtual-hosted-style URL to s3:// so
// DuckDB uses the configured S3 credentials. Duplicated in query package to
// avoid an import cycle (catalog imports query, not the reverse).
func s3PathFromHTTPS(path string) string {
	if !strings.HasPrefix(path, "https://") {
		return path
	}
	rest := path[len("https://"):]
	firstSlash := strings.IndexByte(rest, '/')
	if firstSlash < 0 {
		return path
	}
	host := rest[:firstSlash]
	key := rest[firstSlash+1:]
	if key == "" {
		return path
	}
	dot := strings.IndexByte(host, '.')
	if dot <= 0 {
		return path
	}
	bucket := host[:dot]
	if bucket == "" {
		return path
	}
	return "s3://" + bucket + "/" + key
}

// readerSQL builds the DuckDB reader expression for a location + format.
// HTTPS URLs are converted to s3:// so DuckDB uses the configured S3 credentials.
func readerSQL(location, format string) string {
	loc := s3PathFromHTTPS(location)
	loc = strings.ReplaceAll(loc, "'", "''")
	switch strings.ToLower(format) {
	case "parquet":
		return fmt.Sprintf("read_parquet('%s')", loc)
	case "json":
		return fmt.Sprintf("read_json_auto('%s')", loc)
	case "tsv":
		return fmt.Sprintf("read_csv_auto('%s', delim='\t')", loc)
	default: // csv
		return fmt.Sprintf("read_csv_auto('%s')", loc)
	}
}

func (s *Service) RegisterTable(ctx context.Context, in RegisterTableInput, accessKey, secretKey, endpoint string) (*metastore.Table, error) {
	if err := validIdent("table", in.Name); err != nil {
		return nil, err
	}
	if _, err := s.store.GetDataset(ctx, in.ProjectID, in.Dataset); err != nil {
		return nil, fmt.Errorf("dataset %q: %w", in.Dataset, err)
	}
	// Normalize https S3 URLs to s3:// so DuckDB uses credentials.
	in.Location = s3PathFromHTTPS(in.Location)
	storageClass := in.StorageClass
	if storageClass == "" {
		storageClass = "hdd"
	}

	schemaRes := s.engine.InferSchema(in.Location, accessKey, secretKey, endpoint)
	if schemaRes.Error != "" {
		return nil, fmt.Errorf("infer schema: %s", schemaRes.Error)
	}
	// Use the detected format (from which reader actually worked) rather than
	// trusting the user-supplied format, which may be wrong (e.g. "parquet"
	// for a CSV file). If detection didn't run (e.g. old InferSchema), keep
	// the user-supplied format as-is.
	if schemaRes.Detected != "" {
		in.Format = schemaRes.Detected
	}
	cols := make([]metastore.Column, len(schemaRes.Columns))
	for i, c := range schemaRes.Columns {
		cols[i] = metastore.Column{Name: c.Name, Type: c.Type, Nullable: c.Nullable}
	}

	// Best-effort row-count stat.
	var rowCount int64
	countRes := s.engine.QueryView(
		"SELECT count(*) AS c FROM "+readerSQL(in.Location, in.Format),
		nil, accessKey, secretKey, endpoint)
	if countRes.Error == "" && countRes.RowCount == 1 {
		switch v := countRes.Rows[0][0].(type) {
		case int64:
			rowCount = v
		case int:
			rowCount = int64(v)
		}
	}

	t := &metastore.Table{
		ProjectID:        in.ProjectID,
		Dataset:          in.Dataset,
		Name:             in.Name,
		Kind:             "external",
		Location:         in.Location,
		Format:           strings.ToLower(in.Format),
		StorageClass:     storageClass,
		PartitionColumns: in.PartitionColumns,
		Schema:           cols,
		Stats:            metastore.Stats{RowCount: rowCount},
	}
	if err := s.store.CreateTable(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Service) GetTable(ctx context.Context, projectID, dataset, name string) (*metastore.Table, error) {
	return s.store.GetTable(ctx, projectID, dataset, name)
}

func (s *Service) ListTables(ctx context.Context, projectID, dataset string) ([]*metastore.Table, error) {
	return s.store.ListTables(ctx, projectID, dataset)
}

func (s *Service) DropTable(ctx context.Context, projectID, dataset, name string) error {
	return s.store.DeleteTable(ctx, projectID, dataset, name)
}

// Resolve scans sql for references to the project's catalog tables and returns a
// view binding for each one that appears. Matching is done on the qualified name
// `dataset.table` (and its double-quoted form) at identifier boundaries.
func (s *Service) Resolve(ctx context.Context, projectID, sql string) ([]query.ViewBinding, error) {
	datasets, err := s.store.ListDatasets(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var bindings []query.ViewBinding
	for _, ds := range datasets {
		tables, err := s.store.ListTables(ctx, projectID, ds.Name)
		if err != nil {
			return nil, err
		}
		for _, t := range tables {
			if referencesTable(sql, t.Dataset, t.Name) {
				bindings = append(bindings, query.ViewBinding{
					Schema:    t.Dataset,
					Name:      t.Name,
					ReaderSQL: readerSQL(t.Location, t.Format),
				})
			}
		}
	}
	return bindings, nil
}

func referencesTable(sql, dataset, name string) bool {
	patterns := []string{
		`(?i)\b` + regexp.QuoteMeta(dataset) + `\.` + regexp.QuoteMeta(name) + `\b`,
		`(?i)"` + regexp.QuoteMeta(dataset) + `"\."` + regexp.QuoteMeta(name) + `"`,
	}
	for _, p := range patterns {
		if regexp.MustCompile(p).MatchString(sql) {
			return true
		}
	}
	return false
}

// SaveTablePartitions persists an updated partition list (and row count) for a
// table by recreating its catalog row. Phase 3 load/CTAS uses this after writing
// data; tests use it to inject partition layouts.
func (s *Service) SaveTablePartitions(ctx context.Context, t *metastore.Table) error {
	if err := s.store.DeleteTable(ctx, t.ProjectID, t.Dataset, t.Name); err != nil {
		return err
	}
	// CreateTable refreshes UpdatedAt; CreatedAt is preserved from the original.
	return s.store.CreateTable(ctx, t)
}

// toPlannerPartitions converts stored metastore partitions to planner partitions.
func toPlannerPartitions(in []metastore.Partition) []planner.Partition {
	out := make([]planner.Partition, len(in))
	for i, p := range in {
		out[i] = planner.Partition{
			Values:   p.Values,
			Location: p.Location,
			RowCount: p.RowCount,
			Min:      p.Min,
			Max:      p.Max,
		}
	}
	return out
}

// ResolvePruned is like Resolve, but for partitioned tables with a stored
// partition list it builds the reader expression from only the partitions that
// can satisfy the query's WHERE predicates (partition pruning). Tables without a
// partition list fall back to the base-location reader.
func (s *Service) ResolvePruned(ctx context.Context, projectID, sql string) ([]query.ViewBinding, error) {
	datasets, err := s.store.ListDatasets(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var bindings []query.ViewBinding
	for _, ds := range datasets {
		tables, err := s.store.ListTables(ctx, projectID, ds.Name)
		if err != nil {
			return nil, err
		}
		for _, t := range tables {
			if !referencesTable(sql, t.Dataset, t.Name) {
				continue
			}
			reader := readerSQL(t.Location, t.Format)
			if len(t.PartitionColumns) > 0 && len(t.Stats.Partitions) > 0 {
				kept := planner.Prune(sql, t.PartitionColumns, toPlannerPartitions(t.Stats.Partitions))
				if len(kept) > 0 {
					reader = planner.ReaderLocations(kept, t.Format)
				}
			}
			bindings = append(bindings, query.ViewBinding{
				Schema:    t.Dataset,
				Name:      t.Name,
				ReaderSQL: reader,
			})
		}
	}
	return bindings, nil
}

// PrefixDeleter deletes all objects under an s3 prefix (s3.Client satisfies it).
type PrefixDeleter interface {
	DeletePrefix(ctx context.Context, bucket, prefix string) error
}

// CacheInvalidator removes result-cache entries referencing a table.
type CacheInvalidator interface {
	DeleteCacheEntriesForTable(ctx context.Context, projectID, dataset, table string) error
}

// RegisterManagedInput registers (or replaces) a managed table after its data
// has been written. ProbeReader is the DuckDB reader expression for the just-
// written location, used to infer schema + row count.
type RegisterManagedInput struct {
	ProjectID        string
	Dataset          string
	Name             string
	Location         string // s3:// base prefix of the managed files
	ProbeReader      string // e.g. read_parquet('s3://.../*.parquet') or **/*.parquet
	StorageClass     string
	PartitionColumns []string
}

// RegisterManaged upserts a managed table, inferring schema + stats from the
// written data via ProbeReader. If a table with the same name exists it is
// replaced (drop registration, recreate) so CTAS/load are idempotent.
func (s *Service) RegisterManaged(ctx context.Context, in RegisterManagedInput, accessKey, secretKey, endpoint string) (*metastore.Table, error) {
	if err := validIdent("table", in.Name); err != nil {
		return nil, err
	}
	if _, err := s.store.GetDataset(ctx, in.ProjectID, in.Dataset); err != nil {
		return nil, fmt.Errorf("dataset %q: %w", in.Dataset, err)
	}

	// Infer schema from the written data using DESCRIBE + the probe reader.
	// ProbeReader is a full DuckDB reader expression (e.g. read_parquet('...')), so we
	// use QueryView with DESCRIBE rather than InferSchema (which wraps paths in readers).
	// DESCRIBE returns rows with columns (column_name, column_type, null, key, default, extra).
	descRes := s.engine.QueryView("DESCRIBE SELECT * FROM "+in.ProbeReader, nil, accessKey, secretKey, endpoint)
	if descRes.Error != "" {
		return nil, fmt.Errorf("infer managed schema: %s", descRes.Error)
	}
	cols := make([]metastore.Column, 0, len(descRes.Rows))
	for _, row := range descRes.Rows {
		if len(row) < 2 {
			continue
		}
		name := fmt.Sprintf("%v", row[0])
		typ := fmt.Sprintf("%v", row[1])
		nullable := true
		if len(row) >= 3 {
			nullable = fmt.Sprintf("%v", row[2]) == "YES"
		}
		cols = append(cols, metastore.Column{Name: name, Type: typ, Nullable: nullable})
	}

	var rowCount int64
	countRes := s.engine.QueryView("SELECT count(*) AS c FROM "+in.ProbeReader, nil, accessKey, secretKey, endpoint)
	if countRes.Error == "" && countRes.RowCount == 1 {
		switch v := countRes.Rows[0][0].(type) {
		case int64:
			rowCount = v
		case int32:
			rowCount = int64(v)
		case int:
			rowCount = int64(v)
		}
	}

	storageClass := in.StorageClass
	if storageClass == "" {
		storageClass = "ssd"
	}

	// Replace any existing registration (idempotent CTAS/load).
	_ = s.store.DeleteTable(ctx, in.ProjectID, in.Dataset, in.Name)

	t := &metastore.Table{
		ProjectID:        in.ProjectID,
		Dataset:          in.Dataset,
		Name:             in.Name,
		Kind:             "managed",
		Location:         in.Location,
		Format:           "parquet",
		StorageClass:     storageClass,
		PartitionColumns: in.PartitionColumns,
		Schema:           cols,
		Stats:            metastore.Stats{RowCount: rowCount},
	}
	if err := s.store.CreateTable(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// DropTableWithData drops a table registration. For managed tables it also
// deletes the underlying data objects via the deleter, parsing the bucket +
// prefix from the table's s3:// Location. The cache invalidator is always
// called so dependent cached results are evicted.
func (s *Service) DropTableWithData(ctx context.Context, projectID, dataset, name string, deleter PrefixDeleter, cache CacheInvalidator, accessKey, secretKey, endpoint string) error {
	tbl, err := s.store.GetTable(ctx, projectID, dataset, name)
	if err != nil {
		return err
	}
	if tbl.Kind == "managed" && deleter != nil {
		bucket, prefix, ok := splitS3(tbl.Location)
		if ok {
			if err := deleter.DeletePrefix(ctx, bucket, prefix); err != nil {
				return fmt.Errorf("delete managed data: %w", err)
			}
		}
	}
	if err := s.store.DeleteTable(ctx, projectID, dataset, name); err != nil {
		return err
	}
	if cache != nil {
		_ = cache.DeleteCacheEntriesForTable(ctx, projectID, dataset, name)
	}
	return nil
}

// splitS3 splits an "s3://bucket/key/prefix" into (bucket, "key/prefix"). The
// returned ok is false for non-s3 locations (e.g. external local globs).
func splitS3(location string) (bucket, prefix string, ok bool) {
	const scheme = "s3://"
	if !strings.HasPrefix(location, scheme) {
		return "", "", false
	}
	rest := location[len(scheme):]
	idx := strings.IndexByte(rest, '/')
	if idx < 0 {
		return rest, "", true
	}
	return rest[:idx], rest[idx+1:], true
}
