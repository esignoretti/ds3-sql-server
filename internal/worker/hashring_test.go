package worker

import "testing"

func TestHashRing_Deterministic(t *testing.T) {
	r := NewHashRing([]string{"http://w1", "http://w2", "http://w3"}, 50)
	a := r.Get("sales/orders")
	b := r.Get("sales/orders")
	if a != b {
		t.Fatalf("ring not deterministic: %q vs %q", a, b)
	}
	if a == "" {
		t.Fatal("expected a node")
	}
}

func TestHashRing_DistributesAcrossNodes(t *testing.T) {
	nodes := []string{"http://w1", "http://w2", "http://w3"}
	r := NewHashRing(nodes, 100)
	seen := map[string]bool{}
	for i := 0; i < 300; i++ {
		seen[r.Get(string(rune('a'+i%26))+string(rune('0'+i%10))+string(rune(i)))] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected keys to spread across nodes, only saw %d", len(seen))
	}
}

func TestHashRing_StableOnNodeRemoval(t *testing.T) {
	full := NewHashRing([]string{"http://w1", "http://w2", "http://w3"}, 100)
	reduced := NewHashRing([]string{"http://w1", "http://w2"}, 100)
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	moved := 0
	for _, k := range keys {
		if full.Get(k) != reduced.Get(k) {
			moved++
		}
	}
	// Only keys that hashed to w3 should move; not all keys.
	if moved == len(keys) {
		t.Fatalf("removal remapped every key; consistent hashing should localize churn")
	}
}

func TestHashRing_EmptyReturnsEmpty(t *testing.T) {
	r := NewHashRing(nil, 50)
	if r.Get("x") != "" {
		t.Fatal("empty ring must return empty string")
	}
}
