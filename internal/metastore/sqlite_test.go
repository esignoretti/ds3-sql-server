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
