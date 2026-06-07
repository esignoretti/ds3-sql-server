package catalog

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
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

// readerSQL builds the DuckDB reader expression for a location + format.
func readerSQL(location, format string) string {
	loc := strings.ReplaceAll(location, "'", "''")
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
	storageClass := in.StorageClass
	if storageClass == "" {
		storageClass = "hdd"
	}

	schemaRes := s.engine.InferSchema(in.Location, accessKey, secretKey, endpoint)
	if schemaRes.Error != "" {
		return nil, fmt.Errorf("infer schema: %s", schemaRes.Error)
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
