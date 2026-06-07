package metastore

import (
	"encoding/json"
	"testing"
)

func TestStats_PartitionsBackwardCompatible(t *testing.T) {
	// Old payload with no "partitions" key must unmarshal fine.
	var old Stats
	if err := json.Unmarshal([]byte(`{"row_count":5}`), &old); err != nil {
		t.Fatalf("unmarshal old stats: %v", err)
	}
	if old.RowCount != 5 || old.Partitions != nil {
		t.Fatalf("unexpected old stats: %+v", old)
	}

	// New payload round-trips through marshal/unmarshal.
	s := Stats{
		RowCount: 9,
		Partitions: []Partition{{
			Values:   map[string]string{"dt": "2026-06-07"},
			Location: "s3://b/dt=2026-06-07/",
			RowCount: 9,
			Min:      map[string]string{"id": "1"},
			Max:      map[string]string{"id": "9"},
		}},
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Stats
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Partitions) != 1 || got.Partitions[0].Values["dt"] != "2026-06-07" {
		t.Fatalf("partition round-trip failed: %+v", got)
	}
	if got.Partitions[0].Location != "s3://b/dt=2026-06-07/" {
		t.Fatalf("location round-trip failed: %+v", got.Partitions[0])
	}
}
