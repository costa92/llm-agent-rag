package eval

// This file implements the C-Eval answer-quality benchmark harness — a
// generation-side scoreboard for the standard System.Ask path. v1.3.0
// shipped the pure C-Eval core (ExactMatch, token F1, required-phrase
// recall) plus reflection/grading/active-retrieval signal aggregation
// from existing Diagnostics. v1.4.0 extends the harness with two
// closely-coupled, fully additive features:
//
//   - C-BenchJudge: an optional Judge field on AnswerBenchmark wires the
//     same JudgeRequest{Query, Answer, Context} contract used by
//     TriadEvaluator. When set, per-example Judgement is captured and
//     dataset-level MeanGroundedness / MeanAnswerRelevance are computed.
//     When nil, all judge-side metrics carry math.NaN() and the v1.3.0
//     textual scoring path runs byte-for-byte unchanged.
//   - C-BenchPar: an optional Parallelism field bounds concurrent
//     example dispatch via a buffered-channel semaphore + WaitGroup
//     worker pool. Sequential (Parallelism <= 1) flows through the
//     v1.3.0 single-goroutine loop; parallel (>= 2) writes per-example
//     results into a pre-allocated slice by index so PerExample order
//     is deterministic regardless of completion order.
//
// The Asker contract is the existing eval.Asker from triad.go — the
// benchmark introduces no new seam on rag.System. BenchmarkMetrics
// ships without json: tags because math.NaN() does not round-trip
// through encoding/json; callers wrap in their own wire type.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"

	"github.com/costa92/llm-agent-rag/rag"
)

// AnswerExample is one labeled query for the C-Eval answer-quality
// benchmark. It embeds eval.Example (Query/Namespace/GoldDocIDs/
// GoldChunkIDs/Notes promoted) and adds two generation-side oracles:
// GoldAnswers (any-match counts as ExactMatch, F1Token picks the best
// per gold) and RequiredPhrases (case-sensitive verbatim substrings
// the answer should contain).
//
// Compatibility note: this exported struct may grow additively over
// time, so keyed composite literals are recommended.
type AnswerExample struct {
	Example          // Embedded; promoted fields Query, Namespace, GoldDocIDs, GoldChunkIDs, Notes.
	GoldAnswers     []string `json:"gold_answers,omitempty"`     // GoldAnswers are reference answer strings; any-match counts as ExactMatch.
	RequiredPhrases []string `json:"required_phrases,omitempty"` // RequiredPhrases are verbatim substrings the answer should contain.
}

// AnswerDataset is a named collection of AnswerExamples with a fixed
// TopK — sibling of eval.Dataset for the answer-quality benchmark.
type AnswerDataset struct {
	Name     string          // Name identifies the dataset.
	TopK     int             // TopK is the retrieval cutoff every example is scored at.
	Examples []AnswerExample // Examples are the labeled queries.
}

// LoadAnswerJSONL reads a JSONL file of AnswerExample records. The
// returned AnswerDataset takes its Name from the file's basename
// (without extension) and inherits its TopK from the first line that
// carries a "top_k" field; subsequent "top_k" fields are ignored.
//
// Per-line schema (JSON tags on Example, AnswerExample, plus the
// optional "top_k" sentinel):
//
//	{
//	  "query": "...",
//	  "namespace": "...",
//	  "gold_doc_ids": ["..."],
//	  "gold_chunk_ids": ["..."],
//	  "notes": "...",
//	  "gold_answers": ["..."],
//	  "required_phrases": ["..."],
//	  "top_k": 5
//	}
//
// Lines beginning with "//" or "#" and blank lines are skipped (line
// numbering still advances for error context). Errors include file:line.
func LoadAnswerJSONL(path string) (AnswerDataset, error) {
	f, err := os.Open(path)
	if err != nil {
		return AnswerDataset{}, fmt.Errorf("eval: open %s: %w", path, err)
	}
	defer f.Close()

	dataset := AnswerDataset{
		Name: datasetNameFromPath(path),
		TopK: 5,
	}
	type wireAnswerExample struct {
		AnswerExample
		TopK int `json:"top_k,omitempty"`
	}
	scanner := bufio.NewScanner(f)
	// allow large lines for verbose gold-answer arrays
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	topKSet := false
	for scanner.Scan() {
		lineNo++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "#") {
			continue
		}
		var w wireAnswerExample
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			return AnswerDataset{}, fmt.Errorf("eval: %s:%d: %w", path, lineNo, err)
		}
		if w.TopK > 0 && !topKSet {
			dataset.TopK = w.TopK
			topKSet = true
		}
		dataset.Examples = append(dataset.Examples, w.AnswerExample)
	}
	if err := scanner.Err(); err != nil {
		return AnswerDataset{}, fmt.Errorf("eval: scan %s: %w", path, err)
	}
	return dataset, nil
}

