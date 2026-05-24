package eval

// This file implements the v1.6.0 C-DiagnosticsExport canonical codec
// for eval.BenchmarkResult.
//
// Why a separate codec instead of json: tags on BenchmarkMetrics?
//
//   - math.NaN() is the documented v1.3.0 "feature was off everywhere"
//     sentinel for half the metric fields. encoding/json refuses to
//     marshal NaN (math.IsNaN -> json: unsupported value) and even if
//     it did, the round-trip would be platform-dependent. We canonicalize
//     NaN/Inf to JSON null on the wire and rehydrate to math.NaN() on
//     read. The pointer-to-float wire types are unexported (suffix Wire)
//     so callers depend only on Marshal/Unmarshal functions — future
//     reshuffles of the wire shape do not break the public API.
//
//   - The v1.6.0 design lock explicitly forbids adding json: tags to
//     existing exported structs (BenchmarkMetrics, AnswerExampleResult).
//     Wire types in this new file are the additive, non-breaking path.
//
// The streaming JSONL codec (WriteBenchmarkJSONL / ReadBenchmarkJSONL)
// lives in serialize_jsonl.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

// floatOrNull returns nil if f is NaN or ±Inf, else &f. The pointer is
// to a fresh copy so callers can stash the result in a struct field
// without aliasing the caller's local.
func floatOrNull(f float64) *float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	v := f
	return &v
}

// floatFromPtr returns math.NaN() if p is nil, else *p. It is the inverse
// of floatOrNull for unmarshal: JSON null -> NaN, JSON number -> the
// number.
func floatFromPtr(p *float64) float64 {
	if p == nil {
		return math.NaN()
	}
	return *p
}

// benchmarkMetricsWire is the on-disk shape of BenchmarkMetrics. Every
// finite-or-NaN float is *float64 so JSON null preserves the v1.3.0
// "feature was off everywhere" sentinel. The Examples counter and the
// AdoptedRoundCounts histogram have no NaN flavor and ship as plain
// values.
type benchmarkMetricsWire struct {
	Examples int `json:"examples"`

	ExactMatch           *float64 `json:"exact_match"`
	F1Token              *float64 `json:"f1_token"`
	RequiredPhraseRecall *float64 `json:"required_phrase_recall"`

	ReflectionRoundsMean    *float64 `json:"reflection_rounds_mean"`
	AdoptedRoundCounts      []int    `json:"adopted_round_counts,omitempty"`
	GraderAdoptionRate      *float64 `json:"grader_adoption_rate"`
	FollowupQueriesUsedMean *float64 `json:"followup_queries_used_mean"`
	ActiveRetrievalFireRate *float64 `json:"active_retrieval_fire_rate"`

	MeanGroundedness    *float64 `json:"mean_groundedness"`
	MeanAnswerRelevance *float64 `json:"mean_answer_relevance"`
}

func metricsToWire(m BenchmarkMetrics) benchmarkMetricsWire {
	return benchmarkMetricsWire{
		Examples:                m.Examples,
		ExactMatch:              floatOrNull(m.ExactMatch),
		F1Token:                 floatOrNull(m.F1Token),
		RequiredPhraseRecall:    floatOrNull(m.RequiredPhraseRecall),
		ReflectionRoundsMean:    floatOrNull(m.ReflectionRoundsMean),
		AdoptedRoundCounts:      m.AdoptedRoundCounts,
		GraderAdoptionRate:      floatOrNull(m.GraderAdoptionRate),
		FollowupQueriesUsedMean: floatOrNull(m.FollowupQueriesUsedMean),
		ActiveRetrievalFireRate: floatOrNull(m.ActiveRetrievalFireRate),
		MeanGroundedness:        floatOrNull(m.MeanGroundedness),
		MeanAnswerRelevance:     floatOrNull(m.MeanAnswerRelevance),
	}
}

func metricsFromWire(w benchmarkMetricsWire) BenchmarkMetrics {
	return BenchmarkMetrics{
		Examples:                w.Examples,
		ExactMatch:              floatFromPtr(w.ExactMatch),
		F1Token:                 floatFromPtr(w.F1Token),
		RequiredPhraseRecall:    floatFromPtr(w.RequiredPhraseRecall),
		ReflectionRoundsMean:    floatFromPtr(w.ReflectionRoundsMean),
		AdoptedRoundCounts:      w.AdoptedRoundCounts,
		GraderAdoptionRate:      floatFromPtr(w.GraderAdoptionRate),
		FollowupQueriesUsedMean: floatFromPtr(w.FollowupQueriesUsedMean),
		ActiveRetrievalFireRate: floatFromPtr(w.ActiveRetrievalFireRate),
		MeanGroundedness:        floatFromPtr(w.MeanGroundedness),
		MeanAnswerRelevance:     floatFromPtr(w.MeanAnswerRelevance),
	}
}

// judgementWire mirrors Judgement. The two scores are documented in [0,1]
// in the in-memory shape; they are always finite (no NaN sentinel),
// so plain float64 is correct here.
type judgementWire struct {
	Groundedness    float64 `json:"groundedness"`
	AnswerRelevance float64 `json:"answer_relevance"`
	Rationale       string  `json:"rationale,omitempty"`
}

func judgementToWire(j Judgement) judgementWire {
	return judgementWire{
		Groundedness:    j.Groundedness,
		AnswerRelevance: j.AnswerRelevance,
		Rationale:       j.Rationale,
	}
}

func judgementFromWire(w judgementWire) Judgement {
	return Judgement{
		Groundedness:    w.Groundedness,
		AnswerRelevance: w.AnswerRelevance,
		Rationale:       w.Rationale,
	}
}

