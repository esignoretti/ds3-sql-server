package write

import (
	"context"
	"fmt"
	"path/filepath"
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

// prefixDeleter clears all objects under a managed location's prefix.
type prefixDeleter interface {
	DeletePrefix(ctx context.Context, bucket, prefix string) error
}

// Writer executes write jobs (CTAS, load) against the managed catalog.
type Writer struct {
	engine    writeEngine
	cat       catalogService
	store     versionBumper
	cache     cacheInvalidator
	storage   storageResolver
	deleter   prefixDeleter
	localBase string // test-only: when set, managed locations are local dirs
}

// NewWriter builds a Writer. Any of store/cache/deleter may be nil-tolerant only
// in tests; production wiring always supplies all dependencies.
func NewWriter(engine writeEngine, cat catalogService, store versionBumper, cache cacheInvalidator, storage storageResolver, deleter prefixDeleter) *Writer {
	return &Writer{engine: engine, cat: cat, store: store, cache: cache, storage: storage, deleter: deleter}
}

// managedLocation returns the base location for a managed table's data files.
// In tests localBase makes this a filesystem directory; in production it is an
// s3:// prefix under the storage-class bucket.
func (w *Writer) managedLocation(bucket, dataset, table string) string {
	if w.localBase != "" {
		return filepath.Join(w.localBase, "_managed", dataset, table)
	}
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
