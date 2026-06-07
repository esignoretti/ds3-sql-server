package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// NormalizeSQL produces a stable cache-normalization of a SQL string: trim,
// lowercase, and collapse all runs of whitespace to a single space.
func NormalizeSQL(sql string) string {
	return strings.ToLower(strings.Join(strings.Fields(sql), " "))
}

// CacheKey hashes the normalized SQL together with the project and the sorted
// referenced-table data versions. Any version change yields a new key.
func CacheKey(projectID, sql string, versions map[string]int64) string {
	fqns := make([]string, 0, len(versions))
	for fqn := range versions {
		fqns = append(fqns, fqn)
	}
	sort.Strings(fqns)
	h := sha256.New()
	h.Write([]byte(projectID))
	h.Write([]byte{0})
	h.Write([]byte(NormalizeSQL(sql)))
	for _, fqn := range fqns {
		h.Write([]byte{0})
		h.Write([]byte(fqn))
		h.Write([]byte{'='})
		h.Write([]byte(strconv.FormatInt(versions[fqn], 10)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Blobstore stores and retrieves serialized result payloads by key.
type Blobstore interface {
	Write(key string, data []byte) (location string, err error)
	Read(location string) ([]byte, error)
	Delete(location string) error
}

// DirBlobstore is a filesystem-backed Blobstore (used in tests and Phase 2).
type DirBlobstore struct{ dir string }

func NewDirBlobstore(dir string) *DirBlobstore { return &DirBlobstore{dir: dir} }

func (b *DirBlobstore) Write(key string, data []byte) (string, error) {
	if err := os.MkdirAll(b.dir, 0755); err != nil {
		return "", err
	}
	p := filepath.Join(b.dir, key+".json")
	if err := os.WriteFile(p, data, 0644); err != nil {
		return "", err
	}
	return p, nil
}

func (b *DirBlobstore) Read(location string) ([]byte, error) { return os.ReadFile(location) }
func (b *DirBlobstore) Delete(location string) error {
	err := os.Remove(location)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

type cacheStore interface {
	PutCacheEntry(ctx context.Context, e *metastore.CacheEntry) error
	LookupCacheEntry(ctx context.Context, key string) (*metastore.CacheEntry, error)
	DeleteCacheEntry(ctx context.Context, key string) error
	ListCacheEntries(ctx context.Context) ([]*metastore.CacheEntry, error)
}

type ResultCacheOpts struct {
	TTL      time.Duration
	MaxBytes int64
}

// ResultCache indexes cached query results in the metastore and stores payloads
// in a Blobstore. Eviction is TTL + total-size LRU.
type ResultCache struct {
	store cacheStore
	blobs Blobstore
	opts  ResultCacheOpts
}

func NewResultCache(store cacheStore, blobs Blobstore, opts ResultCacheOpts) *ResultCache {
	return &ResultCache{store: store, blobs: blobs, opts: opts}
}

// Get returns a cached result on hit. Misses (including TTL-expired entries)
// return ok=false. On hit, LastAccessAt is refreshed (LRU bookkeeping).
func (c *ResultCache) Get(ctx context.Context, projectID, sql string, versions map[string]int64) (*query.Result, bool) {
	key := CacheKey(projectID, sql, versions)
	e, err := c.store.LookupCacheEntry(ctx, key)
	if err != nil {
		return nil, false
	}
	if c.opts.TTL > 0 && time.Since(e.CreatedAt) > c.opts.TTL {
		_ = c.store.DeleteCacheEntry(ctx, key)
		_ = c.blobs.Delete(e.Location)
		return nil, false
	}
	data, err := c.blobs.Read(e.Location)
	if err != nil {
		_ = c.store.DeleteCacheEntry(ctx, key)
		return nil, false
	}
	var res query.Result
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, false
	}
	e.LastAccessAt = time.Now().UTC()
	_ = c.store.PutCacheEntry(ctx, e)
	return &res, true
}

// Put serializes the result, stores it, and indexes it. After insertion it runs
// total-size LRU eviction.
func (c *ResultCache) Put(ctx context.Context, projectID, sql string, versions map[string]int64, res *query.Result) error {
	key := CacheKey(projectID, sql, versions)
	data, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	loc, err := c.blobs.Write(key, data)
	if err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	tv, _ := json.Marshal(versions)
	now := time.Now().UTC()
	if err := c.store.PutCacheEntry(ctx, &metastore.CacheEntry{
		Key:           key,
		ProjectID:     projectID,
		SQLNorm:       NormalizeSQL(sql),
		TableVersions: string(tv),
		Location:      loc,
		SizeBytes:     int64(len(data)),
		CreatedAt:     now,
		LastAccessAt:  now,
	}); err != nil {
		return fmt.Errorf("index entry: %w", err)
	}
	return c.evictLRU(ctx)
}

// evictLRU removes least-recently-accessed entries until total size is under the
// cap. ListCacheEntries returns rows ordered by last_access_at ASC (oldest
// first), so we delete from the front while over budget.
func (c *ResultCache) evictLRU(ctx context.Context) error {
	if c.opts.MaxBytes <= 0 {
		return nil
	}
	entries, err := c.store.ListCacheEntries(ctx)
	if err != nil {
		return err
	}
	var total int64
	for _, e := range entries {
		total += e.SizeBytes
	}
	for _, e := range entries {
		if total <= c.opts.MaxBytes {
			break
		}
		_ = c.blobs.Delete(e.Location)
		_ = c.store.DeleteCacheEntry(ctx, e.Key)
		total -= e.SizeBytes
	}
	return nil
}

// RawExec runs a query without caching, given the resolved referenced-table
// versions. It is the function the cache wraps.
type RawExec func(ctx context.Context, projectID, sql, accessKey, secretKey, endpoint string, versions map[string]int64) *query.Result

// VersionSource returns the referenced-table data versions for a SQL string,
// keyed by fully-qualified name (projectID/dataset/table).
type VersionSource func(ctx context.Context, projectID, sql string) (map[string]int64, error)

// CachingExecutor places the result cache in front of a RawExec.
type CachingExecutor struct {
	cache    *ResultCache
	exec     RawExec
	versions VersionSource
}

func NewCachingExecutor(cache *ResultCache, exec RawExec, versions VersionSource) *CachingExecutor {
	return &CachingExecutor{cache: cache, exec: exec, versions: versions}
}

// Run executes with caching. Errors from version resolution or cache storage are
// non-fatal: on any cache-path failure it falls back to a direct execution.
func (c *CachingExecutor) Run(ctx context.Context, projectID, sql, accessKey, secretKey, endpoint string) *query.Result {
	versions, err := c.versions(ctx, projectID, sql)
	if err != nil {
		return c.exec(ctx, projectID, sql, accessKey, secretKey, endpoint, nil)
	}
	if hit, ok := c.cache.Get(ctx, projectID, sql, versions); ok {
		return hit
	}
	res := c.exec(ctx, projectID, sql, accessKey, secretKey, endpoint, versions)
	if res.Error == "" {
		_ = c.cache.Put(ctx, projectID, sql, versions, res)
	}
	return res
}
