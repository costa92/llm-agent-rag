package eval

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/costa92/llm-agent-rag/rag"
)

// DriftAsker runs the DRIFT hybrid-search answer path — a global primer pass,
// a hard-bounded local follow-up loop, and a synthesis step. *rag.System
// satisfies it via AskDrift. It is the DRIFT counterpart of GlobalAsker (the
// map-reduce global path) and Asker (the local retrieve+generate path) — a
// separate seam because DRIFT, like global search, synthesizes an answer with
// no gold chunk set and is scored only on its generation side.
type DriftAsker interface {
	// AskDrift runs the DRIFT hybrid-search answer path for question.
	AskDrift(ctx context.Context, question string, opts rag.DriftOptions) (rag.Answer, error)
}

// DriftExampleResult is the per-example detail behind a DriftEvalResult.
type DriftExampleResult struct {
	Example            Example   `json:"example"`              // Example is the labeled query.
	Answer             string    `json:"answer"`               // Answer is the generated answer text.
	PrimerCommunityIDs []string  `json:"primer_community_ids"` // PrimerCommunityIDs are the communities the primer consulted.
	Rounds             int       `json:"rounds"`               // Rounds is the number of local follow-up rounds run.
	Judgement          Judgement `json:"judgement"`            // Judgement is the judge's verdict on the answer.
}

// DriftEvalResult is the DRIFT-search scoreboard for one dataset run. DRIFT,
// like global search, synthesizes an answer with no gold chunk set, so — unlike
// Metrics or TriadResult — it carries NO chunk recall@k / precision@k: only the
// two generation-side legs of the RAG Triad, groundedness and answer
// relevance.
type DriftEvalResult struct {
	MeanGroundedness    float64              `json:"mean_groundedness"`     // MeanGroundedness is the mean groundedness over the dataset.
	MeanAnswerRelevance float64              `json:"mean_answer_relevance"` // MeanAnswerRelevance is the mean answer relevance over the dataset.
	Examples            int                  `json:"examples"`              // Examples is the number of examples scored.
	PerExample          []DriftExampleResult `json:"per_example"`           // PerExample is the per-example detail.
}

// DriftEvaluator runs a Dataset of whole-corpus questions through the DRIFT
// answer path (DriftAsker.AskDrift) and scores each answer with the RAG-Triad
// Judge. The judge's grounding context is the primer's consulted community
// reports (Answer.Diagnostics.Drift.ConsultedReports), so DRIFT groundedness
// reads as "is the answer grounded in the community reports the primer read";
// answer relevance is question-vs-answer.
//
// The gold chunk/doc fields of Example (GoldDocIDs, GoldChunkIDs) are unused —
// DRIFT search has no chunk-recall notion. RunGraphAB / RetrievalEvaluator
// measure that for the local path; DriftEvaluator does not. This mirrors
// GlobalEvaluator.
type DriftEvaluator struct {
	// Asker is the DRIFT-search path under evaluation.
	Asker DriftAsker
	// Judge scores each answer's groundedness and answer relevance.
	Judge Judge
	// MaxCommunities is passed through to DriftOptions for every example — it
	// caps the primer's breadth. A value <= 0 lets AskDrift pick its own
	// default.
	MaxCommunities int
	// Rounds is passed through to DriftOptions for every example — it bounds
	// the local follow-up loop. A value <= 0 lets AskDrift pick its own
	// default; AskDrift hard-caps it regardless.
	Rounds int
}

// Run executes the DRIFT-search evaluation. It errors only on Asker or Judge
// failure; metric computation never errors.
func (e DriftEvaluator) Run(ctx context.Context, dataset Dataset) (DriftEvalResult, error) {
	if e.Asker == nil {
		return DriftEvalResult{}, errors.New("eval: DriftAsker is required")
	}
	if e.Judge == nil {
		return DriftEvalResult{}, errors.New("eval: Judge is required")
	}
	per := make([]DriftExampleResult, 0, len(dataset.Examples))
	var sumGroundedness, sumRelevance float64
	for _, ex := range dataset.Examples {
		answer, err := e.Asker.AskDrift(ctx, ex.Query, rag.DriftOptions{
			Namespace:      ex.Namespace,
			MaxCommunities: e.MaxCommunities,
			Rounds:         e.Rounds,
		})
		if err != nil {
			return DriftEvalResult{}, fmt.Errorf("eval: ask drift %q: %w", ex.Query, err)
		}
		drift := answer.Diagnostics.Drift
		judgement, err := e.Judge.Judge(ctx, JudgeRequest{
			Query:   ex.Query,
			Answer:  answer.Text,
			Context: reportContext(drift.ConsultedReports),
		})
		if err != nil {
			return DriftEvalResult{}, fmt.Errorf("eval: judge drift %q: %w", ex.Query, err)
		}
		sumGroundedness += judgement.Groundedness
		sumRelevance += judgement.AnswerRelevance
		per = append(per, DriftExampleResult{
			Example:            ex,
			Answer:             answer.Text,
			PrimerCommunityIDs: drift.PrimerCommunityIDs,
			Rounds:             drift.Rounds,
			Judgement:          judgement,
		})
	}
	result := DriftEvalResult{Examples: len(dataset.Examples), PerExample: per}
	if n := float64(len(dataset.Examples)); n > 0 {
		result.MeanGroundedness = sumGroundedness / n
		result.MeanAnswerRelevance = sumRelevance / n
	}
	return result, nil
}

// Summary returns a compact human-readable scoreboard for a DriftEvalResult.
func (r DriftEvalResult) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "DRIFT search — %d examples\n", r.Examples)
	fmt.Fprintf(&b, "  generation: groundedness=%.3f answer_relevance=%.3f\n",
		r.MeanGroundedness, r.MeanAnswerRelevance)
	return b.String()
}
