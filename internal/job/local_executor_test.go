package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

func TestLocalExecutor_EndToEnd(t *testing.T) {
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	eng, err := query.NewEngine(1000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatal(err)
	}
	svc := catalog.NewService(store, eng)
	ctx := context.Background()

	csv := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(csv, []byte("id,total\n1,10\n2,20\n3,30\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := svc.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterTable(ctx, catalog.RegisterTableInput{
		ProjectID: "p1", Dataset: "sales", Name: "orders", Location: csv, Format: "csv",
	}, "", "", ""); err != nil {
		t.Fatal(err)
	}

	ex := NewLocalExecutor(svc, eng)
	res := ex.Execute(ctx, ExecRequest{
		SQL: "SELECT sum(total) AS s FROM sales.orders", ProjectID: "p1",
	})
	if res.Error != "" {
		t.Fatalf("execute error: %s", res.Error)
	}
	if got := fmt.Sprintf("%v", res.Rows[0][0]); got != "60" {
		t.Fatalf("expected sum 60, got %s", got)
	}
}
