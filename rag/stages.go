package rag

// Stage constants used by countingModel to tag generation calls when firing
// Observer.OnGenerateUsage and appending to obs.StageUsageAccumulator. They
// are exported as named constants so callers comparing the stage parameter in
// Observer hooks can use rag.StageAsk instead of the bare "ask" literal.
//
// v1.5.0 introduced the standard five tags as bare string literals at the
// wrapCounting call sites. v1.5.1 lifts those to named constants and adds
// five sub-stage tags for the inner Generate calls inside AskGlobal and
// AskDrift — so consumers can attribute spend across map, reduce, primer,
// local-loop, and synthesis legs separately from the top-level "ask" leg.
//
// Constants are free-form strings; the wrapCounting helper accepts any
// string, so callers may extend with their own tags. The set below is the
// SDK-shipped standard.
const (
	// StageAsk tags the top-level System.Ask generation call. Its value is
	// byte-identical to the v1.5.0 string literal "ask".
	StageAsk = "ask"

	// StageReflectionDecision tags the reflection-decision leg used by
	// ReflectionModeModel. Byte-identical to the v1.5.0 literal.
	StageReflectionDecision = "reflection_decision"

	// StageGrader tags the PromptGrader's per-chunk Generate calls when
	// reflection's EnableChunkGrading is set. Byte-identical to the
	// v1.5.0 literal.
	StageGrader = "grader"

	// StagePlanner tags the PromptQueryPlanner's follow-up-emission
	// Generate calls when reflection's EnableActiveRetrieval is set.
	// Byte-identical to the v1.5.0 literal.
	StagePlanner = "planner"

	// StageJudgeEval tags eval.NewCostObservingJudge's wrapped Generate
	// calls. The constant lives in the rag package so callers comparing
	// rag.Observer.OnGenerateUsage stages in one place can reference all
	// SDK-shipped tags from a single import. The literal "judge_eval" is
	// emitted by eval/instrument.go and eval/judge.go.
	StageJudgeEval = "judge_eval"

	// StageAskGlobalMap (v1.5.1) tags each per-community map-step
	// Generate call inside System.AskGlobal. Previously emitted under
	// StageAsk in v1.5.0.
	StageAskGlobalMap = "global_map"

	// StageAskGlobalReduce (v1.5.1) tags the single reduce-step Generate
	// call inside System.AskGlobal. Previously emitted under StageAsk in
	// v1.5.0.
	StageAskGlobalReduce = "global_reduce"

	// StageAskDriftPrimer (v1.5.1) tags each per-community primer-pass
	// Generate call inside System.AskDrift. Previously emitted under
	// StageAsk in v1.5.0.
	StageAskDriftPrimer = "drift_primer"

	// StageAskDriftLocal (v1.5.1) tags each local-follow-up-round
	// Generate call inside System.AskDrift. Previously emitted under
	// StageAsk in v1.5.0.
	StageAskDriftLocal = "drift_local"

	// StageAskDriftSynth (v1.5.1) tags the single synthesis-step
	// Generate call inside System.AskDrift. Previously emitted under
	// StageAsk in v1.5.0.
	StageAskDriftSynth = "drift_synth"
)
