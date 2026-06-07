package planner

import (
	"reflect"
	"sort"
	"testing"
)

func parts() []Partition {
	return []Partition{
		{Values: map[string]string{"dt": "2026-06-05"}, Location: "s3://b/dt=2026-06-05/"},
		{Values: map[string]string{"dt": "2026-06-06"}, Location: "s3://b/dt=2026-06-06/"},
		{Values: map[string]string{"dt": "2026-06-07"}, Location: "s3://b/dt=2026-06-07/"},
	}
}

func locs(ps []Partition) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Location
	}
	sort.Strings(out)
	return out
}

func TestParseWhere_Equality(t *testing.T) {
	preds := ParseWhere("SELECT * FROM sales.orders WHERE dt = '2026-06-06'", []string{"dt"})
	if len(preds) != 1 {
		t.Fatalf("expected 1 predicate, got %d (%+v)", len(preds), preds)
	}
	p := preds[0]
	if p.Column != "dt" || p.Op != OpEq || len(p.Values) != 1 || p.Values[0] != "2026-06-06" {
		t.Fatalf("unexpected predicate: %+v", p)
	}
}

func TestPrune_Equality(t *testing.T) {
	got := Prune("SELECT * FROM t WHERE dt = '2026-06-06'", []string{"dt"}, parts())
	if want := []string{"s3://b/dt=2026-06-06/"}; !reflect.DeepEqual(locs(got), want) {
		t.Fatalf("got %v want %v", locs(got), want)
	}
}

func TestPrune_In(t *testing.T) {
	got := Prune("SELECT * FROM t WHERE dt IN ('2026-06-05','2026-06-07')", []string{"dt"}, parts())
	want := []string{"s3://b/dt=2026-06-05/", "s3://b/dt=2026-06-07/"}
	if !reflect.DeepEqual(locs(got), want) {
		t.Fatalf("got %v want %v", locs(got), want)
	}
}

func TestPrune_Range(t *testing.T) {
	got := Prune("SELECT * FROM t WHERE dt >= '2026-06-06'", []string{"dt"}, parts())
	want := []string{"s3://b/dt=2026-06-06/", "s3://b/dt=2026-06-07/"}
	if !reflect.DeepEqual(locs(got), want) {
		t.Fatalf("got %v want %v", locs(got), want)
	}
}

func TestPrune_RangeBothSides(t *testing.T) {
	got := Prune("SELECT * FROM t WHERE dt > '2026-06-05' AND dt < '2026-06-07'", []string{"dt"}, parts())
	want := []string{"s3://b/dt=2026-06-06/"}
	if !reflect.DeepEqual(locs(got), want) {
		t.Fatalf("got %v want %v", locs(got), want)
	}
}

func TestPrune_NoPredicate_ReturnsAll(t *testing.T) {
	got := Prune("SELECT * FROM t", []string{"dt"}, parts())
	if len(got) != 3 {
		t.Fatalf("expected all 3 partitions, got %d", len(got))
	}
}

func TestPrune_UnsupportedOr_ReturnsAll(t *testing.T) {
	// OR is unsupported -> conservative full scan.
	got := Prune("SELECT * FROM t WHERE dt = '2026-06-05' OR dt = '2026-06-07'", []string{"dt"}, parts())
	if len(got) != 3 {
		t.Fatalf("expected conservative all 3, got %d", len(got))
	}
}

func TestPrune_NonPartitionPredicate_ReturnsAll(t *testing.T) {
	got := Prune("SELECT * FROM t WHERE total > 100", []string{"dt"}, parts())
	if len(got) != 3 {
		t.Fatalf("expected all 3, got %d", len(got))
	}
}

func TestPrune_MultiColumnAnd(t *testing.T) {
	ps := []Partition{
		{Values: map[string]string{"dt": "2026-06-06", "region": "eu"}, Location: "a"},
		{Values: map[string]string{"dt": "2026-06-06", "region": "us"}, Location: "b"},
		{Values: map[string]string{"dt": "2026-06-07", "region": "eu"}, Location: "c"},
	}
	got := Prune("SELECT * FROM t WHERE dt = '2026-06-06' AND region = 'eu'", []string{"dt", "region"}, ps)
	if want := []string{"a"}; !reflect.DeepEqual(locs(got), want) {
		t.Fatalf("got %v want %v", locs(got), want)
	}
}

func TestReaderLocations(t *testing.T) {
	got := ReaderLocations([]Partition{{Location: "s3://b/p1/"}, {Location: "s3://b/p2/"}}, "parquet")
	want := "read_parquet(['s3://b/p1/', 's3://b/p2/'])"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
