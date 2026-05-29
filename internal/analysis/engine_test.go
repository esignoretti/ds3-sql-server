package analysis

import (
	"database/sql"
	"testing"

	_ "github.com/marcboeker/go-duckdb"
)

func TestAnalyzeNumericColumn(t *testing.T) {
	pool := make(chan *sql.DB, 1)
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	pool <- db

	engine := NewEngine(pool)
	result := engine.Analyze(AnalysisRequest{
		Columns: []ColumnInfo{{Name: "age", Type: "INTEGER"}},
		Rows:    [][]any{{25}, {30}, {35}, {40}, {45}, {50}, {55}, {60}, {65}, {70}},
	})
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	col, ok := result.Columns["age"]
	if !ok {
		t.Fatal("missing column 'age'")
	}
	if col.Type != "numeric" {
		t.Fatalf("expected type 'numeric', got %s", col.Type)
	}
	stats, ok := col.Stats.(map[string]any)
	if !ok {
		t.Fatal("stats not a map")
	}
	if stats["count"] != float64(10) {
		t.Fatalf("expected count=10, got %v", stats["count"])
	}
	if stats["min"] != float64(25) {
		t.Fatalf("expected min=25, got %v", stats["min"])
	}
	if stats["max"] != float64(70) {
		t.Fatalf("expected max=70, got %v", stats["max"])
	}
	if col.Histogram == nil || len(col.Histogram) == 0 {
		t.Fatal("expected histogram bins")
	}
}