// BenchmarkMetrics is the aggregated C-Eval answer-quality scoreboard
// over a dataset run. It carries math.NaN() sentinels for metrics whose
// underlying feature was off across every example (reflection off,
// grading disabled, active retrieval disabled, or no required phrases
// labeled). Callers that serialize this struct should wrap it in their
// own wire type — NaN does not marshal cleanly to JSON, so no json:
// tags ship on this struct in v1.3.0.
//
// Compatibility note: this exported struct may grow additively over
// time, so keyed composite literals are recommended.
type BenchmarkMetrics struct {
	Examples int // Examples is the number of examples scored.

	ExactMatch           float64 // ExactMatch is the mean per-example exact-match rate (post-normalize).
	F1Token              float64 // F1Token is the mean per-example token-F1 score (multiset, HotpotQA-style).
	RequiredPhraseRecall float64 // RequiredPhraseRecall is the micro recall over required phrases; NaN when no example labeled any phrase.

	ReflectionRoundsMean    float64 // ReflectionRoundsMean is the mean RoundDetails length over reflection-enabled examples; NaN when reflection was off everywhere.
	AdoptedRoundCounts      []int   // AdoptedRoundCounts is the histogram of AdoptedRound values (0-indexed by AdoptedRound-1); nil when reflection was off everywhere.
	GraderAdoptionRate      float64 // GraderAdoptionRate is the fraction of grading-enabled examples whose AdoptedRound != last round; NaN when grading was off everywhere.
	FollowupQueriesUsedMean float64 // FollowupQueriesUsedMean is the mean FollowupQueriesUsed over active-retrieval-enabled examples; NaN when active retrieval was off everywhere.
	ActiveRetrievalFireRate float64 // ActiveRetrievalFireRate is the fraction of active-retrieval-enabled examples that emitted at least one follow-up; NaN when active retrieval was off everywhere.

	MeanGroundedness    float64 // MeanGroundedness is the mean Judgement.Groundedness over judge-applied examples; NaN when Judge=nil or dataset empty. v1.4.0.
	MeanAnswerRelevance float64 // MeanAnswerRelevance is the mean Judgement.AnswerRelevance over judge-applied examples; NaN when Judge=nil or dataset empty. v1.4.0.
}

// AnswerExampleResult is the per-example trace behind a BenchmarkResult.
// It carries enough detail (raw answer, per-example component scores,
// reflection signals) for callers to recompute or audit any of the
// aggregated BenchmarkMetrics offline.
//
// Compatibility note: this exported struct may grow additively over
// time, so keyed composite literals are recommended.
type AnswerExampleResult struct {
	Example             AnswerExample // Example is the labeled query this result is for.
	Answer              string        // Answer is the generated answer text (Answer.Text).
	ExactMatch          bool          // ExactMatch is true when any gold answer normalized equal to Answer.
	F1Token             float64       // F1Token is the max token-F1 of Answer against any gold.
	RequiredPhraseHits  int           // RequiredPhraseHits is the count of RequiredPhrases substrings present verbatim in Answer.
	RequiredPhraseTotal int           // RequiredPhraseTotal is len(Example.RequiredPhrases).
	ReflectionRounds    int           // ReflectionRounds is len(Answer.Diagnostics.Reflection.RoundDetails); 0 when reflection was off.
	AdoptedRound        int           // AdoptedRound mirrors Diagnostics.Reflection.AdoptedRound (1-based; 0 when reflection was off).
	FollowupsUsed       int           // FollowupsUsed mirrors Diagnostics.Reflection.FollowupQueriesUsed.
	ActiveFired         bool          // ActiveFired is true when any round had non-empty FollowupQueries.
	GraderEnabled       bool          // GraderEnabled is true when any round had non-empty ChunkScores (grading actually ran).
	ActiveEnabled       bool          // ActiveEnabled mirrors Options.Reflection.EnableActiveRetrieval at run time.
	Judgement           Judgement     // Judgement is the LLM-as-judge verdict on this example; zero value when JudgeApplied=false. v1.4.0.
	JudgeApplied        bool          // JudgeApplied is true when AnswerBenchmark.Judge was non-nil AND the judge call returned no error. v1.4.0.
}

// BenchmarkResult is the full output of an AnswerBenchmark run: the
// dataset that was scored, the aggregated metrics, and the per-example
// trace.
type BenchmarkResult struct {
	Dataset    AnswerDataset         // Dataset is the dataset that was evaluated.
	Metrics    BenchmarkMetrics      // Metrics is the aggregated scoreboard.
	PerExample []AnswerExampleResult // PerExample is the per-example detail.
}

