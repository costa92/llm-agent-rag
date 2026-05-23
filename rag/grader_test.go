package rag

import (
	"context"
	"errors"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/store"
)

// TestNoopGrader_ScoresAreDeterministic pins the contract that NoopGrader
// returns the neutral 0.5 score for both relevance and support, with a
// stable reason string, for any input. NoopGrader is the safe default for
// callers who enable grading wiring before configuring a real Grader.
func TestNoopGrader_ScoresAreDeterministic(t *testing.T) {
	g := NoopGrader{}
	hit := store.Hit{Chunk: store.StoredChunk{ID: "c1", Content: "anything"}, Score: 0.9}

	score, reason, err := g.ScoreRelevance(context.Background(), "any query", hit)
	if err != nil {
		t.Fatalf("ScoreRelevance err = %v, want nil", err)
	}
	if score != 0.5 {
		t.Fatalf("ScoreRelevance score = %v, want 0.5", score)
	}
	if reason == "" {
		t.Fatalf("ScoreRelevance reason = empty, want non-empty stable string")
	}

	score, reason, err = g.ScoreSupport(context.Background(), "any answer", hit)
	if err != nil {
		t.Fatalf("ScoreSupport err = %v, want nil", err)
	}
	if score != 0.5 {
		t.Fatalf("ScoreSupport score = %v, want 0.5", score)
	}
	if reason == "" {
		t.Fatalf("ScoreSupport reason = empty, want non-empty stable string")
	}
}

// TestGrader_InterfaceImplementations pins that both NoopGrader and
// PromptGrader satisfy the Grader interface. Compile-time check; this
// catches any future signature drift.
func TestGrader_InterfaceImplementations(t *testing.T) {
	var _ Grader = NoopGrader{}
	var _ Grader = PromptGrader{}
}

// TestPromptGrader_ParsesNumericScore pins that PromptGrader extracts a
// 0.0-1.0 float from a "score=<float>" reply line and forwards the raw
// text as reason for downstream diagnostics.
func TestPromptGrader_ParsesNumericScore(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "score=0.82"},
			{Text: "score=0.10"},
		},
	}
	g := PromptGrader{Model: model}
	hit := store.Hit{Chunk: store.StoredChunk{ID: "c1", Content: "x"}, Score: 0.9}

	score, reason, err := g.ScoreRelevance(context.Background(), "q", hit)
	if err != nil {
		t.Fatalf("ScoreRelevance err = %v, want nil", err)
	}
	if score != 0.82 {
		t.Fatalf("ScoreRelevance score = %v, want 0.82", score)
	}
	if reason != "score=0.82" {
		t.Fatalf("ScoreRelevance reason = %q, want raw text", reason)
	}

	score, _, err = g.ScoreSupport(context.Background(), "a", hit)
	if err != nil {
		t.Fatalf("ScoreSupport err = %v, want nil", err)
	}
	if score != 0.10 {
		t.Fatalf("ScoreSupport score = %v, want 0.10", score)
	}
}

// TestPromptGrader_ClampsScoreToZeroOne pins the clamp on out-of-range
// numeric scores returned by a misbehaving model. Models occasionally
// emit "1.5" or "-0.2"; the grader must squash these to the [0,1]
// interval rather than propagate junk.
func TestPromptGrader_ClampsScoreToZeroOne(t *testing.T) {
	cases := []struct {
		name     string
		reply    string
		expected float64
	}{
		{name: "above_one", reply: "score=1.5", expected: 1.0},
		{name: "below_zero", reply: "score=-0.3", expected: 0.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := &scriptedReflectionModel{
				responses: []generate.Response{{Text: tc.reply}},
			}
			g := PromptGrader{Model: model}
			hit := store.Hit{Chunk: store.StoredChunk{ID: "c1"}}
			score, _, err := g.ScoreRelevance(context.Background(), "q", hit)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if score != tc.expected {
				t.Fatalf("score = %v, want %v", score, tc.expected)
			}
		})
	}
}

// TestPromptGrader_FailsOpenOnUnparseableReply pins the fail-open
// guarantee: a model reply with no "score=" line or a non-numeric value
// must yield 0.5 with the raw text on reason — never an error. This is
// critical because the grader is called on every retrieved chunk and we
// do not want a single noisy response to break the Ask call.
func TestPromptGrader_FailsOpenOnUnparseableReply(t *testing.T) {
	cases := []struct {
		name  string
		reply string
	}{
		{name: "no_score_line", reply: "I am a chatty model."},
		{name: "non_numeric", reply: "score=very high"},
		{name: "empty", reply: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := &scriptedReflectionModel{
				responses: []generate.Response{{Text: tc.reply}},
			}
			g := PromptGrader{Model: model}
			hit := store.Hit{Chunk: store.StoredChunk{ID: "c1"}}
			score, reason, err := g.ScoreRelevance(context.Background(), "q", hit)
			if err != nil {
				t.Fatalf("err = %v, want nil (fail-open)", err)
			}
			if score != 0.5 {
				t.Fatalf("score = %v, want 0.5 (fail-open)", score)
			}
			if reason == "" {
				t.Fatalf("reason = empty, want non-empty fail-open note")
			}
		})
	}
}

// TestPromptGrader_PropagatesModelError pins that a model-level error
// surfaces as a grader error — fail-open applies to parse failures, not
// transport failures. This lets the reflection loop's FailOpen logic
// distinguish a deterministic neutral score from a real outage.
func TestPromptGrader_PropagatesModelError(t *testing.T) {
	wantErr := errors.New("boom")
	model := &scriptedReflectionModel{
		responses: []generate.Response{{}},
		errors:    []error{wantErr},
	}
	g := PromptGrader{Model: model}
	hit := store.Hit{Chunk: store.StoredChunk{ID: "c1"}}
	_, _, err := g.ScoreRelevance(context.Background(), "q", hit)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

// TestPromptGrader_ReturnsErrorWhenModelNil pins that misconfiguration
// is caught loudly. A nil Model must produce an error so the wiring
// problem surfaces immediately rather than silently neutralizing every
// score.
func TestPromptGrader_ReturnsErrorWhenModelNil(t *testing.T) {
	g := PromptGrader{}
	hit := store.Hit{Chunk: store.StoredChunk{ID: "c1"}}
	if _, _, err := g.ScoreRelevance(context.Background(), "q", hit); err == nil {
		t.Fatalf("ScoreRelevance err = nil, want non-nil for nil model")
	}
	if _, _, err := g.ScoreSupport(context.Background(), "a", hit); err == nil {
		t.Fatalf("ScoreSupport err = nil, want non-nil for nil model")
	}
}
