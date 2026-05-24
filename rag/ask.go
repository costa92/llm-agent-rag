package rag

import (
	"context"
	"strings"
	"time"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/pack"
	"github.com/costa92/llm-agent-rag/prompt"
	"github.com/costa92/llm-agent-rag/rerank"
	retrievepolicy "github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

type askRoundResult struct {
	answer         Answer
	effectiveQuery string
	topScore       float64
	uniqueDocCount int
	// followupQueries are the active-retrieval follow-up search queries
	// the QueryPlanner emitted for this round (nil when active retrieval
	// did not fire). The outer reflection loop plumbs them through
	// buildReflectionRound to ReflectionRoundDiagnostics.FollowupQueries.
	followupQueries []string
	// preGradedRelevance carries the per-chunk relevance scores active
	// retrieval already computed on the seed hit set (Q-D). When non-nil,
	// the outer grading step skips a second ScoreRelevance pass for the
	// seed IDs — ScoreSupport still runs because it requires the
	// generated answer text.
	preGradedRelevance []ChunkScore
}

// Ask runs the standard retrieve-pack-generate answer path: it retrieves
// context for question, packs it into a prompt, and generates an Answer.
func (s *System) Ask(ctx context.Context, question string, opts AskOptions) (Answer, error) {
	if s.model == nil {
		return Answer{}, ErrModelRequired
	}
	// Ask is always top-level: install a fresh obs.Counter so embedding
	// and generation calls — including those nested in retrieve and the
	// preprocessor — are counted, and time each stage. Alongside, install
	// a fresh obs.StageUsageAccumulator (v1.5.0) so every Generate call
	// through countingModel records a StageTokenUsage entry.
	counter := obs.NewCounter()
	ctx = obs.WithCounter(ctx, counter)
	stageUsage := obs.NewStageUsageAccumulator()
	ctx = obs.WithStageUsage(ctx, stageUsage)
	// v1.7.0 C2: install the cumulative-token budget on ctx when
	// AskOptions.MaxTotalTokens > 0. Zero (the default) leaves ctx
	// unchanged — preserving v1.6.0 behavior byte-for-byte. The
	// countingModel installs a post-Append budget check; enforcement
	// is wired in v1.7.0 commit 10.
	if opts.MaxTotalTokens > 0 {
		ctx = obs.WithTokenBudget(ctx, opts.MaxTotalTokens)
	}
	askStart := time.Now()

	var (
		round askRoundResult
		err   error
	)
	if reflectionDisabled(opts.Reflection) {
		// No reflection: pass a zero-value (disabled) budget. Active
		// retrieval requires reflection to be configured.
		round, err = s.askRound(ctx, question, question, opts, activeRetrievalBudget{})
	} else {
		reflection := normalizeReflectionOptions(opts.Reflection)
		// Build the active-retrieval budget once per Ask call. The
		// remaining/used pointers thread the global per-Ask budget
		// through every round so decrements compose correctly.
		// prevAnswerText is updated after each round (commit 6 wires the
		// rewrite composition; commit 5 keeps it empty before the first
		// round and updates as rounds run for rule/model/hybrid modes).
		//
		// Resolve the per-Ask QueryPlanner override (v1.2.1): the
		// per-Ask AskOptions.QueryPlanner takes precedence over
		// Options.QueryPlanner. When both are nil, effectiveQueryPlanner
		// returns NoopQueryPlanner so active retrieval still wires
		// without panic.
		resolvedPlanner := opts.QueryPlanner
		if resolvedPlanner == nil {
			resolvedPlanner = s.effectiveQueryPlanner()
		}
		// activeEnabled tracks whether reflection asked for active
		// retrieval AND a non-Noop planner is wired (per-Ask override
		// or system-level). When neither slot has a real planner we
		// behave like v1.2.0: trigger short-circuits, planner is never
		// consulted, no follow-ups fire.
		_, isNoop := resolvedPlanner.(NoopQueryPlanner)
		activeEnabled := reflection.EnableActiveRetrieval && !isNoop
		perRoundCap, globalCap, floor := effectiveActiveRetrievalConfig(reflection)
		var (
			followupsUsedThisAsk int
			followupsRemaining   int
			prevAnswerText       string
		)
		if activeEnabled {
			followupsRemaining = globalCap
		}
		makeBudget := func() activeRetrievalBudget {
			if !activeEnabled {
				return activeRetrievalBudget{}
			}
			return activeRetrievalBudget{
				enabled:         true,
				perRoundCap:     perRoundCap,
				globalRemaining: &followupsRemaining,
				floor:           floor,
				planner:         resolvedPlanner,
				used:            &followupsUsedThisAsk,
				prevAnswer:      prevAnswerText,
				// v1.2.1 parallel dispatch — defaults zero (sequential).
				parallel:    reflection.ParallelFollowups,
				concurrency: reflection.MaxFollowupConcurrency,
			}
		}
		switch reflection.Mode {
		case ReflectionModeRule:
			query := question
			var rounds []reflectionRound
			var roundResults []askRoundResult
			adaptiveBudget := 0
			if reflection.AdaptiveRetrieval && reflection.EnableChunkGrading {
				adaptiveBudget = 1
			}
			for roundIndex := 1; roundIndex <= reflection.MaxRounds; roundIndex++ {
				round, err = s.askRound(ctx, question, query, opts, makeBudget())
				if err != nil {
					return Answer{}, err
				}
				decision := decideRule(roundIndex, reflection, round)
				built := buildReflectionRound(reflection.Mode, roundIndex, query, round, decision, round.followupQueries)
				var maxRel float64
				if reflection.EnableChunkGrading {
					var scores []ChunkScore
					scores, maxRel = gradeRound(ctx, s.effectiveGrader(), query, round)
					built = attachChunkScores(built, scores)
				}
				rounds = append(rounds, built)
				roundResults = append(roundResults, round)
				// Thread prev answer into the next round's budget so
				// commit-6 cross-round composition has the latest text.
				prevAnswerText = round.answer.Text
				if decision.decision == ReflectionDecisionStop &&
					reflection.AdaptiveRetrieval &&
					reflection.EnableChunkGrading &&
					adaptiveBudget > 0 &&
					roundIndex < reflection.MaxRounds &&
					maxRel < effectiveAdaptiveThreshold(reflection) {
					adaptiveBudget--
					query = question
					continue
				}
				if decision.decision == ReflectionDecisionStop {
					break
				}
				query = question
			}
			adoptedIdx := len(rounds) - 1
			if reflection.SelectionMode == SelectionModeBestByScore && len(rounds) > 1 {
				adoptedIdx = pickBestRound(rounds, reflection.GraderRelevanceWeight, reflection.GraderSupportWeight)
				round = roundResults[adoptedIdx]
			}
			answer := round.answer
			metrics := aggregateReflectionMetrics(rounds)
			metrics.Calls = answer.Diagnostics.Metrics.Calls
			metrics.TotalDuration = answer.Diagnostics.Metrics.TotalDuration
			answer.Diagnostics.Metrics = metrics
			answer.Diagnostics.Reflection = reflectionDiagnosticsFromRounds(reflection.Mode, rounds, 0)
			answer.Diagnostics.Reflection.AdoptedRound = rounds[adoptedIdx].round.Round
			answer.Diagnostics.Reflection.FollowupQueriesUsed = followupsUsedThisAsk
			answer.Trace.Reflection = reflectionTraceFromRounds(reflection.Mode, rounds)
			answer.Trace.Reflection.AdoptedRound = rounds[adoptedIdx].trace.Round
			round.answer = answer
		case ReflectionModeModel, ReflectionModeHybrid:
			query := question
			var rounds []reflectionRound
			var roundResults []askRoundResult
			var previousRound *askRoundResult
			decisionModelCalls := 0
			adaptiveBudget := 0
			if reflection.AdaptiveRetrieval && reflection.EnableChunkGrading {
				adaptiveBudget = 1
			}
			appendRound := func(built reflectionRound, rr askRoundResult) (float64, reflectionRound) {
				var maxRel float64
				if reflection.EnableChunkGrading {
					var scores []ChunkScore
					scores, maxRel = gradeRound(ctx, s.effectiveGrader(), query, rr)
					built = attachChunkScores(built, scores)
				}
				rounds = append(rounds, built)
				roundResults = append(roundResults, rr)
				return maxRel, built
			}
			for roundIndex := 1; roundIndex <= reflection.MaxRounds; roundIndex++ {
				round, err = s.askRound(ctx, question, query, opts, makeBudget())
				if err != nil {
					return Answer{}, err
				}
				var decision reflectionDecisionResult
				if reflection.Mode == ReflectionModeHybrid {
					ruleDecision := decideRule(roundIndex, reflection, round)
					if ruleDecision.decision == ReflectionDecisionStop {
						ruleDecision.mode = ReflectionModeHybrid
						decision = ruleDecision
					} else if unchangedDecision, stop := stopForUnchangedEvidence(round, previousRound); stop {
						unchangedDecision.mode = ReflectionModeHybrid
						decision = unchangedDecision
					} else {
						decisionModelCalls++
						decision, err = decideWithModel(ctx, s.reflectionModel, question, reflection, round)
						if err != nil {
							if reflection.FailOpen {
								decision = reflectionDecisionResult{
									decision:   ReflectionDecisionStop,
									reason:     "reflection decision failed; returning usable answer",
									stopReason: "decision_error",
									mode:       ReflectionModeHybrid,
								}
								appendRound(buildReflectionRound(reflection.Mode, roundIndex, query, round, decision, round.followupQueries), round)
								break
							}
							return Answer{}, err
						}
						outcome := orchestrateModelReflection(query, decision)
						decision = outcome.decision
						if roundIndex >= reflection.MaxRounds && decision.decision != ReflectionDecisionStop {
							decision = clampDecisionAtMaxRounds(decision, ReflectionModeHybrid)
						}
					}
				} else {
					if unchangedDecision, stop := stopForUnchangedEvidence(round, previousRound); stop {
						decision = unchangedDecision
					} else {
						decisionModelCalls++
						decision, err = decideWithModel(ctx, s.reflectionModel, question, reflection, round)
						if err != nil {
							if reflection.FailOpen {
								decision = reflectionDecisionResult{
									decision:   ReflectionDecisionStop,
									reason:     "reflection decision failed; returning usable answer",
									stopReason: "decision_error",
									mode:       ReflectionModeModel,
								}
								appendRound(buildReflectionRound(reflection.Mode, roundIndex, query, round, decision, round.followupQueries), round)
								break
							}
							return Answer{}, err
						}
						outcome := orchestrateModelReflection(query, decision)
						decision = outcome.decision
						if roundIndex >= reflection.MaxRounds && decision.decision != ReflectionDecisionStop {
							decision = clampDecisionAtMaxRounds(decision, ReflectionModeModel)
						}
					}
				}
				maxRel, _ := appendRound(buildReflectionRound(reflection.Mode, roundIndex, query, round, decision, round.followupQueries), round)
				currentRound := round
				previousRound = &currentRound
				prevAnswerText = round.answer.Text
				if decision.decision == ReflectionDecisionStop &&
					reflection.AdaptiveRetrieval &&
					reflection.EnableChunkGrading &&
					adaptiveBudget > 0 &&
					roundIndex < reflection.MaxRounds &&
					maxRel < effectiveAdaptiveThreshold(reflection) {
					adaptiveBudget--
					query = question
					continue
				}
				if decision.decision == ReflectionDecisionStop {
					break
				}
				if decision.decision == ReflectionDecisionRewriteAndContinue && decision.nextQuery != "" {
					query = decision.nextQuery
					continue
				}
				query = question
			}
			adoptedIdx := len(rounds) - 1
			if reflection.SelectionMode == SelectionModeBestByScore && len(rounds) > 1 {
				adoptedIdx = pickBestRound(rounds, reflection.GraderRelevanceWeight, reflection.GraderSupportWeight)
				round = roundResults[adoptedIdx]
			}
			answer := round.answer
			metrics := aggregateReflectionMetrics(rounds)
			metrics.Calls = answer.Diagnostics.Metrics.Calls
			metrics.TotalDuration = answer.Diagnostics.Metrics.TotalDuration
			answer.Diagnostics.Metrics = metrics
			answer.Diagnostics.Reflection = reflectionDiagnosticsFromRounds(reflection.Mode, rounds, decisionModelCalls)
			if len(rounds) > 0 {
				answer.Diagnostics.Reflection.AdoptedRound = rounds[adoptedIdx].round.Round
			}
			answer.Diagnostics.Reflection.FollowupQueriesUsed = followupsUsedThisAsk
			if err != nil && reflection.FailOpen && len(rounds) > 0 {
				answer.Diagnostics.Reflection.FailureFallback = true
				answer.Diagnostics.Reflection.FailureReason = err.Error()
				err = nil
			}
			answer.Trace.Reflection = reflectionTraceFromRounds(reflection.Mode, rounds)
			if len(rounds) > 0 {
				answer.Trace.Reflection.AdoptedRound = rounds[adoptedIdx].trace.Round
			}
			round.answer = answer
		default:
			round, err = s.askRound(ctx, question, question, opts, makeBudget())
			if err != nil {
				return Answer{}, err
			}
			singleRound := buildReflectionRound(reflection.Mode, 1, question, round, reflectionDecisionResult{
				decision:   ReflectionDecisionStop,
				reason:     "reflection mode not implemented",
				stopReason: "mode_not_implemented",
			}, nil)
			answer := round.answer
			answer.Diagnostics.Reflection = reflectionDiagnosticsFromRounds(reflection.Mode, []reflectionRound{singleRound}, 0)
			answer.Trace.Reflection = reflectionTraceFromRounds(reflection.Mode, []reflectionRound{singleRound})
			round.answer = answer
		}
	}
	if err != nil {
		return Answer{}, err
	}
	answer := round.answer
	answer.Diagnostics.Metrics = mergeMetrics(answer.Diagnostics.Metrics, counter.Counts(), time.Since(askStart))
	answer.Diagnostics.Metrics.StageTokenUsage = stageUsage.Snapshot()
	if s.observer.OnAsk != nil {
		s.observer.OnAsk(ctx, answer.Trace)
	}
	return answer, nil
}

func (s *System) askRound(ctx context.Context, originalQuestion string, retrievalQuery string, opts AskOptions, budget activeRetrievalBudget) (askRoundResult, error) {
	metrics := obs.Metrics{}

	stageStart := time.Now()
	hits, retrieveTrace, err := s.retrieve(ctx, retrievalQuery, opts.Search)
	metrics.Stages = append(metrics.Stages, obs.StageTiming{Stage: "retrieve", Duration: time.Since(stageStart)})
	if err != nil {
		return askRoundResult{}, err
	}
	// Active retrieval (v1.2.0): when EnableActiveRetrieval is set on
	// ReflectionOptions, fire one follow-up retrieval pass before
	// rerank/pack/generate. The driver is a no-op when budget.enabled is
	// false, the planner is unconfigured, the global per-Ask budget is
	// exhausted, or the seed retrieval's max relevance is already above
	// the configured floor. Sequential dispatch — deterministic for
	// tests.
	var activeOutcome activeRetrievalOutcome
	if budget.enabled {
		activeOutcome = s.runActiveRetrieval(ctx, originalQuestion, hits, opts.Search, budget)
		hits = activeOutcome.hits
	}
	tpl := opts.Template
	if tpl == nil {
		tpl = s.template
	}
	rankedHits := hits
	rerankedIDs := chunkIDs(rankedHits)
	var rerankScores []rerank.RerankScore
	if opts.Search.EnableRerank && s.reranker != nil {
		var rerankTrace rerank.Trace
		stageStart = time.Now()
		rankedHits, rerankTrace, err = s.reranker.Rerank(ctx, rerank.Request{
			Query: originalQuestion,
			Hits:  hits,
		})
		metrics.Stages = append(metrics.Stages, obs.StageTiming{Stage: "rerank", Duration: time.Since(stageStart)})
		if err != nil {
			return askRoundResult{}, err
		}
		rerankedIDs = append([]string(nil), rerankTrace.OutputChunkIDs...)
		rerankScores = append([]rerank.RerankScore(nil), rerankTrace.Scores...)
	}
	packedHits := rankedHits
	packedIDs := chunkIDs(packedHits)
	var droppedIDs []string
	matchedSections := traceOrFallbackSections(retrieveTrace, rankedHits)
	searchPath := traceOrFallbackSearchPath(retrieveTrace, rankedHits)
	expandedSections := append([]string(nil), retrieveTrace.ExpandedSections...)
	expandedChunkIDs := append([]string(nil), retrieveTrace.ExpandedChunkIDs...)
	if s.packer != nil {
		stageStart = time.Now()
		packed, err := s.packer.Pack(ctx, pack.Request{
			Question:  originalQuestion,
			Hits:      rankedHits,
			MaxTokens: opts.MaxTokens,
		})
		metrics.Stages = append(metrics.Stages, obs.StageTiming{Stage: "pack", Duration: time.Since(stageStart)})
		if err != nil {
			return askRoundResult{}, err
		}
		packedHits = packed.Hits
		packedIDs = append([]string(nil), packed.Trace.SelectedChunkIDs...)
		droppedIDs = append([]string(nil), packed.Trace.DroppedChunkIDs...)
	}
	var injectionFindings []InjectionFinding
	if s.injectionScanner != nil {
		packedHits, injectionFindings = s.sanitizeHits(packedHits)
		packedIDs = chunkIDs(packedHits)
	}
	req, err := tpl.Render(ctx, prompt.RenderContext{
		Question:  originalQuestion,
		Namespace: opts.Search.Namespace,
		Hits:      packedHits,
		Metadata:  opts.Metadata,
	})
	if err != nil {
		return askRoundResult{}, err
	}
	stageStart = time.Now()
	resp, err := s.model.Generate(ctx, req)
	metrics.Stages = append(metrics.Stages, obs.StageTiming{Stage: "generate", Duration: time.Since(stageStart)})
	if err != nil {
		return askRoundResult{}, err
	}
	metrics.Tokens = deriveTokenUsage(req, resp)
	ids := make([]string, 0, len(packedHits))
	citations := make([]Citation, 0, len(packedHits))
	for _, hit := range packedHits {
		ids = append(ids, hit.Chunk.ID)
		citations = append(citations, Citation{
			ChunkID:     hit.Chunk.ID,
			DocID:       hit.Chunk.DocID,
			Namespace:   hit.Chunk.Namespace,
			Title:       hit.Chunk.Title,
			SectionID:   hit.Chunk.SectionID,
			SectionPath: append([]string(nil), hit.Chunk.SectionPath...),
			Score:       hit.Score,
		})
	}
	answer := Answer{
		Text:      resp.Text,
		Hits:      packedHits,
		Prompt:    req,
		Citations: citations,
		Diagnostics: Diagnostics{
			HitCount:            len(hits),
			ReturnedChunkIDs:    append([]string(nil), chunkIDs(hits)...),
			PromptChunkIDs:      append([]string(nil), ids...),
			MatchedSections:     append([]string(nil), matchedSections...),
			ExpandedChunkIDs:    append([]string(nil), expandedChunkIDs...),
			AutoRouteCandidates: cloneAskRouteCandidates(retrieveTrace.AutoRouteCandidates),
			RoutePolicy:         retrieveTrace.RoutePolicy,
			SearchTrajectory:    cloneTrajectory(retrieveTrace.SearchTrajectory),
			RerankScores:        rerankScores,
			Metrics:             metrics,
			InjectionFindings:   injectionFindings,
			GraphTrace:          retrieveTrace.Graph,
		},
		Trace: Trace{
			Question:            originalQuestion,
			Namespace:           opts.Search.Namespace,
			TopK:                opts.Search.TopK,
			Filters:             copyMap(opts.Search.Filters),
			SecurityFilters:     copyMap(opts.Search.SecurityFilters),
			RoutePath:           append([]string(nil), retrieveTrace.RoutePath...),
			AutoRoutePath:       append([]string(nil), retrieveTrace.AutoRoutePath...),
			AutoRouteCandidates: cloneAskRouteCandidates(retrieveTrace.AutoRouteCandidates),
			RoutePolicy:         retrieveTrace.RoutePolicy,
			SearchPath:          append([]string(nil), searchPath...),
			MatchedSections:     append([]string(nil), matchedSections...),
			ExpandedSections:    append([]string(nil), expandedSections...),
			ExpandedChunkIDs:    append([]string(nil), expandedChunkIDs...),
			RerankedChunkIDs:    append([]string(nil), rerankedIDs...),
			PackedChunkIDs:      append([]string(nil), packedIDs...),
			DroppedChunkIDs:     append([]string(nil), droppedIDs...),
			SelectedChunkIDs:    append([]string(nil), ids...),
			SearchTrajectory:    cloneTrajectory(retrieveTrace.SearchTrajectory),
		},
	}
	return askRoundResult{
		answer:             answer,
		effectiveQuery:     retrieveTrace.EffectiveQuery,
		topScore:           topHitScore(hits),
		uniqueDocCount:     uniqueDocCount(packedHits),
		followupQueries:    activeOutcome.followupQueries,
		preGradedRelevance: activeOutcome.preGradedRel,
	}, nil
}

// deriveTokenUsage records the token cost of a generation. When the model
// reported usage (any count > 0) it is used verbatim; otherwise prompt and
// completion tokens are estimated with a pack.TokenCounter and the result
// is flagged Estimated.
func deriveTokenUsage(req generate.Request, resp generate.Response) obs.TokenUsage {
	u := resp.Usage
	if u.PromptTokens > 0 || u.CompletionTokens > 0 || u.TotalTokens > 0 {
		total := u.TotalTokens
		if total == 0 {
			total = u.PromptTokens + u.CompletionTokens
		}
		return obs.TokenUsage{
			PromptTokens:     u.PromptTokens,
			CompletionTokens: u.CompletionTokens,
			TotalTokens:      total,
			Estimated:        false,
		}
	}
	counter := pack.SimpleCounter{}
	pt := counter.Count(promptText(req))
	ct := counter.Count(resp.Text)
	return obs.TokenUsage{
		PromptTokens:     pt,
		CompletionTokens: ct,
		TotalTokens:      pt + ct,
		Estimated:        true,
	}
}

// promptText flattens a rendered generate.Request to a single string for
// token estimation — the system prompt followed by each message's content.
func promptText(req generate.Request) string {
	var b strings.Builder
	b.WriteString(req.SystemPrompt)
	for _, m := range req.Messages {
		b.WriteByte('\n')
		b.WriteString(m.Content)
	}
	return b.String()
}

func copyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func chunkIDs(hits []store.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.Chunk.ID)
	}
	return out
}

