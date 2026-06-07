package write

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

func TestParseCTAS_Valid(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want CTASPlan
	}{
		{
			name: "minimal",
			sql:  "CREATE TABLE sales.daily AS SELECT 1 AS x",
			want: CTASPlan{Dataset: "sales", Table: "daily", Select: "SELECT 1 AS x"},
		},
		{
			name: "partition_and_storage",
			sql:  "CREATE TABLE sales.daily PARTITION BY (dt, region) STORAGE 'ssd' AS SELECT dt, region, n FROM sales.raw",
			want: CTASPlan{
				Dataset: "sales", Table: "daily",
				PartitionBy:  []string{"dt", "region"},
				StorageClass: "ssd",
				Select:       "SELECT dt, region, n FROM sales.raw",
			},
		},
		{
			name: "if_not_exists_and_single_partition",
			sql:  "create table IF NOT EXISTS analytics.t PARTITION BY (dt) AS select * from analytics.src",
			want: CTASPlan{
				Dataset: "analytics", Table: "t",
				PartitionBy: []string{"dt"},
				Select:      "select * from analytics.src",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCTAS(tc.sql)
			if err != nil {
				t.Fatalf("ParseCTAS: %v", err)
			}
			if got.Dataset != tc.want.Dataset || got.Table != tc.want.Table ||
				got.StorageClass != tc.want.StorageClass || got.Select != tc.want.Select ||
				!reflect.DeepEqual(normalizeNil(got.PartitionBy), normalizeNil(tc.want.PartitionBy)) {
				t.Fatalf("ParseCTAS = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func normalizeNil(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func TestParseCTAS_Rejects(t *testing.T) {
	bad := []string{
		"SELECT 1",                                  // not a CREATE TABLE
		"CREATE TABLE orders AS SELECT 1",           // missing dataset qualifier
		"CREATE TABLE sales.daily SELECT 1",         // missing AS
		"CREATE TABLE sales.daily STORAGE 'tape' AS SELECT 1", // bad storage class
		"CREATE TABLE sales.daily AS",               // empty select
		"CREATE OR REPLACE TABLE sales.daily AS SELECT 1", // unsupported form
	}
	for _, sql := range bad {
		if _, err := ParseCTAS(sql); err == nil {
			t.Fatalf("expected error for %q", sql)
		}
	}
}

func TestIsCTAS(t *testing.T) {
	if !IsCTAS("  create   table sales.t AS SELECT 1") {
		t.Fatal("expected IsCTAS true")
	}
	if IsCTAS("SELECT 1") {
		t.Fatal("expected IsCTAS false")
	}
	if IsCTAS("CREATE TABLE sales.t (id INT)") {
		t.Fatal("plain CREATE TABLE (no AS SELECT) must not be CTAS")
	}
}

// localStorage maps every class to a local base directory, so managedLocation
// produces a filesystem path DuckDB can COPY to in tests.
type localStorage struct{ dir string }

func (l localStorage) Resolve(class string) (string, string, bool) {
	return l.dir, "", true
}

func newCTASWriter(t *testing.T) (*Writer, *catalog.Service, string) {
	t.Helper()
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	eng, err := query.NewEngine(100000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.NewService(store, eng)
	baseDir := t.TempDir()
	// Ensure the managed directory exists so DuckDB COPY can write into it.
	// DuckDB creates the leaf directory but needs the parent chain to exist.
	os.MkdirAll(filepath.Join(baseDir, "_managed", "sales"), 0755)
	w := NewWriter(eng, cat, store, noopCache{}, localStorage{dir: baseDir}, nil)
	// Override managedLocation to emit local paths instead of s3:// in tests.
	w.localBase = baseDir
	return w, cat, baseDir
}

type noopCache struct{}

func (noopCache) DeleteCacheEntriesForTable(ctx context.Context, p, d, t string) error { return nil }

func TestRunCTAS_EndToEndLocal(t *testing.T) {
	w, cat, _ := newCTASWriter(t)
	ctx := context.Background()

	// Source external table over a local CSV.
	csv := filepath.Join(t.TempDir(), "raw.csv")
	if err := os.WriteFile(csv, []byte("dt,region,n\n2026-06-01,eu,5\n2026-06-01,us,7\n2026-06-02,eu,3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cat.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.RegisterTable(ctx, catalog.RegisterTableInput{
		ProjectID: "p1", Dataset: "sales", Name: "raw", Location: csv, Format: "csv",
	}, "", "", ""); err != nil {
		t.Fatal(err)
	}

	sql := "CREATE TABLE sales.daily PARTITION BY (dt) STORAGE 'ssd' AS SELECT dt, region, n FROM sales.raw WHERE n > 3"
	tbl, err := w.RunCTAS(ctx, "p1", sql, "", "", "")
	if err != nil {
		t.Fatalf("RunCTAS: %v", err)
	}
	if tbl.Kind != "managed" || tbl.StorageClass != "ssd" {
		t.Fatalf("unexpected table: %+v", tbl)
	}
	if len(tbl.PartitionColumns) != 1 || tbl.PartitionColumns[0] != "dt" {
		t.Fatalf("expected partition by dt, got %+v", tbl.PartitionColumns)
	}
	// 2 rows survive n>3 (5 and 7). Read the written Parquet back.
	res := w.engine.(*query.Engine).QueryView(
		"SELECT count(*) AS c FROM read_parquet('"+filepath.Join(tbl.Location, "**", "*.parquet")+"', hive_partitioning=true)",
		nil, "", "", "")
	if res.Error != "" {
		t.Fatalf("read back: %s", res.Error)
	}
	if toI64(res.Rows[0][0]) != 2 {
		t.Fatalf("expected 2 rows written, got %v", res.Rows[0][0])
	}
}

func toI64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case int:
		return int64(x)
	default:
		return -1
	}
}
