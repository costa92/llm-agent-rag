package rag

import (
	"testing"
)

// TestReflectionRoundDiagnostics_FollowupQueriesField pins that the
// v1.2.0 additive FollowupQueries slice exists on the diagnostic side
// of a reflection round. Keyed-literal compile alone is the test.
func TestReflectionRoundDiagnostics_FollowupQueriesField(t *testing.T) {
	d := ReflectionRoundDiagnostics{
		Round:            1,
		FollowupQueries:  []string{"q1", "q2"},
	}
	if len(d.FollowupQueries) != 2 {
		t.Fatalf("len(FollowupQueries) = %d, want 2", len(d.FollowupQueries))
	}
	if d.FollowupQueries[0] != "q1" {
		t.Fatalf("FollowupQueries[0] = %q, want q1", d.FollowupQueries[0])
	}
}

// TestReflectionRoundTrace_FollowupQueriesField pins that the v1.2.0
// additive FollowupQueries slice exists on the trace side of a
// reflection round.
func TestReflectionRoundTrace_FollowupQueriesField(t *testing.T) {
	tr := ReflectionRoundTrace{
		Round:           1,
		FollowupQueries: []string{"alpha", "beta"},
	}
	if len(tr.FollowupQueries) != 2 {
		t.Fatalf("len(trace.FollowupQueries) = %d, want 2", len(tr.FollowupQueries))
	}
	if tr.FollowupQueries[1] != "beta" {
		t.Fatalf("trace.FollowupQueries[1] = %q, want beta", tr.FollowupQueries[1])
	}
}

// TestReflectionDiagnostics_FollowupQueriesUsedField pins that the
// global per-Ask counter exists on ReflectionDiagnostics.
func TestReflectionDiagnostics_FollowupQueriesUsedField(t *testing.T) {
	d := ReflectionDiagnostics{FollowupQueriesUsed: 3}
	if d.FollowupQueriesUsed != 3 {
		t.Fatalf("FollowupQueriesUsed = %d, want 3", d.FollowupQueriesUsed)
	}
}

// TestReflectionRound_FollowupQueriesDefaultsNil pins the zero-value
// contract: a reflection round with no active-retrieval pass has nil
// FollowupQueries on both the diagnostic and trace side.
func TestReflectionRound_FollowupQueriesDefaultsNil(t *testing.T) {
	var d ReflectionRoundDiagnostics
	var tr ReflectionRoundTrace
	if d.FollowupQueries != nil {
		t.Fatalf("ReflectionRoundDiagnostics.FollowupQueries default = %v, want nil", d.FollowupQueries)
	}
	if tr.FollowupQueries != nil {
		t.Fatalf("ReflectionRoundTrace.FollowupQueries default = %v, want nil", tr.FollowupQueries)
	}
}
