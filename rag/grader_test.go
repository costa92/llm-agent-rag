package rag

import (
	"context"
	"testing"

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
