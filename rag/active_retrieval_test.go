package rag

import (
	"context"
	"sort"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/store"
)

// scriptedQueryPlanner returns a fixed sequence of follow-up query
// lists per call (Q-F). It records inputs so tests can assert the
// active-retrieval driver threaded the right values through.
type scriptedQueryPlanner struct {
	queries        [][]string
	calls          int
	lastQuestion   string
	lastPrevAnswer string
	lastScores     []ChunkScore
}

func (p *scriptedQueryPlanner) PlanFollowups(_ context.Context, question, prevAnswer string, scores []ChunkScore) ([]string, error) {
	p.lastQuestion = question
	p.lastPrevAnswer = prevAnswer
	p.lastScores = append([]ChunkScore(nil), scores...)
	idx := p.calls
	p.calls++
	if idx >= len(p.queries) {
		return nil, nil
	}
	return p.queries[idx], nil
}

// extraHitsRetriever wraps a System's default retriever so test
// follow-up queries surface additional hit IDs the seed query did not
// produce. The map keys are follow-up queries; values are extra IDs
// to inject into the next retrieve call when the query matches.
//
// We can't easily inject a Retriever via Options here (the default
// HybridRetriever is wired by New), so the tests rely on the in-
// memory store containing the documents that match each follow-up
// query directly — see seedDocsForActiveRetrieval.
type extraHitsRetriever struct{}

// seedDocsForActiveRetrieval is the corpus the active-retrieval tests
// import — one doc per follow-up query so the union test sees fresh
// chunk IDs from each follow-up retrieve call.
func seedDocsForActiveRetrieval() []ingest.Document {
	return []ingest.Document{
		{ID: "doc-seed", Content: "Paris seed content."},
		{ID: "doc-alpha", Content: "alpha follow-up content paris."},
		{ID: "doc-beta", Content: "beta follow-up content paris."},
		{ID: "doc-gamma", Content: "gamma follow-up content paris."},
	}
}

// TestActiveRetrieval_TriggersWhenRelevanceBelowFloor pins the
// trigger contract: with EnableActiveRetrieval on, a configured
// planner, and a low-relevance grader, the driver fires follow-up
// retrievals and the round's diagnostics records the planner's
// emitted queries.
func TestActiveRetrieval_TriggersWhenRelevanceBelowFloor(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "round 1 answer"}},
	}
	planner := &scriptedQueryPlanner{
		queries: [][]string{{"alpha", "beta"}},
	}
	grader := lowRelevanceGrader{relevance: 0.1} // below 0.4 floor
	sys := New(Options{Model: model, Grader: grader, QueryPlanner: planner})
	_, err := sys.Import(context.Background(), seedDocsForActiveRetrieval(),
		ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "paris seed", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 4},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:                  ReflectionModeRule,
			MaxRounds:             1,
			EnableActiveRetrieval: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(ans.Diagnostics.Reflection.RoundDetails) != 1 {
		t.Fatalf("len(RoundDetails) = %d, want 1", len(ans.Diagnostics.Reflection.RoundDetails))
	}
	got := ans.Diagnostics.Reflection.RoundDetails[0].FollowupQueries
	if len(got) != 2 {
		t.Fatalf("FollowupQueries = %v, want 2 entries", got)
	}
	if got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("FollowupQueries = %v, want [alpha beta]", got)
	}
	if ans.Diagnostics.Reflection.FollowupQueriesUsed != 2 {
		t.Fatalf("FollowupQueriesUsed = %d, want 2", ans.Diagnostics.Reflection.FollowupQueriesUsed)
	}
	if planner.calls != 1 {
		t.Fatalf("planner.calls = %d, want 1", planner.calls)
	}
}

