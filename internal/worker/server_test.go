package worker

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/query"
)

func newWorkerServer(t *testing.T, secret string) *Server {
	t.Helper()
	eng, err := query.NewEngine(1000, 30, 0, 1, 0, "1GB")
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(eng, secret, nil)
}

func TestWorkerServer_RejectsBadSecret(t *testing.T) {
	s := newWorkerServer(t, "topsecret")
	srv := httptest.NewServer(http.HandlerFunc(s.Execute))
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL, bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-DS3SQL-Worker-Secret", "wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestWorkerServer_ExecutesOverLocalFile(t *testing.T) {
	s := newWorkerServer(t, "topsecret")
	srv := httptest.NewServer(http.HandlerFunc(s.Execute))
	defer srv.Close()

	csv := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(csv, []byte("id,total\n1,10\n2,20\n3,30\n"), 0644); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(ExecuteRequest{
		SQL: "SELECT sum(total) AS s FROM sales.orders",
		Bindings: []WireBinding{{
			Schema: "sales", Name: "orders",
			ReaderSQL: "read_csv_auto('" + csv + "')", StorageClass: "ssd",
		}},
	})
	req, _ := http.NewRequest("POST", srv.URL, bytes.NewReader(body))
	req.Header.Set("X-DS3SQL-Worker-Secret", "topsecret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var res query.Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Fatalf("execute error: %s", res.Error)
	}
	if res.RowCount != 1 {
		t.Fatalf("expected 1 row, got %d", res.RowCount)
	}
}
