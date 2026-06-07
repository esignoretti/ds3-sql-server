package write

import (
	"context"
	"fmt"
	"strings"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// versionBumper is the subset of metastore.Store the writer needs for invalidation.
type versionBumper interface {
	BumpDataVersion(ctx context.Context, projectID, dataset, name string) (int64, error)
}

// cacheInvalidator deletes result-cache entries that reference a table.
type cacheInvalidator interface {
	DeleteCacheEntriesForTable(ctx context.Context, projectID, dataset, table string) error
}

// storageResolver maps a logical storage class to a bucket + endpoint.
// config.Config satisfies a compatible shape via an adapter in main.go.
type storageResolver interface {
	Resolve(class string) (bucket, endpoint string, ok bool)
}

// catalogService is the subset of *catalog.Service the writer uses.
type catalogService interface {
	Resolve(ctx context.Context, projectID, sql string) ([]query.ViewBinding, error)
	RegisterManaged(ctx context.Context, in catalog.RegisterManagedInput, ak, sk, ep string) (*metastore.Table, error)
	GetTable(ctx context.Context, projectID, dataset, name string) (*metastore.Table, error)
}

// writeEngine is the subset of *query.Engine the writer uses.
type writeEngine interface {
	ExecWrite(sql string, bindings []query.ViewBinding, ak, sk, ep string) error
}

// Writer executes write jobs (CTAS, load) against the managed catalog.
type Writer struct {
	engine  writeEngine
	cat     catalogService
	store   versionBumper
	cache   cacheInvalidator
	storage storageResolver
}

// NewWriter builds a Writer. Any of store/cache may be nil-tolerant only in
// tests; production wiring always supplies all dependencies.
func NewWriter(engine writeEngine, cat catalogService, store versionBumper, cache cacheInvalidator, storage storageResolver) *Writer {
	return &Writer{engine: engine, cat: cat, store: store, cache: cache, storage: storage}
}

// managedLocation returns the base S3 prefix for a managed table's data files.
func (w *Writer) managedLocation(bucket, dataset, table string) string {
	return fmt.Sprintf("s3://%s/_managed/%s/%s/", bucket, dataset, table)
}

// afterWrite bumps the table's data_version and invalidates dependent
// result-cache entries. Errors from cache invalidation are non-fatal.
func (w *Writer) afterWrite(ctx context.Context, projectID, dataset, table string) error {
	if w.store != nil {
		if _, err := w.store.BumpDataVersion(ctx, projectID, dataset, table); err != nil {
			return fmt.Errorf("bump data version: %w", err)
		}
	}
	if w.cache != nil {
		_ = w.cache.DeleteCacheEntriesForTable(ctx, projectID, dataset, table)
	}
	return nil
}

// escapeLiteral escapes single quotes for embedding a value in SQL.
func escapeLiteral(s string) string { return strings.ReplaceAll(s, "'", "''") }
