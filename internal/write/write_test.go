package write

import (
	"context"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// fakeStore records BumpDataVersion calls and serves a managed table.
type fakeStore struct {
	bumped int
	tables map[string]*metastore.Table
}

func newFakeStore() *fakeStore { return &fakeStore{tables: map[string]*metastore.Table{}} }

func key(p, d, n string) string { return p + "/" + d + "/" + n }

func (f *fakeStore) BumpDataVersion(ctx context.Context, p, d, n string) (int64, error) {
	f.bumped++
	if t, ok := f.tables[key(p, d, n)]; ok {
		t.DataVersion++
		return t.DataVersion, nil
	}
	return int64(f.bumped), nil
}

// fakeCache records invalidations.
type fakeCache struct{ invalidated int }

func (c *fakeCache) DeleteCacheEntriesForTable(ctx context.Context, p, d, n string) error {
	c.invalidated++
	return nil
}

func TestManagedLocation(t *testing.T) {
	w := &Writer{}
	got := w.managedLocation("ds3-fast", "p1", "sales", "orders")
	want := "s3://ds3-fast/_managed/p1/sales/orders/"
	if got != want {
		t.Fatalf("managedLocation = %q, want %q", got, want)
	}
}

func TestAfterWrite_BumpsAndInvalidates(t *testing.T) {
	store := newFakeStore()
	cache := &fakeCache{}
	w := &Writer{store: store, cache: cache}
	if err := w.afterWrite(context.Background(), "p1", "sales", "orders"); err != nil {
		t.Fatalf("afterWrite: %v", err)
	}
	if store.bumped != 1 {
		t.Fatalf("expected 1 bump, got %d", store.bumped)
	}
	if cache.invalidated != 1 {
		t.Fatalf("expected 1 invalidation, got %d", cache.invalidated)
	}
}
