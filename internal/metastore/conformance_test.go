package metastore

import (
	"context"
	"path/filepath"
	"testing"
)

// storeFactory creates a fresh, isolated Store for one subtest.
type storeFactory func(t *testing.T) Store

// testStoreConformance runs the full behavioural contract against any Store impl.
func testStoreConformance(t *testing.T, newStore storeFactory) {
	t.Run("DatasetCRUD", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.CreateDataset(ctx, &Dataset{ProjectID: "p1", Name: "sales"}); err != nil {
			t.Fatalf("CreateDataset: %v", err)
		}
		if err := s.CreateDataset(ctx, &Dataset{ProjectID: "p1", Name: "sales"}); err == nil {
			t.Fatal("expected duplicate dataset error")
		}
		got, err := s.GetDataset(ctx, "p1", "sales")
		if err != nil || got.Name != "sales" {
			t.Fatalf("GetDataset: %v %+v", err, got)
		}
		if _, err := s.GetDataset(ctx, "p1", "nope"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
		list, err := s.ListDatasets(ctx, "p1")
		if err != nil || len(list) != 1 {
			t.Fatalf("ListDatasets: %v len=%d", err, len(list))
		}
	})

	t.Run("TableCRUDAndVersion", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		_ = s.CreateDataset(ctx, &Dataset{ProjectID: "p1", Name: "sales"})
		tbl := &Table{
			ProjectID: "p1", Dataset: "sales", Name: "orders", Kind: "external",
			Location: "s3://b/orders/*.parquet", Format: "parquet", StorageClass: "hdd",
			PartitionColumns: []string{"dt"},
			Schema:           []Column{{Name: "id", Type: "BIGINT", Nullable: false}},
			Stats: Stats{RowCount: 3, Partitions: []Partition{
				{Values: map[string]string{"dt": "2026-06-07"}, Location: "s3://b/orders/dt=2026-06-07/", RowCount: 3},
			}},
		}
		if err := s.CreateTable(ctx, tbl); err != nil {
			t.Fatalf("CreateTable: %v", err)
		}
		got, err := s.GetTable(ctx, "p1", "sales", "orders")
		if err != nil {
			t.Fatalf("GetTable: %v", err)
		}
		if len(got.Schema) != 1 || got.Schema[0].Name != "id" {
			t.Fatalf("schema round-trip: %+v", got.Schema)
		}
		if len(got.PartitionColumns) != 1 || got.PartitionColumns[0] != "dt" {
			t.Fatalf("partition cols round-trip: %+v", got.PartitionColumns)
		}
		if len(got.Stats.Partitions) != 1 || got.Stats.Partitions[0].Values["dt"] != "2026-06-07" {
			t.Fatalf("partition stats round-trip: %+v", got.Stats.Partitions)
		}
		v, err := s.BumpDataVersion(ctx, "p1", "sales", "orders")
		if err != nil || v != 2 {
			t.Fatalf("BumpDataVersion: v=%d err=%v", v, err)
		}
		list, err := s.ListTables(ctx, "p1", "sales")
		if err != nil || len(list) != 1 {
			t.Fatalf("ListTables: %v len=%d", err, len(list))
		}
		if err := s.DeleteTable(ctx, "p1", "sales", "orders"); err != nil {
			t.Fatalf("DeleteTable: %v", err)
		}
		if _, err := s.GetTable(ctx, "p1", "sales", "orders"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound after delete, got %v", err)
		}
	})
}

func TestSQLiteConformance(t *testing.T) {
	testStoreConformance(t, func(t *testing.T) Store {
		s, err := OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
		if err != nil {
			t.Fatalf("OpenSQLite: %v", err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}
