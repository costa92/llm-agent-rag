// Package eval scores the retrieval pipeline against a labeled dataset.
// It is the standalone module's CI gate: a regression in route policy,
// fanout, converge, namespace isolation, or grounding shows up as a drop
// in one of the four headline metrics.
//
// Metrics:
//
//   - PrecisionAtK = mean over examples of (retrieved ∩ gold) / k
//   - RecallAtK    = mean over examples of (retrieved ∩ gold) / |gold|
//   - MRR          = mean of 1 / rank-of-first-gold-doc (0 if not retrieved)
//   - GroundingAtK = fraction of examples where at least one gold chunk
//     ID appears in retrieved chunks
//
// Datasets can be defined inline (Dataset literal) or loaded from JSONL
// via LoadJSONL. The JSONL path is what 13-03 will use to feed
// production-captured misses back into the same scoring loop.
package eval

import (
	"context"
	"errors"
	"fmt"

	"github.com/costa92/llm-agent-rag/rag"
	"github.com/costa92/llm-agent-rag/store"
)

// Example is one labeled query in a Dataset.
type Example struct {
	Query        string   `json:"query"`                    // Query is the labeled query.
	Namespace    string   `json:"namespace,omitempty"`      // Namespace overrides the evaluator namespace for this example.
	GoldDocIDs   []string `json:"gold_doc_ids,omitempty"`   // GoldDocIDs are the document IDs a correct retrieval should return.
	GoldChunkIDs []string `json:"gold_chunk_ids,omitempty"` // GoldChunkIDs are the chunk IDs a correct retrieval should return.
	Notes        string   `json:"notes,omitempty"`          // Notes is free-form annotation for the example.
}

// Dataset is a named collection of Examples with a fixed TopK.
type Dataset struct {
	Name     string    `json:"name"`     // Name identifies the dataset.
	TopK     int       `json:"top_k"`    // TopK is the retrieval cutoff every example is scored at.
	Examples []Example `json:"examples"` // Examples are the labeled queries.
}

// Metrics is the headline scoreboard for a Dataset run.
type Metrics struct {
	PrecisionAtK float64 // PrecisionAtK is the mean precision at the dataset TopK.
	RecallAtK    float64 // RecallAtK is the mean recall at the dataset TopK.
	MRR          float64 // MRR is the mean reciprocal rank of the first gold document.
	GroundingAtK float64 // GroundingAtK is the fraction of examples with a gold chunk retrieved.
	Examples     int     // Examples is the number of examples scored.
	TopK         int     // TopK is the retrieval cutoff the metrics were computed at.
}

// ExampleResult is the per-example detail behind the aggregated Metrics.
type ExampleResult struct {
	Example            Example  // Example is the labeled query this result is for.
	RetrievedIDs       []string // RetrievedIDs are the chunk IDs retrieved.
	RetrievedDocs      []string // RetrievedDocs are the document IDs retrieved.
	RankOfFirstGoldDoc int      // RankOfFirstGoldDoc is the 1-based rank of the first gold doc; 0 means not found.
	GroundingHit       bool     // GroundingHit is true when at least one gold chunk was retrieved.
}

// RetrievalResult wraps Metrics with the per-example trace useful for
// debugging retrieval regressions.
type RetrievalResult struct {
	Dataset    Dataset         // Dataset is the dataset that was evaluated.
	Metrics    Metrics         // Metrics is the aggregated scoreboard.
	PerExample []ExampleResult // PerExample is the per-example detail.
}

// Retriever is the subset of rag.System eval needs. *rag.System satisfies
// this naturally; tests can provide a stub for synthetic scoring.
type Retriever interface {
	// Retrieve returns the hits for query under opts.
	Retrieve(ctx context.Context, query string, opts rag.SearchOptions) ([]store.Hit, error)
}

// RetrievalEvaluator runs a Dataset against a Retriever using Options as
// the base SearchOptions for every Retrieve call. Each example's Namespace
// overlays Options.Namespace when non-empty; everything else (TopK,
// auto-route knobs, filters) is taken from Options. It scores the
// retrieval recall/MRR/precision path; the answer-side evaluators
// (GlobalEvaluator, DriftEvaluator, TriadEvaluator) are name-prefixed in
// the same way.
type RetrievalEvaluator struct {
	Retriever Retriever         // Retriever is the retrieval system under evaluation.
	Options   rag.SearchOptions // Options is the base SearchOptions applied to every Retrieve call.
}

