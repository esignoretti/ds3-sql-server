package metastore

import (
	"os"
	"testing"
	"time"
)

// newPostgresStore returns an isolated Postgres-backed Store, or skips when no
// test DSN is configured. Each invocation drops and recreates the public tables
// so subtests do not interfere.
func newPostgresStore(t *testing.T) Store {
	dsn := os.Getenv("DS3SQL_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("DS3SQL_TEST_POSTGRES_DSN not set; skipping Postgres conformance")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("OpenPostgres: %v", err)
	}
	// Clean slate: truncate all tables this store owns.
	for _, tbl := range []string{"datasets", "tables", "jobs", "cache_index", "schedules"} {
		if _, err := s.db.Exec("TRUNCATE TABLE " + tbl); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPostgresConformance(t *testing.T) {
	testStoreConformance(t, func(t *testing.T) Store {
		return newPostgresStore(t)
	})
}

// TestNullTime exercises a pure helper with no live DB.
func TestNullTime(t *testing.T) {
	if nullTime(time.Time{}) != nil {
		t.Fatal("zero time should map to nil")
	}
	now := time.Now()
	if v, ok := nullTime(now).(time.Time); !ok || v.IsZero() {
		t.Fatalf("non-zero time should map to a time.Time, got %T", nullTime(now))
	}
}