// AnswerBenchmark runs an AnswerDataset through an Asker and scores
// each generated answer on ExactMatch, token F1, and required-phrase
// recall, plus reflection / active-retrieval / grading signal
// aggregation from the answer's diagnostics. It deliberately reuses
// the existing eval.Asker interface — v1.3.0 introduces no new seam
// onto rag.System.
//
// Compatibility note: this exported struct may grow additively over
// time, so keyed composite literals are recommended.
type AnswerBenchmark struct {
	Asker   Asker          // Asker runs the answer pipeline under evaluation; reuses eval.Asker.
	Options rag.AskOptions // Options is the base AskOptions applied to every Ask call (overlaid per example).
	Judge   Judge          // Judge optionally scores each answer for groundedness/relevance; nil = textual metrics only. v1.4.0.

	// Parallelism bounds concurrent example dispatch:
	//   - 0 or 1   → sequential (v1.3.0 behavior; preserved byte-for-byte).
	//   - negative → coerced to sequential.
	//   - >=2      → bounded worker pool fans out Ask + (optional) Judge.
	//
	// When >=2, AnswerBenchmark.Asker and AnswerBenchmark.Judge MUST be
	// safe for concurrent use by multiple goroutines. *rag.System
	// satisfies that contract. Asker work is treated as I/O-bound, so
	// Parallelism is NOT capped at runtime.NumCPU/GOMAXPROCS — pick a
	// value that matches your downstream concurrency budget.
	//
	// Error semantics: a sticky first-error gate aborts the run. The
	// first failing Ask or Judge wins; in-flight siblings finish their
	// current call naturally (no context cancellation). Determinism:
	// PerExample order matches dataset.Examples regardless of
	// completion order — each goroutine writes to a pre-allocated slot
	// by index.
	//
	// v1.4.0.
	Parallelism int
}

// normalizeParallelism reduces AnswerBenchmark.Parallelism to a valid
// worker count. Values <= 1 (including negatives) collapse to 1, which
// the runner uses to route through the v1.3.0 sequential loop unchanged.
func normalizeParallelism(p int) int {
	if p < 2 {
		return 1
	}
	return p
}

// Run executes the benchmark. The base Options is copied per example;
// Search.TopK is set from dataset.TopK and Search.Namespace is overlaid
// from the example's Namespace when non-empty. The runner aborts on the
// first Ask or Judge error, wrapping it with the offending query.
//
// Dispatch is sequential when normalizeParallelism(b.Parallelism) == 1
// (the v1.3.0 behavior — preserved byte-for-byte). When Parallelism>=2,
// examples are fanned out via a buffered-channel semaphore + WaitGroup
// worker pool that mirrors rag.System.runFollowupsParallel
// (rag/active_retrieval.go:278-313). PerExample order matches
// dataset.Examples order under both modes (index-keyed write into a
// pre-allocated slice).
func (b AnswerBenchmark) Run(ctx context.Context, dataset AnswerDataset) (BenchmarkResult, error) {
	if b.Asker == nil {
		return BenchmarkResult{}, errors.New("eval: Asker is required")
	}
	if dataset.TopK <= 0 {
		return BenchmarkResult{}, fmt.Errorf("eval: dataset %q has TopK <= 0", dataset.Name)
	}

	workers := normalizeParallelism(b.Parallelism)
	if workers == 1 {
		return b.runSequential(ctx, dataset)
	}
	return b.runParallel(ctx, dataset, workers)
}

// runSequential is the v1.3.0 sequential dispatch path, unchanged in
// shape: a single goroutine walks the dataset in order and aborts on
// the first Ask or Judge error.
func (b AnswerBenchmark) runSequential(ctx context.Context, dataset AnswerDataset) (BenchmarkResult, error) {
	per := make([]AnswerExampleResult, 0, len(dataset.Examples))
	for _, ex := range dataset.Examples {
		r, err := b.runOne(ctx, dataset, ex)
		if err != nil {
			return BenchmarkResult{}, err
		}
		per = append(per, r)
	}
	return BenchmarkResult{
		Dataset:    dataset,
		Metrics:    aggregateBenchmarkMetrics(per),
		PerExample: per,
	}, nil
}