// Run executes the evaluation. Returns an error only on Retriever
// failure; metric computation never errors.
func (e RetrievalEvaluator) Run(ctx context.Context, dataset Dataset) (RetrievalResult, error) {
	if e.Retriever == nil {
		return RetrievalResult{}, errors.New("eval: Retriever is required")
	}
	if dataset.TopK <= 0 {
		return RetrievalResult{}, fmt.Errorf("eval: dataset %q has TopK <= 0", dataset.Name)
	}
	per := make([]ExampleResult, 0, len(dataset.Examples))
	var (
		sumPrecision, sumRecall, sumMRR float64
		recallExamples                  int
		groundingHits                   int
	)
	for _, ex := range dataset.Examples {
		opts := e.Options
		opts.TopK = dataset.TopK
		if ex.Namespace != "" {
			opts.Namespace = ex.Namespace
		}
		hits, err := e.Retriever.Retrieve(ctx, ex.Query, opts)
		if err != nil {
			return RetrievalResult{}, fmt.Errorf("eval: retrieve %q: %w", ex.Query, err)
		}
		retrievedIDs := make([]string, 0, len(hits))
		retrievedDocs := make([]string, 0, len(hits))
		for _, hit := range hits {
			retrievedIDs = append(retrievedIDs, hit.Chunk.ID)
			retrievedDocs = append(retrievedDocs, hit.Chunk.DocID)
		}
		// precision @ k
		matched := countMatches(retrievedDocs, ex.GoldDocIDs)
		precision := float64(matched) / float64(dataset.TopK)
		sumPrecision += precision
		// recall @ k (skip examples with zero gold)
		if len(ex.GoldDocIDs) > 0 {
			sumRecall += float64(matched) / float64(len(ex.GoldDocIDs))
			recallExamples++
		}
		// MRR
		rank := firstGoldRank(retrievedDocs, ex.GoldDocIDs)
		if rank > 0 {
			sumMRR += 1.0 / float64(rank)
		}
		// grounding @ k
		grounding := anyOverlap(retrievedIDs, ex.GoldChunkIDs)
		if grounding {
			groundingHits++
		}
		per = append(per, ExampleResult{
			Example:            ex,
			RetrievedIDs:       retrievedIDs,
			RetrievedDocs:      retrievedDocs,
			RankOfFirstGoldDoc: rank,
			GroundingHit:       grounding,
		})
	}
	n := float64(len(dataset.Examples))
	metrics := Metrics{
		Examples: len(dataset.Examples),
		TopK:     dataset.TopK,
	}
	if n > 0 {
		metrics.PrecisionAtK = sumPrecision / n
		metrics.MRR = sumMRR / n
		metrics.GroundingAtK = float64(groundingHits) / n
	}
	if recallExamples > 0 {
		metrics.RecallAtK = sumRecall / float64(recallExamples)
	}
	return RetrievalResult{
		Dataset:    dataset,
		Metrics:    metrics,
		PerExample: per,
	}, nil
}

// countMatches returns how many gold IDs appear in retrieved (each gold
// counted at most once).
func countMatches(retrieved []string, gold []string) int {
	if len(retrieved) == 0 || len(gold) == 0 {
		return 0
	}
	goldSet := make(map[string]struct{}, len(gold))
	for _, g := range gold {
		goldSet[g] = struct{}{}
	}
	seen := make(map[string]struct{}, len(retrieved))
	count := 0
	for _, r := range retrieved {
		if _, alreadySeen := seen[r]; alreadySeen {
			continue
		}
		seen[r] = struct{}{}
		if _, ok := goldSet[r]; ok {
			count++
		}
	}
	return count
}

// firstGoldRank returns the 1-based rank of the first gold-doc match in
// retrieved, or 0 if none.
func firstGoldRank(retrieved []string, gold []string) int {
	if len(retrieved) == 0 || len(gold) == 0 {
		return 0
	}
	goldSet := make(map[string]struct{}, len(gold))
	for _, g := range gold {
		goldSet[g] = struct{}{}
	}
	for i, r := range retrieved {
		if _, ok := goldSet[r]; ok {
			return i + 1
		}
	}
	return 0
}

// anyOverlap returns true if at least one element of b appears in a.
func anyOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, v := range a {
		set[v] = struct{}{}
	}
	for _, v := range b {
		if _, ok := set[v]; ok {
			return true
		}
	}
	return false
}
