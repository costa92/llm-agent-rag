package rag

import (
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)


// TestReflectionTrace_CapturesRawDecisionText pins the requirement that the
// reflection model's raw reply text is preserved verbatim in both
// Diagnostics.Reflection.RoundDetails[i].RawDecisionText and
// Trace.Reflection.Rounds[i].RawDecisionText. D5 closure.
func TestReflectionTrace_CapturesRawDecisionText(t *testing.T) {
	rawReply := "decision=rewrite_and_continue\nreason=need better\nrewrite=better query"
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1"},
			{Text: rawReply},
			{Text: "answer round 2"},
			{Text: "decision=stop\nreason=ok"},
		},
	}
	sys := New(Options{
		Model:        model,
		Preprocessor: conditionalRewritePreprocessor{},
	})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "zzberlin Berlin is in Germany."},
		{ID: "doc2", Content: "zzparis Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:         ReflectionModeModel,
			MaxRounds:    2,
			AllowRewrite: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Diagnostics.Reflection.RoundDetails) < 1 {
		t.Fatalf("RoundDetails empty, want >=1 round")
	}
	got := ans.Diagnostics.Reflection.RoundDetails[0].RawDecisionText
	if got != rawReply {
		t.Fatalf("Diagnostics.Reflection.RoundDetails[0].RawDecisionText = %q, want %q", got, rawReply)
	}
	if len(ans.Trace.Reflection.Rounds) < 1 {
		t.Fatalf("Trace.Reflection.Rounds empty, want >=1")
	}
	gotTrace := ans.Trace.Reflection.Rounds[0].RawDecisionText
	if gotTrace != rawReply {
		t.Fatalf("Trace.Reflection.Rounds[0].RawDecisionText = %q, want %q", gotTrace, rawReply)
	}
}

// TestReflectionTrace_CapturesDecisionPrompt pins the requirement that the
// user-content portion of the reflection decision prompt is preserved on
// Diagnostics.Reflection.RoundDetails[i].DecisionPrompt. D5 closure.
func TestReflectionTrace_CapturesDecisionPrompt(t *testing.T) {
	t.Run("model mode populates DecisionPrompt", func(t *testing.T) {
		rawReply := "decision=stop\nreason=enough"
		model := &scriptedReflectionModel{
			responses: []generate.Response{
				{Text: "answer round 1"},
				{Text: rawReply},
			},
		}
		sys := New(Options{Model: model})
		_, err := sys.Import(context.Background(), []ingest.Document{
			{ID: "doc1", Content: "Paris is the capital of France."},
		}, ingest.ImportOptions{Namespace: "geo"})
		if err != nil {
			t.Fatalf("Import(): %v", err)
		}
		ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
			Search:   SearchOptions{Namespace: "geo", TopK: 1},
			Template: promptRoutingTemplate{},
			Reflection: &ReflectionOptions{
				Mode:         ReflectionModeModel,
				MaxRounds:    2,
				AllowRewrite: true,
			},
		})
		if err != nil {
			t.Fatalf("Ask(): %v", err)
		}
		if len(ans.Diagnostics.Reflection.RoundDetails) < 1 {
			t.Fatalf("RoundDetails empty, want >=1 round")
		}
		got := ans.Diagnostics.Reflection.RoundDetails[0].DecisionPrompt
		if !strings.Contains(got, "Original question:") {
			t.Fatalf("DecisionPrompt = %q, want substring %q", got, "Original question:")
		}
		if !strings.Contains(got, "Current answer:") {
			t.Fatalf("DecisionPrompt = %q, want substring %q", got, "Current answer:")
		}
		if !strings.Contains(got, "Allow rewrite: true") {
			t.Fatalf("DecisionPrompt = %q, want substring %q", got, "Allow rewrite: true")
		}
	})

	t.Run("rule_mode_leaves_DecisionPrompt_empty", func(t *testing.T) {
		sys := New(Options{Model: fakeModel{}})
		_, err := sys.Import(context.Background(), []ingest.Document{
			{ID: "doc1", Content: "Paris is the capital of France."},
		}, ingest.ImportOptions{Namespace: "geo"})
		if err != nil {
			t.Fatalf("Import(): %v", err)
		}
		ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
			Search:   SearchOptions{Namespace: "geo", TopK: 1},
			Template: promptRoutingTemplate{},
			Reflection: &ReflectionOptions{
				Mode:      ReflectionModeRule,
				MaxRounds: 1,
			},
		})
		if err != nil {
			t.Fatalf("Ask(): %v", err)
		}
		if len(ans.Diagnostics.Reflection.RoundDetails) < 1 {
			t.Fatalf("RoundDetails empty, want >=1 round")
		}
		got := ans.Diagnostics.Reflection.RoundDetails[0].DecisionPrompt
		if got != "" {
			t.Fatalf("rule-mode DecisionPrompt = %q, want empty (no model call)", got)
		}
		gotRaw := ans.Diagnostics.Reflection.RoundDetails[0].RawDecisionText
		if gotRaw != "" {
			t.Fatalf("rule-mode RawDecisionText = %q, want empty (no model call)", gotRaw)
		}
	})
}

