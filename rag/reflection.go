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

const (
	defaultReflectionMaxRounds        = 1
	defaultAdaptiveRetrievalThreshold = 0.6
)

// effectiveAdaptiveThreshold returns the configured threshold or the
// SDK default (0.6) when the caller left it at its zero value. The
// default is documented on ReflectionOptions.AdaptiveRetrievalThreshold.
func effectiveAdaptiveThreshold(opts ReflectionOptions) float64 {
	if opts.AdaptiveRetrievalThreshold > 0 {
		return opts.AdaptiveRetrievalThreshold
	}
	return defaultAdaptiveRetrievalThreshold
}

type reflectionDecisionResult struct {
	decision   ReflectionDecision
	reason     string
	stopReason string
	nextQuery  string
	mode       ReflectionMode
	metrics    metricsSnapshot
	// rawText is the model's verbatim reflection-decision reply. It is
	// empty when no model decision was made (rule mode, or hybrid mode
	// rule-path stops).
	rawText string
	// decisionPrompt is the user-content of the reflection decision
	// prompt sent to the model. It is empty when no model decision was
	// made.
	decisionPrompt string
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

// gradeRound calls the configured Grader for every hit packed into the
// round's prompt. It returns the per-chunk scores in the same order as
// round.answer.Hits. Grading is best-effort: any per-call error
// downgrades that chunk to a neutral 0.5 with an error reason so a
// flaky grader never breaks the Ask call (fail-open). gradeRound also
// returns the max relevance score seen — used by adaptive retrieval.
func gradeRound(ctx context.Context, grader Grader, query string, round askRoundResult) ([]ChunkScore, float64) {
	if grader == nil || len(round.answer.Hits) == 0 {
		return nil, 0
	}
	scores := make([]ChunkScore, 0, len(round.answer.Hits))
	maxRel := 0.0
	for _, hit := range round.answer.Hits {
		rel, relReason, err := grader.ScoreRelevance(ctx, query, hit)
		if err != nil {
			rel = 0.5
			relReason = "grader error: " + err.Error()
		}
		sup, supReason, err := grader.ScoreSupport(ctx, round.answer.Text, hit)
		if err != nil {
			sup = 0.5
			supReason = "grader error: " + err.Error()
		}
		if rel > maxRel {
			maxRel = rel
		}
		reason := relReason
		if supReason != "" {
			if reason != "" {
				reason = reason + " | " + supReason
			} else {
				reason = supReason
			}
		}
		scores = append(scores, ChunkScore{
			HitID:     hit.Chunk.ID,
			Relevance: rel,
			Support:   sup,
			Reason:    reason,
		})
	}
	return scores, maxRel
}

// roundAggregateScore returns the weighted aggregate score for a
// reflectionRound used by SelectionModeBestByScore. The weights default
// to 0.5/0.5 when both are <= 0. An empty ChunkScores slice yields 0.
func roundAggregateScore(rr reflectionRound, relWeight, supWeight float64) float64 {
	if len(rr.round.ChunkScores) == 0 {
		return 0
	}
	if relWeight <= 0 && supWeight <= 0 {
		relWeight = 0.5
		supWeight = 0.5
	}
	var relSum, supSum float64
	for _, cs := range rr.round.ChunkScores {
		relSum += cs.Relevance
		supSum += cs.Support
	}
	n := float64(len(rr.round.ChunkScores))
	return relWeight*(relSum/n) + supWeight*(supSum/n)
}

// attachChunkScores writes scores onto both the diagnostic and trace
// sides of a reflectionRound. Empty scores are stored as nil so the
// zero-value contract for ungraded rounds is preserved.
func attachChunkScores(rr reflectionRound, scores []ChunkScore) reflectionRound {
	if len(scores) == 0 {
		return rr
	}
	cloned := make([]ChunkScore, len(scores))
	copy(cloned, scores)
	rr.round.ChunkScores = cloned
	// Trace gets an independent slice copy so observers can't mutate the
	// diagnostic side by accident.
	traceCopy := make([]ChunkScore, len(scores))
	copy(traceCopy, scores)
	rr.trace.ChunkScores = traceCopy
	return rr
}

// pickBestRound selects the round with the highest weighted aggregate
// ChunkScores. Ties are broken by earliest round index, so a degenerate
// "all zero" case still produces a deterministic pick — matching the
// last-round default's determinism. Returns the input as-is when there
// is one or zero rounds.
func pickBestRound(rounds []reflectionRound, relWeight, supWeight float64) int {
	if len(rounds) <= 1 {
		return len(rounds) - 1
	}
	bestIdx := 0
	bestScore := roundAggregateScore(rounds[0], relWeight, supWeight)
	for i := 1; i < len(rounds); i++ {
		s := roundAggregateScore(rounds[i], relWeight, supWeight)
		if s > bestScore {
			bestScore = s
			bestIdx = i
		}
	}
	return bestIdx
}

func buildReflectionRound(mode ReflectionMode, roundIndex int, inputQuery string, round askRoundResult, decision reflectionDecisionResult) reflectionRound {
	decisionMode := mode
	if decision.mode != "" {
		decisionMode = decision.mode
	}
	autoRoute := append([]string(nil), round.answer.Trace.AutoRoutePath...)
	diag := ReflectionRoundDiagnostics{
		Round:               roundIndex,
		InputQuery:          inputQuery,
		EffectiveQuery:      round.effectiveQuery,
		RewrittenQuery:      decision.nextQuery,
		ReturnedChunkIDs:    append([]string(nil), round.answer.Diagnostics.ReturnedChunkIDs...),
		PromptChunkIDs:      append([]string(nil), round.answer.Diagnostics.PromptChunkIDs...),
		UniqueDocCount:      round.uniqueDocCount,
		TopScore:            round.topScore,
		Decision:            decision.decision,
		DecisionMode:        decisionMode,
		DecisionReason:      decision.reason,
		RawDecisionText:     decision.rawText,
		DecisionPrompt:      decision.decisionPrompt,
		RoutePath:           append([]string(nil), round.answer.Trace.RoutePath...),
		AutoRoutePath:       append([]string(nil), autoRoute...),
		AutoRouteCandidates: cloneAskRouteCandidates(round.answer.Trace.AutoRouteCandidates),
		SearchTrajectory:    cloneTrajectory(round.answer.Trace.SearchTrajectory),
		GraphTrace:          round.answer.Diagnostics.GraphTrace,
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
		RawDecisionText:  decision.rawText,
		AutoRoutePath:    append([]string(nil), autoRoute...),
	}
	return reflectionRound{
		round:      diag,
		trace:      trace,
		stopReason: decision.stopReason,
		metrics:    combineMetricsSnapshots(snapshotMetrics(round.answer.Diagnostics.Metrics), decision.metrics),
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

func combineMetricsSnapshots(left metricsSnapshot, right metricsSnapshot) metricsSnapshot {
	combined := metricsSnapshot{
		Stages: append(append(make([]stageTimingSnapshot, 0, len(left.Stages)+len(right.Stages)), left.Stages...), right.Stages...),
		Tokens: tokenUsageSnapshot{
			PromptTokens:     left.Tokens.PromptTokens + right.Tokens.PromptTokens,
			CompletionTokens: left.Tokens.CompletionTokens + right.Tokens.CompletionTokens,
			TotalTokens:      left.Tokens.TotalTokens + right.Tokens.TotalTokens,
			Estimated:        left.Tokens.Estimated || right.Tokens.Estimated,
		},
	}
	return combined
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

func mergeMetrics(base obs.Metrics, calls obs.CallCounts, totalDuration time.Duration) obs.Metrics {
	base.Calls = calls
	base.TotalDuration = totalDuration
	return base
}

func clampDecisionAtMaxRounds(decision reflectionDecisionResult, mode ReflectionMode) reflectionDecisionResult {
	decision.decision = ReflectionDecisionStop
	decision.reason = "max rounds reached"
	decision.stopReason = "max_rounds"
	decision.mode = mode
	return decision
}

func decideWithModel(ctx context.Context, model generate.Model, originalQuestion string, opts ReflectionOptions, round askRoundResult) (reflectionDecisionResult, error) {
	promptContent := reflectionDecisionPrompt(originalQuestion, opts, round)
	req := generate.Request{
		SystemPrompt: "You decide whether a self-RAG system should stop, continue, or rewrite before continuing.",
		Messages: []generate.Message{{
			Role:    "user",
			Content: promptContent,
		}},
	}
	start := time.Now()
	resp, err := model.Generate(ctx, req)
	if err != nil {
		return reflectionDecisionResult{}, err
	}
	usage := deriveTokenUsage(req, resp)
	decision, reason, rewrite, err := parseReflectionDecision(resp.Text)
	if err != nil {
		return reflectionDecisionResult{}, err
	}
	if reason == "" {
		reason = "model decision"
	}
	result := reflectionDecisionResult{
		decision:       decision,
		reason:         reason,
		nextQuery:      strings.TrimSpace(rewrite),
		mode:           ReflectionModeModel,
		rawText:        resp.Text,
		decisionPrompt: promptContent,
		metrics: metricsSnapshot{
			Stages: []stageTimingSnapshot{{
				Stage:    "reflect",
				Duration: time.Since(start),
			}},
			Tokens: tokenUsageSnapshot{
				PromptTokens:     usage.PromptTokens,
				CompletionTokens: usage.CompletionTokens,
				TotalTokens:      usage.TotalTokens,
				Estimated:        usage.Estimated,
			},
		},
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
			// Normalize the value to lowercase before enum match so the
			// parser accepts model drift on capitalization (Stop, STOP,
			// Rewrite_and_continue, etc.). The protocol documented in
			// reflectionDecisionPrompt still asks for lowercase; this is
			// just defensive robustness for real-world model output.
			normalized := ReflectionDecision(strings.ToLower(value))
			switch normalized {
			case ReflectionDecisionStop, ReflectionDecisionContinue, ReflectionDecisionRewriteAndContinue:
				decision = normalized
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
