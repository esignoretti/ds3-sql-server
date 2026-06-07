package job

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

func TestMetastoreSink_PersistsLifecycle(t *testing.T) {
	store, err := metastore.OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	sink := NewMetastoreSink(store)
	ctx := context.Background()

	j := &Job{ID: "j1", ProjectID: "p1", Type: "query", SQL: "SELECT 1", Status: "queued"}
	if err := sink.Save(ctx, j); err != nil {
		t.Fatalf("Save (create): %v", err)
	}

	j.Status = "done"
	j.Result = &query.Result{RowCount: 7}
	if err := sink.Save(ctx, j); err != nil {
		t.Fatalf("Save (update): %v", err)
	}

	rec, err := store.GetJob(ctx, "j1")
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if rec.Status != "done" || rec.RowCount != 7 || rec.ProjectID != "p1" {
		t.Fatalf("unexpected persisted record: %+v", rec)
	}

	list, err := store.ListJobs(ctx, "p1", 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListJobs: err=%v len=%d", err, len(list))
	}
}
