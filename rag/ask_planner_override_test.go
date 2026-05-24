package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
)

// labeledPlanner is a QueryPlanner that records its label on every
// call so tests can pin which planner instance was consulted.
type labeledPlanner struct {
	label   string
	queries [][]string
	calls   int
}

func (p *labeledPlanner) PlanFollowups(_ context.Context, _ string, _ string, _ []ChunkScore) ([]string, error) {
	idx := p.calls
	p.calls++
	if idx >= len(p.queries) {
		return nil, nil
	}
	return p.queries[idx], nil
}

// TestAskOptions_QueryPlanner_OverridesSystemPlanner pins the v1.2.1
// per-Ask override: when AskOptions.QueryPlanner is non-nil, it takes
// precedence over Options.QueryPlanner — the system-level planner is
// never consulted for this Ask call.
func TestAskOptions_QueryPlanner_OverridesSystemPlanner(t *testing.T) {
	systemPlanner := &labeledPlanner{
		label:   "system",
		queries: [][]string{{"system-q1"}},
	}
	perAskPlanner := &labeledPlanner{
		label:   "per-ask",
		queries: [][]string{{"per-ask-q1"}},
	}
	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "answer"}},
	}
	grader := lowRelevanceGrader{relevance: 0.1}
	sys := New(Options{
		Model:        model,
		Grader:       grader,
		QueryPlanner: systemPlanner,
	})
	_, err := sys.Import(context.Background(), seedDocsForActiveRetrieval(),
		ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "paris seed", AskOptions{
		Search:       SearchOptions{Namespace: "geo", TopK: 4},
		Template:     promptRoutingTemplate{},
		QueryPlanner: perAskPlanner,
		Reflection: &ReflectionOptions{
			Mode:                  ReflectionModeRule,
			MaxRounds:             1,
			EnableActiveRetrieval: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if systemPlanner.calls != 0 {
		t.Fatalf("system planner calls = %d, want 0 (per-Ask override must take precedence)", systemPlanner.calls)
	}
	if perAskPlanner.calls != 1 {
		t.Fatalf("per-Ask planner calls = %d, want 1", perAskPlanner.calls)
	}
	got := ans.Diagnostics.Reflection.RoundDetails[0].FollowupQueries
	if len(got) != 1 || got[0] != "per-ask-q1" {
		t.Fatalf("FollowupQueries = %v, want [per-ask-q1] (per-Ask planner output)", got)
	}
}

// TestAskOptions_QueryPlanner_NilFallsBackToSystem pins the resolution
// order: a nil AskOptions.QueryPlanner falls back to the system planner.
func TestAskOptions_QueryPlanner_NilFallsBackToSystem(t *testing.T) {
	systemPlanner := &labeledPlanner{
		label:   "system",
		queries: [][]string{{"system-q1"}},
	}
	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "answer"}},
	}
	grader := lowRelevanceGrader{relevance: 0.1}
	sys := New(Options{
		Model:        model,
		Grader:       grader,
		QueryPlanner: systemPlanner,
	})
	_, err := sys.Import(context.Background(), seedDocsForActiveRetrieval(),
		ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "paris seed", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 4},
		Template: promptRoutingTemplate{},
		// QueryPlanner nil — fall back to system.
		Reflection: &ReflectionOptions{
			Mode:                  ReflectionModeRule,
			MaxRounds:             1,
			EnableActiveRetrieval: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if systemPlanner.calls != 1 {
		t.Fatalf("system planner calls = %d, want 1 (no per-Ask override)", systemPlanner.calls)
	}
	got := ans.Diagnostics.Reflection.RoundDetails[0].FollowupQueries
	if len(got) != 1 || got[0] != "system-q1" {
		t.Fatalf("FollowupQueries = %v, want [system-q1] (system planner output)", got)
	}
}

// TestAskOptions_QueryPlanner_FallsBackToNoopWhenBothNil pins safety:
// when neither AskOptions.QueryPlanner nor Options.QueryPlanner is set
// but EnableActiveRetrieval is true, the system uses NoopQueryPlanner
// (no follow-ups; no panic).
func TestAskOptions_QueryPlanner_FallsBackToNoopWhenBothNil(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "answer"}},
	}
	grader := lowRelevanceGrader{relevance: 0.1}
	sys := New(Options{
		Model:  model,
		Grader: grader,
		// Options.QueryPlanner intentionally nil.
	})
	_, err := sys.Import(context.Background(), seedDocsForActiveRetrieval(),
		ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "paris seed", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 4},
		Template: promptRoutingTemplate{},
		// AskOptions.QueryPlanner intentionally nil.
		Reflection: &ReflectionOptions{
			Mode:                  ReflectionModeRule,
			MaxRounds:             1,
			EnableActiveRetrieval: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v (NoopQueryPlanner fallback must not error)", err)
	}
	if used := ans.Diagnostics.Reflection.FollowupQueriesUsed; used != 0 {
		t.Fatalf("FollowupQueriesUsed = %d, want 0 (Noop emits no follow-ups)", used)
	}
}

// TestAskOptions_QueryPlanner_ZeroValueSurface pins the additive API:
// AskOptions has a QueryPlanner field with the expected name and type.
// A keyed literal pins it at compile time.
func TestAskOptions_QueryPlanner_ZeroValueSurface(t *testing.T) {
	planner := &labeledPlanner{label: "x"}
	opts := AskOptions{
		QueryPlanner: planner,
	}
	if opts.QueryPlanner == nil {
		t.Fatalf("AskOptions.QueryPlanner is nil after explicit set")
	}
	var zero AskOptions
	if zero.QueryPlanner != nil {
		t.Fatalf("zero-value AskOptions.QueryPlanner = %v, want nil", zero.QueryPlanner)
	}
}
