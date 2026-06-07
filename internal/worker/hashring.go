package worker

import (
	"hash/crc32"
	"sort"
	"strconv"
)

// HashRing is a consistent-hash ring over worker endpoints with virtual nodes.
type HashRing struct {
	hashes []uint32          // sorted vnode hashes
	owner  map[uint32]string // vnode hash -> endpoint
}

// NewHashRing builds a ring over endpoints with `replicas` virtual nodes each.
func NewHashRing(endpoints []string, replicas int) *HashRing {
	if replicas < 1 {
		replicas = 1
	}
	r := &HashRing{owner: make(map[uint32]string)}
	for _, ep := range endpoints {
		for i := 0; i < replicas; i++ {
			h := crc32.ChecksumIEEE([]byte(ep + "#" + strconv.Itoa(i)))
			r.hashes = append(r.hashes, h)
			r.owner[h] = ep
		}
	}
	sort.Slice(r.hashes, func(i, j int) bool { return r.hashes[i] < r.hashes[j] })
	return r
}

// Get returns the endpoint owning the given key (the first vnode clockwise from
// the key's hash). Returns "" for an empty ring.
func (r *HashRing) Get(key string) string {
	if len(r.hashes) == 0 {
		return ""
	}
	h := crc32.ChecksumIEEE([]byte(key))
	idx := sort.Search(len(r.hashes), func(i int) bool { return r.hashes[i] >= h })
	if idx == len(r.hashes) {
		idx = 0 // wrap around
	}
	return r.owner[r.hashes[idx]]
}
