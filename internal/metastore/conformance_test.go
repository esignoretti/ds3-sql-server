package metastore

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// storeFactory creates a fresh, isolated Store for one subtest.
type storeFactory func(t *testing.T) Store

// testStoreConformance runs the full behavioural contract against any Store impl.
func testStoreConformance(t *testing.T, newStore storeFactory) {
	t.Run("DatasetCRUD", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.CreateDataset(ctx, &Dataset{ProjectID: "p1", Name: "sales"}); err != nil {
			t.Fatalf("CreateDataset: %v", err)
		}
		if err := s.CreateDataset(ctx, &Dataset{ProjectID: "p1", Name: "sales"}); err == nil {
			t.Fatal("expected duplicate dataset error")
		}
		got, err := s.GetDataset(ctx, "p1", "sales")
		if err != nil || got.Name != "sales" {
			t.Fatalf("GetDataset: %v %+v", err, got)
		}
		if _, err := s.GetDataset(ctx, "p1", "nope"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
		list, err := s.ListDatasets(ctx, "p1")
		if err != nil || len(list) != 1 {
			t.Fatalf("ListDatasets: %v len=%d", err, len(list))
		}
	})

	t.Run("TableCRUDAndVersion", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		_ = s.CreateDataset(ctx, &Dataset{ProjectID: "p1", Name: "sales"})
		tbl := &Table{
			ProjectID: "p1", Dataset: "sales", Name: "orders", Kind: "external",
			Location: "s3://b/orders/*.parquet", Format: "parquet", StorageClass: "hdd",
			PartitionColumns: []string{"dt"},
			Schema:           []Column{{Name: "id", Type: "BIGINT", Nullable: false}},
			Stats: Stats{RowCount: 3, Partitions: []Partition{
				{Values: map[string]string{"dt": "2026-06-07"}, Location: "s3://b/orders/dt=2026-06-07/", RowCount: 3},
			}},
		}
		if err := s.CreateTable(ctx, tbl); err != nil {
			t.Fatalf("CreateTable: %v", err)
		}
		got, err := s.GetTable(ctx, "p1", "sales", "orders")
		if err != nil {
			t.Fatalf("GetTable: %v", err)
		}
		if len(got.Schema) != 1 || got.Schema[0].Name != "id" {
			t.Fatalf("schema round-trip: %+v", got.Schema)
		}
		if len(got.PartitionColumns) != 1 || got.PartitionColumns[0] != "dt" {
			t.Fatalf("partition cols round-trip: %+v", got.PartitionColumns)
		}
		if len(got.Stats.Partitions) != 1 || got.Stats.Partitions[0].Values["dt"] != "2026-06-07" {
			t.Fatalf("partition stats round-trip: %+v", got.Stats.Partitions)
		}
		v, err := s.BumpDataVersion(ctx, "p1", "sales", "orders")
		if err != nil || v != 2 {
			t.Fatalf("BumpDataVersion: v=%d err=%v", v, err)
		}
		list, err := s.ListTables(ctx, "p1", "sales")
		if err != nil || len(list) != 1 {
			t.Fatalf("ListTables: %v len=%d", err, len(list))
		}
		if err := s.DeleteTable(ctx, "p1", "sales", "orders"); err != nil {
			t.Fatalf("DeleteTable: %v", err)
		}
		if _, err := s.GetTable(ctx, "p1", "sales", "orders"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound after delete, got %v", err)
		}
	})

	t.Run("JobLifecycle", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		j := &JobRecord{ID: "j1", ProjectID: "p1", Type: "query", SQL: "SELECT 1", Status: "running"}
		if err := s.CreateJob(ctx, j); err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		j.Status = "done"
		j.RowCount = 5
		j.FinishedAt = time.Now()
		if err := s.UpdateJob(ctx, j); err != nil {
			t.Fatalf("UpdateJob: %v", err)
		}
		got, err := s.GetJob(ctx, "j1")
		if err != nil || got.Status != "done" || got.RowCount != 5 {
			t.Fatalf("GetJob: %v %+v", err, got)
		}
		list, err := s.ListJobs(ctx, "p1", 10)
		if err != nil || len(list) != 1 {
			t.Fatalf("ListJobs: %v len=%d", err, len(list))
		}
		if _, err := s.GetJob(ctx, "missing"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("CacheIndex", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		e := &CacheEntry{Key: "k1", ProjectID: "p1", SQLNorm: "select 1",
			TableVersions: `{"p1/sales/orders":2}`, Location: "s3://fast/k1", SizeBytes: 100}
		if err := s.PutCacheEntry(ctx, e); err != nil {
			t.Fatalf("PutCacheEntry: %v", err)
		}
		got, err := s.LookupCacheEntry(ctx, "k1")
		if err != nil || got.Location != "s3://fast/k1" {
			t.Fatalf("LookupCacheEntry: %v %+v", err, got)
		}
		all, err := s.ListCacheEntries(ctx)
		if err != nil || len(all) != 1 {
			t.Fatalf("ListCacheEntries: %v len=%d", err, len(all))
		}
		if err := s.DeleteCacheEntriesForTable(ctx, "p1", "sales", "orders"); err != nil {
			t.Fatalf("DeleteCacheEntriesForTable: %v", err)
		}
		if _, err := s.LookupCacheEntry(ctx, "k1"); err != ErrNotFound {
			t.Fatalf("expected entry deleted by table invalidation, got %v", err)
		}
	})

	t.Run("Schedules", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		sc := &Schedule{ID: "s1", ProjectID: "p1", Cron: "0 * * * *",
			SQL: "SELECT 1", NextRunAt: time.Now().Add(-time.Minute)}
		if err := s.CreateSchedule(ctx, sc); err != nil {
			t.Fatalf("CreateSchedule: %v", err)
		}
		due, err := s.GetDueSchedules(ctx, time.Now())
		if err != nil || len(due) != 1 {
			t.Fatalf("GetDueSchedules: %v len=%d", err, len(due))
		}
		if err := s.UpdateScheduleRun(ctx, "s1", time.Now(), true); err != nil {
			t.Fatalf("UpdateScheduleRun: %v", err)
		}
		// Running schedules are no longer due.
		due2, _ := s.GetDueSchedules(ctx, time.Now())
		if len(due2) != 0 {
			t.Fatalf("expected 0 due after marking running, got %d", len(due2))
		}
		got, err := s.GetSchedule(ctx, "s1")
		if err != nil || !got.Running {
			t.Fatalf("GetSchedule: %v %+v", err, got)
		}
		list, err := s.ListSchedules(ctx, "p1")
		if err != nil || len(list) != 1 {
			t.Fatalf("ListSchedules: %v len=%d", err, len(list))
		}
		if err := s.DeleteSchedule(ctx, "s1"); err != nil {
			t.Fatalf("DeleteSchedule: %v", err)
		}
		if _, err := s.GetSchedule(ctx, "s1"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound after delete, got %v", err)
		}
	})
}

func TestSQLiteConformance(t *testing.T) {
	testStoreConformance(t, func(t *testing.T) Store {
		s, err := OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
		if err != nil {
			t.Fatalf("OpenSQLite: %v", err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}
