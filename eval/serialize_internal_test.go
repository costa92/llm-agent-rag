package eval

import (
	"bytes"
	"math"
	"reflect"
	"strings"
	"testing"
)

// TestFloatOrNullPreservesFinite asserts floatOrNull returns a pointer
// to the value for ordinary finite inputs (including 0.0).
func TestFloatOrNullPreservesFinite(t *testing.T) {
	v := 0.5
	got := floatOrNull(v)
	if got == nil {
		t.Fatalf("floatOrNull(0.5) = nil; want non-nil")
	}
	if *got != 0.5 {
		t.Fatalf("floatOrNull(0.5) deref = %v; want 0.5", *got)
	}
	zero := 0.0
	if p := floatOrNull(zero); p == nil || *p != 0.0 {
		t.Fatalf("floatOrNull(0.0) = %v; want non-nil pointing at 0", p)
	}
}

// TestFloatOrNullReturnsNilForNaN asserts NaN collapses to nil.
func TestFloatOrNullReturnsNilForNaN(t *testing.T) {
	if got := floatOrNull(math.NaN()); got != nil {
		t.Fatalf("floatOrNull(NaN) = %v; want nil", got)
	}
}

// TestFloatOrNullReturnsNilForInf asserts both ±Inf collapse to nil.
func TestFloatOrNullReturnsNilForInf(t *testing.T) {
	if got := floatOrNull(math.Inf(1)); got != nil {
		t.Fatalf("floatOrNull(+Inf) = %v; want nil", got)
	}
	if got := floatOrNull(math.Inf(-1)); got != nil {
		t.Fatalf("floatOrNull(-Inf) = %v; want nil", got)
	}
}

// TestFloatFromPtrNaNForNil asserts nil rehydrates as NaN.
func TestFloatFromPtrNaNForNil(t *testing.T) {
	got := floatFromPtr(nil)
	if !math.IsNaN(got) {
		t.Fatalf("floatFromPtr(nil) = %v; want NaN", got)
	}
}

// TestFloatFromPtrPassthrough asserts non-nil pointer values pass through.
func TestFloatFromPtrPassthrough(t *testing.T) {
	v := 0.5
	if got := floatFromPtr(&v); got != 0.5 {
		t.Fatalf("floatFromPtr(&0.5) = %v; want 0.5", got)
	}
	z := 0.0
	if got := floatFromPtr(&z); got != 0.0 {
		t.Fatalf("floatFromPtr(&0.0) = %v; want 0.0", got)
	}
}

// sampleBenchmarkResult builds a BenchmarkResult with a mix of finite +
// NaN metric fields. Used by the round-trip tests below.
func sampleBenchmarkResult() BenchmarkResult {
	return BenchmarkResult{
		Dataset: AnswerDataset{
			Name: "synth",
			TopK: 5,
			Examples: []AnswerExample{
				{
					Example:         Example{Query: "what is RAG?", Namespace: "docs", GoldDocIDs: []string{"d1"}, GoldChunkIDs: []string{"c1"}, Notes: "intro"},
					GoldAnswers:     []string{"retrieval-augmented generation"},
					RequiredPhrases: []string{"RAG"},
				},
				{
					Example:     Example{Query: "drift?", Namespace: "docs"},
					GoldAnswers: []string{"a hybrid"},
				},
			},
		},
		Metrics: BenchmarkMetrics{
			Examples:                2,
			ExactMatch:              0.5,
			F1Token:                 0.62,
			RequiredPhraseRecall:    math.NaN(), // no phrases labeled across whole dataset
			ReflectionRoundsMean:    math.NaN(), // reflection off
			AdoptedRoundCounts:      nil,
			GraderAdoptionRate:      math.NaN(),
			FollowupQueriesUsedMean: 1.0,
			ActiveRetrievalFireRate: 0.5,
			MeanGroundedness:        0.85,
			MeanAnswerRelevance:     math.NaN(),
		},
		PerExample: []AnswerExampleResult{
			{
				Example:             AnswerExample{Example: Example{Query: "what is RAG?", Namespace: "docs"}, GoldAnswers: []string{"retrieval-augmented generation"}, RequiredPhrases: []string{"RAG"}},
				Answer:              "Retrieval-augmented generation.",
				ExactMatch:          true,
				F1Token:             0.92,
				RequiredPhraseHits:  1,
				RequiredPhraseTotal: 1,
				ReflectionRounds:    0,
				AdoptedRound:        0,
				FollowupsUsed:       0,
				ActiveFired:         false,
				GraderEnabled:       false,
				ActiveEnabled:       true,
				Judgement:           Judgement{Groundedness: 0.9, AnswerRelevance: 0.95, Rationale: "good"},
				JudgeApplied:        true,
			},
			{
				Example: AnswerExample{Example: Example{Query: "drift?", Namespace: "docs"}, GoldAnswers: []string{"a hybrid"}},
				Answer:  "a hybrid search path",
				F1Token: 0.33,
			},
		},
	}
}

