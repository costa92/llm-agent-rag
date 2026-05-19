package eval

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/rag"
)

// GlobalAsker runs the map-reduce global-search answer path. *rag.System
// satisfies it via AskGlobal. It is the global-search counterpart of Asker
// (the local retrieve+generate path) — kept a separate seam because global
// search has no gold chunk set and is scored only on its generation side.
type GlobalAsker interface {
	AskGlobal(ctx context.Context, question string, opts rag.GlobalOptions) (rag.Answer, error)
}

// GlobalExampleResult is the per-example detail behind a GlobalEvalResult.
type GlobalExampleResult struct {
	Example      Example   `json:"example"`
	Answer       string    `json:"answer"`
	CommunityIDs []string  `json:"community_ids"`
	Judgement    Judgement `json:"judgement"`
}

// GlobalEvalResult is the global-search scoreboard for one dataset run. Global
// search synthesizes an answer with no gold chunk set, so — unlike Metrics or
// TriadResult — it carries NO chunk recall@k / precision@k: only the two
// generation-side legs of the RAG Triad, groundedness and answer relevance.
type GlobalEvalResult struct {
	MeanGroundedness    float64               `json:"mean_groundedness"`
	MeanAnswerRelevance float64               `json:"mean_answer_relevance"`
	Examples            int                   `json:"examples"`
	PerExample          []GlobalExampleResult `json:"per_example"`
}

// GlobalEvaluator runs a Dataset of whole-corpus questions through the
// global-search path (GlobalAsker.AskGlobal) and scores each answer with the
// RAG-Triad Judge. The judge's grounding context is the community reports the
// answer actually consulted (Answer.Diagnostics.Global.ConsultedReports), so
// global-search groundedness reads as "is the answer grounded in the community
// reports it read"; answer relevance is question-vs-answer.
//
// The gold chunk/doc fields of Example (GoldDocIDs, GoldChunkIDs) are unused —
// global search has no chunk-recall notion. RunGraphAB / Evaluator measure
// that for the local path; GlobalEvaluator does not.
type GlobalEvaluator struct {
	// Asker is the global-search path under evaluation.
	Asker GlobalAsker
	// Judge scores each answer's groundedness and answer relevance.
	Judge Judge
	// MaxCommunities is passed through to GlobalOptions for every example. A
	// value <= 0 lets AskGlobal pick its own default.
	MaxCommunities int
}

// Run executes the global-search evaluation. It errors only on Asker or Judge
// failure; metric computation never errors.
func (e GlobalEvaluator) Run(ctx context.Context, dataset Dataset) (GlobalEvalResult, error) {
	if e.Asker == nil {
		return GlobalEvalResult{}, errors.New("eval: GlobalAsker is required")
	}
	if e.Judge == nil {
		return GlobalEvalResult{}, errors.New("eval: Judge is required")
	}
	per := make([]GlobalExampleResult, 0, len(dataset.Examples))
	var sumGroundedness, sumRelevance float64
	for _, ex := range dataset.Examples {
		answer, err := e.Asker.AskGlobal(ctx, ex.Query, rag.GlobalOptions{
			Namespace:      ex.Namespace,
			MaxCommunities: e.MaxCommunities,
		})
		if err != nil {
			return GlobalEvalResult{}, fmt.Errorf("eval: ask global %q: %w", ex.Query, err)
		}
		reports := answer.Diagnostics.Global.ConsultedReports
		judgement, err := e.Judge.Judge(ctx, JudgeRequest{
			Query:   ex.Query,
			Answer:  answer.Text,
			Context: reportContext(reports),
		})
		if err != nil {
			return GlobalEvalResult{}, fmt.Errorf("eval: judge global %q: %w", ex.Query, err)
		}
		sumGroundedness += judgement.Groundedness
		sumRelevance += judgement.AnswerRelevance
		per = append(per, GlobalExampleResult{
			Example:      ex,
			Answer:       answer.Text,
			CommunityIDs: answer.Diagnostics.Global.CommunityIDs,
			Judgement:    judgement,
		})
	}
	result := GlobalEvalResult{Examples: len(dataset.Examples), PerExample: per}
	if n := float64(len(dataset.Examples)); n > 0 {
		result.MeanGroundedness = sumGroundedness / n
		result.MeanAnswerRelevance = sumRelevance / n
	}
	return result, nil
}

// reportContext renders the consulted community reports into the judge's
// context passages — one passage per report, "Title: Summary". An empty
// report set yields an empty slice; the Judge then sees no grounding context.
func reportContext(reports []graph.CommunityReport) []string {
	if len(reports) == 0 {
		return nil
	}
	out := make([]string, 0, len(reports))
	for _, r := range reports {
		var b strings.Builder
		if r.Title != "" {
			b.WriteString(r.Title)
			b.WriteString(": ")
		}
		b.WriteString(r.Summary)
		out = append(out, b.String())
	}
	return out
}

// Summary returns a compact human-readable scoreboard for a GlobalEvalResult.
func (r GlobalEvalResult) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Global search — %d examples\n", r.Examples)
	fmt.Fprintf(&b, "  generation: groundedness=%.3f answer_relevance=%.3f\n",
		r.MeanGroundedness, r.MeanAnswerRelevance)
	return b.String()
}
