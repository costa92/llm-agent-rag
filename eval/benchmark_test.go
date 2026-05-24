package eval_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/rag"
)

// scriptedAsker is a deterministic eval.Asker for the benchmark tests.
// It records every (question, opts) it sees and returns a hand-built
// rag.Answer keyed by question. Setting err makes the next Ask return
// (zero, err); leaving byQuery missing yields a zero-value Answer.
type scriptedAsker struct {
	byQuery  map[string]rag.Answer
	lastOpts []rag.AskOptions
	calls    []string
	err      error
}

func (s *scriptedAsker) Ask(_ context.Context, q string, opts rag.AskOptions) (rag.Answer, error) {
	s.calls = append(s.calls, q)
	s.lastOpts = append(s.lastOpts, opts)
	if s.err != nil {
		return rag.Answer{}, s.err
	}
	return s.byQuery[q], nil
}

// TestAnswerBenchmarkRequiresAsker asserts a nil Asker is a runner-level
// error, not a panic.
func TestAnswerBenchmarkRequiresAsker(t *testing.T) {
	ds := eval.AnswerDataset{Name: "d", TopK: 3, Examples: []eval.AnswerExample{
		{Example: eval.Example{Query: "q"}},
	}}
	if _, err := (eval.AnswerBenchmark{}).Run(context.Background(), ds); err == nil {
		t.Fatalf("Run with nil Asker: want error")
	}
}

// TestAnswerBenchmarkRejectsZeroTopK asserts the dataset-level TopK
// check fires before any Ask call.
func TestAnswerBenchmarkRejectsZeroTopK(t *testing.T) {
	asker := &scriptedAsker{}
	ds := eval.AnswerDataset{Name: "zero", TopK: 0, Examples: []eval.AnswerExample{
		{Example: eval.Example{Query: "q"}},
	}}
	_, err := (eval.AnswerBenchmark{Asker: asker}).Run(context.Background(), ds)
	if err == nil {
		t.Fatalf("Run with TopK=0: want error")
	}
	if !strings.Contains(err.Error(), "TopK") {
		t.Fatalf("error %q does not mention TopK", err)
	}
	if len(asker.calls) != 0 {
		t.Fatalf("Asker was called %d times before validation fired", len(asker.calls))
	}
}

// TestAnswerBenchmarkPerExampleExactMatchAndF1 runs three hand-built
// answers and pins the per-example ExactMatch / F1Token / required-phrase
// scoring path.
func TestAnswerBenchmarkPerExampleExactMatchAndF1(t *testing.T) {
	asker := &scriptedAsker{
		byQuery: map[string]rag.Answer{
			"q1": {Text: "Go"},                   // exact match on first gold
			"q2": {Text: "the answer is amodei"}, // partial overlap, required phrase miss (case sensitive)
			"q3": {Text: "four"},                 // exact match on second gold
		},
	}
	ds := eval.AnswerDataset{
		Name: "shape",
		TopK: 3,
		Examples: []eval.AnswerExample{
			{
				Example:         eval.Example{Query: "q1"},
				GoldAnswers:     []string{"Go", "golang"},
				RequiredPhrases: []string{"Go"},
			},
			{
				Example:         eval.Example{Query: "q2"},
				GoldAnswers:     []string{"Dario Amodei"},
				RequiredPhrases: []string{"Amodei"}, // verbatim case-sensitive — "amodei" misses
			},
			{
				Example:         eval.Example{Query: "q3"},
				GoldAnswers:     []string{"4", "four"},
				RequiredPhrases: []string{"4"}, // "four" does not contain "4"
			},
		},
	}
	res, err := (eval.AnswerBenchmark{Asker: asker}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.PerExample) != 3 {
		t.Fatalf("PerExample len = %d, want 3", len(res.PerExample))
	}

	// q1: ExactMatch true, F1 1.0, phrase 1/1
	r0 := res.PerExample[0]
	if !r0.ExactMatch {
		t.Errorf("q1 ExactMatch = false, want true")
	}
	if math.Abs(r0.F1Token-1.0) > 1e-9 {
		t.Errorf("q1 F1Token = %v, want 1.0", r0.F1Token)
	}
	if r0.RequiredPhraseHits != 1 || r0.RequiredPhraseTotal != 1 {
		t.Errorf("q1 phrase hits = %d/%d, want 1/1", r0.RequiredPhraseHits, r0.RequiredPhraseTotal)
	}
	if r0.Answer != "Go" {
		t.Errorf("q1 Answer = %q, want %q", r0.Answer, "Go")
	}

	// q2: ExactMatch false, partial F1>0, phrase 0/1 (case sensitive)
	r1 := res.PerExample[1]
	if r1.ExactMatch {
		t.Errorf("q2 ExactMatch = true, want false")
	}
	if r1.F1Token <= 0 {
		t.Errorf("q2 F1Token = %v, want > 0 (partial overlap)", r1.F1Token)
	}
	if r1.RequiredPhraseHits != 0 || r1.RequiredPhraseTotal != 1 {
		t.Errorf("q2 phrase hits = %d/%d, want 0/1", r1.RequiredPhraseHits, r1.RequiredPhraseTotal)
	}

	// q3: ExactMatch true on second gold, phrase 0/1
	r2 := res.PerExample[2]
	if !r2.ExactMatch {
		t.Errorf("q3 ExactMatch = false, want true")
	}
	if r2.RequiredPhraseHits != 0 || r2.RequiredPhraseTotal != 1 {
		t.Errorf("q3 phrase hits = %d/%d, want 0/1", r2.RequiredPhraseHits, r2.RequiredPhraseTotal)
	}

	if res.Dataset.Name != "shape" {
		t.Errorf("BenchmarkResult.Dataset.Name = %q, want %q", res.Dataset.Name, "shape")
	}
	if res.Metrics.Examples != 3 {
		t.Errorf("Metrics.Examples = %d, want 3", res.Metrics.Examples)
	}
}

