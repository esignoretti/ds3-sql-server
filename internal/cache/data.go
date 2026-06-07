package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ObjectStore fetches whole objects from the backing store (S3/DS3 in
// production; a local-dir fake in tests). Get returns the bytes and an etag.
type ObjectStore interface {
	Get(ctx context.Context, bucket, key string) (data []byte, etag string, err error)
}

// ObjectRef identifies one object backing a table binding.
type ObjectRef struct {
	Bucket string
	Key    string
}

// Binding is the worker-side view of a table to execute over. It mirrors
// query.ViewBinding plus the storage class and the concrete objects so the data
// cache can decide whether to localize the data.
type Binding struct {
	Schema       string
	Name         string
	ReaderSQL    string
	StorageClass string
	Objects      []ObjectRef
}

// etagOf is a content-hash etag used by the local-dir test object store and as a
// fallback when the store does not provide one.
func etagOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

type cacheItem struct {
	path     string
	size     int64
	accessed time.Time
}

// DataCache copies HDD-class objects onto local SSD (read-through), evicting LRU
// when over the size cap, and rewrites reader SQL to point at the local copies.
type DataCache struct {
	store    ObjectStore
	dir      string
	maxBytes int64

	mu    sync.Mutex
	items map[string]*cacheItem // cacheKey -> item
	total int64
}

func NewDataCache(store ObjectStore, dir string, maxBytes int64) *DataCache {
	return &DataCache{
		store:    store,
		dir:      dir,
		maxBytes: maxBytes,
		items:    make(map[string]*cacheItem),
	}
}

func cacheKeyFor(bucket, key, etag string) string {
	sum := sha256.Sum256([]byte(bucket + "/" + key + "@" + etag))
	return hex.EncodeToString(sum[:])
}

// Ensure makes sure the object is present on local SSD and returns its local
// path. The bool reports whether it was already cached (a hit).
func (c *DataCache) Ensure(ctx context.Context, bucket, key string) (string, bool, error) {
	data, etag, err := c.store.Get(ctx, bucket, key)
	if err != nil {
		return "", false, fmt.Errorf("fetch %s/%s: %w", bucket, key, err)
	}
	ck := cacheKeyFor(bucket, key, etag)

	c.mu.Lock()
	if it, ok := c.items[ck]; ok {
		it.accessed = time.Now()
		path := it.path
		c.mu.Unlock()
		return path, true, nil
	}
	c.mu.Unlock()

	if err := os.MkdirAll(c.dir, 0755); err != nil {
		return "", false, err
	}
	path := filepath.Join(c.dir, ck+filepath.Ext(key))
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", false, err
	}

	c.mu.Lock()
	c.items[ck] = &cacheItem{path: path, size: int64(len(data)), accessed: time.Now()}
	c.total += int64(len(data))
	c.evictLocked()
	c.mu.Unlock()
	return path, false, nil
}

// evictLocked removes least-recently-used items until total <= maxBytes. The
// caller must hold c.mu.
func (c *DataCache) evictLocked() {
	if c.maxBytes <= 0 || c.total <= c.maxBytes {
		return
	}
	type kv struct {
		key string
		it  *cacheItem
	}
	all := make([]kv, 0, len(c.items))
	for k, it := range c.items {
		all = append(all, kv{k, it})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].it.accessed.Before(all[j].it.accessed) })
	for _, e := range all {
		if c.total <= c.maxBytes {
			break
		}
		_ = os.Remove(e.it.path)
		c.total -= e.it.size
		delete(c.items, e.key)
	}
}

// TotalBytes reports the current cache footprint (for tests/metrics).
func (c *DataCache) TotalBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// RewriteBindings localizes HDD-class bindings' objects and rewrites their
// ReaderSQL to read the local copies; SSD-class bindings pass through unchanged.
// When a binding has a single object, the reader points at its local file; with
// multiple objects it points at a brace-expanded list. The reader function is
// preserved (read_parquet/read_csv_auto/...) by replacing only the path list.
func (c *DataCache) RewriteBindings(ctx context.Context, bindings []Binding) ([]Binding, error) {
	out := make([]Binding, len(bindings))
	for i, b := range bindings {
		out[i] = b
		if strings.ToLower(b.StorageClass) == "ssd" || len(b.Objects) == 0 {
			continue // SSD tables are already fast: bypass the cache.
		}
		localPaths := make([]string, 0, len(b.Objects))
		for _, obj := range b.Objects {
			p, _, err := c.Ensure(ctx, obj.Bucket, obj.Key)
			if err != nil {
				return nil, err
			}
			localPaths = append(localPaths, p)
		}
		out[i].ReaderSQL = rewriteReader(b.ReaderSQL, localPaths)
	}
	return out, nil
}

// rewriteReader keeps the leading reader function (up to the first single-quoted
// path argument) and substitutes the local path list. For a single file:
//
//	read_parquet('local')      ; for many:  read_parquet(['l1','l2'])
func rewriteReader(reader string, localPaths []string) string {
	open := strings.IndexByte(reader, '(')
	if open < 0 {
		return reader
	}
	fn := reader[:open] // e.g. "read_parquet"
	// Preserve any trailing options after the path argument (e.g. ", delim='\t'")
	// by locating the closing paren of the original call.
	rest := ""
	if close := strings.LastIndexByte(reader, ')'); close > open {
		// Find the end of the first quoted path to capture trailing options.
		firstQuote := strings.IndexByte(reader[open:], '\'')
		if firstQuote >= 0 {
			afterPathQuote := strings.IndexByte(reader[open+firstQuote+1:], '\'')
			if afterPathQuote >= 0 {
				tail := reader[open+firstQuote+1+afterPathQuote+1 : close]
				rest = strings.TrimSpace(tail)
			}
		}
	}
	var pathArg string
	if len(localPaths) == 1 {
		pathArg = "'" + escapePath(localPaths[0]) + "'"
	} else {
		quoted := make([]string, len(localPaths))
		for i, p := range localPaths {
			quoted[i] = "'" + escapePath(p) + "'"
		}
		pathArg = "[" + strings.Join(quoted, ", ") + "]"
	}
	if rest != "" {
		return fn + "(" + pathArg + rest + ")"
	}
	return fn + "(" + pathArg + ")"
}

func escapePath(s string) string { return strings.ReplaceAll(s, "'", "''") }
