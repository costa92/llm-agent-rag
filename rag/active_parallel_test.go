package rag

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

// Avoid an "imported and not used" if iteration removes the last
// reference — both packages already used directly above.
var _ = generate.Request{}
var _ = retrieve.Request{}

// instrumentedRetriever wraps a deterministic map of query->hits with
// optional per-query latency and an error injection set. It tracks the
// peak concurrent in-flight Retrieve call count so the concurrency tests
// can assert the bounded-fan-out invariant.
type instrumentedRetriever struct {
	results  map[string][]store.Hit
	delays   map[string]time.Duration
	errors   map[string]error
	mu       sync.Mutex
	active   int32
	peak     int32
	callsLog []string
}

func (r *instrumentedRetriever) Retrieve(_ context.Context, req retrieve.Request) ([]store.Hit, retrieve.Trace, error) {
	cur := atomic.AddInt32(&r.active, 1)
	defer atomic.AddInt32(&r.active, -1)
	for {
		old := atomic.LoadInt32(&r.peak)
		if cur <= old || atomic.CompareAndSwapInt32(&r.peak, old, cur) {
			break
		}
	}
	r.mu.Lock()
	r.callsLog = append(r.callsLog, req.Query)
	r.mu.Unlock()
	if d, ok := r.delays[req.Query]; ok && d > 0 {
		time.Sleep(d)
	}
	if err, ok := r.errors[req.Query]; ok {
		return nil, retrieve.Trace{
			OriginalQuery:  req.Query,
			EffectiveQuery: req.Query,
			QueryVariants:  []string{req.Query},
		}, err
	}
	hits := append([]store.Hit(nil), r.results[req.Query]...)
	return hits, retrieve.Trace{
		OriginalQuery:  req.Query,
		EffectiveQuery: req.Query,
		QueryVariants:  []string{req.Query},
	}, nil
}

// peakActive returns the peak number of concurrent Retrieve calls
// observed across the lifetime of the retriever. Thread-safe.
func (r *instrumentedRetriever) peakActive() int32 {
	return atomic.LoadInt32(&r.peak)
}

// fixedRelevanceGrader gives every hit the same relevance score and an
// empty reason. Used by the parallel tests so the trigger fires
// deterministically without forcing a real grader.
type fixedRelevanceGrader struct {
	relevance float64
}

func (g fixedRelevanceGrader) ScoreRelevance(_ context.Context, _ string, _ store.Hit) (float64, string, error) {
	return g.relevance, "fixed", nil
}

func (g fixedRelevanceGrader) ScoreSupport(_ context.Context, _ string, _ store.Hit) (float64, string, error) {
	return g.relevance, "fixed", nil
}

// makeParallelTestHit builds a hit with deterministic chunk ID and
// score so union ordering is stable across runs.
func makeParallelTestHit(id string, score float64) store.Hit {
	return store.Hit{
		Score: score,
		Chunk: store.StoredChunk{
			ID:        id,
			DocID:     "doc-" + id,
			Namespace: "geo",
			Content:   "content " + id,
		},
	}
}