// TestReflectionTrace_CarriesPerRoundAutoRoute pins the requirement that
// Diagnostics.Reflection.RoundDetails[i].AutoRoutePath reflects the route
// chosen IN THAT ROUND — not the last round of the Ask. D4 closure.
//
// Without per-round forwarding, only the last round's routing intel survives
// (it's overwritten on every askRound), and multi-round reflection runs lose
// the ability to answer "why did round N pick a different route than N-1".
func TestReflectionTrace_CarriesPerRoundAutoRoute(t *testing.T) {
	round1Hit := store.Hit{
		Chunk: store.StoredChunk{
			ID:        "doc1:0",
			DocID:     "doc1",
			Namespace: "geo",
			Content:   "Berlin is in Germany.",
		},
		Score: 0.9,
	}
	round2Hit := store.Hit{
		Chunk: store.StoredChunk{
			ID:        "doc2:0",
			DocID:     "doc2",
			Namespace: "geo",
			Content:   "Paris is the capital of France.",
		},
		Score: 0.95,
	}
	type queryRoute struct {
		query string
		route []string
		hit   store.Hit
	}
	queries := []queryRoute{
		{query: "capital of france", route: []string{"section.a"}, hit: round1Hit},
		{query: "zzparis", route: []string{"section.b"}, hit: round2Hit},
	}
	// Build a switching retriever — same instance, but query-keyed routes
	// and a query-keyed hit so each round sees a distinct retrieval result.
	routes := map[string][]string{}
	hits := map[string]store.Hit{}
	for _, q := range queries {
		routes[q.query] = q.route
		hits[q.query] = q.hit
	}
	ret := perQueryHitRetriever{routes: routes, hits: hits}

	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1"},
			{Text: "decision=rewrite_and_continue\nreason=need better\nrewrite=zzparis"},
			{Text: "answer round 2"},
			{Text: "decision=stop\nreason=ok"},
		},
	}
	sys := New(Options{
		Model:     model,
		Retriever: ret,
	})
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:         ReflectionModeModel,
			MaxRounds:    2,
			AllowRewrite: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Diagnostics.Reflection.RoundDetails) != 2 {
		t.Fatalf("len(RoundDetails) = %d, want 2", len(ans.Diagnostics.Reflection.RoundDetails))
	}
	r1 := ans.Diagnostics.Reflection.RoundDetails[0].AutoRoutePath
	r2 := ans.Diagnostics.Reflection.RoundDetails[1].AutoRoutePath
	if len(r1) == 0 {
		t.Fatalf("RoundDetails[0].AutoRoutePath empty, want %v", queries[0].route)
	}
	if len(r2) == 0 {
		t.Fatalf("RoundDetails[1].AutoRoutePath empty, want %v", queries[1].route)
	}
	if r1[0] == r2[0] {
		t.Fatalf("per-round AutoRoutePath did not change: r1=%v r2=%v (last-round-wins bug)", r1, r2)
	}
	if r1[0] != queries[0].route[0] {
		t.Fatalf("RoundDetails[0].AutoRoutePath = %v, want %v", r1, queries[0].route)
	}
	if r2[0] != queries[1].route[0] {
		t.Fatalf("RoundDetails[1].AutoRoutePath = %v, want %v", r2, queries[1].route)
	}
	// Trace-side AutoRoutePath mirrors the diagnostic-side per-round value.
	if len(ans.Trace.Reflection.Rounds) != 2 {
		t.Fatalf("len(Trace.Reflection.Rounds) = %d, want 2", len(ans.Trace.Reflection.Rounds))
	}
	traceR1 := ans.Trace.Reflection.Rounds[0].AutoRoutePath
	traceR2 := ans.Trace.Reflection.Rounds[1].AutoRoutePath
	if len(traceR1) == 0 || len(traceR2) == 0 || traceR1[0] == traceR2[0] {
		t.Fatalf("Trace per-round AutoRoutePath not distinct: r1=%v r2=%v", traceR1, traceR2)
	}
}

// perQueryHitRetriever returns a different (hit, AutoRoutePath) per query —
// used by TestReflectionTrace_CarriesPerRoundAutoRoute.
type perQueryHitRetriever struct {
	routes map[string][]string
	hits   map[string]store.Hit
}

func (r perQueryHitRetriever) Retrieve(_ context.Context, req retrieve.Request) ([]store.Hit, retrieve.Trace, error) {
	route := r.routes[req.Query]
	if route == nil {
		route = []string{"default"}
	}
	hit, ok := r.hits[req.Query]
	if !ok {
		hit = store.Hit{Chunk: store.StoredChunk{ID: "fallback:0", Namespace: req.Namespace}, Score: 0.1}
	}
	trace := retrieve.Trace{
		OriginalQuery:  req.Query,
		EffectiveQuery: req.Query,
		AutoRoutePath:  append([]string(nil), route...),
		AutoRouteCandidates: []retrieve.RouteCandidate{{
			Path:     append([]string(nil), route...),
			Score:    1.0,
			Selected: true,
		}},
		SearchTrajectory: []retrieve.TrajectoryStep{{
			Route:    append([]string(nil), route...),
			HitCount: 1,
		}},
	}
	return []store.Hit{hit}, trace, nil
}

