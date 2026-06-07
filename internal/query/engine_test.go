package query

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestErrorOnInvalidSQL(t *testing.T) {
	engine, err := NewEngine(100, 10, 1024*1024, 1, 0, "128MB")
	if err != nil {
		t.Fatal(err)
	}
	result := engine.Query("SELECT BADSYNTAX", "", "", "")
	if result.Error == "" {
		t.Fatal("expected error for invalid SQL")
	}
	t.Logf("got expected error: %s", result.Error)
}

func TestTrimToMaxRows(t *testing.T) {
	engine, err := NewEngine(5, 10, 1024*1024, 1, 0, "128MB")
	if err != nil {
		t.Fatal(err)
	}
	result := engine.Query("SELECT * FROM range(100)", "", "", "")
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.RowCount > 5 {
		t.Fatalf("expected at most 5 rows, got %d", result.RowCount)
	}
	t.Logf("got %d rows (limited to 5)", result.RowCount)
}

func TestQueryView_LocalFile(t *testing.T) {
	e, err := NewEngine(1000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	csv := filepath.Join(t.TempDir(), "people.csv")
	if err := os.WriteFile(csv, []byte("id,name\n1,alice\n2,bob\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	bindings := []ViewBinding{{
		Schema:    "sales",
		Name:      "people",
		ReaderSQL: "read_csv_auto('" + csv + "')",
	}}
	// Empty creds: local files don't need S3, applyS3Creds is harmless.
	res := e.QueryView("SELECT count(*) AS c FROM sales.people", bindings, "", "", "")
	if res.Error != "" {
		t.Fatalf("query error: %s", res.Error)
	}
	if res.RowCount != 1 {
		t.Fatalf("expected 1 row, got %d", res.RowCount)
	}
	if got := fmt.Sprintf("%v", res.Rows[0][0]); got != "2" {
		t.Fatalf("expected count 2, got %s", got)
	}
}
