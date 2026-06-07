package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestTableHandler_RegisterListDescribeDrop(t *testing.T) {
	cat := newTestCatalog(t)
	if err := cat.CreateDataset(context.Background(), "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	h := NewTableHandler(cat)

	csv := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(csv, []byte("id,total\n1,10\n2,20\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Register (uses creds params; empty creds are fine for local files).
	body := `{"name":"orders","location":"` + csv + `","format":"csv"}`
	req := httptest.NewRequest("POST", "/datasets/sales/tables", strings.NewReader(body))
	req = withURLParam(req, "dataset", "sales")
	w := httptest.NewRecorder()
	h.RegisterForProject(w, req, "p1", "", "", "")
	if w.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body=%s", w.Code, w.Body.String())
	}

	// List
	req = httptest.NewRequest("GET", "/datasets/sales/tables", nil)
	req = withURLParam(req, "dataset", "sales")
	w = httptest.NewRecorder()
	h.ListForProject(w, req, "p1")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "orders") {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}

	// Describe
	req = httptest.NewRequest("GET", "/datasets/sales/tables/orders", nil)
	req = withURLParam(req, "dataset", "sales")
	req = withURLParam(req, "table", "orders")
	w = httptest.NewRecorder()
	h.DescribeForProject(w, req, "p1")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "row_count") {
		t.Fatalf("describe failed: %d %s", w.Code, w.Body.String())
	}

	// Drop
	req = httptest.NewRequest("DELETE", "/datasets/sales/tables/orders", nil)
	req = withURLParam(req, "dataset", "sales")
	req = withURLParam(req, "table", "orders")
	w = httptest.NewRecorder()
	h.DropForProject(w, req, "p1")
	if w.Code != http.StatusNoContent {
		t.Fatalf("drop status = %d", w.Code)
	}
}

// withURLParam injects a chi URL param into the request context for handler tests.
func withURLParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	if existing := chi.RouteContext(r.Context()); existing != nil {
		rctx = existing
	}
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