// runParallel dispatches examples over a bounded worker pool. The
// semaphore is a buffered chan with capacity `workers`. Results are
// written by index into a pre-allocated slice so the per-example order
// matches dataset.Examples regardless of completion order. Errors are
// sticky: the first Ask or Judge error wins via errMu/firstErr; siblings
// finish naturally (no context cancellation of siblings — see v1.4.0
// design Q-G).
func (b AnswerBenchmark) runParallel(ctx context.Context, dataset AnswerDataset, workers int) (BenchmarkResult, error) {
	results := make([]AnswerExampleResult, len(dataset.Examples))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error

	for i, ex := range dataset.Examples {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int, ex AnswerExample) {
			defer wg.Done()
			defer func() { <-sem }()

			// Sticky-error gate: if a sibling already failed, skip
			// further work. In-flight workers still finish naturally.
			errMu.Lock()
			already := firstErr != nil
			errMu.Unlock()
			if already {
				return
			}

			r, err := b.runOne(ctx, dataset, ex)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				return
			}
			results[i] = r
		}(i, ex)
	}
	wg.Wait()

	if firstErr != nil {
		return BenchmarkResult{}, firstErr
	}
	return BenchmarkResult{
		Dataset:    dataset,
		Metrics:    aggregateBenchmarkMetrics(results),
		PerExample: results,
	}, nil
}

// runOne executes the Ask + (optional) Judge step for a single example,
// returning the scored AnswerExampleResult or an error wrapped with the
// offending query. It is the unit of work shared by the sequential and
// parallel dispatch paths.
//
// Concurrency: when called from runParallel, multiple goroutines may
// execute runOne in parallel; the Asker and Judge implementations MUST
// be safe for concurrent use under that mode. The local rag.AskOptions
// is a fresh copy of b.Options per call, so no Options mutation races.
func (b AnswerBenchmark) runOne(ctx context.Context, dataset AnswerDataset, ex AnswerExample) (AnswerExampleResult, error) {
	opts := b.Options
	opts.Search.TopK = dataset.TopK
	if ex.Namespace != "" {
		opts.Search.Namespace = ex.Namespace
	}
	ans, err := b.Asker.Ask(ctx, ex.Query, opts)
	if err != nil {
		return AnswerExampleResult{}, fmt.Errorf("eval: ask %q: %w", ex.Query, err)
	}
	r := scoreAnswerExample(ex, ans, opts)
	if b.Judge != nil {
		j, jerr := b.Judge.Judge(ctx, JudgeRequest{
			Query:   ex.Query,
			Answer:  ans.Text,
			Context: contextPassages(ans),
		})
		if jerr != nil {
			return AnswerExampleResult{}, fmt.Errorf("eval: judge %q: %w", ex.Query, jerr)
		}
		r.Judgement = j
		r.JudgeApplied = true
	}
	return r, nil
}

