package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestResolvePruned_SelectsMatchingPartitions(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()

	// Build a Hive-style local layout: <root>/dt=2026-06-06/data.csv etc.
	root := t.TempDir()
	mk := func(dt, body string) string {
		dir := filepath.Join(root, "dt="+dt)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "data.csv")
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p6 := mk("2026-06-06", "id,total\n1,10\n")
	p7 := mk("2026-06-07", "id,total\n2,20\n")

	if err := svc.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	// Register with one base partition glob, then store the partition list directly.
	if _, err := svc.RegisterTable(ctx, RegisterTableInput{
		ProjectID: "p1", Dataset: "sales", Name: "orders",
		Location: p6, Format: "csv", PartitionColumns: []string{"dt"},
	}, "", "", ""); err != nil {
		t.Fatal(err)
	}
	// Inject the partition list (Phase 3 normally populates this on load/CTAS).
	tbl, err := svc.GetTable(ctx, "p1", "sales", "orders")
	if err != nil {
		t.Fatal(err)
	}
	tbl.Stats.Partitions = []metastore.Partition{
		{Values: map[string]string{"dt": "2026-06-06"}, Location: p6},
		{Values: map[string]string{"dt": "2026-06-07"}, Location: p7},
	}
	if err := svc.SaveTablePartitions(ctx, tbl); err != nil {
		t.Fatal(err)
	}

	// Pruned: only the 2026-06-07 partition.
	bindings, err := svc.ResolvePruned(ctx, "p1",
		"SELECT * FROM sales.orders WHERE dt = '2026-06-07'")
	if err != nil {
		t.Fatalf("ResolvePruned: %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if !strings.Contains(bindings[0].ReaderSQL, p7) || strings.Contains(bindings[0].ReaderSQL, p6) {
		t.Fatalf("expected reader over only p7, got %q", bindings[0].ReaderSQL)
	}

	// No predicate: both partitions present.
	all, err := svc.ResolvePruned(ctx, "p1", "SELECT * FROM sales.orders")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all[0].ReaderSQL, p6) || !strings.Contains(all[0].ReaderSQL, p7) {
		t.Fatalf("expected reader over both partitions, got %q", all[0].ReaderSQL)
	}
}
