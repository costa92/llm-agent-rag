package rag

import (
	"time"

	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/store"
)

const defaultReflectionMaxRounds = 1

type reflectionDecisionResult struct {
	decision   ReflectionDecision
	reason     string
	stopReason string
	nextQuery  string
}

type reflectionRound struct {
	round      ReflectionRoundDiagnostics
	trace      ReflectionRoundTrace
	stopReason string
	metrics    metricsSnapshot
}

type metricsSnapshot struct {
	Stages []stageTimingSnapshot
	Tokens tokenUsageSnapshot
}

type stageTimingSnapshot struct {
	Stage    string
	Duration time.Duration
}

type tokenUsageSnapshot struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Estimated        bool
}

func reflectionDisabled(opts *ReflectionOptions) bool {
	return opts == nil || opts.Mode == "" || opts.Mode == ReflectionModeOff
}

func normalizeReflectionOptions(opts *ReflectionOptions) ReflectionOptions {
	if opts == nil {
		return ReflectionOptions{Mode: ReflectionModeOff}
	}
	normalized := *opts
	if normalized.MaxRounds <= 0 {
		normalized.MaxRounds = defaultReflectionMaxRounds
	}
	return normalized
}

func decideRule(roundIndex int, opts ReflectionOptions, round askRoundResult) reflectionDecisionResult {
	meetsHits := len(round.answer.Hits) >= opts.MinHits
	meetsScore := round.topScore >= opts.MinScore
	meetsDocs := round.uniqueDocCount >= opts.MinUniqueDocs
	meetsCitations := !opts.RequireCitations || len(round.answer.Citations) > 0

	if meetsHits && meetsScore && meetsDocs && meetsCitations {
		return reflectionDecisionResult{
			decision:   ReflectionDecisionStop,
			reason:     "rule thresholds satisfied",
			stopReason: "satisfied",
		}
	}
	if roundIndex >= opts.MaxRounds {
		return reflectionDecisionResult{
			decision:   ReflectionDecisionStop,
			reason:     "max rounds reached",
			stopReason: "max_rounds",
		}
	}
	return reflectionDecisionResult{
		decision:   ReflectionDecisionContinue,
		reason:     "rule thresholds not satisfied",
		stopReason: "continue",
		nextQuery:  "",
	}
}

func buildReflectionRound(mode ReflectionMode, roundIndex int, inputQuery string, round askRoundResult, decision reflectionDecisionResult) reflectionRound {
	diag := ReflectionRoundDiagnostics{
		Round:            roundIndex,
		InputQuery:       inputQuery,
		EffectiveQuery:   round.effectiveQuery,
		ReturnedChunkIDs: append([]string(nil), round.answer.Diagnostics.ReturnedChunkIDs...),
		PromptChunkIDs:   append([]string(nil), round.answer.Diagnostics.PromptChunkIDs...),
		UniqueDocCount:   round.uniqueDocCount,
		TopScore:         round.topScore,
		Decision:         decision.decision,
		DecisionMode:     mode,
		DecisionReason:   decision.reason,
	}
	trace := ReflectionRoundTrace{
		Round:            roundIndex,
		InputQuery:       inputQuery,
		EffectiveQuery:   round.effectiveQuery,
		ReturnedChunkIDs: append([]string(nil), round.answer.Diagnostics.ReturnedChunkIDs...),
		PromptChunkIDs:   append([]string(nil), round.answer.Diagnostics.PromptChunkIDs...),
		Decision:         decision.decision,
		DecisionReason:   decision.reason,
	}
	return reflectionRound{
		round:      diag,
		trace:      trace,
		stopReason: decision.stopReason,
		metrics:    snapshotMetrics(round.answer.Diagnostics.Metrics),
	}
}

func reflectionDiagnosticsFromRounds(mode ReflectionMode, rounds []reflectionRound) ReflectionDiagnostics {
	if len(rounds) == 0 {
		return ReflectionDiagnostics{}
	}
	details := make([]ReflectionRoundDiagnostics, 0, len(rounds))
	for _, round := range rounds {
		details = append(details, round.round)
	}
	last := rounds[len(rounds)-1]
	return ReflectionDiagnostics{
		Mode:         mode,
		Rounds:       len(rounds),
		AdoptedRound: last.round.Round,
		StopReason:   last.stopReason,
		RoundDetails: details,
	}
}

func reflectionTraceFromRounds(mode ReflectionMode, rounds []reflectionRound) ReflectionTrace {
	if len(rounds) == 0 {
		return ReflectionTrace{}
	}
	traceRounds := make([]ReflectionRoundTrace, 0, len(rounds))
	for _, round := range rounds {
		traceRounds = append(traceRounds, round.trace)
	}
	last := rounds[len(rounds)-1]
	return ReflectionTrace{
		Mode:         mode,
		AdoptedRound: last.trace.Round,
		StopReason:   last.stopReason,
		Rounds:       traceRounds,
	}
}

func topHitScore(hits []store.Hit) float64 {
	if len(hits) == 0 {
		return 0
	}
	return hits[0].Score
}

func uniqueDocCount(hits []store.Hit) int {
	if len(hits) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		if hit.Chunk.DocID == "" {
			continue
		}
		seen[hit.Chunk.DocID] = struct{}{}
	}
	return len(seen)
}

func snapshotMetrics(metrics obs.Metrics) metricsSnapshot {
	stages := make([]stageTimingSnapshot, 0, len(metrics.Stages))
	for _, stage := range metrics.Stages {
		stages = append(stages, stageTimingSnapshot{
			Stage:    stage.Stage,
			Duration: stage.Duration,
		})
	}
	return metricsSnapshot{
		Stages: stages,
		Tokens: tokenUsageSnapshot{
			PromptTokens:     metrics.Tokens.PromptTokens,
			CompletionTokens: metrics.Tokens.CompletionTokens,
			TotalTokens:      metrics.Tokens.TotalTokens,
			Estimated:        metrics.Tokens.Estimated,
		},
	}
}

func aggregateReflectionMetrics(rounds []reflectionRound) obs.Metrics {
	if len(rounds) == 0 {
		return obs.Metrics{}
	}
	aggregated := obs.Metrics{
		Stages: make([]obs.StageTiming, 0, len(rounds)*2),
	}
	allReported := true
	for _, round := range rounds {
		for _, stage := range round.metrics.Stages {
			aggregated.Stages = append(aggregated.Stages, obs.StageTiming{
				Stage:    stage.Stage,
				Duration: stage.Duration,
			})
		}
		aggregated.Tokens.PromptTokens += round.metrics.Tokens.PromptTokens
		aggregated.Tokens.CompletionTokens += round.metrics.Tokens.CompletionTokens
		aggregated.Tokens.TotalTokens += round.metrics.Tokens.TotalTokens
		if round.metrics.Tokens.Estimated {
			allReported = false
		}
	}
	aggregated.Tokens.Estimated = !allReported
	return aggregated
}
