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

func TestFixedWidthRoundtrip(t *testing.T) {
	dir, err := os.MkdirTemp("", "ds3sql-columns-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	s0 := 0
	e0 := 16
	s1 := 17
	e1 := 30
	s2 := 31

	cfg := &ColumnConfig{
		Bucket:  "logs",
		Pattern: "*.dat",
		Mode:    "fixed_width",
		Columns: []ColumnDef{
			{Name: "timestamp", Type: "VARCHAR", Start: &s0, End: &e0},
			{Name: "level", Type: "VARCHAR", Start: &s1, End: &e1},
			{Name: "message", Type: "VARCHAR", Start: &s2},
		},
	}

	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Get("logs", "*.dat")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mode != "fixed_width" {
		t.Fatalf("expected mode fixed_width, got %q", loaded.Mode)
	}
	if len(loaded.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(loaded.Columns))
	}
	if *loaded.Columns[0].Start != 0 || *loaded.Columns[0].End != 16 {
		t.Fatalf("unexpected col0 start/end: %d/%d", *loaded.Columns[0].Start, *loaded.Columns[0].End)
	}
	if loaded.Columns[2].End != nil {
		t.Fatal("expected nil End for last column")
	}

	// Backward compat: configs saved without Mode default to "delimiter"
	oldCfg := &ColumnConfig{
		Bucket:  "old",
		Pattern: "*.log",
		Columns: []ColumnDef{{Name: "line", Type: "VARCHAR"}},
	}
	if err := store.Save(oldCfg); err != nil {
		t.Fatal(err)
	}
	loadedOld, err := store.Get("old", "*.log")
	if err != nil {
		t.Fatal(err)
	}
	if loadedOld.Mode != "" {
		t.Fatalf("expected empty mode for old config, got %q", loadedOld.Mode)
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
