package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
)

func TestCatalogFragmentHandler_Tree(t *testing.T) {
	cat := newTestCatalog(t)
	ctx := context.Background()
	if err := cat.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	csv := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(csv, []byte("id,total\n1,10\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.RegisterTable(ctx, registerOrdersInput(csv), "", "", ""); err != nil {
		t.Fatal(err)
	}

	h := NewCatalogFragmentHandler(cat)
	req := httptest.NewRequest("GET", "/ui/catalog", nil)
	w := httptest.NewRecorder()
	h.TreeForProject(w, req, "p1")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "sales") || !strings.Contains(body, "orders") {
		t.Fatalf("fragment missing dataset/table: %s", body)
	}
	// Must carry the data attributes catalog.js / onclick uses.
	if !strings.Contains(body, `data-dataset="sales"`) || !strings.Contains(body, `data-table="orders"`) {
		t.Fatalf("fragment missing data attributes: %s", body)
	}
	// Must be HTML, not JSON.
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected text/html, got %q", ct)
	}
}

func TestCatalogFragmentHandler_Empty(t *testing.T) {
	cat := newTestCatalog(t)
	h := NewCatalogFragmentHandler(cat)
	req := httptest.NewRequest("GET", "/ui/catalog", nil)
	w := httptest.NewRecorder()
	h.TreeForProject(w, req, "p1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "No datasets") {
		t.Fatalf("expected empty-state text, got %s", w.Body.String())
	}
}

// registerOrdersInput wraps catalog.RegisterTableInput so the test helper
// stays self-contained without importing catalog types twice.
func registerOrdersInput(csv string) catalog.RegisterTableInput {
	return catalog.RegisterTableInput{ProjectID: "p1", Dataset: "sales", Name: "orders", Location: csv, Format: "csv"}
}
