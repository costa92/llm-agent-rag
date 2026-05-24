package rag

import "testing"

// TestStageConstantsExportedAndUnique pins that the v1.5.1 stage constants
// — both the v1.5.0 standard names lifted out as exported constants and the
// five v1.5.1 sub-stage additions — are non-empty and pairwise distinct.
// Existing OnGenerateUsage consumers comparing stage strings keep working
// because the constant values match the prior string literals byte-for-byte.
func TestStageConstantsExportedAndUnique(t *testing.T) {
	pairs := []struct {
		name  string
		value string
	}{
		// v1.5.0 standard set lifted to named constants.
		{"StageAsk", StageAsk},
		{"StageReflectionDecision", StageReflectionDecision},
		{"StageGrader", StageGrader},
		{"StagePlanner", StagePlanner},
		{"StageJudgeEval", StageJudgeEval},
		// v1.5.1 additions — sub-stages for AskGlobal / AskDrift.
		{"StageAskGlobalMap", StageAskGlobalMap},
		{"StageAskGlobalReduce", StageAskGlobalReduce},
		{"StageAskDriftPrimer", StageAskDriftPrimer},
		{"StageAskDriftLocal", StageAskDriftLocal},
		{"StageAskDriftSynth", StageAskDriftSynth},
	}
	seen := map[string]string{}
	for _, p := range pairs {
		if p.value == "" {
			t.Fatalf("%s is empty string — every stage constant must be non-empty", p.name)
		}
		if other, ok := seen[p.value]; ok {
			t.Fatalf("%s collides with %s — both have value %q", p.name, other, p.value)
		}
		seen[p.value] = p.name
	}
	// Pin the v1.5.0 literal values so the lift to constants stays byte-
	// identical with prior consumers that compared against the bare string.
	for _, want := range []struct{ name, value, expected string }{
		{"StageAsk", StageAsk, "ask"},
		{"StageReflectionDecision", StageReflectionDecision, "reflection_decision"},
		{"StageGrader", StageGrader, "grader"},
		{"StagePlanner", StagePlanner, "planner"},
		{"StageJudgeEval", StageJudgeEval, "judge_eval"},
	} {
		if want.value != want.expected {
			t.Fatalf("%s = %q, want %q (must stay byte-identical to v1.5.0 string literal)", want.name, want.value, want.expected)
		}
	}
}