// aggregateBenchmarkMetrics reduces the per-example trace into the
// dataset-level scoreboard. Feature-gated metrics (reflection-rounds
// mean, adopted-round histogram, grader adoption rate, follow-up usage,
// active-retrieval fire rate, required-phrase recall) carry a separate
// "applicable" counter — when it hits zero across the dataset the
// metric is emitted as math.NaN() and the histogram slice as nil. This
// preserves the "no information vs zero" distinction required for the
// scoreboard to be honest about which features were exercised.
func aggregateBenchmarkMetrics(per []AnswerExampleResult) BenchmarkMetrics {
	metrics := BenchmarkMetrics{Examples: len(per)}
	n := len(per)
	if n == 0 {
		// Empty dataset: every feature-gated metric is vacuously NaN.
		metrics.ExactMatch = math.NaN()
		metrics.F1Token = math.NaN()
		metrics.RequiredPhraseRecall = math.NaN()
		metrics.ReflectionRoundsMean = math.NaN()
		metrics.GraderAdoptionRate = math.NaN()
		metrics.FollowupQueriesUsedMean = math.NaN()
		metrics.ActiveRetrievalFireRate = math.NaN()
		metrics.MeanGroundedness = math.NaN()
		metrics.MeanAnswerRelevance = math.NaN()
		return metrics
	}

	var (
		emSum, f1Sum                        float64
		phraseHits, phraseTotal             int
		reflectionApplicable                int
		reflectionRoundsSum                 int
		maxAdoptedRound                     int
		adoptedRoundCounts                  []int
		graderApplicable, graderEarlyAdopts int
		activeApplicable                    int
		activeFollowupsSum                  int
		activeFired                         int
		judgeApplicable                     int
		sumGroundedness, sumRelevance       float64
	)

	for _, r := range per {
		if r.ExactMatch {
			emSum++
		}
		f1Sum += r.F1Token
		phraseHits += r.RequiredPhraseHits
		phraseTotal += r.RequiredPhraseTotal

		if r.ReflectionRounds > 0 {
			reflectionApplicable++
			reflectionRoundsSum += r.ReflectionRounds
			if r.AdoptedRound > maxAdoptedRound {
				maxAdoptedRound = r.AdoptedRound
			}
		}
		if r.GraderEnabled {
			graderApplicable++
			// AdoptedRound != last round = earlier adoption attributable to grading.
			if r.AdoptedRound > 0 && r.AdoptedRound != r.ReflectionRounds {
				graderEarlyAdopts++
			}
		}
		if r.ActiveEnabled {
			activeApplicable++
			activeFollowupsSum += r.FollowupsUsed
			if r.ActiveFired {
				activeFired++
			}
		}
		if r.JudgeApplied {
			judgeApplicable++
			sumGroundedness += r.Judgement.Groundedness
			sumRelevance += r.Judgement.AnswerRelevance
		}
	}

	metrics.ExactMatch = emSum / float64(n)
	metrics.F1Token = f1Sum / float64(n)

	if phraseTotal > 0 {
		metrics.RequiredPhraseRecall = float64(phraseHits) / float64(phraseTotal)
	} else {
		metrics.RequiredPhraseRecall = math.NaN()
	}

	if reflectionApplicable > 0 {
		metrics.ReflectionRoundsMean = float64(reflectionRoundsSum) / float64(reflectionApplicable)
		adoptedRoundCounts = make([]int, maxAdoptedRound)
		for _, r := range per {
			if r.AdoptedRound > 0 {
				adoptedRoundCounts[r.AdoptedRound-1]++
			}
		}
		metrics.AdoptedRoundCounts = adoptedRoundCounts
	} else {
		metrics.ReflectionRoundsMean = math.NaN()
		metrics.AdoptedRoundCounts = nil
	}

	if graderApplicable > 0 {
		metrics.GraderAdoptionRate = float64(graderEarlyAdopts) / float64(graderApplicable)
	} else {
		metrics.GraderAdoptionRate = math.NaN()
	}

	if activeApplicable > 0 {
		metrics.FollowupQueriesUsedMean = float64(activeFollowupsSum) / float64(activeApplicable)
		metrics.ActiveRetrievalFireRate = float64(activeFired) / float64(activeApplicable)
	} else {
		metrics.FollowupQueriesUsedMean = math.NaN()
		metrics.ActiveRetrievalFireRate = math.NaN()
	}

	if judgeApplicable > 0 {
		metrics.MeanGroundedness = sumGroundedness / float64(judgeApplicable)
		metrics.MeanAnswerRelevance = sumRelevance / float64(judgeApplicable)
	} else {
		metrics.MeanGroundedness = math.NaN()
		metrics.MeanAnswerRelevance = math.NaN()
	}

	return metrics
}

// contextPassages projects the rag.Answer's retrieved hits into the
// flat []string shape expected by JudgeRequest.Context. The order is
// preserved (Hits[i] → Context[i]) so the judge sees passages in the
// same rank order they were retrieved. Mirrors the projection in
// triad.go's TriadEvaluator.Run (see triad.go:81-87).
func contextPassages(ans rag.Answer) []string {
	if len(ans.Hits) == 0 {
		return nil
	}
	out := make([]string, 0, len(ans.Hits))
	for _, hit := range ans.Hits {
		out = append(out, hit.Chunk.Content)
	}
	return out
}

// scoreAnswerExample assembles the per-example trace. Aggregation
// across examples is layered on top in BenchmarkMetrics (commit 4).
func scoreAnswerExample(ex AnswerExample, ans rag.Answer, opts rag.AskOptions) AnswerExampleResult {
	res := AnswerExampleResult{
		Example:             ex,
		Answer:              ans.Text,
		ExactMatch:          exactMatch(ans.Text, ex.GoldAnswers),
		F1Token:             f1(ans.Text, ex.GoldAnswers),
		RequiredPhraseTotal: len(ex.RequiredPhrases),
	}
	for _, phrase := range ex.RequiredPhrases {
		if strings.Contains(ans.Text, phrase) {
			res.RequiredPhraseHits++
		}
	}
	reflection := ans.Diagnostics.Reflection
	res.ReflectionRounds = len(reflection.RoundDetails)
	res.AdoptedRound = reflection.AdoptedRound
	res.FollowupsUsed = reflection.FollowupQueriesUsed
	for _, rd := range reflection.RoundDetails {
		if len(rd.FollowupQueries) > 0 {
			res.ActiveFired = true
		}
		if len(rd.ChunkScores) > 0 {
			res.GraderEnabled = true
		}
	}
	if opts.Reflection != nil {
		res.ActiveEnabled = opts.Reflection.EnableActiveRetrieval
	}
	return res
}
