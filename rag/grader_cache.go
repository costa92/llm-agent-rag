package rag

// This file implements C-Cache (v1.8.0) — an in-memory LRU cache layer for
// Grader.ScoreRelevance and Grader.ScoreSupport calls. The cache is
// caller-side: it is NOT auto-wired into rag.System or the reflection
// path. Callers compose it explicitly via WrapGrader or NewCachingGrader
// (see commit 4).
//
// Cache key canonicalization mirrors eval.normalize() (lowercase +
// whitespace collapse) so semantically identical (query, hitID, answer)
// triples share a cache slot — the same canonicalization ExactMatch and
// F1Token already apply to gold/candidate strings.
//
// The cache key format is FROZEN as the v1.x cache-key contract; see
// CHANGELOG v1.8.0. Cache files are NOT portable across major versions.
//
// v1.8.0 is pure LRU (cap-based eviction) — no TTL, no disk persistence,
// no chunk-version invalidation. Callers that need invalidation construct
// a fresh cache instance.

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/costa92/llm-agent-rag/store"
)

// Mode constants for GraderCacheKey. They are exported because the mode
// parameter is a free-form string and callers building keys directly
// should not have to spell the literal.
const (
	// GraderCacheModeRelevance tags a ScoreRelevance cache entry.
	GraderCacheModeRelevance = "relevance"
	// GraderCacheModeSupport tags a ScoreSupport cache entry.
	GraderCacheModeSupport = "support"
)

// CacheStats is a point-in-time snapshot of cache counters and current
// size. Counters (Hits/Misses/Evictions) are read atomically; Size is
// read under the cache mutex. The snapshot is NOT transactionally
// consistent across counters vs Size under concurrent load.
//
// Compatibility note: this exported struct may grow additively over
// time, so keyed composite literals are recommended.
type CacheStats struct {
	Hits      int64 // Hits is the cumulative count of Get calls that returned ok=true.
	Misses    int64 // Misses is the cumulative count of Get calls that returned ok=false.
	Evictions int64 // Evictions is the cumulative count of LRU evictions caused by cap overflow.
	Size      int   // Size is the current number of entries in the cache.
}

// GraderCache is the abstract cache contract used by WrapGrader. The two
// shipping implementations are MemoryGraderCache (an LRU) and the nil
// value (which WrapGrader treats as "no cache, return inner unchanged").
//
// Implementations of Get must be safe for concurrent use; Put may assume
// a single caller per key in steady state but must still be safe to call
// from multiple goroutines without data corruption.
type GraderCache interface {
	// Get retrieves a cached ChunkScore. A miss returns the zero ChunkScore
	// and ok=false. Hits/Misses counters should be updated by the cache
	// implementation.
	Get(key string) (ChunkScore, bool)
	// Put stores a ChunkScore under key. Implementations must enforce
	// their own capacity policy (e.g. LRU eviction).
	Put(key string, score ChunkScore)
	// Stats returns a point-in-time snapshot.
	Stats() CacheStats
}

// MemoryGraderCache is a thread-safe in-memory LRU GraderCache. It is the
// default cache implementation shipped with v1.8.0.
type MemoryGraderCache struct {
	mu        sync.Mutex
	cap       int
	order     *list.List // doubly linked list, front = most-recently-used
	byKey     map[string]*list.Element
	hits      atomic.Int64
	misses    atomic.Int64
	evictions atomic.Int64
}

// graderCacheEntry is the *list.Element value held by MemoryGraderCache.
type graderCacheEntry struct {
	key   string
	score ChunkScore
}

// NewMemoryGraderCache returns a MemoryGraderCache with the given
// capacity. When cap <= 0 the default of 1024 is used.
func NewMemoryGraderCache(cap int) *MemoryGraderCache {
	if cap <= 0 {
		cap = 1024
	}
	return &MemoryGraderCache{
		cap:   cap,
		order: list.New(),
		byKey: make(map[string]*list.Element, cap),
	}
}

// Get returns the stored ChunkScore and ok=true on a hit, refreshing the
// entry's LRU recency. Misses return (zero ChunkScore, false).
func (c *MemoryGraderCache) Get(key string) (ChunkScore, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byKey[key]
	if !ok {
		c.misses.Add(1)
		return ChunkScore{}, false
	}
	c.order.MoveToFront(el)
	c.hits.Add(1)
	return el.Value.(*graderCacheEntry).score, true
}

// Put stores score under key, evicting the LRU entry when the cache is
// full. Re-Put of an existing key refreshes its recency and overwrites
// the stored ChunkScore.
func (c *MemoryGraderCache) Put(key string, score ChunkScore) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byKey[key]; ok {
		el.Value.(*graderCacheEntry).score = score
		c.order.MoveToFront(el)
		return
	}
	el := c.order.PushFront(&graderCacheEntry{key: key, score: score})
	c.byKey[key] = el
	for c.order.Len() > c.cap {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		ent := oldest.Value.(*graderCacheEntry)
		c.order.Remove(oldest)
		delete(c.byKey, ent.key)
		c.evictions.Add(1)
	}
}