// floatEqualNaN compares two floats, treating NaN==NaN as true.
func floatEqualNaN(a, b float64) bool {
	if math.IsNaN(a) && math.IsNaN(b) {
		return true
	}
	return a == b
}

// metricsEqual is a shallow BenchmarkMetrics comparator that handles NaN
// per-field (reflect.DeepEqual treats NaN != NaN).
func metricsEqual(a, b BenchmarkMetrics) bool {
	if a.Examples != b.Examples {
		return false
	}
	if !reflect.DeepEqual(a.AdoptedRoundCounts, b.AdoptedRoundCounts) {
		return false
	}
	pairs := []struct{ x, y float64 }{
		{a.ExactMatch, b.ExactMatch},
		{a.F1Token, b.F1Token},
		{a.RequiredPhraseRecall, b.RequiredPhraseRecall},
		{a.ReflectionRoundsMean, b.ReflectionRoundsMean},
		{a.GraderAdoptionRate, b.GraderAdoptionRate},
		{a.FollowupQueriesUsedMean, b.FollowupQueriesUsedMean},
		{a.ActiveRetrievalFireRate, b.ActiveRetrievalFireRate},
		{a.MeanGroundedness, b.MeanGroundedness},
		{a.MeanAnswerRelevance, b.MeanAnswerRelevance},
	}
	for _, p := range pairs {
		if !floatEqualNaN(p.x, p.y) {
			return false
		}
	}
	return true
}

// TestMarshalBenchmark_RoundTrip exercises a populated BenchmarkResult
// through Marshal -> Unmarshal and asserts finite fields exact-equal and
// NaN fields stay NaN.
func TestMarshalBenchmark_RoundTrip(t *testing.T) {
	in := sampleBenchmarkResult()
	b, err := MarshalBenchmark(in)
	if err != nil {
		t.Fatalf("MarshalBenchmark: %v", err)
	}
	out, err := UnmarshalBenchmark(b)
	if err != nil {
		t.Fatalf("UnmarshalBenchmark: %v", err)
	}
	if !metricsEqual(in.Metrics, out.Metrics) {
		t.Fatalf("metrics differ:\n in=%+v\nout=%+v", in.Metrics, out.Metrics)
	}
	if !reflect.DeepEqual(in.Dataset, out.Dataset) {
		t.Fatalf("dataset differs:\n in=%+v\nout=%+v", in.Dataset, out.Dataset)
	}
	if len(in.PerExample) != len(out.PerExample) {
		t.Fatalf("PerExample len = %d; want %d", len(out.PerExample), len(in.PerExample))
	}
	for i := range in.PerExample {
		ai, bi := in.PerExample[i], out.PerExample[i]
		// F1Token is finite in fixture; compare directly.
		if ai.F1Token != bi.F1Token {
			t.Fatalf("PerExample[%d].F1Token = %v; want %v", i, bi.F1Token, ai.F1Token)
		}
		if !reflect.DeepEqual(ai.Example, bi.Example) {
			t.Fatalf("PerExample[%d].Example differs", i)
		}
	}
}