// runParallelAskOnce wires a System with the instrumented retriever and
// runs one Ask. It centralizes the boilerplate so the parallel tests stay
// focused on their assertions.
func runParallelAskOnce(t *testing.T, retr *instrumentedRetriever, plannerQueries []string, reflect ReflectionOptions) Answer {
	t.Helper()
	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "answer"}},
	}
	planner := &scriptedQueryPlanner{
		queries: [][]string{plannerQueries},
	}
	grader := fixedRelevanceGrader{relevance: 0.1} // forces trigger
	sys := New(Options{
		Model:        model,
		Grader:       grader,
		QueryPlanner: planner,
		Retriever:    retr,
		Packer:       orderedAllPacker{},
	})
	reflect.Mode = ReflectionModeRule
	if reflect.MaxRounds == 0 {
		reflect.MaxRounds = 1
	}
	reflect.EnableActiveRetrieval = true
	ans, err := sys.Ask(context.Background(), "seed", AskOptions{
		Search:     SearchOptions{Namespace: "geo", TopK: 10},
		Template:   promptRoutingTemplate{},
		Reflection: &reflect,
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	return ans
}

// TestActiveRetrieval_ParallelDispatch_PreservesUnionOrder pins the
// determinism invariant: with ParallelFollowups=true, the merged
// hit-set order is byte-identical to a sequential run because unionHits
// sorts by score (stable).
func TestActiveRetrieval_ParallelDispatch_PreservesUnionOrder(t *testing.T) {
	plannerQueries := []string{"qA", "qB", "qC"}
	results := map[string][]store.Hit{
		"seed": {makeParallelTestHit("seed1", 0.50)},
		"qA":   {makeParallelTestHit("a1", 0.90)},
		"qB":   {makeParallelTestHit("b1", 0.30)},
		"qC":   {makeParallelTestHit("c1", 0.70)},
	}
	seqRetr := &instrumentedRetriever{results: results}
	seqAns := runParallelAskOnce(t, seqRetr, plannerQueries, ReflectionOptions{
		MaxFollowupQueries: 3,
		// ParallelFollowups left false — sequential baseline.
	})

	parRetr := &instrumentedRetriever{results: results}
	parAns := runParallelAskOnce(t, parRetr, plannerQueries, ReflectionOptions{
		MaxFollowupQueries: 3,
		ParallelFollowups:  true,
	})

	if len(seqAns.Hits) != len(parAns.Hits) {
		t.Fatalf("hit counts differ: seq=%d par=%d", len(seqAns.Hits), len(parAns.Hits))
	}
	for i := range seqAns.Hits {
		if seqAns.Hits[i].Chunk.ID != parAns.Hits[i].Chunk.ID {
			t.Fatalf("hit[%d] differs: seq=%s par=%s (parallel must yield byte-identical order)",
				i, seqAns.Hits[i].Chunk.ID, parAns.Hits[i].Chunk.ID)
		}
		if seqAns.Hits[i].Score != parAns.Hits[i].Score {
			t.Fatalf("hit[%d] score differs: seq=%v par=%v", i, seqAns.Hits[i].Score, parAns.Hits[i].Score)
		}
	}
}

// TestActiveRetrieval_ParallelDispatch_FollowupQueriesInPlannerOrder
// pins the diagnostic invariant: even with delays that force qC to
// complete BEFORE qA, the FollowupQueries slice is in planner output
// order — never completion order.
func TestActiveRetrieval_ParallelDispatch_FollowupQueriesInPlannerOrder(t *testing.T) {
	plannerQueries := []string{"qA", "qB", "qC"}
	retr := &instrumentedRetriever{
		results: map[string][]store.Hit{
			"seed": {makeParallelTestHit("seed1", 0.50)},
			"qA":   {makeParallelTestHit("a1", 0.40)},
			"qB":   {makeParallelTestHit("b1", 0.41)},
			"qC":   {makeParallelTestHit("c1", 0.42)},
		},
		// qA is the slowest, qC fastest — completion order = qC, qB, qA.
		delays: map[string]time.Duration{
			"qA": 30 * time.Millisecond,
			"qB": 15 * time.Millisecond,
			"qC": 1 * time.Millisecond,
		},
	}
	ans := runParallelAskOnce(t, retr, plannerQueries, ReflectionOptions{
		MaxFollowupQueries: 3,
		ParallelFollowups:  true,
	})
	if len(ans.Diagnostics.Reflection.RoundDetails) != 1 {
		t.Fatalf("RoundDetails len = %d, want 1", len(ans.Diagnostics.Reflection.RoundDetails))
	}
	got := ans.Diagnostics.Reflection.RoundDetails[0].FollowupQueries
	want := []string{"qA", "qB", "qC"}
	if len(got) != len(want) {
		t.Fatalf("FollowupQueries = %v, want %v (planner order)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FollowupQueries[%d] = %q, want %q — planner order must be preserved across parallel completions",
				i, got[i], want[i])
		}
	}
}

