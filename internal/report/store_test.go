package report

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDiskStoreCRUD(t *testing.T) {
	dir, err := os.MkdirTemp("", "ds3sql-reports-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	projectID := uuid.New().String()

	r := &Report{
		ID:        uuid.New().String(),
		CreatedAt: time.Now(),
		Title:     "Test Report",
		SQL:       "SELECT * FROM test",
		ProjectID: projectID,
		QueryRows: [][]any{{"hello"}, {"world"}},
		Charts:    []ChartConfig{{ID: "c1", Type: "bar", XColumn: "x"}},
	}

	if err := store.Save(r); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Get(projectID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != "Test Report" {
		t.Fatalf("expected title 'Test Report', got %s", loaded.Title)
	}
	if len(loaded.Charts) != 1 {
		t.Fatalf("expected 1 chart, got %d", len(loaded.Charts))
	}

	list, err := store.List(projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 report in list, got %d", len(list))
	}
	if list[0].RowCount != 2 {
		t.Fatalf("expected row_count 2, got %d", list[0].RowCount)
	}

	if err := store.Delete(projectID, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(projectID, r.ID); err == nil {
		t.Fatal("expected error after delete")
	}
}
