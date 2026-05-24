package rag

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/costa92/llm-agent-rag/store"
)

// TestGraderCacheModeConstants pins the exported mode constant values. The
// values are part of the v1.x cache-key contract: changing them would
// invalidate cache hits for callers that recorded keys under the prior
// values.
func TestGraderCacheModeConstants(t *testing.T) {
	if GraderCacheModeRelevance != "relevance" {
		t.Fatalf("GraderCacheModeRelevance = %q, want %q", GraderCacheModeRelevance, "relevance")
	}
	if GraderCacheModeSupport != "support" {
		t.Fatalf("GraderCacheModeSupport = %q, want %q", GraderCacheModeSupport, "support")
	}
}

// TestGraderCacheKey_DeterministicAcrossNormalize pins the canonicalization
// contract: query/hitID/answer are lowercased and whitespace-collapsed
// before hashing, so semantically identical inputs share a cache slot.
func TestGraderCacheKey_DeterministicAcrossNormalize(t *testing.T) {
	a := GraderCacheKey("Q  X", "Hit-1", "Answer Text", GraderCacheModeRelevance)
	b := GraderCacheKey("q x", "hit-1", "answer text", GraderCacheModeRelevance)
	if a != b {
		t.Fatalf("normalize collision missing: %q vs %q", a, b)
	}
}

// TestGraderCacheKey_DistinguishesMode pins that relevance vs support
// scores get distinct cache slots.
func TestGraderCacheKey_DistinguishesMode(t *testing.T) {
	rel := GraderCacheKey("q", "h", "a", GraderCacheModeRelevance)
	sup := GraderCacheKey("q", "h", "a", GraderCacheModeSupport)
	if rel == sup {
		t.Fatalf("relevance and support keys collided: %q", rel)
	}
}

// TestGraderCacheKey_ContractIsSHA256Hex pins that the output is the hex
// of a SHA-256 digest (64 lowercase hex chars). The format is frozen as
// part of the v1.x cache-key contract.
func TestGraderCacheKey_ContractIsSHA256Hex(t *testing.T) {
	k := GraderCacheKey("q", "h", "a", GraderCacheModeRelevance)
	if len(k) != 64 {
		t.Fatalf("len(key) = %d, want 64", len(k))
	}
	for _, c := range k {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("key contains non-hex byte %q in %s", c, k)
		}
	}
}

// TestMemoryGraderCache_GetMiss_ReturnsZeroFalse pins that a miss returns
// (zero ChunkScore, false).
func TestMemoryGraderCache_GetMiss_ReturnsZeroFalse(t *testing.T) {
	c := NewMemoryGraderCache(8)
	got, ok := c.Get("nope")
	if ok {
		t.Fatalf("Get on empty cache returned ok=true")
	}
	if (got != ChunkScore{}) {
		t.Fatalf("Get miss returned non-zero ChunkScore: %+v", got)
	}
}

// TestMemoryGraderCache_PutThenGet_ReturnsStored pins basic insertion +
// retrieval semantics.
func TestMemoryGraderCache_PutThenGet_ReturnsStored(t *testing.T) {
	c := NewMemoryGraderCache(8)
	want := ChunkScore{HitID: "h1", Relevance: 0.9, Support: 0.7, Reason: "ok"}
	c.Put("k1", want)
	got, ok := c.Get("k1")
	if !ok {
		t.Fatalf("Get(k1) ok=false, want true")
	}
	if got != want {
		t.Fatalf("Get(k1) = %+v, want %+v", got, want)
	}
}

// TestMemoryGraderCache_LRUEvictsOldest pins that exceeding cap evicts the
// least-recently-used entry and bumps Stats.Evictions.
func TestMemoryGraderCache_LRUEvictsOldest(t *testing.T) {
	c := NewMemoryGraderCache(2)
	c.Put("A", ChunkScore{HitID: "A"})
	c.Put("B", ChunkScore{HitID: "B"})
	c.Put("C", ChunkScore{HitID: "C"})

	if _, ok := c.Get("A"); ok {
		t.Fatalf("Get(A) ok=true, want false (A should have been evicted)")
	}
	if _, ok := c.Get("B"); !ok {
		t.Fatalf("Get(B) ok=false, want true")
	}
	if _, ok := c.Get("C"); !ok {
		t.Fatalf("Get(C) ok=false, want true")
	}
	stats := c.Stats()
	if stats.Evictions != 1 {
		t.Fatalf("Stats.Evictions = %d, want 1", stats.Evictions)
	}
}

// TestMemoryGraderCache_GetTouchesRecency pins that Get refreshes an
// entry's recency: with cap=2, putting A,B then getting A then putting C
// must evict B (not A).
func TestMemoryGraderCache_GetTouchesRecency(t *testing.T) {
	c := NewMemoryGraderCache(2)
	c.Put("A", ChunkScore{HitID: "A"})
	c.Put("B", ChunkScore{HitID: "B"})
	if _, ok := c.Get("A"); !ok {
		t.Fatalf("Get(A) ok=false before C insert")
	}
	c.Put("C", ChunkScore{HitID: "C"})
	if _, ok := c.Get("A"); !ok {
		t.Fatalf("Get(A) ok=false, want true (A was just refreshed)")
	}
	if _, ok := c.Get("B"); ok {
		t.Fatalf("Get(B) ok=true, want false (B should have been evicted)")
	}
}

