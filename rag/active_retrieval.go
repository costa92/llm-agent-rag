package rag

import (
	"context"
	"sort"

	"github.com/costa92/llm-agent-rag/store"
)

// activeRetrievalBudget threads the active-retrieval configuration and
// per-Ask remaining budget through into askRound. enabled is the master
// switch: when false, askRound short-circuits and behaves exactly as
// v1.1.x. perRoundCap caps follow-ups in one round; globalRemaining is
// a pointer back to a counter declared in Ask so that decrementing
// across rounds composes correctly. used is the diagnostic counter,
// also a pointer, that Ask reads at the end of the loop to populate
// ReflectionDiagnostics.FollowupQueriesUsed.
//
// prevAnswer is the latest round's answer text — empty on the first
// round — and is plumbed to the QueryPlanner so it can read the gap
// signal between the question and the existing answer.
type activeRetrievalBudget struct {
	enabled         bool
	perRoundCap     int
	globalRemaining *int // pointer for cross-round decrement
	floor           float64
	planner         QueryPlanner
	used            *int // pointer for diagnostics counter
	prevAnswer      string
}

// activeRetrievalDefaults are the v1.2.0 defaults applied when a field
// on ReflectionOptions is left at its zero value.
const (
	defaultActiveRetrievalPerRoundCap = 2
	defaultActiveRetrievalGlobalCap   = 4
	defaultActiveRetrievalFloor       = 0.4
)

// effectiveActiveRetrievalConfig resolves a ReflectionOptions into the
// (perRoundCap, globalCap, floor) tuple the active-retrieval driver
// consumes. Zero-value fields fall back to the v1.2.0 defaults.
func effectiveActiveRetrievalConfig(opts ReflectionOptions) (perRoundCap, globalCap int, floor float64) {
	perRoundCap = opts.MaxFollowupQueries
	if perRoundCap <= 0 {
		perRoundCap = defaultActiveRetrievalPerRoundCap
	}
	globalCap = opts.MaxFollowupQueriesPerAsk
	if globalCap <= 0 {
		globalCap = defaultActiveRetrievalGlobalCap
	}
	floor = opts.ActiveRetrievalRelevanceFloor
	if floor <= 0 {
		floor = defaultActiveRetrievalFloor
	}
	return perRoundCap, globalCap, floor
}

// unionHits dedupes seed and follow-up hits by Chunk.ID, keeping the
// max Score per ID, and returns the result sorted by Score desc
// (stable). When capK > 0 the result is truncated to capK entries.
//
// Per the v1.2.0 design (Q-A): order is (1) seed hits in their input
// order, (2) follow-up hits in dispatch order — the dedupe map
// records first-seen IDs; ties between the seed and follow-up sets
// keep the higher-scoring entry; the final sort by Score is stable so
// equal-scored entries retain their first-seen order.
func unionHits(seeds []store.Hit, followups [][]store.Hit, capK int) []store.Hit {
	byID := map[string]store.Hit{}
	order := []string{}
	add := func(h store.Hit) {
		if prev, ok := byID[h.Chunk.ID]; ok {
			if h.Score > prev.Score {
				byID[h.Chunk.ID] = h
			}
			return
		}
		byID[h.Chunk.ID] = h
		order = append(order, h.Chunk.ID)
	}
	for _, h := range seeds {
		add(h)
	}
	for _, fh := range followups {
		for _, h := range fh {
			add(h)
		}
	}
	out := make([]store.Hit, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})
	if capK > 0 && len(out) > capK {
		out = out[:capK]
	}
	return out
}

