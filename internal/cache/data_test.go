package cache

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dirObjectStore simulates an object store backed by a local directory:
// "bucket/key" maps to <root>/bucket/key.
type dirObjectStore struct{ root string }

func (s *dirObjectStore) Get(ctx context.Context, bucket, key string) ([]byte, string, error) {
	p := filepath.Join(s.root, bucket, key)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	// Use size as a cheap stand-in etag for the test.
	return data, etagOf(data), nil
}

func writeObject(t *testing.T, root, bucket, key, content string) {
	t.Helper()
	p := filepath.Join(root, bucket, key)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDataCache_CopiesOnMissAndHits(t *testing.T) {
	srcRoot := t.TempDir()
	writeObject(t, srcRoot, "cold", "data/a.parquet", "PARQUET-A")
	os := &dirObjectStore{root: srcRoot}
	dc := NewDataCache(os, t.TempDir(), 1<<20)
	ctx := context.Background()

	local1, hit1, err := dc.Ensure(ctx, "cold", "data/a.parquet")
	if err != nil {
		t.Fatalf("Ensure miss: %v", err)
	}
	if hit1 {
		t.Fatal("first Ensure should be a miss")
	}
	if b, _ := readFile(local1); b != "PARQUET-A" {
		t.Fatalf("local copy content = %q", b)
	}

	local2, hit2, err := dc.Ensure(ctx, "cold", "data/a.parquet")
	if err != nil {
		t.Fatalf("Ensure hit: %v", err)
	}
	if !hit2 {
		t.Fatal("second Ensure should be a hit")
	}
	if local1 != local2 {
		t.Fatalf("cache path changed between calls: %q vs %q", local1, local2)
	}
}

func TestDataCache_LRUEviction(t *testing.T) {
	srcRoot := t.TempDir()
	writeObject(t, srcRoot, "cold", "a", "AAAAAAAAAA")
	writeObject(t, srcRoot, "cold", "b", "BBBBBBBBBB")
	writeObject(t, srcRoot, "cold", "c", "CCCCCCCCCC")
	store := &dirObjectStore{root: srcRoot}
	dc := NewDataCache(store, t.TempDir(), 25) // ~25 bytes: holds 2 of the 10-byte objects
	ctx := context.Background()

	_, _, _ = dc.Ensure(ctx, "cold", "a")
	_, _, _ = dc.Ensure(ctx, "cold", "b")
	_, _, _ = dc.Ensure(ctx, "cold", "c") // should evict "a"

	if dc.TotalBytes() > 25 {
		t.Fatalf("cache over cap: %d", dc.TotalBytes())
	}
}

func TestDataCache_RewriteBindings_HDDOnly(t *testing.T) {
	srcRoot := t.TempDir()
	writeObject(t, srcRoot, "cold", "orders.parquet", "DATA")
	store := &dirObjectStore{root: srcRoot}
	dc := NewDataCache(store, t.TempDir(), 1<<20)
	ctx := context.Background()

	bindings := []Binding{
		{Schema: "sales", Name: "orders", ReaderSQL: "read_parquet('s3://cold/orders.parquet')", StorageClass: "hdd", Objects: []ObjectRef{{Bucket: "cold", Key: "orders.parquet"}}},
		{Schema: "sales", Name: "fast", ReaderSQL: "read_parquet('s3://fast/f.parquet')", StorageClass: "ssd", Objects: []ObjectRef{{Bucket: "fast", Key: "f.parquet"}}},
	}
	out, err := dc.RewriteBindings(ctx, bindings)
	if err != nil {
		t.Fatalf("RewriteBindings: %v", err)
	}
	// HDD binding now points at a local file path.
	if strings.Contains(out[0].ReaderSQL, "s3://cold") {
		t.Fatalf("hdd binding not rewritten: %q", out[0].ReaderSQL)
	}
	if !strings.Contains(out[0].ReaderSQL, "read_parquet('") {
		t.Fatalf("hdd binding lost reader wrapper: %q", out[0].ReaderSQL)
	}
	// SSD binding is unchanged (bypass).
	if out[1].ReaderSQL != "read_parquet('s3://fast/f.parquet')" {
		t.Fatalf("ssd binding should bypass cache, got %q", out[1].ReaderSQL)
	}
}

// helpers
func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
