package convert

import (
	"testing"
)

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"server.log", "text"},
		{"auth.syslog", "syslog"},
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
