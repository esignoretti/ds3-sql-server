package metastore

import (
	"context"
	"testing"
	"time"
)

func TestCacheEntryCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e := &CacheEntry{
		Key:           "k1",
		ProjectID:     "p1",
		SQLNorm:       "select count(*) from sales.orders",
		TableVersions: `{"p1/sales/orders":3}`,
		Location:      "/tmp/cache/k1.json",
		SizeBytes:     128,
		CreatedAt:     time.Now().UTC(),
		LastAccessAt:  time.Now().UTC(),
	}
	if err := s.PutCacheEntry(ctx, e); err != nil {
		t.Fatalf("PutCacheEntry: %v", err)
	}

	// Put is an upsert: updating LastAccessAt must not error.
	e.LastAccessAt = time.Now().UTC().Add(time.Minute)
	if err := s.PutCacheEntry(ctx, e); err != nil {
		t.Fatalf("PutCacheEntry upsert: %v", err)
	}

	got, err := s.LookupCacheEntry(ctx, "k1")
	if err != nil {
		t.Fatalf("LookupCacheEntry: %v", err)
	}
	if got.Location != "/tmp/cache/k1.json" || got.SizeBytes != 128 {
		t.Fatalf("round-trip failed: %+v", got)
	}

	if _, err := s.LookupCacheEntry(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	list, err := s.ListCacheEntries(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListCacheEntries: err=%v len=%d", err, len(list))
	}

	if err := s.DeleteCacheEntry(ctx, "k1"); err != nil {
		t.Fatalf("DeleteCacheEntry: %v", err)
	}
	if _, err := s.LookupCacheEntry(ctx, "k1"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteCacheEntriesForTable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Two entries reference sales.orders; one references sales.other.
	put := func(key, versions string) {
		if err := s.PutCacheEntry(ctx, &CacheEntry{
			Key: key, ProjectID: "p1", SQLNorm: "x", TableVersions: versions,
			Location: "/tmp/" + key, SizeBytes: 1, CreatedAt: time.Now().UTC(), LastAccessAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}
	put("a", `{"p1/sales/orders":1}`)
	put("b", `{"p1/sales/orders":2,"p1/sales/lines":1}`)
	put("c", `{"p1/sales/other":1}`)

	if err := s.DeleteCacheEntriesForTable(ctx, "p1", "sales", "orders"); err != nil {
		t.Fatalf("DeleteCacheEntriesForTable: %v", err)
	}

	if _, err := s.LookupCacheEntry(ctx, "a"); err != ErrNotFound {
		t.Fatalf("entry a should be gone")
	}
	if _, err := s.LookupCacheEntry(ctx, "b"); err != ErrNotFound {
		t.Fatalf("entry b should be gone")
	}
	if _, err := s.LookupCacheEntry(ctx, "c"); err != nil {
		t.Fatalf("entry c must survive: %v", err)
	}
}
