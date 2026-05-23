package rag

import (
	"testing"
)

// TestReflectionOptions_V110AdditiveFields pins the v1.1.0 additive
// fields on ReflectionOptions exist and accept the values described in
// the brief. The zero-value behavior is validated in a separate
// preservation test.
func TestReflectionOptions_V110AdditiveFields(t *testing.T) {
	opts := ReflectionOptions{
		EnableChunkGrading:        true,
		GraderRelevanceWeight:     0.6,
		GraderSupportWeight:       0.4,
		SelectionMode:             SelectionModeBestByScore,
		AdaptiveRetrieval:         true,
		AdaptiveRetrievalThreshold: 0.7,
	}
	if !opts.EnableChunkGrading {
		t.Fatalf("EnableChunkGrading = false, want true")
	}
	if opts.GraderRelevanceWeight != 0.6 {
		t.Fatalf("GraderRelevanceWeight = %v, want 0.6", opts.GraderRelevanceWeight)
	}
	if opts.GraderSupportWeight != 0.4 {
		t.Fatalf("GraderSupportWeight = %v, want 0.4", opts.GraderSupportWeight)
	}
	if opts.SelectionMode != SelectionModeBestByScore {
		t.Fatalf("SelectionMode = %v, want SelectionModeBestByScore", opts.SelectionMode)
	}
	if !opts.AdaptiveRetrieval {
		t.Fatalf("AdaptiveRetrieval = false, want true")
	}
	if opts.AdaptiveRetrievalThreshold != 0.7 {
		t.Fatalf("AdaptiveRetrievalThreshold = %v, want 0.7", opts.AdaptiveRetrievalThreshold)
	}
}

// TestSelectionMode_DefaultIsLastRound pins that the zero-value of
// SelectionMode is SelectionModeLastRound — preserving v1.0.x semantics
// for any ReflectionOptions left at its zero value.
func TestSelectionMode_DefaultIsLastRound(t *testing.T) {
	var sm SelectionMode
	if sm != SelectionModeLastRound {
		t.Fatalf("zero-value SelectionMode = %v, want SelectionModeLastRound", sm)
	}
}

// TestSelectionMode_ConstantsAreStable pins the iota values used by the
// SelectionMode enum so a future contributor can't accidentally reorder
// the constants and silently flip the default behavior.
func TestSelectionMode_ConstantsAreStable(t *testing.T) {
	if SelectionModeLastRound != 0 {
		t.Fatalf("SelectionModeLastRound = %d, want 0", SelectionModeLastRound)
	}
	if SelectionModeBestByScore != 1 {
		t.Fatalf("SelectionModeBestByScore = %d, want 1", SelectionModeBestByScore)
	}
}

// TestReflectionOptions_ZeroValueDefaultsArePreserved pins the
// backward-compatibility contract: an unset ReflectionOptions{Mode:
// ReflectionModeRule, MaxRounds: 1} (all v1.1.0 fields zero) must
// behave the same as it did in v1.0.x.
func TestReflectionOptions_ZeroValueDefaultsArePreserved(t *testing.T) {
	opts := ReflectionOptions{Mode: ReflectionModeRule, MaxRounds: 1}
	if opts.EnableChunkGrading {
		t.Fatalf("EnableChunkGrading default = true, want false")
	}
	if opts.GraderRelevanceWeight != 0 {
		t.Fatalf("GraderRelevanceWeight default = %v, want 0", opts.GraderRelevanceWeight)
	}
	if opts.GraderSupportWeight != 0 {
		t.Fatalf("GraderSupportWeight default = %v, want 0", opts.GraderSupportWeight)
	}
	if opts.SelectionMode != SelectionModeLastRound {
		t.Fatalf("SelectionMode default = %v, want SelectionModeLastRound", opts.SelectionMode)
	}
	if opts.AdaptiveRetrieval {
		t.Fatalf("AdaptiveRetrieval default = true, want false")
	}
	if opts.AdaptiveRetrievalThreshold != 0 {
		t.Fatalf("AdaptiveRetrievalThreshold default = %v, want 0", opts.AdaptiveRetrievalThreshold)
	}
}
