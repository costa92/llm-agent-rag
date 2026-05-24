package rag

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/store"
)

// TestObserver_OnPlanFollowups_FiresWithPlannedQueries pins the new
// active-retrieval observability hook: when the planner returns a
// non-empty plan, OnPlanFollowups fires once with the question,
// pre-graded scores, and the planned queries in planner output order.
func TestObserver_OnPlanFollowups_FiresWithPlannedQueries(t *testing.T) {
	plannerQueries := []string{"qA", "qB"}
	retr := &instrumentedRetriever{
		results: map[string][]store.Hit{
			"seed": {makeParallelTestHit("seed1", 0.50)},
			"qA":   {makeParallelTestHit("a1", 0.40)},
			"qB":   {makeParallelTestHit("b1", 0.41)},
		},
	}

	var (
		mu          sync.Mutex
		planCalls   int
		gotQuestion string
		gotPlanned  []string
		gotScores   []ChunkScore
	)

	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "answer"}},
	}
	planner := &scriptedQueryPlanner{queries: [][]string{plannerQueries}}
	grader := fixedRelevanceGrader{relevance: 0.1}
	sys := New(Options{
		Model:        model,
		Grader:       grader,
		QueryPlanner: planner,
		Retriever:    retr,
		Packer:       orderedAllPacker{},
		Observer: Observer{
			OnPlanFollowups: func(_ context.Context, q string, scores []ChunkScore, planned []string) {
				mu.Lock()
				defer mu.Unlock()
				planCalls++
				gotQuestion = q
				gotPlanned = append([]string(nil), planned...)
				gotScores = append([]ChunkScore(nil), scores...)
			},
		},
	})

	_, err := sys.Ask(context.Background(), "seed", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 10},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:                  ReflectionModeRule,
			MaxRounds:             1,
			EnableActiveRetrieval: true,
			MaxFollowupQueries:    2,
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if planCalls != 1 {
		t.Fatalf("OnPlanFollowups calls = %d, want 1", planCalls)
	}
	if gotQuestion != "seed" {
		t.Fatalf("OnPlanFollowups question = %q, want %q", gotQuestion, "seed")
	}
	if len(gotPlanned) != 2 || gotPlanned[0] != "qA" || gotPlanned[1] != "qB" {
		t.Fatalf("OnPlanFollowups planned = %v, want [qA qB]", gotPlanned)
	}
	if len(gotScores) == 0 {
		t.Fatalf("OnPlanFollowups scores = empty, want pre-graded relevance scores")
	}
}

// TestObserver_OnFollowupRetrieve_FiresPerFollowup_OnSuccess pins
// per-follow-up observability under sequential dispatch: one callback
// per planned follow-up, in some order (sequential dispatches in
// planner order, but the test asserts on counts + queries-seen, not on
// the call order, so it remains valid for parallel dispatch too).
func TestObserver_OnFollowupRetrieve_FiresPerFollowup_OnSuccess(t *testing.T) {
	plannerQueries := []string{"qA", "qB", "qC"}
	retr := &instrumentedRetriever{
		results: map[string][]store.Hit{
			"seed": {makeParallelTestHit("seed1", 0.50)},
			"qA":   {makeParallelTestHit("a1", 0.40)},
			"qB":   {makeParallelTestHit("b1", 0.41)},
			"qC":   {makeParallelTestHit("c1", 0.42)},
		},
	}

	var (
		mu      sync.Mutex
		queries []string
		errs    []error
		hits    []int
	)

	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "answer"}},
	}
	planner := &scriptedQueryPlanner{queries: [][]string{plannerQueries}}
	grader := fixedRelevanceGrader{relevance: 0.1}
	sys := New(Options{
		Model:        model,
		Grader:       grader,
		QueryPlanner: planner,
		Retriever:    retr,
		Packer:       orderedAllPacker{},
		Observer: Observer{
			OnFollowupRetrieve: func(_ context.Context, q string, h []store.Hit, err error) {
				mu.Lock()
				defer mu.Unlock()
				queries = append(queries, q)
				errs = append(errs, err)
				hits = append(hits, len(h))
			},
		},
	})

	_, err := sys.Ask(context.Background(), "seed", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 10},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:                  ReflectionModeRule,
			MaxRounds:             1,
			EnableActiveRetrieval: true,
			MaxFollowupQueries:    3,
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(queries) != 3 {
		t.Fatalf("OnFollowupRetrieve calls = %d, want 3", len(queries))
	}
	// Build a set so we don't depend on ordering (parallel-safe).
	seen := map[string]bool{}
	for i, q := range queries {
		seen[q] = true
		if errs[i] != nil {
			t.Fatalf("OnFollowupRetrieve[%q] err = %v, want nil", q, errs[i])
		}
		if hits[i] == 0 {
			t.Fatalf("OnFollowupRetrieve[%q] returned 0 hits, want >0", q)
		}
	}
	for _, want := range []string{"qA", "qB", "qC"} {
		if !seen[want] {
			t.Fatalf("OnFollowupRetrieve missing %q (saw %v)", want, queries)
		}
	}
}