// answerExampleResultWire mirrors AnswerExampleResult. The embedded
// AnswerExample is reused as-is (it already has json tags via Example).
type answerExampleResultWire struct {
	Example             AnswerExample `json:"example"`
	Answer              string        `json:"answer"`
	ExactMatch          bool          `json:"exact_match"`
	F1Token             float64       `json:"f1_token"`
	RequiredPhraseHits  int           `json:"required_phrase_hits"`
	RequiredPhraseTotal int           `json:"required_phrase_total"`
	ReflectionRounds    int           `json:"reflection_rounds"`
	AdoptedRound        int           `json:"adopted_round"`
	FollowupsUsed       int           `json:"followups_used"`
	ActiveFired         bool          `json:"active_fired"`
	GraderEnabled       bool          `json:"grader_enabled"`
	ActiveEnabled       bool          `json:"active_enabled"`
	Judgement           judgementWire `json:"judgement"`
	JudgeApplied        bool          `json:"judge_applied"`
}

func exampleResultToWire(r AnswerExampleResult) answerExampleResultWire {
	return answerExampleResultWire{
		Example:             r.Example,
		Answer:              r.Answer,
		ExactMatch:          r.ExactMatch,
		F1Token:             r.F1Token,
		RequiredPhraseHits:  r.RequiredPhraseHits,
		RequiredPhraseTotal: r.RequiredPhraseTotal,
		ReflectionRounds:    r.ReflectionRounds,
		AdoptedRound:        r.AdoptedRound,
		FollowupsUsed:       r.FollowupsUsed,
		ActiveFired:         r.ActiveFired,
		GraderEnabled:       r.GraderEnabled,
		ActiveEnabled:       r.ActiveEnabled,
		Judgement:           judgementToWire(r.Judgement),
		JudgeApplied:        r.JudgeApplied,
	}
}

func exampleResultFromWire(w answerExampleResultWire) AnswerExampleResult {
	return AnswerExampleResult{
		Example:             w.Example,
		Answer:              w.Answer,
		ExactMatch:          w.ExactMatch,
		F1Token:             w.F1Token,
		RequiredPhraseHits:  w.RequiredPhraseHits,
		RequiredPhraseTotal: w.RequiredPhraseTotal,
		ReflectionRounds:    w.ReflectionRounds,
		AdoptedRound:        w.AdoptedRound,
		FollowupsUsed:       w.FollowupsUsed,
		ActiveFired:         w.ActiveFired,
		GraderEnabled:       w.GraderEnabled,
		ActiveEnabled:       w.ActiveEnabled,
		Judgement:           judgementFromWire(w.Judgement),
		JudgeApplied:        w.JudgeApplied,
	}
}

// answerDatasetWire mirrors AnswerDataset.
type answerDatasetWire struct {
	Name     string          `json:"name"`
	TopK     int             `json:"top_k"`
	Examples []AnswerExample `json:"examples,omitempty"`
}

// answerDatasetSummaryWire is the dataset summary written into the JSONL
// header line — it omits Examples since the streaming format carries the
// labeled queries on the per-example lines and the reader rebuilds them.
type answerDatasetSummaryWire struct {
	Name string `json:"name"`
	TopK int    `json:"top_k"`
}

// benchmarkResultWire is the on-disk shape of BenchmarkResult for the
// single-document codec (MarshalBenchmark / UnmarshalBenchmark).
type benchmarkResultWire struct {
	Dataset    answerDatasetWire         `json:"dataset"`
	Metrics    benchmarkMetricsWire      `json:"metrics"`
	PerExample []answerExampleResultWire `json:"per_example"`
}

func benchmarkToWire(r BenchmarkResult) benchmarkResultWire {
	per := make([]answerExampleResultWire, len(r.PerExample))
	for i, e := range r.PerExample {
		per[i] = exampleResultToWire(e)
	}
	return benchmarkResultWire{
		Dataset: answerDatasetWire{
			Name:     r.Dataset.Name,
			TopK:     r.Dataset.TopK,
			Examples: r.Dataset.Examples,
		},
		Metrics:    metricsToWire(r.Metrics),
		PerExample: per,
	}
}

func benchmarkFromWire(w benchmarkResultWire) BenchmarkResult {
	per := make([]AnswerExampleResult, len(w.PerExample))
	for i, e := range w.PerExample {
		per[i] = exampleResultFromWire(e)
	}
	return BenchmarkResult{
		Dataset: AnswerDataset{
			Name:     w.Dataset.Name,
			TopK:     w.Dataset.TopK,
			Examples: w.Dataset.Examples,
		},
		Metrics:    metricsFromWire(w.Metrics),
		PerExample: per,
	}
}

// MarshalBenchmark serializes a BenchmarkResult to canonical JSON. NaN
// and ±Inf in BenchmarkMetrics are encoded as JSON null per field; round
// through UnmarshalBenchmark to recover math.NaN().
//
// The output is a single JSON document. Use WriteBenchmarkJSONL for the
// streaming line-per-example format.
func MarshalBenchmark(r BenchmarkResult) ([]byte, error) {
	return json.Marshal(benchmarkToWire(r))
}

// UnmarshalBenchmark parses canonical JSON written by MarshalBenchmark.
// JSON null for any float field rehydrates to math.NaN(). Unknown
// top-level fields are rejected (DisallowUnknownFields) so callers catch
// schema drift early; future minor versions add fields and bump readers.
func UnmarshalBenchmark(b []byte) (BenchmarkResult, error) {
	var w benchmarkResultWire
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return BenchmarkResult{}, fmt.Errorf("eval: unmarshal benchmark: %w", err)
	}
	return benchmarkFromWire(w), nil
}