// TestActiveRetrieval_DoesNotTriggerAboveFloor pins the inverse:
// when seed relevance is at or above the floor, the planner is never
// consulted. The trigger short-circuits without spending the budget.
func TestActiveRetrieval_DoesNotTriggerAboveFloor(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "round 1 answer"}},
	}
	planner := &scriptedQueryPlanner{
		queries: [][]string{{"should-not-run"}},
	}
	grader := lowRelevanceGrader{relevance: 0.9} // well above 0.4 floor
	sys := New(Options{Model: model, Grader: grader, QueryPlanner: planner})
	_, err := sys.Import(context.Background(), seedDocsForActiveRetrieval(),
		ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "paris seed", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 4},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:                  ReflectionModeRule,
			MaxRounds:             1,
			EnableActiveRetrieval: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if planner.calls != 0 {
		t.Fatalf("planner.calls = %d, want 0 (relevance above floor)", planner.calls)
	}
	if ans.Diagnostics.Reflection.FollowupQueriesUsed != 0 {
		t.Fatalf("FollowupQueriesUsed = %d, want 0", ans.Diagnostics.Reflection.FollowupQueriesUsed)
	}
	if got := ans.Diagnostics.Reflection.RoundDetails[0].FollowupQueries; len(got) != 0 {
		t.Fatalf("FollowupQueries = %v, want empty", got)
	}
}

// TestActiveRetrieval_UnionDedupesByID pins Q-A: when a follow-up
// retrieval returns a hit ID already present in the seed set, the
// union keeps a single entry per ID with the higher score, and the
// final order is by Score desc.
func TestActiveRetrieval_UnionDedupesByID(t *testing.T) {
	seeds := []store.Hit{
		{Chunk: store.StoredChunk{ID: "c1"}, Score: 0.5},
		{Chunk: store.StoredChunk{ID: "c2"}, Score: 0.3},
	}
	followups := [][]store.Hit{
		{
			{Chunk: store.StoredChunk{ID: "c1"}, Score: 0.9}, // higher than seed c1
			{Chunk: store.StoredChunk{ID: "c3"}, Score: 0.7},
		},
		{
			{Chunk: store.StoredChunk{ID: "c2"}, Score: 0.1}, // lower than seed c2
		},
	}
	got := unionHits(seeds, followups, 0)
	if len(got) != 3 {
		t.Fatalf("len(union) = %d, want 3 (dedup by ID)", len(got))
	}
	// Sorted by Score desc: c1=0.9, c3=0.7, c2=0.3
	if got[0].Chunk.ID != "c1" || got[0].Score != 0.9 {
		t.Fatalf("got[0] = (%s, %v), want (c1, 0.9)", got[0].Chunk.ID, got[0].Score)
	}
	if got[1].Chunk.ID != "c3" || got[1].Score != 0.7 {
		t.Fatalf("got[1] = (%s, %v), want (c3, 0.7)", got[1].Chunk.ID, got[1].Score)
	}
	if got[2].Chunk.ID != "c2" || got[2].Score != 0.3 {
		t.Fatalf("got[2] = (%s, %v), want (c2, 0.3)", got[2].Chunk.ID, got[2].Score)
	}
	// capK truncation also works.
	truncated := unionHits(seeds, followups, 2)
	if len(truncated) != 2 {
		t.Fatalf("len(truncated) = %d, want 2 (capK=2)", len(truncated))
	}
}

// TestActiveRetrieval_RespectsPerRoundCap pins MaxFollowupQueries:
// when the planner emits more follow-ups than the cap, the driver
// truncates and only the first cap are dispatched.
func TestActiveRetrieval_RespectsPerRoundCap(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "round 1 answer"}},
	}
	planner := &scriptedQueryPlanner{
		queries: [][]string{{"alpha", "beta", "gamma"}},
	}
	grader := lowRelevanceGrader{relevance: 0.1}
	sys := New(Options{Model: model, Grader: grader, QueryPlanner: planner})
	_, err := sys.Import(context.Background(), seedDocsForActiveRetrieval(),
		ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "paris seed", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 4},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:                  ReflectionModeRule,
			MaxRounds:             1,
			EnableActiveRetrieval: true,
			MaxFollowupQueries:    2, // cap at 2
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	got := ans.Diagnostics.Reflection.RoundDetails[0].FollowupQueries
	if len(got) != 2 {
		t.Fatalf("len(FollowupQueries) = %d, want 2 (cap)", len(got))
	}
	if got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("FollowupQueries = %v, want [alpha beta]", got)
	}
	if ans.Diagnostics.Reflection.FollowupQueriesUsed != 2 {
		t.Fatalf("FollowupQueriesUsed = %d, want 2", ans.Diagnostics.Reflection.FollowupQueriesUsed)
	}
}

