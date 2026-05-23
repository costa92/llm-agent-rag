package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

// scoreByHitIDGrader returns scores keyed on the hit ID so each round's
// distinct retrieval is graded distinctly. Used to drive
// SelectionModeBestByScore to pick a non-last round.
type scoreByHitIDGrader struct {
	scores map[string]float64
}

func (g scoreByHitIDGrader) ScoreRelevance(_ context.Context, _ string, hit store.Hit) (float64, string, error) {
	if v, ok := g.scores[hit.Chunk.ID]; ok {
		return v, "scripted", nil
	}
	return 0.1, "default", nil
}

func (g scoreByHitIDGrader) ScoreSupport(_ context.Context, _ string, hit store.Hit) (float64, string, error) {
	if v, ok := g.scores[hit.Chunk.ID]; ok {
		return v, "scripted", nil
	}
	return 0.1, "default", nil
}

// TestAskReflection_SelectionModeBestByScore_PicksHighestScoringRound
// pins the contract: when SelectionModeBestByScore is enabled and
// round 1 has a higher aggregate ChunkScore than the last round, the
// adopted answer is round 1's — not the last round's. Loop semantics
// (when to stop, when to rewrite) stay unchanged.
func TestAskReflection_SelectionModeBestByScore_PicksHighestScoringRound(t *testing.T) {
	round1Hit := store.Hit{
		Chunk: store.StoredChunk{ID: "round1-hit", DocID: "doc1", Namespace: "geo", Content: "round 1 chunk"},
		Score: 0.9,
	}
	round2Hit := store.Hit{
		Chunk: store.StoredChunk{ID: "round2-hit", DocID: "doc2", Namespace: "geo", Content: "round 2 chunk"},
		Score: 0.95,
	}
	ret := perQueryHitRetriever{
		routes: map[string][]string{
			"capital of france": {"section.a"},
			"different":         {"section.b"},
		},
		hits: map[string]store.Hit{
			"capital of france": round1Hit,
			"different":         round2Hit,
		},
	}
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1"},
			{Text: "decision=rewrite_and_continue\nreason=try again\nrewrite=different"},
			{Text: "answer round 2"},
			{Text: "decision=stop\nreason=ok"},
		},
	}
	// Round 1 scores high (relevant), round 2 scores low (off-topic).
	grader := scoreByHitIDGrader{scores: map[string]float64{
		"round1-hit": 0.95,
		"round2-hit": 0.10,
	}}
	sys := New(Options{Model: model, Retriever: ret, Grader: grader})
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:               ReflectionModeModel,
			MaxRounds:          2,
			AllowRewrite:       true,
			EnableChunkGrading: true,
			SelectionMode:      SelectionModeBestByScore,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Diagnostics.Reflection.RoundDetails) != 2 {
		t.Fatalf("len(RoundDetails) = %d, want 2", len(ans.Diagnostics.Reflection.RoundDetails))
	}
	if ans.Diagnostics.Reflection.AdoptedRound != 1 {
		t.Fatalf("AdoptedRound = %d, want 1 (highest-scoring round)", ans.Diagnostics.Reflection.AdoptedRound)
	}
	if ans.Text != "answer round 1" {
		t.Fatalf("answer.Text = %q, want round 1 answer", ans.Text)
	}
	if ans.Trace.Reflection.AdoptedRound != 1 {
		t.Fatalf("Trace.AdoptedRound = %d, want 1", ans.Trace.Reflection.AdoptedRound)
	}
}

// TestAskReflection_SelectionModeLastRound_IsDefault pins the
// backward-compatibility guarantee: the zero-value SelectionMode keeps
// last-round-wins, even when the configured Grader would prefer an
// earlier round. v1.0.x semantics preserved.
func TestAskReflection_SelectionModeLastRound_IsDefault(t *testing.T) {
	round1Hit := store.Hit{
		Chunk: store.StoredChunk{ID: "round1-hit", DocID: "doc1", Namespace: "geo", Content: "round 1 chunk"},
		Score: 0.9,
	}
	round2Hit := store.Hit{
		Chunk: store.StoredChunk{ID: "round2-hit", DocID: "doc2", Namespace: "geo", Content: "round 2 chunk"},
		Score: 0.95,
	}
	ret := perQueryHitRetriever{
		routes: map[string][]string{
			"capital of france": {"section.a"},
			"different":         {"section.b"},
		},
		hits: map[string]store.Hit{
			"capital of france": round1Hit,
			"different":         round2Hit,
		},
	}
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1"},
			{Text: "decision=rewrite_and_continue\nreason=try again\nrewrite=different"},
			{Text: "answer round 2"},
			{Text: "decision=stop\nreason=ok"},
		},
	}
	grader := scoreByHitIDGrader{scores: map[string]float64{
		"round1-hit": 0.95,
		"round2-hit": 0.10,
	}}
	sys := New(Options{Model: model, Retriever: ret, Grader: grader})
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:               ReflectionModeModel,
			MaxRounds:          2,
			AllowRewrite:       true,
			EnableChunkGrading: true,
			// SelectionMode left at zero value — SelectionModeLastRound.
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Diagnostics.Reflection.AdoptedRound != 2 {
		t.Fatalf("AdoptedRound = %d, want 2 (last-round default)", ans.Diagnostics.Reflection.AdoptedRound)
	}
	if ans.Text != "answer round 2" {
		t.Fatalf("answer.Text = %q, want last round answer", ans.Text)
	}
}

// TestPickBestRound_TiesBreakByEarliestIndex pins the deterministic
// tie-break: with equal aggregate scores the earliest round wins, so
// a degenerate "all equal" case still selects deterministically.
func TestPickBestRound_TiesBreakByEarliestIndex(t *testing.T) {
	rounds := []reflectionRound{
		{round: ReflectionRoundDiagnostics{Round: 1, ChunkScores: []ChunkScore{{Relevance: 0.5, Support: 0.5}}}},
		{round: ReflectionRoundDiagnostics{Round: 2, ChunkScores: []ChunkScore{{Relevance: 0.5, Support: 0.5}}}},
	}
	idx := pickBestRound(rounds, 0.5, 0.5)
	if idx != 0 {
		t.Fatalf("pickBestRound idx = %d, want 0 (earliest-tie)", idx)
	}
}

// TestRoundAggregateScore_AppliesWeights pins the documented formula:
// score = relWeight*mean(relevance) + supWeight*mean(support).
func TestRoundAggregateScore_AppliesWeights(t *testing.T) {
	rr := reflectionRound{
		round: ReflectionRoundDiagnostics{
			Round: 1,
			ChunkScores: []ChunkScore{
				{Relevance: 1.0, Support: 0.0},
				{Relevance: 0.0, Support: 1.0},
			},
		},
	}
	// mean(rel)=0.5, mean(sup)=0.5; weights 0.8/0.2 → 0.5*0.8 + 0.5*0.2 = 0.5
	got := roundAggregateScore(rr, 0.8, 0.2)
	want := 0.5
	if got != want {
		t.Fatalf("aggregate = %v, want %v", got, want)
	}
	// All-zero weights should fall back to 0.5/0.5 defaults.
	got = roundAggregateScore(rr, 0, 0)
	if got != 0.5 {
		t.Fatalf("default-weight aggregate = %v, want 0.5", got)
	}
}

// guarantee scoped to this test file — the route+hit driver shape is
// shared with reflection_test.go's perQueryHitRetriever.
var _ retrieve.Retriever = perQueryHitRetriever{}
