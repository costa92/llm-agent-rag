package eval

// This file implements C-Cache (v1.8.0) — the eval-side mirror of
// rag.MemoryGraderCache. It provides an in-memory LRU cache layer for
// Judge.Judge calls so callers that retry an answer through TriadEvaluator
// / AnswerBenchmark / agentic.CorrectiveAsker do not pay the LLM-judge
// cost twice on identical (query, answer, context) tuples.
//
// Like the rag-side cache, this is caller-side: it is NOT auto-wired into
// AnswerBenchmark or TriadEvaluator. Callers compose it via WrapJudge or
// NewCachingJudge.
//
// Key canonicalization uses the same package-local normalize() helper as
// ExactMatch and F1Token — lowercase + whitespace collapse. The key
// format is FROZEN as the v1.x cache-key contract; see CHANGELOG v1.8.0.
//
// Stats are returned via rag.CacheStats — there is intentionally no
// parallel struct in the eval package, so callers that read
// MemoryGraderCache.Stats() and MemoryJudgeCache.Stats() side by side
// can share a single result type.

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/costa92/llm-agent-rag/rag"
)

// JudgeCache is the abstract cache contract used by WrapJudge. The two
// shipping implementations are MemoryJudgeCache (an LRU) and the nil
// value (which WrapJudge treats as "no cache, return inner unchanged").
//
// Implementations of Get must be safe for concurrent use; Put may assume
// a single caller per key in steady state but must still be safe to call
// from multiple goroutines without data corruption.
type JudgeCache interface {
	// Get retrieves a cached Judgement. A miss returns the zero Judgement
	// and ok=false.
	Get(key string) (Judgement, bool)
	// Put stores a Judgement under key. Implementations must enforce
	// their own capacity policy (e.g. LRU eviction).
	Put(key string, j Judgement)
	// Stats returns a point-in-time snapshot. Reuses rag.CacheStats so
	// callers reading Grader-cache and Judge-cache stats side by side
	// share a single struct shape.
	Stats() rag.CacheStats
}

// MemoryJudgeCache is a thread-safe in-memory LRU JudgeCache. Mirror of
// rag.MemoryGraderCache; the two implementations share their LRU shape
// but the value types differ (Judgement vs ChunkScore).
type MemoryJudgeCache struct {
	mu        sync.Mutex
	cap       int
	order     *list.List
	byKey     map[string]*list.Element
	hits      atomic.Int64
	misses    atomic.Int64
	evictions atomic.Int64
}

// judgeCacheEntry is the *list.Element value held by MemoryJudgeCache.
type judgeCacheEntry struct {
	key string
	j   Judgement
}

// NewMemoryJudgeCache returns a MemoryJudgeCache with the given capacity.
// When cap <= 0 the default of 1024 is used (mirror of
// rag.NewMemoryGraderCache).
func NewMemoryJudgeCache(cap int) *MemoryJudgeCache {
	if cap <= 0 {
		cap = 1024
	}
	return &MemoryJudgeCache{
		cap:   cap,
		order: list.New(),
		byKey: make(map[string]*list.Element, cap),
	}
}

// Get returns the stored Judgement and ok=true on a hit, refreshing LRU
// recency. Misses return (zero Judgement, false).
func (c *MemoryJudgeCache) Get(key string) (Judgement, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byKey[key]
	if !ok {
		c.misses.Add(1)
		return Judgement{}, false
	}
	c.order.MoveToFront(el)
	c.hits.Add(1)
	return el.Value.(*judgeCacheEntry).j, true
}

// Put stores j under key, evicting the LRU entry when over cap. Re-Put of
// an existing key refreshes recency and overwrites the value.
func (c *MemoryJudgeCache) Put(key string, j Judgement) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byKey[key]; ok {
		el.Value.(*judgeCacheEntry).j = j
		c.order.MoveToFront(el)
		return
	}
	el := c.order.PushFront(&judgeCacheEntry{key: key, j: j})
	c.byKey[key] = el
	for c.order.Len() > c.cap {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		ent := oldest.Value.(*judgeCacheEntry)
		c.order.Remove(oldest)
		delete(c.byKey, ent.key)
		c.evictions.Add(1)
	}
}