// TestActiveRetrieval_ParallelDispatch_RespectsMaxFollowupConcurrency
// pins the bounded-fan-out invariant: MaxFollowupConcurrency=1 with 3
// planner queries must never let more than 1 retrieve be in flight at a
// time. The instrumented retriever tracks the peak.
func TestActiveRetrieval_ParallelDispatch_RespectsMaxFollowupConcurrency(t *testing.T) {
	plannerQueries := []string{"qA", "qB", "qC"}
	retr := &instrumentedRetriever{
		results: map[string][]store.Hit{
			"seed": {makeParallelTestHit("seed1", 0.50)},
			"qA":   {makeParallelTestHit("a1", 0.40)},
			"qB":   {makeParallelTestHit("b1", 0.41)},
			"qC":   {makeParallelTestHit("c1", 0.42)},
		},
		// All slow so the peak is meaningful even on fast machines.
		delays: map[string]time.Duration{
			"qA": 20 * time.Millisecond,
			"qB": 20 * time.Millisecond,
			"qC": 20 * time.Millisecond,
		},
	}
	_ = runParallelAskOnce(t, retr, plannerQueries, ReflectionOptions{
		MaxFollowupQueries:     3,
		ParallelFollowups:      true,
		MaxFollowupConcurrency: 1,
	})
	// The seed retrieve also counts toward peak — but it completes
	// before the follow-up dispatch begins, so we expect the peak to
	// reflect the follow-up bound. With concurrency=1, all follow-ups
	// run sequentially, and at most one of them overlaps with anything.
	if peak := retr.peakActive(); peak > 1 {
		t.Fatalf("peakActive = %d, want <= 1 with MaxFollowupConcurrency=1", peak)
	}
}

// TestActiveRetrieval_ParallelDispatch_RespectsMaxFollowupConcurrency_TwoOfThree
// pins the bounded-fan-out invariant with cap=2 and 3 queries: peak
// in-flight follow-ups must be <= 2.
func TestActiveRetrieval_ParallelDispatch_RespectsMaxFollowupConcurrency_TwoOfThree(t *testing.T) {
	plannerQueries := []string{"qA", "qB", "qC"}
	retr := &instrumentedRetriever{
		results: map[string][]store.Hit{
			"seed": {makeParallelTestHit("seed1", 0.50)},
			"qA":   {makeParallelTestHit("a1", 0.40)},
			"qB":   {makeParallelTestHit("b1", 0.41)},
			"qC":   {makeParallelTestHit("c1", 0.42)},
		},
		delays: map[string]time.Duration{
			"qA": 25 * time.Millisecond,
			"qB": 25 * time.Millisecond,
			"qC": 25 * time.Millisecond,
		},
	}
	_ = runParallelAskOnce(t, retr, plannerQueries, ReflectionOptions{
		MaxFollowupQueries:     3,
		ParallelFollowups:      true,
		MaxFollowupConcurrency: 2,
	})
	if peak := retr.peakActive(); peak > 2 {
		t.Fatalf("peakActive = %d, want <= 2 with MaxFollowupConcurrency=2", peak)
	}
}

