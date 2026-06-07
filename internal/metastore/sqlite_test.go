package metastore

import (
	"context"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "meta.db")
	s, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenSQLite_CreatesSchema(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.ListDatasets(context.Background(), "proj-1"); err != nil {
		t.Fatalf("ListDatasets on empty store: %v", err)
	}
}
