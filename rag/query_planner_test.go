package rag

import (
	"context"
	"testing"
)

// TestNoopQueryPlanner_ReturnsNil pins the no-op contract: regardless
// of inputs, NoopQueryPlanner.PlanFollowups returns (nil, nil) — the
// safe default that disables active retrieval at the planner seam.
func TestNoopQueryPlanner_ReturnsNil(t *testing.T) {
	p := NoopQueryPlanner{}
	got, err := p.PlanFollowups(context.Background(), "anything", "prev answer", []ChunkScore{
		{HitID: "c1", Relevance: 0.2},
	})
	if err != nil {
		t.Fatalf("NoopQueryPlanner err = %v, want nil", err)
	}
	if got != nil {
		t.Fatalf("NoopQueryPlanner got = %v, want nil", got)
	}
}

// TestQueryPlanner_InterfaceImplementations compile-checks that the two
// shipped planners satisfy QueryPlanner. The var assignment alone is
// the test — if a planner stops satisfying the interface, the package
// fails to compile.
func TestQueryPlanner_InterfaceImplementations(t *testing.T) {
	var (
		_ QueryPlanner = NoopQueryPlanner{}
		_ QueryPlanner = PromptQueryPlanner{}
	)
}
