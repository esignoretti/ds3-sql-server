package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// fakeDeleter records DeletePrefix calls.
type fakeDeleter struct {
	calls []string // "bucket|prefix"
}

func (d *fakeDeleter) DeletePrefix(ctx context.Context, bucket, prefix string) error {
	d.calls = append(d.calls, bucket+"|"+prefix)
	return nil
}

// fakeInvalidator records cache invalidations.
type fakeInvalidator struct{ n int }

func (c *fakeInvalidator) DeleteCacheEntriesForTable(ctx context.Context, p, d, t string) error {
	c.n++
	return nil
}

func TestDropTableWithData_Managed(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	csv := filepath.Join(t.TempDir(), "m.csv")
	_ = os.WriteFile(csv, []byte("id\n1\n"), 0644)
	if err := svc.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	// Register a managed table whose Location is an s3:// prefix.
	tbl, err := svc.RegisterManaged(ctx, RegisterManagedInput{
		ProjectID: "p1", Dataset: "sales", Name: "orders",
		Location:     "s3://ds3-fast/_managed/sales/orders/",
		ProbeReader:  "read_csv_auto('" + csv + "')",
		StorageClass: "ssd",
	}, "", "", "")
	if err != nil {
		t.Fatalf("RegisterManaged: %v", err)
	}
	if tbl.Kind != "managed" {
		t.Fatalf("expected managed kind, got %q", tbl.Kind)
	}

	del := &fakeDeleter{}
	inv := &fakeInvalidator{}
	if err := svc.DropTableWithData(ctx, "p1", "sales", "orders", del, inv, "", "", ""); err != nil {
		t.Fatalf("DropTableWithData: %v", err)
	}
	if len(del.calls) != 1 || del.calls[0] != "ds3-fast|_managed/sales/orders/" {
		t.Fatalf("expected one delete of the managed prefix, got %v", del.calls)
	}
	if inv.n != 1 {
		t.Fatalf("expected one cache invalidation, got %d", inv.n)
	}
	if _, err := svc.GetTable(ctx, "p1", "sales", "orders"); err != metastore.ErrNotFound {
		t.Fatalf("expected ErrNotFound after drop, got %v", err)
	}
}

func TestDropTableWithData_ExternalSkipsDelete(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	csv := filepath.Join(t.TempDir(), "e.csv")
	_ = os.WriteFile(csv, []byte("id\n1\n"), 0644)
	if err := svc.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterTable(ctx, RegisterTableInput{
		ProjectID: "p1", Dataset: "sales", Name: "ext", Location: csv, Format: "csv",
	}, "", "", ""); err != nil {
		t.Fatal(err)
	}
	del := &fakeDeleter{}
	inv := &fakeInvalidator{}
	if err := svc.DropTableWithData(ctx, "p1", "sales", "ext", del, inv, "", "", ""); err != nil {
		t.Fatalf("DropTableWithData: %v", err)
	}
	if len(del.calls) != 0 {
		t.Fatalf("external table must not delete data, got %v", del.calls)
	}
	if inv.n != 1 {
		t.Fatalf("expected cache invalidation even for external, got %d", inv.n)
	}
}