// TestAnswerBenchmarkAbortsOnAskError asserts the runner stops at the
// first failing Ask and wraps the error with the offending query.
func TestAnswerBenchmarkAbortsOnAskError(t *testing.T) {
	asker := &scriptedAsker{err: errors.New("boom")}
	ds := eval.AnswerDataset{Name: "d", TopK: 3, Examples: []eval.AnswerExample{
		{Example: eval.Example{Query: "first"}},
		{Example: eval.Example{Query: "second"}},
	}}
	_, err := (eval.AnswerBenchmark{Asker: asker}).Run(context.Background(), ds)
	if err == nil {
		t.Fatalf("Run: want error")
	}
	if !strings.Contains(err.Error(), "first") {
		t.Fatalf("error %q does not mention failing query", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error %q does not wrap the inner error", err)
	}
	if len(asker.calls) != 1 {
		t.Fatalf("Asker called %d times, want 1 (abort-on-first)", len(asker.calls))
	}
}

// TestAnswerBenchmarkOverlaysNamespaceFromExample asserts the runner
// applies the example's Namespace into the Ask opts, and that
// dataset.TopK propagates to opts.Search.TopK.
func TestAnswerBenchmarkOverlaysNamespaceFromExample(t *testing.T) {
	asker := &scriptedAsker{byQuery: map[string]rag.Answer{
		"q1": {Text: "x"},
		"q2": {Text: "y"},
	}}
	base := rag.AskOptions{Search: rag.SearchOptions{Namespace: "base-ns", TopK: 1}}
	ds := eval.AnswerDataset{
		Name: "ns",
		TopK: 7,
		Examples: []eval.AnswerExample{
			{Example: eval.Example{Query: "q1", Namespace: "geo"}},
			{Example: eval.Example{Query: "q2"}}, // no per-example namespace; inherits base
		},
	}
	if _, err := (eval.AnswerBenchmark{Asker: asker, Options: base}).Run(context.Background(), ds); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(asker.lastOpts) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(asker.lastOpts))
	}
	if got := asker.lastOpts[0].Search.Namespace; got != "geo" {
		t.Errorf("call 0 Namespace = %q, want %q (overlay from example)", got, "geo")
	}
	if got := asker.lastOpts[0].Search.TopK; got != 7 {
		t.Errorf("call 0 TopK = %d, want 7 (dataset TopK overrides base)", got)
	}
	if got := asker.lastOpts[1].Search.Namespace; got != "base-ns" {
		t.Errorf("call 1 Namespace = %q, want %q (no overlay)", got, "base-ns")
	}
	if got := asker.lastOpts[1].Search.TopK; got != 7 {
		t.Errorf("call 1 TopK = %d, want 7", got)
	}
}
