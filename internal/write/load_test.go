package write

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// recordingDeleter records overwrite deletions and also wipes a local dir so the
// subsequent read-back reflects overwrite semantics in tests.
type recordingDeleter struct{ calls int }

func (d *recordingDeleter) DeletePrefix(ctx context.Context, bucket, prefix string) error {
	d.calls++
	os.RemoveAll(filepath.Join(bucket, prefix)) // local "bucket" == base dir
	return nil
}

func newLoadWriter(t *testing.T, del *recordingDeleter) (*Writer, *catalog.Service) {
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
	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, "_managed", "sales"), 0755)
	w := NewWriter(eng, cat, store, noopCache{}, localStorage{dir: base}, del)
	w.localBase = base
	return w, cat
}

func TestRunLoad_AppendThenOverwrite(t *testing.T) {
	del := &recordingDeleter{}
	w, cat := newLoadWriter(t, del)
	ctx := context.Background()
	if err := cat.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	src1 := filepath.Join(dir, "a.csv")
	_ = os.WriteFile(src1, []byte("id,v\n1,x\n2,y\n"), 0644)

	// Append into a fresh managed table.
	tbl, err := w.RunLoad(ctx, "p1", LoadRequest{
		Source: src1, Into: "sales.events", Format: "csv", Mode: "append",
	}, "", "", "")
	if err != nil {
		t.Fatalf("RunLoad append: %v", err)
	}
	if tbl.Stats.RowCount != 2 {
		t.Fatalf("expected 2 rows after first append, got %d", tbl.Stats.RowCount)
	}

	// Append a second file: total becomes 4.
	src2 := filepath.Join(dir, "b.csv")
	_ = os.WriteFile(src2, []byte("id,v\n3,z\n4,w\n"), 0644)
	tbl, err = w.RunLoad(ctx, "p1", LoadRequest{
		Source: src2, Into: "sales.events", Format: "csv", Mode: "append",
	}, "", "", "")
	if err != nil {
		t.Fatalf("RunLoad append 2: %v", err)
	}
	if tbl.Stats.RowCount != 4 {
		t.Fatalf("expected 4 rows after second append, got %d", tbl.Stats.RowCount)
	}
	if del.calls != 0 {
		t.Fatalf("append must not delete, got %d deletes", del.calls)
	}

	// Overwrite with a single 1-row file: total becomes 1, prefix cleared once.
	src3 := filepath.Join(dir, "c.csv")
	_ = os.WriteFile(src3, []byte("id,v\n9,q\n"), 0644)
	tbl, err = w.RunLoad(ctx, "p1", LoadRequest{
		Source: src3, Into: "sales.events", Format: "csv", Mode: "overwrite",
	}, "", "", "")
	if err != nil {
		t.Fatalf("RunLoad overwrite: %v", err)
	}
	if del.calls != 1 {
		t.Fatalf("overwrite must delete prefix once, got %d", del.calls)
	}
	if tbl.Stats.RowCount != 1 {
		t.Fatalf("expected 1 row after overwrite, got %d", tbl.Stats.RowCount)
	}
}

func TestRunLoad_Partitioned(t *testing.T) {
	del := &recordingDeleter{}
	w, cat := newLoadWriter(t, del)
	ctx := context.Background()
	if err := cat.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "p.csv")
	_ = os.WriteFile(src, []byte("dt,v\n2026-01-01,a\n2026-01-01,b\n2026-01-02,c\n"), 0644)

	tbl, err := w.RunLoad(ctx, "p1", LoadRequest{
		Source: src, Into: "sales.part", Format: "csv", Mode: "overwrite",
		PartitionBy: []string{"dt"},
	}, "", "", "")
	if err != nil {
		t.Fatalf("RunLoad partitioned: %v", err)
	}
	if len(tbl.PartitionColumns) != 1 || tbl.PartitionColumns[0] != "dt" {
		t.Fatalf("expected partition dt, got %+v", tbl.PartitionColumns)
	}
	if tbl.Stats.RowCount != 3 {
		t.Fatalf("expected 3 rows, got %d", tbl.Stats.RowCount)
	}
}