// Stats returns a point-in-time snapshot of the cache counters and size.
// The snapshot is not transactionally consistent across counters vs Size.
func (c *MemoryGraderCache) Stats() CacheStats {
	c.mu.Lock()
	size := c.order.Len()
	c.mu.Unlock()
	return CacheStats{
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Evictions: c.evictions.Load(),
		Size:      size,
	}
}

// normalizeGraderCache canonicalizes a string for the cache key: lowercase
// + whitespace collapse. This mirrors eval.normalize() (and the v1.3.0
// ExactMatch/F1Token canonicalization) exactly. Duplicated here so the
// rag package keeps zero dependencies on eval — see the v1.8.0 design
// note in CHANGELOG.
func normalizeGraderCache(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// GraderCacheKey returns the canonical SHA-256-hex cache key for a
// (query, hitID, answer, mode) tuple. The mode argument is one of
// GraderCacheModeRelevance or GraderCacheModeSupport. query/hitID/answer
// are passed through normalizeGraderCache (lowercase + whitespace
// collapse) before hashing.
//
// The key format is FROZEN as the v1.x cache-key contract. Cache files
// recorded under this format are NOT portable across major versions of
// llm-agent-rag.
func GraderCacheKey(query, hitID, answer, mode string) string {
	h := sha256.New()
	h.Write([]byte("v1\x00grader\x00"))
	h.Write([]byte(normalizeGraderCache(query)))
	h.Write([]byte{0})
	h.Write([]byte(normalizeGraderCache(hitID)))
	h.Write([]byte{0})
	h.Write([]byte(normalizeGraderCache(answer)))
	h.Write([]byte{0})
	h.Write([]byte(mode))
	return hex.EncodeToString(h.Sum(nil))
}

// cachingGrader is the WrapGrader implementation. It composes any Grader
// with any GraderCache. Cache hits short-circuit the inner Grader; cache
// misses run the inner Grader and cache its result. Failures from the
// inner Grader are NOT cached — a subsequent retry sees a cache miss.
//
// Cache hits BYPASS Observer.OnGenerateUsage and AskOptions.MaxTotalTokens
// budget consumption (since no Generate fires on a hit). This is the
// intended behavior — see CHANGELOG v1.8.0.
type cachingGrader struct {
	inner Grader
	cache GraderCache
}

// WrapGrader composes inner with cache. When cache is nil, WrapGrader
// returns inner unchanged (no allocation, no wrapping). Otherwise every
// ScoreRelevance / ScoreSupport call is keyed by GraderCacheKey using
// the corresponding mode constant.
//
// Cache hits BYPASS the underlying Generate, so neither
// Observer.OnGenerateUsage nor AskOptions.MaxTotalTokens consume budget
// on a hit. This is intentional but worth noting when reading dashboards
// of grader spend. Inner-Grader errors are propagated untouched and NOT
// cached.
//
// Thread-safety: WrapGrader is safe for concurrent use iff `inner` is
// safe for concurrent use. The shipping MemoryGraderCache is safe for
// concurrent use.
func WrapGrader(inner Grader, cache GraderCache) Grader {
	if cache == nil {
		return inner
	}
	return cachingGrader{inner: inner, cache: cache}
}

// NewCachingGrader is a convenience constructor combining
// NewMemoryGraderCache(cap) and WrapGrader. The cap follows the
// MemoryGraderCache rule: cap <= 0 → 1024.
func NewCachingGrader(inner Grader, cap int) Grader {
	return WrapGrader(inner, NewMemoryGraderCache(cap))
}

// ScoreRelevance implements Grader. Cache key uses GraderCacheModeRelevance
// with answer="" (relevance does not depend on answer text).
func (c cachingGrader) ScoreRelevance(ctx context.Context, query string, hit store.Hit) (float64, string, error) {
	key := GraderCacheKey(query, hit.Chunk.ID, "", GraderCacheModeRelevance)
	if cs, ok := c.cache.Get(key); ok {
		return cs.Relevance, cs.Reason, nil
	}
	score, reason, err := c.inner.ScoreRelevance(ctx, query, hit)
	if err != nil {
		return score, reason, err
	}
	c.cache.Put(key, ChunkScore{HitID: hit.Chunk.ID, Relevance: score, Reason: reason})
	return score, reason, nil
}

// ScoreSupport implements Grader. Cache key uses GraderCacheModeSupport
// with query="" (support does not depend on query text).
func (c cachingGrader) ScoreSupport(ctx context.Context, answer string, hit store.Hit) (float64, string, error) {
	key := GraderCacheKey("", hit.Chunk.ID, answer, GraderCacheModeSupport)
	if cs, ok := c.cache.Get(key); ok {
		return cs.Support, cs.Reason, nil
	}
	score, reason, err := c.inner.ScoreSupport(ctx, answer, hit)
	if err != nil {
		return score, reason, err
	}
	c.cache.Put(key, ChunkScore{HitID: hit.Chunk.ID, Support: score, Reason: reason})
	return score, reason, nil
}