// TestMemoryGraderCache_DefaultCapWhenZeroOrNegative pins that cap<=0
// defaults to 1024.
func TestMemoryGraderCache_DefaultCapWhenZeroOrNegative(t *testing.T) {
	for _, in := range []int{0, -1, -1024} {
		c := NewMemoryGraderCache(in)
		// Insert 1025 entries; if cap defaulted to 1024 exactly one
		// eviction should fire.
		for i := 0; i < 1025; i++ {
			c.Put(string(rune('a'+i%26))+itoa(i), ChunkScore{HitID: itoa(i)})
		}
		if got := c.Stats().Evictions; got != 1 {
			t.Fatalf("cap=%d Stats.Evictions = %d, want 1 (cap default = 1024)", in, got)
		}
	}
}

// TestMemoryGraderCache_StatsSnapshot pins that Stats reports Hits, Misses,
// Evictions, and Size with the expected sense.
func TestMemoryGraderCache_StatsSnapshot(t *testing.T) {
	c := NewMemoryGraderCache(2)
	if _, ok := c.Get("X"); ok {
		t.Fatalf("Get(X) ok=true on empty cache")
	}
	c.Put("A", ChunkScore{HitID: "A"})
	if _, ok := c.Get("A"); !ok {
		t.Fatalf("Get(A) ok=false right after Put")
	}
	c.Put("B", ChunkScore{HitID: "B"})
	c.Put("C", ChunkScore{HitID: "C"}) // evicts A (B is more recent)

	s := c.Stats()
	if s.Hits != 1 {
		t.Fatalf("Stats.Hits = %d, want 1", s.Hits)
	}
	if s.Misses != 1 {
		t.Fatalf("Stats.Misses = %d, want 1", s.Misses)
	}
	if s.Evictions != 1 {
		t.Fatalf("Stats.Evictions = %d, want 1", s.Evictions)
	}
	if s.Size != 2 {
		t.Fatalf("Stats.Size = %d, want 2", s.Size)
	}
}

// TestMemoryGraderCache_ConcurrentSafe exercises the cache from multiple
// goroutines under -race. Keys are disjoint per worker so we can assert
// the final size exactly without relying on map ordering.
func TestMemoryGraderCache_ConcurrentSafe(t *testing.T) {
	const workers = 16
	const ops = 1000
	c := NewMemoryGraderCache(workers * ops) // large cap; no evictions expected

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				k := itoa(w) + "-" + itoa(i)
				c.Put(k, ChunkScore{HitID: k})
				if got, ok := c.Get(k); !ok || got.HitID != k {
					t.Errorf("worker %d: round-trip mismatch for %q (ok=%v got=%+v)", w, k, ok, got)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	stats := c.Stats()
	if stats.Size != workers*ops {
		t.Fatalf("Stats.Size = %d, want %d", stats.Size, workers*ops)
	}
	if stats.Evictions != 0 {
		t.Fatalf("Stats.Evictions = %d, want 0 (cap was generous)", stats.Evictions)
	}
}

// fakeGrader is a counting/error-injecting Grader for WrapGrader tests.
// It is safe for concurrent use.
type fakeGrader struct {
	calls     atomic.Int64
	failNext  atomic.Bool // when true, the next call returns err and resets the flag
	relevance float64
	support   float64
	reason    string
}

func (f *fakeGrader) ScoreRelevance(_ context.Context, _ string, hit store.Hit) (float64, string, error) {
	f.calls.Add(1)
	if f.failNext.Swap(false) {
		return 0, "", errors.New("fakeGrader: injected relevance failure")
	}
	return f.relevance, f.reason, nil
}

func (f *fakeGrader) ScoreSupport(_ context.Context, _ string, hit store.Hit) (float64, string, error) {
	f.calls.Add(1)
	if f.failNext.Swap(false) {
		return 0, "", errors.New("fakeGrader: injected support failure")
	}
	return f.support, f.reason, nil
}

// TestWrapGrader_MissCallsInnerAndCaches pins that two identical calls
// trigger the inner Grader exactly once (cache hit on the second call).
func TestWrapGrader_MissCallsInnerAndCaches(t *testing.T) {
	inner := &fakeGrader{relevance: 0.8, reason: "first"}
	g := WrapGrader(inner, NewMemoryGraderCache(8))
	hit := store.Hit{Chunk: store.StoredChunk{ID: "c1", Content: "x"}}

	s1, r1, err := g.ScoreRelevance(context.Background(), "Q", hit)
	if err != nil || s1 != 0.8 || r1 != "first" {
		t.Fatalf("first call: score=%v reason=%q err=%v", s1, r1, err)
	}
	s2, r2, err := g.ScoreRelevance(context.Background(), "Q", hit)
	if err != nil || s2 != 0.8 || r2 != "first" {
		t.Fatalf("second call: score=%v reason=%q err=%v", s2, r2, err)
	}
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("inner.calls = %d, want 1 (second should be a cache hit)", got)
	}
}