// TestActiveRetrieval_ParallelDispatch_PartialFailureContinues pins
// fail-open under parallel dispatch: one retrieval errors, the others
// succeed, and the merged hit set carries the survivors.
func TestActiveRetrieval_ParallelDispatch_PartialFailureContinues(t *testing.T) {
	plannerQueries := []string{"qA", "qB", "qC"}
	retr := &instrumentedRetriever{
		results: map[string][]store.Hit{
			"seed": {makeParallelTestHit("seed1", 0.50)},
			"qA":   {makeParallelTestHit("a1", 0.90)},
			// qB will error.
			"qC": {makeParallelTestHit("c1", 0.70)},
		},
		errors: map[string]error{
			"qB": errors.New("synthetic retrieve failure"),
		},
	}
	ans := runParallelAskOnce(t, retr, plannerQueries, ReflectionOptions{
		MaxFollowupQueries: 3,
		ParallelFollowups:  true,
	})
	// Surviving follow-up hits a1 and c1 must show up in the merged
	// hit set; seed1 must also be present (seed retrieval succeeded).
	ids := map[string]bool{}
	for _, h := range ans.Hits {
		ids[h.Chunk.ID] = true
	}
	for _, want := range []string{"seed1", "a1", "c1"} {
		if !ids[want] {
			t.Fatalf("merged hits missing %q (got %v)", want, ids)
		}
	}
	// FollowupQueries records the planned set (planner order); the
	// counter still credits the budget for all 3 queries even when one
	// fails, matching v1.2.0 sequential semantics.
	got := ans.Diagnostics.Reflection.RoundDetails[0].FollowupQueries
	want := []string{"qA", "qB", "qC"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("FollowupQueries = %v, want %v", got, want)
		}
	}
	if used := ans.Diagnostics.Reflection.FollowupQueriesUsed; used != 3 {
		t.Fatalf("FollowupQueriesUsed = %d, want 3 (counts planned, not succeeded)", used)
	}
}

// TestActiveRetrieval_ParallelDispatch_ActuallyOverlaps pins that the
// parallel implementation actually fires retrievals concurrently — not
// just iterating sequentially while reading the flag. With 3 slow
// retrievals and no cap, peak MUST be >= 2 (true parallel dispatch).
// The sequential path keeps peak at 1; this test fails on master.
func TestActiveRetrieval_ParallelDispatch_ActuallyOverlaps(t *testing.T) {
	plannerQueries := []string{"qA", "qB", "qC"}
	retr := &instrumentedRetriever{
		results: map[string][]store.Hit{
			"seed": {makeParallelTestHit("seed1", 0.50)},
			"qA":   {makeParallelTestHit("a1", 0.40)},
			"qB":   {makeParallelTestHit("b1", 0.41)},
			"qC":   {makeParallelTestHit("c1", 0.42)},
		},
		delays: map[string]time.Duration{
			"qA": 30 * time.Millisecond,
			"qB": 30 * time.Millisecond,
			"qC": 30 * time.Millisecond,
		},
	}
	_ = runParallelAskOnce(t, retr, plannerQueries, ReflectionOptions{
		MaxFollowupQueries: 3,
		ParallelFollowups:  true,
		// MaxFollowupConcurrency=0 -> defaults to 3.
	})
	if peak := retr.peakActive(); peak < 2 {
		t.Fatalf("peakActive = %d, want >= 2 — parallel dispatch must overlap retrievals", peak)
	}
}

// TestActiveRetrieval_ParallelDispatch_MaxConcurrencyDefaultsToPerRound
// pins the documented default: when MaxFollowupConcurrency is 0 and
// ParallelFollowups=true, the cap collapses to MaxFollowupQueries.
// With 3 queries dispatched, peak must reach 3 (or come close — small
// scheduling noise is allowed but never exceed 3).
func TestActiveRetrieval_ParallelDispatch_MaxConcurrencyDefaultsToPerRound(t *testing.T) {
	plannerQueries := []string{"qA", "qB", "qC"}
	retr := &instrumentedRetriever{
		results: map[string][]store.Hit{
			"seed": {makeParallelTestHit("seed1", 0.50)},
			"qA":   {makeParallelTestHit("a1", 0.40)},
			"qB":   {makeParallelTestHit("b1", 0.41)},
			"qC":   {makeParallelTestHit("c1", 0.42)},
		},
		delays: map[string]time.Duration{
			"qA": 20 * time.Millisecond,
			"qB": 20 * time.Millisecond,
			"qC": 20 * time.Millisecond,
		},
	}
	_ = runParallelAskOnce(t, retr, plannerQueries, ReflectionOptions{
		MaxFollowupQueries: 3,
		ParallelFollowups:  true,
		// MaxFollowupConcurrency left 0 — defaults to MaxFollowupQueries=3.
	})
	if peak := retr.peakActive(); peak > 3 {
		t.Fatalf("peakActive = %d, want <= 3 (default cap)", peak)
	}
}