// TestActiveRetrieval_RespectsPerAskCap pins
// MaxFollowupQueriesPerAsk: across multiple rounds the global counter
// caps the total follow-ups consumed by one Ask call.
func TestActiveRetrieval_RespectsPerAskCap(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "round 1 answer"},
			{Text: "round 2 answer"},
		},
	}
	// Two rounds, each plans 2 follow-ups; global cap = 3.
	planner := &scriptedQueryPlanner{
		queries: [][]string{
			{"alpha", "beta"},
			{"gamma", "delta"},
		},
	}
	grader := lowRelevanceGrader{relevance: 0.1}
	sys := New(Options{Model: model, Grader: grader, QueryPlanner: planner})
	_, err := sys.Import(context.Background(), seedDocsForActiveRetrieval(),
		ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "paris seed", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 4},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:                     ReflectionModeRule,
			MaxRounds:                2,
			MinHits:                  100, // force non-stop after round 1
			EnableActiveRetrieval:    true,
			MaxFollowupQueries:       2,
			MaxFollowupQueriesPerAsk: 3, // total cap
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	used := ans.Diagnostics.Reflection.FollowupQueriesUsed
	if used != 3 {
		t.Fatalf("FollowupQueriesUsed = %d, want 3 (global cap)", used)
	}
	// Round 1: 2 follow-ups; round 2: 1 follow-up (budget exhausted).
	r1 := ans.Diagnostics.Reflection.RoundDetails[0].FollowupQueries
	if len(r1) != 2 {
		t.Fatalf("round 1 FollowupQueries = %v, want 2", r1)
	}
	if len(ans.Diagnostics.Reflection.RoundDetails) >= 2 {
		r2 := ans.Diagnostics.Reflection.RoundDetails[1].FollowupQueries
		if len(r2) != 1 {
			t.Fatalf("round 2 FollowupQueries = %v, want 1 (budget cap)", r2)
		}
	}
}

// TestActiveRetrieval_DisabledByDefault pins backward compat: a
// reflection run with EnableActiveRetrieval at its zero-value false
// does not call the planner and leaves FollowupQueries / Used empty.
func TestActiveRetrieval_DisabledByDefault(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "round 1 answer"}},
	}
	planner := &scriptedQueryPlanner{
		queries: [][]string{{"should-not-run"}},
	}
	grader := lowRelevanceGrader{relevance: 0.0}
	sys := New(Options{Model: model, Grader: grader, QueryPlanner: planner})
	_, err := sys.Import(context.Background(), seedDocsForActiveRetrieval(),
		ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "paris seed", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 4},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:      ReflectionModeRule,
			MaxRounds: 1,
			// EnableActiveRetrieval left at zero-value (false).
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if planner.calls != 0 {
		t.Fatalf("planner.calls = %d, want 0 (active retrieval disabled)", planner.calls)
	}
	if ans.Diagnostics.Reflection.FollowupQueriesUsed != 0 {
		t.Fatalf("FollowupQueriesUsed = %d, want 0", ans.Diagnostics.Reflection.FollowupQueriesUsed)
	}
	got := ans.Diagnostics.Reflection.RoundDetails[0].FollowupQueries
	if len(got) != 0 {
		t.Fatalf("FollowupQueries = %v, want empty", got)
	}
}

// TestUnionHits_SortedByScoreStable defends the stable sort guarantee:
// equal-score entries preserve their first-seen order across multiple
// dedupe passes — a deterministic baseline for downstream packers.
func TestUnionHits_SortedByScoreStable(t *testing.T) {
	seeds := []store.Hit{
		{Chunk: store.StoredChunk{ID: "a"}, Score: 0.5},
		{Chunk: store.StoredChunk{ID: "b"}, Score: 0.5},
		{Chunk: store.StoredChunk{ID: "c"}, Score: 0.5},
	}
	got := unionHits(seeds, nil, 0)
	if len(got) != 3 {
		t.Fatalf("len(union) = %d, want 3", len(got))
	}
	ids := []string{got[0].Chunk.ID, got[1].Chunk.ID, got[2].Chunk.ID}
	want := []string{"a", "b", "c"}
	if !equalStringSlices(ids, want) {
		// Sort for a stable diff message — equal scores must keep
		// first-seen order, so a non-equal result indicates a bug.
		sort.Strings(ids)
		sort.Strings(want)
		t.Fatalf("union order = %v, want %v (stable)", ids, want)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
