package eval_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/rag"
)

// fakeJudge is a counting/error-injecting Judge for WrapJudge tests. It is
// safe for concurrent use.
type fakeJudge struct {
	calls    atomic.Int64
	failNext atomic.Bool
	verdict  eval.Judgement
}

func (f *fakeJudge) Judge(_ context.Context, _ eval.JudgeRequest) (eval.Judgement, error) {
	f.calls.Add(1)
	if f.failNext.Swap(false) {
		return eval.Judgement{}, errors.New("fakeJudge: injected failure")
	}
	return f.verdict, nil
}

// TestJudgeCacheKey_NormalizesAllThreeInputs pins that query/answer and
// every context passage are passed through normalize() before hashing —
// whitespace + lowercase collisions land on the same cache slot.
func TestJudgeCacheKey_NormalizesAllThreeInputs(t *testing.T) {
	a := eval.JudgeCacheKey("Q ", "A Text", []string{" a ", "b"})
	b := eval.JudgeCacheKey("q", "a text", []string{"a", "b"})
	if a != b {
		t.Fatalf("normalize collision missing: %q vs %q", a, b)
	}
}

// TestJudgeCacheKey_DistinguishesContextOrder pins that context ordering
// is part of the cache key. ["a","b"] and ["b","a"] must produce
// distinct keys.
func TestJudgeCacheKey_DistinguishesContextOrder(t *testing.T) {
	ab := eval.JudgeCacheKey("q", "a", []string{"a", "b"})
	ba := eval.JudgeCacheKey("q", "a", []string{"b", "a"})
	if ab == ba {
		t.Fatalf("context order collided: %q", ab)
	}
}

// TestJudgeCacheKey_NilVsEmptyContextEqual pins the explicit contract
// that a nil context slice and an empty context slice produce the same
// cache key.
func TestJudgeCacheKey_NilVsEmptyContextEqual(t *testing.T) {
	nilCtx := eval.JudgeCacheKey("q", "a", nil)
	empty := eval.JudgeCacheKey("q", "a", []string{})
	if nilCtx != empty {
		t.Fatalf("nil vs empty context produced different keys: %q vs %q", nilCtx, empty)
	}
}

// TestMemoryJudgeCache_PutGetEvict pins core LRU behavior: cap=2,
// inserting A,B,C evicts A.
func TestMemoryJudgeCache_PutGetEvict(t *testing.T) {
	c := eval.NewMemoryJudgeCache(2)
	c.Put("A", eval.Judgement{Groundedness: 1, Rationale: "a"})
	c.Put("B", eval.Judgement{Groundedness: 2, Rationale: "b"})
	c.Put("C", eval.Judgement{Groundedness: 3, Rationale: "c"})

	if _, ok := c.Get("A"); ok {
		t.Fatalf("A should have been evicted")
	}
	if j, ok := c.Get("B"); !ok || j.Rationale != "b" {
		t.Fatalf("Get(B) = %+v ok=%v", j, ok)
	}
	if j, ok := c.Get("C"); !ok || j.Rationale != "c" {
		t.Fatalf("Get(C) = %+v ok=%v", j, ok)
	}
	if got := c.Stats().Evictions; got != 1 {
		t.Fatalf("Stats.Evictions = %d, want 1", got)
	}
}

// TestMemoryJudgeCache_StatsSnapshot pins that Stats reports a
// rag.CacheStats snapshot consistent with Get/Put activity.
func TestMemoryJudgeCache_StatsSnapshot(t *testing.T) {
	c := eval.NewMemoryJudgeCache(2)
	if _, ok := c.Get("X"); ok {
		t.Fatalf("Get(X) ok=true on empty cache")
	}
	c.Put("A", eval.Judgement{Rationale: "a"})
	if _, ok := c.Get("A"); !ok {
		t.Fatalf("Get(A) ok=false after Put")
	}
	c.Put("B", eval.Judgement{Rationale: "b"})
	c.Put("C", eval.Judgement{Rationale: "c"}) // evicts A
	stats := c.Stats()
	// Stats is rag.CacheStats — assert that explicitly.
	var _ rag.CacheStats = stats
	if stats.Hits != 1 {
		t.Fatalf("Stats.Hits = %d, want 1", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Fatalf("Stats.Misses = %d, want 1", stats.Misses)
	}
	if stats.Evictions != 1 {
		t.Fatalf("Stats.Evictions = %d, want 1", stats.Evictions)
	}
	if stats.Size != 2 {
		t.Fatalf("Stats.Size = %d, want 2", stats.Size)
	}
}

// TestWrapJudge_HitSkipsInner pins that two identical Judge calls trigger
// the inner Judge exactly once.
func TestWrapJudge_HitSkipsInner(t *testing.T) {
	inner := &fakeJudge{verdict: eval.Judgement{Groundedness: 0.8, AnswerRelevance: 0.7, Rationale: "ok"}}
	j := eval.WrapJudge(inner, eval.NewMemoryJudgeCache(8))
	req := eval.JudgeRequest{Query: "Q", Answer: "A", Context: []string{"c1", "c2"}}

	for i := 0; i < 3; i++ {
		got, err := j.Judge(context.Background(), req)
		if err != nil {
			t.Fatalf("iter %d err = %v", i, err)
		}
		if got.Groundedness != 0.8 || got.AnswerRelevance != 0.7 || got.Rationale != "ok" {
			t.Fatalf("iter %d Judgement = %+v", i, got)
		}
	}
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("inner.calls = %d, want 1", got)
	}
}

