package write

import (
	"reflect"
	"testing"
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
