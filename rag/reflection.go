package rag

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/store"
)

const defaultReflectionMaxRounds = 1

type reflectionDecisionResult struct {
	decision   ReflectionDecision
	reason     string
	stopReason string
	nextQuery  string
	mode       ReflectionMode
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

type modelReflectionOutcome struct {
	decision reflectionDecisionResult
	stop     bool
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
			mode:       ReflectionModeRule,
		}
	}
	if roundIndex >= opts.MaxRounds {
		return reflectionDecisionResult{
			decision:   ReflectionDecisionStop,
			reason:     "max rounds reached",
			stopReason: "max_rounds",
			mode:       ReflectionModeRule,
		}
	}
	return reflectionDecisionResult{
		decision:   ReflectionDecisionContinue,
		reason:     "rule thresholds not satisfied",
		stopReason: "continue",
		nextQuery:  "",
		mode:       ReflectionModeRule,
	}
}

func buildReflectionRound(mode ReflectionMode, roundIndex int, inputQuery string, round askRoundResult, decision reflectionDecisionResult) reflectionRound {
	decisionMode := mode
	if decision.mode != "" {
		decisionMode = decision.mode
	}
	diag := ReflectionRoundDiagnostics{
		Round:            roundIndex,
		InputQuery:       inputQuery,
		EffectiveQuery:   round.effectiveQuery,
		RewrittenQuery:   decision.nextQuery,
		ReturnedChunkIDs: append([]string(nil), round.answer.Diagnostics.ReturnedChunkIDs...),
		PromptChunkIDs:   append([]string(nil), round.answer.Diagnostics.PromptChunkIDs...),
		UniqueDocCount:   round.uniqueDocCount,
		TopScore:         round.topScore,
		Decision:         decision.decision,
		DecisionMode:     decisionMode,
		DecisionReason:   decision.reason,
	}
	trace := ReflectionRoundTrace{
		Round:            roundIndex,
		InputQuery:       inputQuery,
		EffectiveQuery:   round.effectiveQuery,
		RewrittenQuery:   decision.nextQuery,
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

func reflectionDiagnosticsFromRounds(mode ReflectionMode, rounds []reflectionRound, decisionModelCalls int) ReflectionDiagnostics {
	if len(rounds) == 0 {
		return ReflectionDiagnostics{}
	}
	details := make([]ReflectionRoundDiagnostics, 0, len(rounds))
	for _, round := range rounds {
		details = append(details, round.round)
	}
	last := rounds[len(rounds)-1]
	return ReflectionDiagnostics{
		Mode:               mode,
		Rounds:             len(rounds),
		AdoptedRound:       last.round.Round,
		StopReason:         last.stopReason,
		DecisionModelCalls: decisionModelCalls,
		RoundDetails:       details,
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

func decideWithModel(ctx context.Context, model generate.Model, originalQuestion string, opts ReflectionOptions, round askRoundResult) (reflectionDecisionResult, error) {
	req := generate.Request{
		SystemPrompt: "You decide whether a self-RAG system should stop, continue, or rewrite before continuing.",
		Messages: []generate.Message{{
			Role:    "user",
			Content: reflectionDecisionPrompt(originalQuestion, opts, round),
		}},
	}
	resp, err := model.Generate(ctx, req)
	if err != nil {
		return reflectionDecisionResult{}, err
	}
	decision, reason, rewrite, err := parseReflectionDecision(resp.Text)
	if err != nil {
		return reflectionDecisionResult{}, err
	}
	if reason == "" {
		reason = "model decision"
	}
	result := reflectionDecisionResult{
		decision:  decision,
		reason:    reason,
		nextQuery: strings.TrimSpace(rewrite),
		mode:      ReflectionModeModel,
	}
	switch decision {
	case ReflectionDecisionStop:
		result.stopReason = "model_stop"
	case ReflectionDecisionContinue:
		result.stopReason = "continue"
	case ReflectionDecisionRewriteAndContinue:
		result.stopReason = "continue"
		if !opts.AllowRewrite || result.nextQuery == "" {
			result.decision = ReflectionDecisionContinue
			result.nextQuery = ""
			result.reason = "model requested rewrite but rewrite is unavailable"
		}
	default:
		result.decision = ReflectionDecisionStop
		result.stopReason = "model_stop"
	}
	return result, nil
}

func resolveModelRewrite(currentQuery string, decision reflectionDecisionResult) modelReflectionOutcome {
	if decision.decision != ReflectionDecisionRewriteAndContinue {
		return modelReflectionOutcome{decision: decision}
	}
	if strings.TrimSpace(decision.nextQuery) == strings.TrimSpace(currentQuery) {
		decision.decision = ReflectionDecisionStop
		decision.reason = "rewrite unchanged from current query"
		decision.stopReason = "rewrite_unchanged"
		decision.nextQuery = ""
		return modelReflectionOutcome{
			decision: decision,
			stop:     true,
		}
	}
	return modelReflectionOutcome{decision: decision}
}

func stopForUnchangedEvidence(currentRound askRoundResult, previousRound *askRoundResult) (reflectionDecisionResult, bool) {
	if previousRound == nil {
		return reflectionDecisionResult{}, false
	}
	if !evidenceSetsEqual(previousRound.answer.Diagnostics.PromptChunkIDs, currentRound.answer.Diagnostics.PromptChunkIDs) {
		return reflectionDecisionResult{}, false
	}
	return reflectionDecisionResult{
		decision:   ReflectionDecisionStop,
		reason:     "evidence set unchanged from previous round",
		stopReason: "evidence_unchanged",
		mode:       ReflectionModeModel,
	}, true
}

func orchestrateModelReflection(currentQuery string, decision reflectionDecisionResult) modelReflectionOutcome {
	outcome := resolveModelRewrite(currentQuery, decision)
	return outcome
}

func reflectionDecisionPrompt(originalQuestion string, opts ReflectionOptions, round askRoundResult) string {
	return fmt.Sprintf(
		"Original question: %s\nCurrent answer: %s\nRetrieved hits: %d\nUnique docs: %d\nTop score: %.6f\nCitations: %d\nAllow rewrite: %t\n\nRespond with lines:\ndecision=<stop|continue|rewrite_and_continue>\nreason=<short reason>\nrewrite=<next query, only when rewriting>",
		originalQuestion,
		round.answer.Text,
		len(round.answer.Hits),
		round.uniqueDocCount,
		round.topScore,
		len(round.answer.Citations),
		opts.AllowRewrite,
	)
}

func parseReflectionDecision(text string) (ReflectionDecision, string, string, error) {
	var (
		decision ReflectionDecision
		reason   string
		rewrite  string
		found    bool
	)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "decision":
			found = true
			switch ReflectionDecision(value) {
			case ReflectionDecisionStop, ReflectionDecisionContinue, ReflectionDecisionRewriteAndContinue:
				decision = ReflectionDecision(value)
			default:
				return "", "", "", fmt.Errorf("invalid reflection decision %q", value)
			}
		case "reason":
			reason = value
		case "rewrite":
			rewrite = value
		}
	}
	if !found {
		return "", "", "", fmt.Errorf("missing reflection decision")
	}
	return decision, reason, rewrite, nil
}

func evidenceSetsEqual(left, right []string) bool {
	leftSet := make(map[string]struct{}, len(left))
	for _, id := range left {
		leftSet[id] = struct{}{}
	}
	rightSet := make(map[string]struct{}, len(right))
	for _, id := range right {
		rightSet[id] = struct{}{}
	}
	if len(leftSet) != len(rightSet) {
		return false
	}
	for id := range leftSet {
		if _, ok := rightSet[id]; !ok {
			return false
		}
	}
	return true
}