// TestWrapJudge_InnerErrorNotCached pins that a Judge error is propagated
// and NOT cached: retry must MISS.
func TestWrapJudge_InnerErrorNotCached(t *testing.T) {
	inner := &fakeJudge{verdict: eval.Judgement{Groundedness: 0.5}}
	cache := eval.NewMemoryJudgeCache(8)
	j := eval.WrapJudge(inner, cache)
	req := eval.JudgeRequest{Query: "Q", Answer: "A"}

	inner.failNext.Store(true)
	if _, err := j.Judge(context.Background(), req); err == nil {
		t.Fatalf("expected injected error, got nil")
	}
	if size := cache.Stats().Size; size != 0 {
		t.Fatalf("cache stored entry after inner error: Size=%d", size)
	}

	// Retry: must MISS, then succeed.
	got, err := j.Judge(context.Background(), req)
	if err != nil {
		t.Fatalf("retry err = %v", err)
	}
	if got.Groundedness != 0.5 {
		t.Fatalf("retry Judgement = %+v", got)
	}
	stats := cache.Stats()
	if stats.Misses < 2 {
		t.Fatalf("expected at least 2 misses, got %d", stats.Misses)
	}
	if stats.Hits != 0 {
		t.Fatalf("unexpected hits: %d", stats.Hits)
	}
}

// TestNewCachingJudge_EquivalentToWrapJudge pins the ctor sugar.
func TestNewCachingJudge_EquivalentToWrapJudge(t *testing.T) {
	inner := &fakeJudge{verdict: eval.Judgement{Groundedness: 0.42, Rationale: "via-ctor"}}
	j := eval.NewCachingJudge(inner, 8)
	req := eval.JudgeRequest{Query: "Q", Answer: "A"}
	for i := 0; i < 3; i++ {
		if _, err := j.Judge(context.Background(), req); err != nil {
			t.Fatalf("iter %d err = %v", i, err)
		}
	}
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("inner.calls = %d, want 1", got)
	}
}

// TestWrapJudge_InterfaceConformance pins compile-time that WrapJudge
// returns an eval.Judge.
func TestWrapJudge_InterfaceConformance(t *testing.T) {
	var _ eval.Judge = eval.WrapJudge(&fakeJudge{}, eval.NewMemoryJudgeCache(1))
	var _ eval.Judge = eval.NewCachingJudge(&fakeJudge{}, 1)
}

// TestWrapJudge_NilCacheIsAllowed pins that WrapJudge(inner, nil) returns
// inner unchanged.
func TestWrapJudge_NilCacheIsAllowed(t *testing.T) {
	inner := &fakeJudge{verdict: eval.Judgement{Rationale: "noop"}}
	got := eval.WrapJudge(inner, nil)
	if got != eval.Judge(inner) {
		t.Fatalf("WrapJudge(inner, nil) != inner")
	}
	if _, err := got.Judge(context.Background(), eval.JudgeRequest{}); err != nil {
		t.Fatalf("returned Judge err = %v", err)
	}
}

// TestMemoryJudgeCache_ConcurrentSafe exercises the cache under -race
// from multiple goroutines on disjoint keys.
func TestMemoryJudgeCache_ConcurrentSafe(t *testing.T) {
	const workers = 16
	const ops = 1000
	c := eval.NewMemoryJudgeCache(workers * ops)

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				k := itoaJudge(w) + "-" + itoaJudge(i)
				c.Put(k, eval.Judgement{Rationale: k})
				if got, ok := c.Get(k); !ok || got.Rationale != k {
					t.Errorf("worker %d: round-trip mismatch for %q ok=%v got=%+v", w, k, ok, got)
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

// itoaJudge is a stdlib-free int → decimal-string for test keys (kept
// local to avoid colliding with itoa() in benchmark_test.go).
func itoaJudge(n int) string {
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
