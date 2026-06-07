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
	"github.com/esignoretti/ds3-sql-server/internal/catalog"
)

func TestTableHandler_DropManagedDeletesData(t *testing.T) {
	cat := newTestCatalog(t)
	ctx := context.Background()
	if err := cat.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	csv := filepath.Join(t.TempDir(), "m.csv")
	_ = os.WriteFile(csv, []byte("id\n1\n"), 0644)
	if _, err := cat.RegisterManaged(ctx, catalog.RegisterManagedInput{
		ProjectID: "p1", Dataset: "sales", Name: "m",
		Location: "s3://ds3-fast/_managed/sales/m/", ProbeReader: "read_csv_auto('" + csv + "')",
		StorageClass: "ssd",
	}, "", "", ""); err != nil {
		t.Fatal(err)
	}

	del := &apiFakeDeleter{}
	inv := &apiFakeInvalidator{}
	h := NewTableHandler(cat)

	req := httptest.NewRequest("DELETE", "/datasets/sales/tables/m", nil)
	req = withURLParam(req, "dataset", "sales")
	req = withURLParam(req, "table", "m")
	w := httptest.NewRecorder()
	h.DropWithDeps(w, req, "p1", del, inv, "", "", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("drop status = %d body=%s", w.Code, w.Body.String())
	}
	if len(del.calls) != 1 {
		t.Fatalf("expected managed data deletion, got %d calls", len(del.calls))
	}
}

type apiFakeDeleter struct{ calls []string }

func (d *apiFakeDeleter) DeletePrefix(ctx context.Context, bucket, prefix string) error {
	d.calls = append(d.calls, bucket+"|"+prefix)
	return nil
}

type apiFakeInvalidator struct{ n int }

func (c *apiFakeInvalidator) DeleteCacheEntriesForTable(ctx context.Context, p, d, t string) error {
	c.n++
	return nil
}

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