// TestWrapGrader_RelevanceAndSupportCacheIndependently pins that
// (query=Q, hit) and (answer=Q, hit) score independently — relevance and
// support occupy distinct cache slots even when the query and answer
// strings happen to match.
func TestWrapGrader_RelevanceAndSupportCacheIndependently(t *testing.T) {
	inner := &fakeGrader{relevance: 0.6, support: 0.9, reason: "ok"}
	g := WrapGrader(inner, NewMemoryGraderCache(8))
	hit := store.Hit{Chunk: store.StoredChunk{ID: "c1", Content: "x"}}

	if _, _, err := g.ScoreRelevance(context.Background(), "shared", hit); err != nil {
		t.Fatalf("ScoreRelevance err = %v", err)
	}
	if _, _, err := g.ScoreSupport(context.Background(), "shared", hit); err != nil {
		t.Fatalf("ScoreSupport err = %v", err)
	}
	if got := inner.calls.Load(); got != 2 {
		t.Fatalf("inner.calls = %d, want 2 (rel and sup must not collide)", got)
	}
	// Repeat: both should hit cache.
	if s, _, err := g.ScoreRelevance(context.Background(), "shared", hit); err != nil || s != 0.6 {
		t.Fatalf("rel re-call: score=%v err=%v", s, err)
	}
	if s, _, err := g.ScoreSupport(context.Background(), "shared", hit); err != nil || s != 0.9 {
		t.Fatalf("sup re-call: score=%v err=%v", s, err)
	}
	if got := inner.calls.Load(); got != 2 {
		t.Fatalf("inner.calls after re-call = %d, want 2 (both cached)", got)
	}
}

// TestWrapGrader_InnerErrorNotCached pins that an inner-Grader error is
// propagated and NOT cached: the next call must MISS again.
func TestWrapGrader_InnerErrorNotCached(t *testing.T) {
	inner := &fakeGrader{relevance: 0.7, reason: "second"}
	cache := NewMemoryGraderCache(8)
	g := WrapGrader(inner, cache)
	hit := store.Hit{Chunk: store.StoredChunk{ID: "c1"}}

	inner.failNext.Store(true)
	if _, _, err := g.ScoreRelevance(context.Background(), "Q", hit); err == nil {
		t.Fatalf("expected injected error, got nil")
	}
	stats := cache.Stats()
	if stats.Size != 0 {
		t.Fatalf("cache stored an entry after inner error: Size=%d", stats.Size)
	}

	// Retry must MISS (no cached failure) and then succeed.
	s, _, err := g.ScoreRelevance(context.Background(), "Q", hit)
	if err != nil {
		t.Fatalf("retry err = %v", err)
	}
	if s != 0.7 {
		t.Fatalf("retry score = %v, want 0.7", s)
	}
	stats = cache.Stats()
	if stats.Misses < 2 {
		t.Fatalf("expected at least 2 misses (initial + retry), got %d", stats.Misses)
	}
	if stats.Hits != 0 {
		t.Fatalf("unexpected hits: %d", stats.Hits)
	}
}

// TestWrapGrader_NilCacheIsAllowed pins that WrapGrader(inner, nil)
// returns inner unchanged: no wrapping, no panic, no cache statistics.
func TestWrapGrader_NilCacheIsAllowed(t *testing.T) {
	inner := &fakeGrader{relevance: 0.5, reason: "noop"}
	got := WrapGrader(inner, nil)
	if got != Grader(inner) {
		t.Fatalf("WrapGrader(inner, nil) != inner")
	}
	// Sanity-check that the returned Grader still works.
	if _, _, err := got.ScoreRelevance(context.Background(), "q", store.Hit{}); err != nil {
		t.Fatalf("returned Grader err = %v", err)
	}
}

// TestNewCachingGrader_EquivalentToWrapGraderWithMemoryCache pins that
// NewCachingGrader composes the same behavior as the explicit
// WrapGrader+MemoryGraderCache pair.
func TestNewCachingGrader_EquivalentToWrapGraderWithMemoryCache(t *testing.T) {
	inner := &fakeGrader{relevance: 0.42, reason: "via-ctor"}
	g := NewCachingGrader(inner, 8)
	hit := store.Hit{Chunk: store.StoredChunk{ID: "c1"}}

	for i := 0; i < 3; i++ {
		s, _, err := g.ScoreRelevance(context.Background(), "Q", hit)
		if err != nil {
			t.Fatalf("iter %d err = %v", i, err)
		}
		if s != 0.42 {
			t.Fatalf("iter %d score = %v, want 0.42", i, s)
		}
	}
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("inner.calls = %d, want 1 (only the first call should miss)", got)
	}
}

// TestWrapGrader_InterfaceConformance pins compile-time that
// WrapGrader(...) returns a Grader.
func TestWrapGrader_InterfaceConformance(t *testing.T) {
	var _ Grader = WrapGrader(NoopGrader{}, NewMemoryGraderCache(1))
	var _ Grader = NewCachingGrader(NoopGrader{}, 1)
}

// itoa is a tiny stdlib-free int → decimal-string for test keys.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
