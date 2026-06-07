package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

func newService(t *testing.T) *Service {
	t.Helper()
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	eng, err := query.NewEngine(1000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return NewService(store, eng)
}

func TestCreateDataset_Validation(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	if err := svc.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatalf("valid dataset rejected: %v", err)
	}
	if err := svc.CreateDataset(ctx, "p1", "bad name!"); err == nil {
		t.Fatal("expected validation error for bad dataset name")
	}
}

func TestRegisterTable_InfersSchemaAndStats(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	csv := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(csv, []byte("id,total\n1,10\n2,20\n3,30\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	if err := svc.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	tbl, err := svc.RegisterTable(ctx, RegisterTableInput{
		ProjectID: "p1", Dataset: "sales", Name: "orders",
		Location: csv, Format: "csv",
	}, "", "", "")
	if err != nil {
		t.Fatalf("RegisterTable: %v", err)
	}
	if len(tbl.Schema) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(tbl.Schema))
	}
	if tbl.Stats.RowCount != 3 {
		t.Fatalf("expected 3 rows, got %d", tbl.Stats.RowCount)
	}
	// Registering into a missing dataset must fail.
	if _, err := svc.RegisterTable(ctx, RegisterTableInput{
		ProjectID: "p1", Dataset: "missing", Name: "x", Location: csv, Format: "csv",
	}, "", "", ""); err == nil {
		t.Fatal("expected error registering into missing dataset")
	}
}
