package metastore

import (
	"context"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "meta.db")
	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenSQLite_CreatesSchema(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.ListDatasets(context.Background(), "proj-1"); err != nil {
		t.Fatalf("ListDatasets on empty store: %v", err)
	}
}

func TestDatasetCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateDataset(ctx, &Dataset{ProjectID: "p1", Name: "sales"}); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	// Duplicate create must fail.
	if err := s.CreateDataset(ctx, &Dataset{ProjectID: "p1", Name: "sales"}); err == nil {
		t.Fatal("expected error on duplicate dataset")
	}
	// Same name under a different project is allowed.
	if err := s.CreateDataset(ctx, &Dataset{ProjectID: "p2", Name: "sales"}); err != nil {
		t.Fatalf("CreateDataset p2: %v", err)
	}

	got, err := s.GetDataset(ctx, "p1", "sales")
	if err != nil {
		t.Fatalf("GetDataset: %v", err)
	}
	if got.Name != "sales" || got.ProjectID != "p1" {
		t.Fatalf("unexpected dataset: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("CreatedAt not set")
	}

	if _, err := s.GetDataset(ctx, "p1", "missing"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	list, err := s.ListDatasets(ctx, "p1")
	if err != nil {
		t.Fatalf("ListDatasets: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 dataset for p1, got %d", len(list))
	}
}

func sampleTable() *Table {
	return &Table{
		ProjectID:        "p1",
		Dataset:          "sales",
		Name:             "orders",
		Kind:             "external",
		Location:         "s3://bucket/orders/*.parquet",
		Format:           "parquet",
		StorageClass:     "hdd",
		PartitionColumns: []string{"dt"},
		Schema:           []Column{{Name: "id", Type: "BIGINT", Nullable: false}},
		Stats:            Stats{RowCount: 42},
		DataVersion:      1,
	}
}

func TestTableCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateTable(ctx, sampleTable()); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if err := s.CreateTable(ctx, sampleTable()); err == nil {
		t.Fatal("expected error on duplicate table")
	}

	got, err := s.GetTable(ctx, "p1", "sales", "orders")
	if err != nil {
		t.Fatalf("GetTable: %v", err)
	}
	if got.Format != "parquet" || len(got.Schema) != 1 || got.Schema[0].Name != "id" {
		t.Fatalf("schema round-trip failed: %+v", got)
	}
	if len(got.PartitionColumns) != 1 || got.PartitionColumns[0] != "dt" {
		t.Fatalf("partition columns round-trip failed: %+v", got.PartitionColumns)
	}
	if got.Stats.RowCount != 42 {
		t.Fatalf("stats round-trip failed: %+v", got.Stats)
	}

	list, err := s.ListTables(ctx, "p1", "sales")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListTables: err=%v len=%d", err, len(list))
	}

	v, err := s.BumpDataVersion(ctx, "p1", "sales", "orders")
	if err != nil {
		t.Fatalf("BumpDataVersion: %v", err)
	}
	if v != 2 {
		t.Fatalf("expected version 2, got %d", v)
	}

	if err := s.DeleteTable(ctx, "p1", "sales", "orders"); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}
	if _, err := s.GetTable(ctx, "p1", "sales", "orders"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
