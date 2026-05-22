package query

import (
	"testing"
)

func TestErrorOnInvalidSQL(t *testing.T) {
	engine := NewEngine(100, 10, 1024*1024)
	result := engine.Query("SELECT BADSYNTAX", "", "", "")
	if result.Error == "" {
		t.Fatal("expected error for invalid SQL")
	}
	t.Logf("got expected error: %s", result.Error)
}

func TestTrimToMaxRows(t *testing.T) {
	engine := NewEngine(5, 10, 1024*1024)
	result := engine.Query("SELECT * FROM range(100)", "", "", "")
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.RowCount > 5 {
		t.Fatalf("expected at most 5 rows, got %d", result.RowCount)
	}
	t.Logf("got %d rows (limited to 5)", result.RowCount)
}
