package rag

import (
	"context"

	"github.com/costa92/llm-agent-rag/generate"
)

// QueryPlanner generates follow-up search queries for the active-retrieval
// pass of a reflection round. It is consulted only when
// ReflectionOptions.EnableActiveRetrieval is true and the round's seed
// retrieval looks weak (max relevance below ActiveRetrievalRelevanceFloor).
//
// PlanFollowups must be deterministic with respect to its inputs in the
// sense that two calls with identical inputs may yield the same queries —
// the active-retrieval driver does not memoize. Implementations should be
// fail-open: prefer returning (nil, nil) over an error so a flaky planner
// never breaks an Ask call.
//
// Compatibility note: this interface is additive — it is not consumed by
// any existing seam and only takes effect when wired through
// ReflectionOptions.EnableActiveRetrieval.
type QueryPlanner interface {
	// PlanFollowups returns up to a small number of follow-up search
	// queries that would surface additional supporting evidence for
	// question. prevAnswer is the previous round's answer text (empty
	// before the first round) and scores carries the per-chunk grader
	// signal from the same seed retrieval, so the planner can read the
	// gap before issuing queries.
	PlanFollowups(ctx context.Context, question string, prevAnswer string, scores []ChunkScore) ([]string, error)
}

// NoopQueryPlanner is a deterministic QueryPlanner that returns no
// follow-up queries. It is the safe default when active retrieval is
// wired but no real planner is configured.
type NoopQueryPlanner struct{}

// PlanFollowups always returns (nil, nil) — the no-op contract.
func (NoopQueryPlanner) PlanFollowups(_ context.Context, _ string, _ string, _ []ChunkScore) ([]string, error) {
	return nil, nil
}

// PromptQueryPlanner asks a generate.Model to emit a small list of
// follow-up search queries, one per line. Inspired by Self-RAG's
// gap-driven query rewriting. It fails open: a model error, an empty
// reply, or a parse-empty reply all yield (nil, nil) — never an error.
// A nil Model is the one exception and surfaces an error so callers can
// detect misconfiguration (parity with PromptGrader).
type PromptQueryPlanner struct {
	// Model is the underlying generation model used to plan follow-ups.
	// A nil Model surfaces an error so callers can detect misconfiguration.
	Model generate.Model
	// MaxQueries caps how many follow-ups the planner is asked to emit
	// in a single call. A value <= 0 defaults to 2 — the active-retrieval
	// driver applies its own per-round cap on top of this.
	MaxQueries int
}

// PlanFollowups asks Model for up to MaxQueries follow-ups for question.
func (p PromptQueryPlanner) PlanFollowups(_ context.Context, _ string, _ string, _ []ChunkScore) ([]string, error) {
	// Implementation deferred to commit 2 — this stub gives commit 1 a
	// PromptQueryPlanner type that already satisfies the interface so
	// callers can declare typed nils against it.
	return nil, nil
}

// Compile-time assertions that both shipped planners implement QueryPlanner.
var (
	_ QueryPlanner = NoopQueryPlanner{}
	_ QueryPlanner = PromptQueryPlanner{}
)
