package column

import (
	"os"
	"testing"
)

func TestStoreCRUD(t *testing.T) {
	dir, err := os.MkdirTemp("", "ds3sql-columns-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &ColumnConfig{
		Bucket:    "test-logs",
		Pattern:   "*.log",
		Delimiter: " ",
		Quote:     "\"",
		Columns:   []ColumnDef{{Name: "ip", Type: "VARCHAR"}, {Name: "request", Type: "VARCHAR"}},
	}

	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Get("test-logs", "*.log")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(loaded.Columns))
	}

	list, err := store.List("test-logs")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 config, got %d", len(list))
	}

	if err := store.Delete("test-logs", "*.log"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("test-logs", "*.log"); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestMatch(t *testing.T) {
	dir, err := os.MkdirTemp("", "ds3sql-columns-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	store.Save(&ColumnConfig{Bucket: "logs", Pattern: "*.log", Delimiter: " "})
	store.Save(&ColumnConfig{Bucket: "logs", Pattern: "apache_*.log", Delimiter: " "})

	cfg := store.Match("logs", "apache_access.log")
	if cfg == nil {
		t.Fatal("expected match")
	}
	if cfg.Pattern != "apache_*.log" {
		t.Fatalf("expected 'apache_*.log', got %s", cfg.Pattern)
	}

	cfg2 := store.Match("logs", "syslog.log")
	if cfg2 == nil {
		t.Fatal("expected match")
	}

	cfg3 := store.Match("other", "test.log")
	if cfg3 != nil {
		t.Fatal("expected no match")
	}
}
