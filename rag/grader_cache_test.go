package rag

import (
	"sync"
	"testing"
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
