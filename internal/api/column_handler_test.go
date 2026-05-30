package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/column"
)

func TestSaveFixedWidthConfig(t *testing.T) {
	dir, err := os.MkdirTemp("", "ds3sql-api-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := column.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	h := NewColumnHandler(store)

	s0 := 0
	e0 := 16
	s1 := 17

	cfg := column.ColumnConfig{
		Bucket:  "test",
		Pattern: "*.dat",
		Mode:    "fixed_width",
		Columns: []column.ColumnDef{
			{Name: "ts", Type: "VARCHAR", Start: &s0, End: &e0},
			{Name: "msg", Type: "VARCHAR", Start: &s1},
		},
	}

	body, _ := json.Marshal(cfg)
	req := httptest.NewRequest("POST", "/convert/columns", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.SaveConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify it was saved
	loaded, err := store.Get("test", "*.dat")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mode != "fixed_width" {
		t.Fatalf("expected mode fixed_width, got %q", loaded.Mode)
	}
	if *loaded.Columns[0].Start != 0 {
		t.Fatalf("expected start 0, got %d", *loaded.Columns[0].Start)
	}
}