func sectionIDs(hits []store.Hit) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		if hit.Chunk.SectionID == "" {
			continue
		}
		if _, ok := seen[hit.Chunk.SectionID]; ok {
			continue
		}
		seen[hit.Chunk.SectionID] = struct{}{}
		out = append(out, hit.Chunk.SectionID)
	}
	return out
}

func sectionPathTrail(hits []store.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		for _, path := range hit.Chunk.SectionPath {
			out = append(out, path)
		}
	}
	return out
}

func traceOrFallbackSections(trace retrievepolicy.Trace, hits []store.Hit) []string {
	if len(trace.MatchedSections) > 0 {
		return append([]string(nil), trace.MatchedSections...)
	}
	return sectionIDs(hits)
}

func traceOrFallbackSearchPath(trace retrievepolicy.Trace, hits []store.Hit) []string {
	if len(trace.SearchPath) > 0 {
		return append([]string(nil), trace.SearchPath...)
	}
	return sectionPathTrail(hits)
}

func cloneAskRouteCandidates(src []retrievepolicy.RouteCandidate) []retrievepolicy.RouteCandidate {
	if len(src) == 0 {
		return nil
	}
	out := make([]retrievepolicy.RouteCandidate, 0, len(src))
	for _, candidate := range src {
		out = append(out, retrievepolicy.RouteCandidate{
			Path:    append([]string(nil), candidate.Path...),
			Score:   candidate.Score,
			Queries: append([]string(nil), candidate.Queries...),
		})
	}
	return out
}

func cloneTrajectory(src []retrievepolicy.TrajectoryStep) []retrievepolicy.TrajectoryStep {
	if len(src) == 0 {
		return nil
	}
	out := make([]retrievepolicy.TrajectoryStep, 0, len(src))
	for _, step := range src {
		out = append(out, retrievepolicy.TrajectoryStep{
			Route:            append([]string(nil), step.Route...),
			Confidence:       step.Confidence,
			Mode:             step.Mode,
			HitCount:         step.HitCount,
			HitIDs:           append([]string(nil), step.HitIDs...),
			MatchedSections:  append([]string(nil), step.MatchedSections...),
			ExpandedSections: append([]string(nil), step.ExpandedSections...),
			Rationale:        step.Rationale,
		})
	}
	return out
}