// gradeRelevanceOnly calls Grader.ScoreRelevance for every hit and
// returns the per-chunk relevance scores plus the max seen. Unlike
// gradeRound (which also calls ScoreSupport), this only consults the
// relevance side so the active-retrieval trigger can decide before the
// answer is generated. Fail-open: a per-call error downgrades that
// chunk to a neutral 0.5 with an error reason — the trigger never
// breaks the Ask call.
//
// Per the design (Q-D), the relevance scores produced here are stashed
// back into askRoundResult so the outer loop's grading step can reuse
// them without a second ScoreRelevance call. ScoreSupport still runs in
// the outer loop because it requires the generated answer text.
func gradeRelevanceOnly(ctx context.Context, grader Grader, query string, hits []store.Hit) ([]ChunkScore, float64) {
	if grader == nil || len(hits) == 0 {
		return nil, 0
	}
	scores := make([]ChunkScore, 0, len(hits))
	maxRel := 0.0
	for _, hit := range hits {
		rel, reason, err := grader.ScoreRelevance(ctx, query, hit)
		if err != nil {
			rel = 0.5
			reason = "grader error: " + err.Error()
		}
		if rel > maxRel {
			maxRel = rel
		}
		scores = append(scores, ChunkScore{
			HitID:     hit.Chunk.ID,
			Relevance: rel,
			Reason:    reason,
		})
	}
	return scores, maxRel
}

// activeRetrievalOutcome carries the work products of one active-
// retrieval pass: the (possibly augmented) hit set the caller should
// use downstream, the planner's emitted queries (for diagnostics), and
// the pre-graded relevance scores (so the outer loop's grading step
// does not double-call ScoreRelevance).
type activeRetrievalOutcome struct {
	hits             []store.Hit
	followupQueries  []string
	preGradedRel     []ChunkScore
	preGradedMaxRel  float64
	preGradedApplied bool
}

// runActiveRetrieval is the per-round active-retrieval driver: it
// pre-grades the seed hits' relevance, fires up to budget.perRoundCap
// follow-up retrievals (capped by *budget.globalRemaining), and unions
// the results with the seed set. Returns the augmented hit set plus
// the planner's emitted queries. Fail-open across the board: a
// planner error or a per-query retrieval error degrades to "no
// follow-ups this round".
//
// Sequential dispatch (Q-B): follow-up retrievals fire one-by-one,
// deterministic for tests.
func (s *System) runActiveRetrieval(ctx context.Context, originalQuestion string, seedHits []store.Hit, opts SearchOptions, budget activeRetrievalBudget) activeRetrievalOutcome {
	out := activeRetrievalOutcome{hits: seedHits}
	if !budget.enabled || budget.planner == nil {
		return out
	}
	if budget.globalRemaining == nil || *budget.globalRemaining <= 0 {
		return out
	}
	// Pre-grade relevance on the seed hits so the trigger can read the
	// gap without a separate grader pass. Stash for the outer loop.
	scores, maxRel := gradeRelevanceOnly(ctx, s.effectiveGrader(), originalQuestion, seedHits)
	out.preGradedRel = scores
	out.preGradedMaxRel = maxRel
	out.preGradedApplied = true
	if maxRel >= budget.floor {
		// Strong enough seed — no follow-ups needed.
		return out
	}
	// Plan follow-ups. Fail-open: an error becomes no follow-ups.
	planned, err := budget.planner.PlanFollowups(ctx, originalQuestion, budget.prevAnswer, scores)
	if err != nil || len(planned) == 0 {
		return out
	}
	// Apply per-round cap and global cap.
	perRound := budget.perRoundCap
	if perRound <= 0 {
		perRound = defaultActiveRetrievalPerRoundCap
	}
	if len(planned) > perRound {
		planned = planned[:perRound]
	}
	if budget.globalRemaining != nil && len(planned) > *budget.globalRemaining {
		planned = planned[:*budget.globalRemaining]
	}
	if len(planned) == 0 {
		return out
	}
	// Sequential dispatch.
	followupHitSets := make([][]store.Hit, 0, len(planned))
	for _, q := range planned {
		hits, _, rerr := s.retrieve(ctx, q, opts)
		if rerr != nil {
			// Fail-open: skip this query but keep the rest.
			continue
		}
		followupHitSets = append(followupHitSets, hits)
	}
	// Union seed + follow-up hits, capped at opts.TopK when set.
	out.hits = unionHits(seedHits, followupHitSets, opts.TopK)
	out.followupQueries = append([]string(nil), planned...)
	// Decrement the global remaining counter by the actual number of
	// planned follow-ups (even if some retrievals failed) — the budget
	// is "queries planned", not "queries that succeeded".
	if budget.globalRemaining != nil {
		*budget.globalRemaining -= len(planned)
	}
	if budget.used != nil {
		*budget.used += len(planned)
	}
	return out
}
