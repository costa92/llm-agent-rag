package eval_test

import (
	"context"
	"math"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/rag"
)

// TestBenchmarkMetricsReflectionOffYieldsNaN asserts the four
// feature-gated metrics carry math.NaN() sentinels when reflection
// is off across the whole dataset. AdoptedRoundCounts must be nil.
func TestBenchmarkMetricsReflectionOffYieldsNaN(t *testing.T) {
	asker := &scriptedAsker{byQuery: map[string]rag.Answer{
		"q1": {Text: "a"},
		"q2": {Text: "b"},
	}}
	ds := eval.AnswerDataset{Name: "off", TopK: 3, Examples: []eval.AnswerExample{
		{Example: eval.Example{Query: "q1"}, GoldAnswers: []string{"a"}},
		{Example: eval.Example{Query: "q2"}, GoldAnswers: []string{"b"}},
	}}
	res, err := (eval.AnswerBenchmark{Asker: asker}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !math.IsNaN(res.Metrics.ReflectionRoundsMean) {
		t.Errorf("ReflectionRoundsMean = %v, want NaN", res.Metrics.ReflectionRoundsMean)
	}
	if !math.IsNaN(res.Metrics.GraderAdoptionRate) {
		t.Errorf("GraderAdoptionRate = %v, want NaN", res.Metrics.GraderAdoptionRate)
	}
	if !math.IsNaN(res.Metrics.FollowupQueriesUsedMean) {
		t.Errorf("FollowupQueriesUsedMean = %v, want NaN", res.Metrics.FollowupQueriesUsedMean)
	}
	if !math.IsNaN(res.Metrics.ActiveRetrievalFireRate) {
		t.Errorf("ActiveRetrievalFireRate = %v, want NaN", res.Metrics.ActiveRetrievalFireRate)
	}
	if res.Metrics.AdoptedRoundCounts != nil {
		t.Errorf("AdoptedRoundCounts = %v, want nil", res.Metrics.AdoptedRoundCounts)
	}
	// ExactMatch is not feature-gated and should be a real number.
	if math.IsNaN(res.Metrics.ExactMatch) {
		t.Errorf("ExactMatch = NaN, want real number (both examples matched)")
	}
	if math.Abs(res.Metrics.ExactMatch-1.0) > 1e-9 {
		t.Errorf("ExactMatch = %v, want 1.0", res.Metrics.ExactMatch)
	}
}

// answerWithRound is a fixture helper that hand-builds an Answer whose
// ReflectionDiagnostics shows N rounds with the adopted round set to
// adopted (1-based). Used to drive the AdoptedRoundCounts distribution
// and GraderAdoptionRate tests.
func answerWithRound(rounds, adopted int, text string, grading bool) rag.Answer {
	details := make([]rag.ReflectionRoundDiagnostics, 0, rounds)
	for i := 1; i <= rounds; i++ {
		rd := rag.ReflectionRoundDiagnostics{Round: i}
		if grading {
			rd.ChunkScores = []rag.ChunkScore{{HitID: "x", Relevance: 0.5, Support: 0.5}}
		}
		details = append(details, rd)
	}
	return rag.Answer{
		Text: text,
		Diagnostics: rag.Diagnostics{
			Reflection: rag.ReflectionDiagnostics{
				Mode:         rag.ReflectionModeRule,
				Rounds:       rounds,
				AdoptedRound: adopted,
				RoundDetails: details,
			},
		},
	}
}

// TestBenchmarkMetricsAdoptedRoundCountsDistribution pins the histogram
// shape — 3 examples with AdoptedRound 1, 2, 2 → []int{1, 2}.
func TestBenchmarkMetricsAdoptedRoundCountsDistribution(t *testing.T) {
	asker := &scriptedAsker{byQuery: map[string]rag.Answer{
		"q1": answerWithRound(2, 1, "ans1", false),
		"q2": answerWithRound(3, 2, "ans2", false),
		"q3": answerWithRound(2, 2, "ans3", false),
	}}
	ds := eval.AnswerDataset{Name: "hist", TopK: 3, Examples: []eval.AnswerExample{
		{Example: eval.Example{Query: "q1"}},
		{Example: eval.Example{Query: "q2"}},
		{Example: eval.Example{Query: "q3"}},
	}}
	res, err := (eval.AnswerBenchmark{Asker: asker}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := res.Metrics.AdoptedRoundCounts
	if len(got) != 2 {
		t.Fatalf("AdoptedRoundCounts len = %d (%v), want 2", len(got), got)
	}
	if got[0] != 1 || got[1] != 2 {
		t.Fatalf("AdoptedRoundCounts = %v, want [1 2]", got)
	}
	// ReflectionRoundsMean over (2+3+2)/3 = 7/3
	wantMean := 7.0 / 3.0
	if math.Abs(res.Metrics.ReflectionRoundsMean-wantMean) > 1e-9 {
		t.Errorf("ReflectionRoundsMean = %v, want %v", res.Metrics.ReflectionRoundsMean, wantMean)
	}
}

// TestBenchmarkMetricsGraderAdoptionRate pins the
// AdoptedRound != last round semantics over grading-enabled examples:
// 2 grading-on examples, one picks earlier round → 0.5.
func TestBenchmarkMetricsGraderAdoptionRate(t *testing.T) {
	asker := &scriptedAsker{byQuery: map[string]rag.Answer{
		"qLast":   answerWithRound(3, 3, "tail", true),  // grading on, adopted = last
		"qEarly":  answerWithRound(3, 1, "early", true), // grading on, adopted != last
		"qNoGrad": answerWithRound(3, 2, "ng", false),   // grading off — excluded from denom
	}}
	ds := eval.AnswerDataset{Name: "grade", TopK: 3, Examples: []eval.AnswerExample{
		{Example: eval.Example{Query: "qLast"}},
		{Example: eval.Example{Query: "qEarly"}},
		{Example: eval.Example{Query: "qNoGrad"}},
	}}
	res, err := (eval.AnswerBenchmark{Asker: asker}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if math.Abs(res.Metrics.GraderAdoptionRate-0.5) > 1e-9 {
		t.Fatalf("GraderAdoptionRate = %v, want 0.5", res.Metrics.GraderAdoptionRate)
	}
}

// answerActive builds an answer whose RoundDetails has the given
// followup-query counts per round. Total FollowupQueriesUsed = sum.
func answerActive(perRoundFollowups []int) rag.Answer {
	details := make([]rag.ReflectionRoundDiagnostics, 0, len(perRoundFollowups))
	total := 0
	for i, n := range perRoundFollowups {
		fq := make([]string, n)
		for j := 0; j < n; j++ {
			fq[j] = "fq"
		}
		details = append(details, rag.ReflectionRoundDiagnostics{
			Round:           i + 1,
			FollowupQueries: fq,
		})
		total += n
	}
	return rag.Answer{
		Text: "active",
		Diagnostics: rag.Diagnostics{
			Reflection: rag.ReflectionDiagnostics{
				Mode:                rag.ReflectionModeRule,
				Rounds:              len(perRoundFollowups),
				AdoptedRound:        len(perRoundFollowups),
				FollowupQueriesUsed: total,
				RoundDetails:        details,
			},
		},
	}
}

// TestBenchmarkMetricsActiveRetrievalFireRate asserts the fire rate is
// computed over active-retrieval-enabled examples only (not the whole
// dataset). 2 active-on examples, 1 fired → 0.5.
func TestBenchmarkMetricsActiveRetrievalFireRate(t *testing.T) {
	asker := &scriptedAsker{byQuery: map[string]rag.Answer{
		"qFired":    answerActive([]int{2, 1}), // 3 followups total, fired
		"qNotFired": answerActive([]int{0, 0}), // active-on, didn't fire
	}}
	activeOpts := rag.AskOptions{Reflection: &rag.ReflectionOptions{
		Mode:                  rag.ReflectionModeRule,
		EnableActiveRetrieval: true,
		MaxRounds:             3,
	}}
	ds := eval.AnswerDataset{Name: "active", TopK: 3, Examples: []eval.AnswerExample{
		{Example: eval.Example{Query: "qFired"}},
		{Example: eval.Example{Query: "qNotFired"}},
	}}
	res, err := (eval.AnswerBenchmark{Asker: asker, Options: activeOpts}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if math.Abs(res.Metrics.ActiveRetrievalFireRate-0.5) > 1e-9 {
		t.Fatalf("ActiveRetrievalFireRate = %v, want 0.5", res.Metrics.ActiveRetrievalFireRate)
	}
	// FollowupQueriesUsedMean over (3+0)/2 = 1.5
	if math.Abs(res.Metrics.FollowupQueriesUsedMean-1.5) > 1e-9 {
		t.Fatalf("FollowupQueriesUsedMean = %v, want 1.5", res.Metrics.FollowupQueriesUsedMean)
	}
}

// scriptedAggJudge returns a pre-scripted sequence of Judgements, one
// per call, regardless of request content. Used to drive aggregation
// tests where the runner must call Judge once per example.
type scriptedAggJudge struct {
	verdicts []eval.Judgement
	calls    int
}

func (s *scriptedAggJudge) Judge(_ context.Context, _ eval.JudgeRequest) (eval.Judgement, error) {
	v := s.verdicts[s.calls]
	s.calls++
	return v, nil
}

// TestBenchmarkMetricsJudgeMeansComputeOverDataset pins the new flat
// MeanGroundedness / MeanAnswerRelevance fields. Three examples with
// scripted verdicts {0.4,0.6}, {0.8,0.4}, {0.6,0.5} → means 0.6, 0.5.
func TestBenchmarkMetricsJudgeMeansComputeOverDataset(t *testing.T) {
	asker := &scriptedAsker{byQuery: map[string]rag.Answer{
		"q1": {Text: "a"},
		"q2": {Text: "b"},
		"q3": {Text: "c"},
	}}
	judge := &scriptedAggJudge{verdicts: []eval.Judgement{
		{Groundedness: 0.4, AnswerRelevance: 0.6},
		{Groundedness: 0.8, AnswerRelevance: 0.4},
		{Groundedness: 0.6, AnswerRelevance: 0.5},
	}}
	ds := eval.AnswerDataset{Name: "agg", TopK: 3, Examples: []eval.AnswerExample{
		{Example: eval.Example{Query: "q1"}},
		{Example: eval.Example{Query: "q2"}},
		{Example: eval.Example{Query: "q3"}},
	}}
	res, err := (eval.AnswerBenchmark{Asker: asker, Judge: judge}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if math.Abs(res.Metrics.MeanGroundedness-0.6) > 1e-9 {
		t.Errorf("MeanGroundedness = %v, want 0.6", res.Metrics.MeanGroundedness)
	}
	if math.Abs(res.Metrics.MeanAnswerRelevance-0.5) > 1e-9 {
		t.Errorf("MeanAnswerRelevance = %v, want 0.5", res.Metrics.MeanAnswerRelevance)
	}
}

// TestBenchmarkMetricsJudgeMeansNaNWhenJudgeNil asserts both judge-side
// flat means are math.NaN() when Judge is nil — they carry the
// "no information" sentinel just like the reflection-feature metrics.
func TestBenchmarkMetricsJudgeMeansNaNWhenJudgeNil(t *testing.T) {
	asker := &scriptedAsker{byQuery: map[string]rag.Answer{
		"q1": {Text: "a"},
		"q2": {Text: "b"},
	}}
	ds := eval.AnswerDataset{Name: "no-judge", TopK: 3, Examples: []eval.AnswerExample{
		{Example: eval.Example{Query: "q1"}},
		{Example: eval.Example{Query: "q2"}},
	}}
	res, err := (eval.AnswerBenchmark{Asker: asker}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !math.IsNaN(res.Metrics.MeanGroundedness) {
		t.Errorf("MeanGroundedness = %v, want NaN (Judge nil)", res.Metrics.MeanGroundedness)
	}
	if !math.IsNaN(res.Metrics.MeanAnswerRelevance) {
		t.Errorf("MeanAnswerRelevance = %v, want NaN (Judge nil)", res.Metrics.MeanAnswerRelevance)
	}
}

// TestBenchmarkMetricsJudgeMeansEmptyDatasetNaN asserts both flat means
// are NaN over the empty-dataset branch, even though a non-nil Judge is
// configured.
func TestBenchmarkMetricsJudgeMeansEmptyDatasetNaN(t *testing.T) {
	asker := &scriptedAsker{}
	ds := eval.AnswerDataset{Name: "empty", TopK: 3}
	res, err := (eval.AnswerBenchmark{Asker: asker, Judge: wordOverlapJudge{}}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !math.IsNaN(res.Metrics.MeanGroundedness) {
		t.Errorf("MeanGroundedness = %v, want NaN (empty dataset)", res.Metrics.MeanGroundedness)
	}
	if !math.IsNaN(res.Metrics.MeanAnswerRelevance) {
		t.Errorf("MeanAnswerRelevance = %v, want NaN (empty dataset)", res.Metrics.MeanAnswerRelevance)
	}
}

// TestBenchmarkMetricsRequiredPhraseRecallMissingDenominator asserts the
// micro-recall metric is NaN when no example labeled any required
// phrase (zero denominator avoidance).
func TestBenchmarkMetricsRequiredPhraseRecallMissingDenominator(t *testing.T) {
	asker := &scriptedAsker{byQuery: map[string]rag.Answer{
		"q1": {Text: "x"},
		"q2": {Text: "y"},
	}}
	ds := eval.AnswerDataset{Name: "nophrase", TopK: 3, Examples: []eval.AnswerExample{
		{Example: eval.Example{Query: "q1"}, GoldAnswers: []string{"x"}},
		{Example: eval.Example{Query: "q2"}, GoldAnswers: []string{"y"}},
	}}
	res, err := (eval.AnswerBenchmark{Asker: asker}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !math.IsNaN(res.Metrics.RequiredPhraseRecall) {
		t.Errorf("RequiredPhraseRecall = %v, want NaN (no phrases labeled)", res.Metrics.RequiredPhraseRecall)
	}
}

// TestBenchmarkMetricsRequiredPhraseRecallMicroAverage pins the
// "hits-over-totals" micro semantics across examples (not a per-example
// average). 2 hits over 3 total labeled phrases → 2/3.
func TestBenchmarkMetricsRequiredPhraseRecallMicroAverage(t *testing.T) {
	asker := &scriptedAsker{byQuery: map[string]rag.Answer{
		"q1": {Text: "Go and Anthropic"},
		"q2": {Text: "missing word"},
	}}
	ds := eval.AnswerDataset{Name: "phrase", TopK: 3, Examples: []eval.AnswerExample{
		{
			Example:         eval.Example{Query: "q1"},
			RequiredPhrases: []string{"Go", "Anthropic"}, // both hit
		},
		{
			Example:         eval.Example{Query: "q2"},
			RequiredPhrases: []string{"absent"}, // miss
		},
	}}
	res, err := (eval.AnswerBenchmark{Asker: asker}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := 2.0 / 3.0
	if math.Abs(res.Metrics.RequiredPhraseRecall-want) > 1e-9 {
		t.Fatalf("RequiredPhraseRecall = %v, want %v (2/3 micro)", res.Metrics.RequiredPhraseRecall, want)
	}
}
