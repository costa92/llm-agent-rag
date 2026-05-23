package rag

import (
	"testing"
)

// TestReflectionOptions_ActiveRetrievalFieldsExist pins that the v1.2.0
// active-retrieval fields on ReflectionOptions exist and accept the
// values described in the brief. The zero-value preservation contract
// is validated in a separate test.
func TestReflectionOptions_ActiveRetrievalFieldsExist(t *testing.T) {
	opts := ReflectionOptions{
		EnableActiveRetrieval:         true,
		MaxFollowupQueries:            3,
		MaxFollowupQueriesPerAsk:      6,
		ActiveRetrievalRelevanceFloor: 0.5,
	}
	if !opts.EnableActiveRetrieval {
		t.Fatalf("EnableActiveRetrieval = false, want true")
	}
	if opts.MaxFollowupQueries != 3 {
		t.Fatalf("MaxFollowupQueries = %d, want 3", opts.MaxFollowupQueries)
	}
	if opts.MaxFollowupQueriesPerAsk != 6 {
		t.Fatalf("MaxFollowupQueriesPerAsk = %d, want 6", opts.MaxFollowupQueriesPerAsk)
	}
	if opts.ActiveRetrievalRelevanceFloor != 0.5 {
		t.Fatalf("ActiveRetrievalRelevanceFloor = %v, want 0.5", opts.ActiveRetrievalRelevanceFloor)
	}
}

// TestReflectionOptions_ActiveRetrievalDefaultsArePreserved pins
// backward compatibility: a ReflectionOptions left at its v1.1.x shape
// reports the v1.2.0 active-retrieval fields at their zero values, so
// existing callers are unaffected.
func TestReflectionOptions_ActiveRetrievalDefaultsArePreserved(t *testing.T) {
	opts := ReflectionOptions{Mode: ReflectionModeRule, MaxRounds: 1}
	if opts.EnableActiveRetrieval {
		t.Fatalf("EnableActiveRetrieval default = true, want false")
	}
	if opts.MaxFollowupQueries != 0 {
		t.Fatalf("MaxFollowupQueries default = %d, want 0", opts.MaxFollowupQueries)
	}
	if opts.MaxFollowupQueriesPerAsk != 0 {
		t.Fatalf("MaxFollowupQueriesPerAsk default = %d, want 0", opts.MaxFollowupQueriesPerAsk)
	}
	if opts.ActiveRetrievalRelevanceFloor != 0 {
		t.Fatalf("ActiveRetrievalRelevanceFloor default = %v, want 0", opts.ActiveRetrievalRelevanceFloor)
	}
}

// TestOptions_QueryPlannerField pins that Options carries an additive
// QueryPlanner field — the v1.2.0 brief wires the planner dependency
// into the System via this struct.
func TestOptions_QueryPlannerField(t *testing.T) {
	p := NoopQueryPlanner{}
	opts := Options{QueryPlanner: p}
	if opts.QueryPlanner == nil {
		t.Fatalf("QueryPlanner = nil, want NoopQueryPlanner")
	}
}

// TestSystem_EffectiveQueryPlanner_DefaultsToNoop pins that a System
// constructed without a QueryPlanner reports NoopQueryPlanner from
// effectiveQueryPlanner — the safe default that keeps the wiring
// functional on misconfiguration. Mirrors effectiveGrader.
func TestSystem_EffectiveQueryPlanner_DefaultsToNoop(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	got := sys.effectiveQueryPlanner()
	if got == nil {
		t.Fatalf("effectiveQueryPlanner() = nil, want NoopQueryPlanner")
	}
	if _, ok := got.(NoopQueryPlanner); !ok {
		t.Fatalf("effectiveQueryPlanner() = %T, want NoopQueryPlanner", got)
	}
}

// TestSystem_EffectiveQueryPlanner_HonorsConfigured pins that a
// configured QueryPlanner survives construction and is what
// effectiveQueryPlanner returns.
func TestSystem_EffectiveQueryPlanner_HonorsConfigured(t *testing.T) {
	planner := PromptQueryPlanner{Model: fakeModel{}, MaxQueries: 4}
	sys := New(Options{Model: fakeModel{}, QueryPlanner: planner})
	got := sys.effectiveQueryPlanner()
	pq, ok := got.(PromptQueryPlanner)
	if !ok {
		t.Fatalf("effectiveQueryPlanner() = %T, want PromptQueryPlanner", got)
	}
	if pq.MaxQueries != 4 {
		t.Fatalf("PromptQueryPlanner.MaxQueries = %d, want 4", pq.MaxQueries)
	}
}
