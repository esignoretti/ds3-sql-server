package query

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecWrite_CopyToLocalParquet(t *testing.T) {
	e, err := NewEngine(1000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	csv := filepath.Join(t.TempDir(), "src.csv")
	if err := os.WriteFile(csv, []byte("id,total\n1,10\n2,20\n3,30\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	out := filepath.Join(t.TempDir(), "out.parquet")

	bindings := []ViewBinding{{
		Schema:    "sales",
		Name:      "orders",
		ReaderSQL: "read_csv_auto('" + csv + "')",
	}}
	copySQL := "COPY (SELECT * FROM sales.orders WHERE total > 10) TO '" + out + "' (FORMAT PARQUET)"
	if err := e.ExecWrite(copySQL, bindings, "", "", ""); err != nil {
		t.Fatalf("ExecWrite: %v", err)
	}

	// Read the written Parquet back and assert 2 rows survived the filter.
	res := e.QueryView("SELECT count(*) AS c FROM read_parquet('"+out+"')", nil, "", "", "")
	if res.Error != "" {
		t.Fatalf("read back: %s", res.Error)
	}
	if got := res.Rows[0][0]; toInt64(got) != 2 {
		t.Fatalf("expected 2 rows in output, got %v", got)
	}
}

func toInt64(v any) int64 {
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
