package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/costa92/llm-agent-rag/rag"
)

// Asker runs the full retrieve+generate pipeline. *rag.System satisfies it.
type Asker interface {
	// Ask runs the full retrieve-and-generate pipeline for question.
	Ask(ctx context.Context, question string, opts rag.AskOptions) (rag.Answer, error)
}

// GenerationMetrics is the generation-side scoreboard — legs 2 and 3 of the
// RAG Triad, averaged over a dataset.
type GenerationMetrics struct {
	MeanGroundedness    float64 `json:"mean_groundedness"`     // MeanGroundedness is the mean groundedness over the dataset.
	MeanAnswerRelevance float64 `json:"mean_answer_relevance"` // MeanAnswerRelevance is the mean answer relevance over the dataset.
	Examples            int     `json:"examples"`              // Examples is the number of examples scored.
}

// TriadExampleResult is the per-example detail behind a TriadResult.
type TriadExampleResult struct {
	Example      Example   `json:"example"`       // Example is the labeled query.
	Answer       string    `json:"answer"`        // Answer is the generated answer text.
	RetrievedIDs []string  `json:"retrieved_ids"` // RetrievedIDs are the chunk IDs retrieved for the answer.
	Judgement    Judgement `json:"judgement"`     // Judgement is the judge's verdict on the answer.
}

// TriadResult carries retrieval and generation metrics for one dataset run.
type TriadResult struct {
	Dataset    Dataset              `json:"dataset"`     // Dataset is the dataset that was evaluated.
	Retrieval  Metrics              `json:"retrieval"`   // Retrieval is the retrieval-side scoreboard.
	Generation GenerationMetrics    `json:"generation"`  // Generation is the generation-side scoreboard.
	PerExample []TriadExampleResult `json:"per_example"` // PerExample is the per-example detail.
}

// TriadEvaluator runs a Dataset through the full Ask pipeline and scores
// both retrieval quality and generation quality (via Judge) in one pass.
type TriadEvaluator struct {
	Asker   Asker          // Asker runs the answer pipeline under evaluation.
	Judge   Judge          // Judge scores each generated answer.
	Options rag.AskOptions // Options is the base AskOptions applied to every Ask call.
}

// Run executes the triad evaluation. It errors only on Asker or Judge
// failure; metric computation never errors.
func (e TriadEvaluator) Run(ctx context.Context, dataset Dataset) (TriadResult, error) {
	if e.Asker == nil {
		return TriadResult{}, errors.New("eval: Asker is required")
	}
	if e.Judge == nil {
		return TriadResult{}, errors.New("eval: Judge is required")
	}
	if dataset.TopK <= 0 {
		return TriadResult{}, fmt.Errorf("eval: dataset %q has TopK <= 0", dataset.Name)
	}
	per := make([]TriadExampleResult, 0, len(dataset.Examples))
	var (
		sumPrecision, sumRecall, sumMRR float64
		recallExamples, groundingHits   int
		sumGroundedness, sumRelevance   float64
	)
	for _, ex := range dataset.Examples {
		opts := e.Options
		opts.Search.TopK = dataset.TopK
		if ex.Namespace != "" {
			opts.Search.Namespace = ex.Namespace
		}
		answer, err := e.Asker.Ask(ctx, ex.Query, opts)
		if err != nil {
			return TriadResult{}, fmt.Errorf("eval: ask %q: %w", ex.Query, err)
		}
		retrievedIDs := make([]string, 0, len(answer.Hits))
		retrievedDocs := make([]string, 0, len(answer.Hits))
		contextPassages := make([]string, 0, len(answer.Hits))
		for _, hit := range answer.Hits {
			retrievedIDs = append(retrievedIDs, hit.Chunk.ID)
			retrievedDocs = append(retrievedDocs, hit.Chunk.DocID)
			contextPassages = append(contextPassages, hit.Chunk.Content)
		}
		matched := countMatches(retrievedDocs, ex.GoldDocIDs)
		sumPrecision += float64(matched) / float64(dataset.TopK)
		if len(ex.GoldDocIDs) > 0 {
			sumRecall += float64(matched) / float64(len(ex.GoldDocIDs))
			recallExamples++
		}
		if rank := firstGoldRank(retrievedDocs, ex.GoldDocIDs); rank > 0 {
			sumMRR += 1.0 / float64(rank)
		}
		if anyOverlap(retrievedIDs, ex.GoldChunkIDs) {
			groundingHits++
		}
		judgement, err := e.Judge.Judge(ctx, JudgeRequest{
			Query:   ex.Query,
			Answer:  answer.Text,
			Context: contextPassages,
		})
		if err != nil {
			return TriadResult{}, fmt.Errorf("eval: judge %q: %w", ex.Query, err)
		}
		sumGroundedness += judgement.Groundedness
		sumRelevance += judgement.AnswerRelevance
		per = append(per, TriadExampleResult{
			Example:      ex,
			Answer:       answer.Text,
			RetrievedIDs: retrievedIDs,
			Judgement:    judgement,
		})
	}
	n := float64(len(dataset.Examples))
	retrieval := Metrics{Examples: len(dataset.Examples), TopK: dataset.TopK}
	generation := GenerationMetrics{Examples: len(dataset.Examples)}
	if n > 0 {
		retrieval.PrecisionAtK = sumPrecision / n
		retrieval.MRR = sumMRR / n
		retrieval.GroundingAtK = float64(groundingHits) / n
		generation.MeanGroundedness = sumGroundedness / n
		generation.MeanAnswerRelevance = sumRelevance / n
	}
	if recallExamples > 0 {
		retrieval.RecallAtK = sumRecall / float64(recallExamples)
	}
	return TriadResult{
		Dataset:    dataset,
		Retrieval:  retrieval,
		Generation: generation,
		PerExample: per,
	}, nil
}

// WriteJSONL writes a TriadResult as JSONL: one line per example, then a
// final summary line of the form {"summary": {...}}.
func WriteJSONL(w io.Writer, r TriadResult) error {
	enc := json.NewEncoder(w)
	for _, ex := range r.PerExample {
		if err := enc.Encode(ex); err != nil {
			return fmt.Errorf("eval: write triad jsonl: %w", err)
		}
	}
	var summary struct {
		Summary struct {
			Dataset    string            `json:"dataset"`
			Retrieval  Metrics           `json:"retrieval"`
			Generation GenerationMetrics `json:"generation"`
		} `json:"summary"`
	}
	summary.Summary.Dataset = r.Dataset.Name
	summary.Summary.Retrieval = r.Retrieval
	summary.Summary.Generation = r.Generation
	if err := enc.Encode(summary); err != nil {
		return fmt.Errorf("eval: write triad summary: %w", err)
	}
	return nil
}

// Summary returns a compact human-readable scoreboard for a TriadResult.
func (r TriadResult) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "RAG Triad — dataset %q (%d examples, TopK %d)\n",
		r.Dataset.Name, r.Retrieval.Examples, r.Retrieval.TopK)
	fmt.Fprintf(&b, "  retrieval:  precision@k=%.3f recall@k=%.3f mrr=%.3f grounding@k=%.3f\n",
		r.Retrieval.PrecisionAtK, r.Retrieval.RecallAtK, r.Retrieval.MRR, r.Retrieval.GroundingAtK)
	fmt.Fprintf(&b, "  generation: groundedness=%.3f answer_relevance=%.3f\n",
		r.Generation.MeanGroundedness, r.Generation.MeanAnswerRelevance)
	return b.String()
}
