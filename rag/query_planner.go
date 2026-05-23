package rag

import (
	"context"
	"fmt"
	"strings"

	"github.com/costa92/llm-agent-rag/generate"
)

// defaultPlannerMaxQueries is the cap PromptQueryPlanner uses when
// MaxQueries is left at its zero value.
const defaultPlannerMaxQueries = 2

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
	// in a single call. A value <= 0 defaults to defaultPlannerMaxQueries
	// (2) — the active-retrieval driver applies its own per-round cap on
	// top of this.
	MaxQueries int
}

// PlanFollowups asks Model for up to MaxQueries follow-ups for question.
// Fails open: a model error, an empty reply, or a parse-empty reply all
// yield (nil, nil) — never an error. A nil Model is the one exception.
func (p PromptQueryPlanner) PlanFollowups(ctx context.Context, question string, prevAnswer string, scores []ChunkScore) ([]string, error) {
	if p.Model == nil {
		return nil, fmt.Errorf("rag: PromptQueryPlanner.Model is nil")
	}
	n := p.MaxQueries
	if n <= 0 {
		n = defaultPlannerMaxQueries
	}
	req := generate.Request{
		SystemPrompt: queryPlannerSystem,
		Messages: []generate.Message{{
			Role:    "user",
			Content: queryPlannerPrompt(question, prevAnswer, scores, n),
		}},
	}
	resp, err := p.Model.Generate(ctx, req)
	if err != nil {
		// Fail-open on model error.
		return nil, nil
	}
	queries := parsePlannerReply(resp.Text, n)
	if len(queries) == 0 {
		// Fail-open on empty/parse-empty reply.
		return nil, nil
	}
	return queries, nil
}

// queryPlannerSystem is the system prompt for PromptQueryPlanner. It
// pins the contract: one query per line, no numbering, no commentary.
const queryPlannerSystem = "You generate follow-up search queries for a RAG retrieval system. Reply with one query per line, no numbering, no commentary."

// queryPlannerPrompt builds the user-content portion of the planner
// request. It includes the original question, the previous answer (when
// available), and any low-relevance chunk evidence to give the planner
// a gap signal.
func queryPlannerPrompt(question, prevAnswer string, scores []ChunkScore, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Original question: %s\n", question)
	if prevAnswer != "" {
		fmt.Fprintf(&b, "Previous answer: %s\n", prevAnswer)
	}
	if len(scores) > 0 {
		// Emit the gap signal — chunks scored below 0.6 — so the
		// planner can target missing evidence rather than re-search the
		// same well-covered angle.
		var gap []ChunkScore
		for _, s := range scores {
			if s.Relevance < 0.6 {
				gap = append(gap, s)
			}
		}
		if len(gap) > 0 {
			b.WriteString("Low-relevance evidence (gap signal):\n")
			for _, s := range gap {
				fmt.Fprintf(&b, "- chunk %s (rel=%.2f): %s\n", s.HitID, s.Relevance, s.Reason)
			}
		}
	}
	fmt.Fprintf(&b, "\nProduce up to %d follow-up search queries that would surface additional supporting evidence. One query per line. No numbering. No quotes. No empty lines.", n)
	return b.String()
}

// parsePlannerReply turns a line-delimited planner reply into a clean
// list of follow-up queries, capped at n. It is tolerant of bullet
// prefixes ("- ", "* "), numbered prefixes ("1. ", "1) "), surrounding
// whitespace, and empty lines.
func parsePlannerReply(text string, n int) []string {
	if n <= 0 {
		return nil
	}
	out := make([]string, 0, n)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		line = stripPlannerPrefix(line)
		if line == "" {
			continue
		}
		out = append(out, line)
		if len(out) >= n {
			break
		}
	}
	return out
}

// stripPlannerPrefix removes a single leading bullet, numbered prefix,
// or surrounding quote so the parsed query is a clean search string.
// Called once per line; idempotent on already-clean input.
func stripPlannerPrefix(line string) string {
	// Strip a leading bullet ("- ", "* ").
	if len(line) >= 2 && (line[0] == '-' || line[0] == '*') && line[1] == ' ' {
		return strings.TrimSpace(line[2:])
	}
	// Strip a leading numbered prefix ("1. ", "12) ") — defensively
	// scan digits then look for a "." or ")" separator.
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 && i < len(line) && (line[i] == '.' || line[i] == ')') {
		rest := strings.TrimSpace(line[i+1:])
		if rest != "" {
			return rest
		}
	}
	return line
}

// Compile-time assertions that both shipped planners implement QueryPlanner.
var (
	_ QueryPlanner = NoopQueryPlanner{}
	_ QueryPlanner = PromptQueryPlanner{}
)