// Stats returns a point-in-time snapshot. Not transactionally consistent
// across counters vs Size under concurrent load.
func (c *MemoryJudgeCache) Stats() rag.CacheStats {
	c.mu.Lock()
	size := c.order.Len()
	c.mu.Unlock()
	return rag.CacheStats{
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Evictions: c.evictions.Load(),
		Size:      size,
	}
}

// normalize is a 1-line duplicate of benchmark_score.go's package-local
// normalize() — used as the JudgeCacheKey canonicalization step. Kept
// inline here so the cache and the score helpers share canonicalization
// without an extra cross-file dependency.
//
// We do NOT call benchmark_score.go's normalize() directly to keep this
// file self-contained for godoc readers — the symbol resolves to the
// same package function either way.

// JudgeCacheKey returns the canonical SHA-256-hex cache key for a
// (query, answer, context) tuple. query / answer / each passage in
// context are passed through normalize() (lowercase + whitespace
// collapse) before hashing.
//
// Context order is part of the key: ["a","b"] and ["b","a"] produce
// distinct keys. nil and empty context produce the same key.
//
// The format is FROZEN as the v1.x cache-key contract — cache files
// recorded under this format are NOT portable across major versions.
func JudgeCacheKey(query, answer string, context []string) string {
	h := sha256.New()
	h.Write([]byte("v1\x00judge\x00"))
	h.Write([]byte(normalize(query)))
	h.Write([]byte{0})
	h.Write([]byte(normalize(answer)))
	h.Write([]byte{0})
	// Context passages are separated by 0x01 inside the trailing field.
	normalizedCtx := make([]string, len(context))
	for i, p := range context {
		normalizedCtx[i] = normalize(p)
	}
	h.Write([]byte(strings.Join(normalizedCtx, "\x01")))
	return hex.EncodeToString(h.Sum(nil))
}

// cachingJudge is the WrapJudge implementation. Cache hits short-circuit
// the inner Judge; cache misses run the inner Judge and cache its
// result. Failures from the inner Judge are NOT cached.
//
// Cache hits BYPASS Observer.OnGenerateUsage and AskOptions.MaxTotalTokens
// budget consumption (since no Generate fires on a hit) — same semantics
// as rag.WrapGrader. See CHANGELOG v1.8.0.
type cachingJudge struct {
	inner Judge
	cache JudgeCache
}

// WrapJudge composes inner with cache. When cache is nil, WrapJudge
// returns inner unchanged.
//
// Cache hits BYPASS the underlying Generate, so neither
// Observer.OnGenerateUsage nor AskOptions.MaxTotalTokens consume budget
// on a hit. Inner-Judge errors are propagated untouched and NOT cached.
//
// Thread-safety: WrapJudge is safe for concurrent use iff `inner` is
// safe for concurrent use. The shipping MemoryJudgeCache is safe for
// concurrent use.
func WrapJudge(inner Judge, cache JudgeCache) Judge {
	if cache == nil {
		return inner
	}
	return cachingJudge{inner: inner, cache: cache}
}

// NewCachingJudge is a convenience constructor combining
// NewMemoryJudgeCache(cap) and WrapJudge. The cap follows the
// MemoryJudgeCache rule: cap <= 0 → 1024.
func NewCachingJudge(inner Judge, cap int) Judge {
	return WrapJudge(inner, NewMemoryJudgeCache(cap))
}

// Judge implements the Judge interface.
func (c cachingJudge) Judge(ctx context.Context, req JudgeRequest) (Judgement, error) {
	key := JudgeCacheKey(req.Query, req.Answer, req.Context)
	if j, ok := c.cache.Get(key); ok {
		return j, nil
	}
	j, err := c.inner.Judge(ctx, req)
	if err != nil {
		return j, err
	}
	c.cache.Put(key, j)
	return j, nil
}
