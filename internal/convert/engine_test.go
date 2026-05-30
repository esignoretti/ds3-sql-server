package convert

import (
	"strings"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/column"
)

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"server.log", "log"},
		{"auth.syslog", "syslog"},
		{"access.log", "log"},
		{"data.json", "json"},
		{"data.jsonl", "json"},
		{"output.txt", "text"},
		{"error.out", "text"},
		{"crash.err", "text"},
	}
	for _, tt := range tests {
		got := detectFormat(tt.filename)
		if got != tt.expected {
			t.Errorf("detectFormat(%q) = %q, want %q", tt.filename, got, tt.expected)
		}
	}
}

func TestBuildFixedWidthSQL(t *testing.T) {
	s0 := 0
	e0 := 16
	s2 := 31

	cfg := &column.ColumnConfig{
		Mode: "fixed_width",
		Columns: []column.ColumnDef{
			{Name: "ts", Type: "VARCHAR", Start: &s0, End: &e0},
			{Name: "msg", Type: "VARCHAR", Start: &s2},
		},
	}

	sql, err := buildFixedWidthSQL(cfg, "s3://bucket/file.dat")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(sql, "read_text") {
		t.Error("expected read_text in SQL")
	}
	if !strings.Contains(sql, `substr(text, 1, 16)`) {
		t.Errorf("expected substr(text, 1, 16) for col0, got: %s", sql)
	}
	if !strings.Contains(sql, `substr(text, 32)`) {
		t.Errorf("expected substr(text, 32) for col1 (no end), got: %s", sql)
	}
	if !strings.Contains(sql, `AS "ts"`) {
		t.Errorf("expected quoted column name ts, got: %s", sql)
	}
	if !strings.Contains(sql, `AS "msg"`) {
		t.Errorf("expected quoted column name msg, got: %s", sql)
	}

	// SQL injection: column name with double quote
	malicious := &column.ColumnConfig{
		Mode: "fixed_width",
		Columns: []column.ColumnDef{
			{Name: `bad"name`, Type: "VARCHAR", Start: &s0},
		},
	}
	sql2, err := buildFixedWidthSQL(malicious, "s3://b/f")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql2, `"bad""name"`) {
		t.Errorf("expected escaped double quote in column name, got: %s", sql2)
	}

	// Empty columns
	empty := &column.ColumnConfig{Mode: "fixed_width"}
	_, err = buildFixedWidthSQL(empty, "s3://b/f")
	if err == nil {
		t.Error("expected error for empty columns")
	}

	// Nil Start defaults to 0
	nilStart := &column.ColumnConfig{
		Mode: "fixed_width",
		Columns: []column.ColumnDef{
			{Name: "x", Type: "VARCHAR"},
		},
	}
	sql3, err := buildFixedWidthSQL(nilStart, "s3://b/f")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql3, `substr(text, 1)`) {
		t.Errorf("expected substr(text, 1) for nil start, got: %s", sql3)
	}
}

func TestConvertibleExt(t *testing.T) {
	if convertibleExt(".parquet") {
		t.Error(".parquet should not be convertible")
	}
	if convertibleExt(".csv") {
		t.Error(".csv should not be convertible")
	}
	if !convertibleExt(".log") {
		t.Error(".log should be convertible")
	}
	if !convertibleExt(".txt") {
		t.Error(".txt should be convertible")
	}
}
