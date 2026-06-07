package cache

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

func newResultCache(t *testing.T) (*ResultCache, *metastore.SQLiteStore) {
	t.Helper()
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	bs := NewDirBlobstore(t.TempDir())
	rc := NewResultCache(store, bs, ResultCacheOpts{TTL: time.Hour, MaxBytes: 1 << 20})
	return rc, store
}

func TestNormalizeSQL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT  *   FROM t", "select * from t"},
		{"\tselect *\nfrom t\n", "select * from t"},
		{"Select * From T", "select * from t"},
	}
	for _, c := range cases {
		if got := NormalizeSQL(c.in); got != c.want {
			t.Fatalf("NormalizeSQL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCacheKey_StableAndVersionSensitive(t *testing.T) {
	v1 := map[string]int64{"p1/sales/orders": 3, "p1/sales/lines": 1}
	v2 := map[string]int64{"p1/sales/lines": 1, "p1/sales/orders": 3}
	k1 := CacheKey("p1", "SELECT * FROM sales.orders", v1)
	k2 := CacheKey("p1", "select  *  from sales.orders", v2)
	if k1 != k2 {
		t.Fatalf("key should be stable across map order and SQL whitespace/case: %s vs %s", k1, k2)
	}
	v3 := map[string]int64{"p1/sales/orders": 4, "p1/sales/lines": 1}
	if CacheKey("p1", "SELECT * FROM sales.orders", v3) == k1 {
		t.Fatal("version bump must change the cache key")
	}
}

func TestResultCache_PutGet(t *testing.T) {
	rc, _ := newResultCache(t)
	ctx := context.Background()
	versions := map[string]int64{"p1/sales/orders": 1}
	res := &query.Result{
		Columns:  []query.ColumnInfo{{Name: "c", Type: "BIGINT"}},
		Rows:     [][]any{{float64(2)}},
		RowCount: 1,
	}

	if got, ok := rc.Get(ctx, "p1", "SELECT count(*) FROM sales.orders", versions); ok {
		t.Fatalf("expected miss on empty cache, got %+v", got)
	}
	if err := rc.Put(ctx, "p1", "SELECT count(*) FROM sales.orders", versions, res); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := rc.Get(ctx, "p1", "SELECT count(*) FROM sales.orders", versions)
	if !ok {
		t.Fatal("expected hit after Put")
	}
	if got.RowCount != 1 || len(got.Columns) != 1 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	bumped := map[string]int64{"p1/sales/orders": 2}
	if _, ok := rc.Get(ctx, "p1", "SELECT count(*) FROM sales.orders", bumped); ok {
		t.Fatal("expected miss after version bump")
	}
}

func TestResultCache_PerTableInvalidation(t *testing.T) {
	rc, store := newResultCache(t)
	ctx := context.Background()
	versions := map[string]int64{"p1/sales/orders": 1}
	res := &query.Result{RowCount: 0}
	if err := rc.Put(ctx, "p1", "SELECT 1 FROM sales.orders", versions, res); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteCacheEntriesForTable(ctx, "p1", "sales", "orders"); err != nil {
		t.Fatal(err)
	}
	if _, ok := rc.Get(ctx, "p1", "SELECT 1 FROM sales.orders", versions); ok {
		t.Fatal("entry should be invalidated after DeleteCacheEntriesForTable")
	}
}

func TestResultCache_TTLExpiry(t *testing.T) {
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	rc := NewResultCache(store, NewDirBlobstore(t.TempDir()), ResultCacheOpts{TTL: time.Nanosecond, MaxBytes: 1 << 20})
	ctx := context.Background()
	versions := map[string]int64{"p1/sales/orders": 1}
	if err := rc.Put(ctx, "p1", "SELECT 1", versions, &query.Result{RowCount: 0}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, ok := rc.Get(ctx, "p1", "SELECT 1", versions); ok {
		t.Fatal("expected TTL-expired miss")
	}
}

func TestResultCache_LRUEviction(t *testing.T) {
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	rc := NewResultCache(store, NewDirBlobstore(t.TempDir()), ResultCacheOpts{TTL: time.Hour, MaxBytes: 500})
	ctx := context.Background()
	big := make([]any, 20)
	for i := range big {
		big[i] = "0123456789"
	}
	for i := 0; i < 5; i++ {
		v := map[string]int64{"p1/t/x": int64(i)}
		if err := rc.Put(ctx, "p1", "SELECT "+string(rune('a'+i)), v, &query.Result{Rows: [][]any{big}, RowCount: 1}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	entries, err := store.ListCacheEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, e := range entries {
		total += e.SizeBytes
	}
	if total > 500 {
		t.Fatalf("LRU did not keep cache under cap: total=%d", total)
	}
	if len(entries) == 0 {
		t.Fatal("eviction removed everything; expected the most-recent entry to survive")
	}
}

type fakeExec struct{ calls int32 }

func (f *fakeExec) Execute(ctx context.Context, projectID, sql, ak, sk, ep string, versions map[string]int64) *query.Result {
	atomic.AddInt32(&f.calls, 1)
	return &query.Result{RowCount: 42}
}

func TestCachingExecutor_HitSkipsExecution(t *testing.T) {
	rc, _ := newResultCache(t)
	ctx := context.Background()
	fe := &fakeExec{}
	versions := map[string]int64{"p1/sales/orders": 1}
	vs := func(ctx context.Context, projectID, sql string) (map[string]int64, error) { return versions, nil }
	ce := NewCachingExecutor(rc, fe.Execute, vs)

	r1 := ce.Run(ctx, "p1", "SELECT * FROM sales.orders", "", "", "")
	if r1.RowCount != 42 {
		t.Fatalf("first run result: %+v", r1)
	}
	r2 := ce.Run(ctx, "p1", "SELECT * FROM sales.orders", "", "", "")
	if r2.RowCount != 42 {
		t.Fatalf("second run result: %+v", r2)
	}
	if atomic.LoadInt32(&fe.calls) != 1 {
		t.Fatalf("expected 1 underlying execution (2nd served from cache), got %d", fe.calls)
	}
}