// TestMarshalBenchmark_AllNaN exercises an empty-dataset Metrics (every
// NaN-able field NaN) through the codec.
func TestMarshalBenchmark_AllNaN(t *testing.T) {
	in := BenchmarkResult{
		Dataset: AnswerDataset{Name: "empty", TopK: 5},
		Metrics: BenchmarkMetrics{
			Examples:                0,
			ExactMatch:              math.NaN(),
			F1Token:                 math.NaN(),
			RequiredPhraseRecall:    math.NaN(),
			ReflectionRoundsMean:    math.NaN(),
			GraderAdoptionRate:      math.NaN(),
			FollowupQueriesUsedMean: math.NaN(),
			ActiveRetrievalFireRate: math.NaN(),
			MeanGroundedness:        math.NaN(),
			MeanAnswerRelevance:     math.NaN(),
		},
	}
	b, err := MarshalBenchmark(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out, err := UnmarshalBenchmark(b)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !metricsEqual(in.Metrics, out.Metrics) {
		t.Fatalf("all-NaN metrics did not round-trip:\n in=%+v\nout=%+v", in.Metrics, out.Metrics)
	}
}

// TestMarshalBenchmark_NoNaN exercises a populated BenchmarkResult that
// has no NaN anywhere; the second marshal should be byte-equal to the
// first.
func TestMarshalBenchmark_NoNaN(t *testing.T) {
	in := BenchmarkResult{
		Dataset: AnswerDataset{Name: "finite", TopK: 5},
		Metrics: BenchmarkMetrics{
			Examples:                1,
			ExactMatch:              1.0,
			F1Token:                 1.0,
			RequiredPhraseRecall:    1.0,
			ReflectionRoundsMean:    2.0,
			AdoptedRoundCounts:      []int{0, 1},
			GraderAdoptionRate:      0.5,
			FollowupQueriesUsedMean: 0.0,
			ActiveRetrievalFireRate: 0.0,
			MeanGroundedness:        1.0,
			MeanAnswerRelevance:     1.0,
		},
		PerExample: []AnswerExampleResult{{
			Example: AnswerExample{Example: Example{Query: "q"}},
			Answer:  "a",
			F1Token: 1.0,
		}},
	}
	b1, err := MarshalBenchmark(in)
	if err != nil {
		t.Fatalf("Marshal #1: %v", err)
	}
	mid, err := UnmarshalBenchmark(b1)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	b2, err := MarshalBenchmark(mid)
	if err != nil {
		t.Fatalf("Marshal #2: %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("byte stability lost:\nfirst : %s\nsecond: %s", b1, b2)
	}
}

// TestUnmarshalBenchmark_StrictUnknownField asserts unknown top-level
// keys error (DisallowUnknownFields). Forward-compat trade-off documented
// in CHANGELOG v1.6.0.
func TestUnmarshalBenchmark_StrictUnknownField(t *testing.T) {
	raw := []byte(`{"dataset":{"name":"x","top_k":5},"metrics":{"examples":0,"exact_match":null,"f1_token":null,"required_phrase_recall":null,"reflection_rounds_mean":null,"grader_adoption_rate":null,"followup_queries_used_mean":null,"active_retrieval_fire_rate":null,"mean_groundedness":null,"mean_answer_relevance":null},"per_example":null,"weird_extra":42}`)
	_, err := UnmarshalBenchmark(raw)
	if err == nil {
		t.Fatalf("UnmarshalBenchmark accepted unknown field; want error")
	}
	if !strings.Contains(err.Error(), "weird_extra") {
		t.Fatalf("error does not mention unknown field: %v", err)
	}
}
