package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

func newTestCatalog(t *testing.T) *catalog.Service {
	t.Helper()
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	eng, err := query.NewEngine(1000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatal(err)
	}
	return catalog.NewService(store, eng)
}

func TestDatasetHandler_CreateAndList(t *testing.T) {
	h := NewDatasetHandler(newTestCatalog(t))

	// Create
	req := httptest.NewRequest("POST", "/datasets", strings.NewReader(`{"name":"sales"}`))
	w := httptest.NewRecorder()
	h.CreateForProject(w, req, "p1")
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", w.Code, w.Body.String())
	}

	// List
	req = httptest.NewRequest("GET", "/datasets", nil)
	w = httptest.NewRecorder()
	h.ListForProject(w, req, "p1")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	var out struct {
		Datasets []struct {
			Name string `json:"name"`
		} `json:"datasets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Datasets) != 1 || out.Datasets[0].Name != "sales" {
		t.Fatalf("unexpected datasets: %+v", out.Datasets)
	}

	// Invalid name -> 400
	req = httptest.NewRequest("POST", "/datasets", strings.NewReader(`{"name":"bad name"}`))
	w = httptest.NewRecorder()
	h.CreateForProject(w, req, "p1")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad name, got %d", w.Code)
	}
}