// TestObserver_OnFollowupRetrieve_FiresWithError_OnFailure pins
// fail-open observability: when a follow-up retrieval errors, the
// callback fires with the error and a nil hits slice.
func TestObserver_OnFollowupRetrieve_FiresWithError_OnFailure(t *testing.T) {
	plannerQueries := []string{"qA", "qB"}
	failure := errors.New("synthetic retrieve failure")
	retr := &instrumentedRetriever{
		results: map[string][]store.Hit{
			"seed": {makeParallelTestHit("seed1", 0.50)},
			"qA":   {makeParallelTestHit("a1", 0.40)},
		},
		errors: map[string]error{
			"qB": failure,
		},
	}

	var (
		mu        sync.Mutex
		seenError error
		seenQuery string
		seenHits  int
	)

	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "answer"}},
	}
	planner := &scriptedQueryPlanner{queries: [][]string{plannerQueries}}
	grader := fixedRelevanceGrader{relevance: 0.1}
	sys := New(Options{
		Model:        model,
		Grader:       grader,
		QueryPlanner: planner,
		Retriever:    retr,
		Packer:       orderedAllPacker{},
		Observer: Observer{
			OnFollowupRetrieve: func(_ context.Context, q string, h []store.Hit, err error) {
				if err == nil {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				seenError = err
				seenQuery = q
				seenHits = len(h)
			},
		},
	})

	_, err := sys.Ask(context.Background(), "seed", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 10},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:                  ReflectionModeRule,
			MaxRounds:             1,
			EnableActiveRetrieval: true,
			MaxFollowupQueries:    2,
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !errors.Is(seenError, failure) {
		t.Fatalf("OnFollowupRetrieve error = %v, want %v", seenError, failure)
	}
	if seenQuery != "qB" {
		t.Fatalf("OnFollowupRetrieve query on failure = %q, want qB", seenQuery)
	}
	if seenHits != 0 {
		t.Fatalf("OnFollowupRetrieve hits on failure = %d, want 0", seenHits)
	}
}

// TestObserver_OnPlanFollowups_NilSafe pins backward compat: a nil
// callback must not panic — Active retrieval keeps working.
func TestObserver_OnPlanFollowups_NilSafe(t *testing.T) {
	plannerQueries := []string{"qA"}
	retr := &instrumentedRetriever{
		results: map[string][]store.Hit{
			"seed": {makeParallelTestHit("seed1", 0.50)},
			"qA":   {makeParallelTestHit("a1", 0.40)},
		},
	}
	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "answer"}},
	}
	planner := &scriptedQueryPlanner{queries: [][]string{plannerQueries}}
	grader := fixedRelevanceGrader{relevance: 0.1}
	sys := New(Options{
		Model:        model,
		Grader:       grader,
		QueryPlanner: planner,
		Retriever:    retr,
		Packer:       orderedAllPacker{},
		Observer:     Observer{ /* nil OnPlanFollowups */ },
	})
	_, err := sys.Ask(context.Background(), "seed", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 10},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:                  ReflectionModeRule,
			MaxRounds:             1,
			EnableActiveRetrieval: true,
			MaxFollowupQueries:    1,
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v (must not panic on nil OnPlanFollowups)", err)
	}
}

// TestObserver_OnFollowupRetrieve_NilSafe pins backward compat: a nil
// callback must not panic — Active retrieval keeps working.
func TestObserver_OnFollowupRetrieve_NilSafe(t *testing.T) {
	plannerQueries := []string{"qA"}
	retr := &instrumentedRetriever{
		results: map[string][]store.Hit{
			"seed": {makeParallelTestHit("seed1", 0.50)},
			"qA":   {makeParallelTestHit("a1", 0.40)},
		},
	}
	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "answer"}},
	}
	planner := &scriptedQueryPlanner{queries: [][]string{plannerQueries}}
	grader := fixedRelevanceGrader{relevance: 0.1}
	sys := New(Options{
		Model:        model,
		Grader:       grader,
		QueryPlanner: planner,
		Retriever:    retr,
		Packer:       orderedAllPacker{},
		Observer:     Observer{ /* nil OnFollowupRetrieve */ },
	})
	_, err := sys.Ask(context.Background(), "seed", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 10},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:                  ReflectionModeRule,
			MaxRounds:             1,
			EnableActiveRetrieval: true,
			MaxFollowupQueries:    1,
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v (must not panic on nil OnFollowupRetrieve)", err)
	}
}

// TestObserver_OnFollowupRetrieve_FiresUnderParallelDispatch pins that
// the per-follow-up hook also fires when ParallelFollowups=true. The
// callback must be thread-safe (we use a mutex). The order is not
// guaranteed under parallel, but the call count is.
func TestObserver_OnFollowupRetrieve_FiresUnderParallelDispatch(t *testing.T) {
	plannerQueries := []string{"qA", "qB", "qC"}
	retr := &instrumentedRetriever{
		results: map[string][]store.Hit{
			"seed": {makeParallelTestHit("seed1", 0.50)},
			"qA":   {makeParallelTestHit("a1", 0.40)},
			"qB":   {makeParallelTestHit("b1", 0.41)},
			"qC":   {makeParallelTestHit("c1", 0.42)},
		},
	}
	var (
		mu    sync.Mutex
		count int
	)
	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "answer"}},
	}
	planner := &scriptedQueryPlanner{queries: [][]string{plannerQueries}}
	grader := fixedRelevanceGrader{relevance: 0.1}
	sys := New(Options{
		Model:        model,
		Grader:       grader,
		QueryPlanner: planner,
		Retriever:    retr,
		Packer:       orderedAllPacker{},
		Observer: Observer{
			OnFollowupRetrieve: func(_ context.Context, _ string, _ []store.Hit, _ error) {
				mu.Lock()
				count++
				mu.Unlock()
			},
		},
	})
	_, err := sys.Ask(context.Background(), "seed", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 10},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:                  ReflectionModeRule,
			MaxRounds:             1,
			EnableActiveRetrieval: true,
			MaxFollowupQueries:    3,
			ParallelFollowups:     true,
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if count != 3 {
		t.Fatalf("OnFollowupRetrieve fired %d times under parallel, want 3", count)
	}
}
