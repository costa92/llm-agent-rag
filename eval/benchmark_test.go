package eval_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/rag"
	"github.com/costa92/llm-agent-rag/store"
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

// recordingBenchmarkJudge captures every JudgeRequest it sees and replies with a
// fixed Judgement. Used to pin JudgeRequest.Context shape (Commit 2).
type recordingBenchmarkJudge struct {
	calls   []eval.JudgeRequest
	verdict eval.Judgement
	err     error
}

func (r *recordingBenchmarkJudge) Judge(_ context.Context, req eval.JudgeRequest) (eval.Judgement, error) {
	r.calls = append(r.calls, req)
	if r.err != nil {
		return eval.Judgement{}, r.err
	}
	return r.verdict, nil
}

// errAfterNJudge errors on the n-th call (1-indexed). Used to pin
// abort-on-first-judge-error in the sequential path (Commit 2).
type errAfterNJudge struct {
	n      int
	calls  int
	errMsg string
}

func (j *errAfterNJudge) Judge(_ context.Context, _ eval.JudgeRequest) (eval.Judgement, error) {
	j.calls++
	if j.calls == j.n {
		return eval.Judgement{}, errors.New(j.errMsg)
	}
	return eval.Judgement{Groundedness: 0.5, AnswerRelevance: 0.5}, nil
}

// answerWithHits builds a rag.Answer carrying store.Hit context with the
// given content strings. Used by benchmark tests that pin JudgeRequest.Context
// against Answer.Hits[i].Chunk.Content order.
func answerWithHits(text string, contents ...string) rag.Answer {
	hits := make([]store.Hit, 0, len(contents))
	for i, c := range contents {
		hits = append(hits, store.Hit{
			Chunk: store.StoredChunk{
				ID:      fmt.Sprintf("c%d", i),
				DocID:   "d",
				Content: c,
			},
			Score: 1.0 - float64(i)*0.1,
		})
	}
	return rag.Answer{Text: text, Hits: hits}
}

// TestAnswerBenchmarkInvokesJudgePerExample asserts that when
// AnswerBenchmark.Judge != nil the runner calls Judge.Judge once per
// example and stores the verdict on PerExample[i].Judgement with
// JudgeApplied=true.
func TestAnswerBenchmarkInvokesJudgePerExample(t *testing.T) {
	asker := &scriptedAsker{byQuery: map[string]rag.Answer{
		"paris museums":   answerWithHits("museums in paris", "paris museums info", "paris travel"),
		"french pastries": answerWithHits("french pastries are great", "french pastry overview"),
	}}
	ds := eval.AnswerDataset{Name: "judge-on", TopK: 3, Examples: []eval.AnswerExample{
		{Example: eval.Example{Query: "paris museums"}, GoldAnswers: []string{"museums in paris"}},
		{Example: eval.Example{Query: "french pastries"}, GoldAnswers: []string{"french pastries are great"}},
	}}
	res, err := (eval.AnswerBenchmark{Asker: asker, Judge: wordOverlapJudge{}}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.PerExample) != 2 {
		t.Fatalf("PerExample len = %d, want 2", len(res.PerExample))
	}
	for i, r := range res.PerExample {
		if !r.JudgeApplied {
			t.Errorf("PerExample[%d].JudgeApplied = false, want true", i)
		}
		if r.Judgement.Groundedness <= 0 {
			t.Errorf("PerExample[%d].Judgement.Groundedness = %v, want > 0", i, r.Judgement.Groundedness)
		}
	}
}

// TestAnswerBenchmarkJudgePassesContextFromHits pins that
// JudgeRequest.Context equals the answer-hit Content list, in the same
// order as Answer.Hits.
func TestAnswerBenchmarkJudgePassesContextFromHits(t *testing.T) {
	rec := &recordingBenchmarkJudge{verdict: eval.Judgement{Groundedness: 0.4, AnswerRelevance: 0.6, Rationale: "stub"}}
	asker := &scriptedAsker{byQuery: map[string]rag.Answer{
		"q1": answerWithHits("ans1", "ctxA", "ctxB", "ctxC"),
	}}
	ds := eval.AnswerDataset{Name: "ctx", TopK: 3, Examples: []eval.AnswerExample{
		{Example: eval.Example{Query: "q1"}},
	}}
	res, err := (eval.AnswerBenchmark{Asker: asker, Judge: rec}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("judge calls = %d, want 1", len(rec.calls))
	}
	req := rec.calls[0]
	if req.Query != "q1" {
		t.Errorf("JudgeRequest.Query = %q, want %q", req.Query, "q1")
	}
	if req.Answer != "ans1" {
		t.Errorf("JudgeRequest.Answer = %q, want %q", req.Answer, "ans1")
	}
	want := []string{"ctxA", "ctxB", "ctxC"}
	if len(req.Context) != len(want) {
		t.Fatalf("JudgeRequest.Context len = %d, want %d (%v)", len(req.Context), len(want), req.Context)
	}
	for i, c := range want {
		if req.Context[i] != c {
			t.Errorf("JudgeRequest.Context[%d] = %q, want %q", i, req.Context[i], c)
		}
	}
	// Verdict propagation
	if res.PerExample[0].Judgement != rec.verdict {
		t.Errorf("Judgement = %+v, want %+v", res.PerExample[0].Judgement, rec.verdict)
	}
}

// TestAnswerBenchmarkAbortsOnJudgeError asserts the runner stops at the
// first failing Judge and wraps the error with the offending query under
// the "eval: judge" prefix.
func TestAnswerBenchmarkAbortsOnJudgeError(t *testing.T) {
	asker := &scriptedAsker{byQuery: map[string]rag.Answer{
		"q1": {Text: "a"},
		"q2": {Text: "b"},
		"q3": {Text: "c"},
	}}
	judge := &errAfterNJudge{n: 2, errMsg: "judge-boom"}
	ds := eval.AnswerDataset{Name: "jerr", TopK: 3, Examples: []eval.AnswerExample{
		{Example: eval.Example{Query: "q1"}},
		{Example: eval.Example{Query: "q2"}},
		{Example: eval.Example{Query: "q3"}},
	}}
	_, err := (eval.AnswerBenchmark{Asker: asker, Judge: judge}).Run(context.Background(), ds)
	if err == nil {
		t.Fatalf("Run: want error")
	}
	if !strings.Contains(err.Error(), "eval: judge") {
		t.Fatalf("error %q does not carry 'eval: judge' prefix", err)
	}
	if !strings.Contains(err.Error(), "q2") {
		t.Fatalf("error %q does not name the offending query", err)
	}
	if !strings.Contains(err.Error(), "judge-boom") {
		t.Fatalf("error %q does not wrap the inner error", err)
	}
	if judge.calls != 2 {
		t.Fatalf("judge.calls = %d, want 2 (abort-on-first)", judge.calls)
	}
	if len(asker.calls) != 2 {
		t.Fatalf("asker.calls = %d, want 2 (abort halts further Ask)", len(asker.calls))
	}
}

// TestAnswerExampleResultJudgementZeroValueWhenNoJudge asserts that when
// AnswerBenchmark.Judge is nil, every per-example trace carries
// JudgeApplied=false and a zero-value Judgement. This pins the
// "no judge configured" sentinel introduced in v1.4.0.
func TestAnswerExampleResultJudgementZeroValueWhenNoJudge(t *testing.T) {
	asker := &scriptedAsker{byQuery: map[string]rag.Answer{
		"q1": {Text: "a"},
		"q2": {Text: "b"},
	}}
	ds := eval.AnswerDataset{Name: "no-judge", TopK: 3, Examples: []eval.AnswerExample{
		{Example: eval.Example{Query: "q1"}, GoldAnswers: []string{"a"}},
		{Example: eval.Example{Query: "q2"}, GoldAnswers: []string{"b"}},
	}}
	res, err := (eval.AnswerBenchmark{Asker: asker}).Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.PerExample) != 2 {
		t.Fatalf("PerExample len = %d, want 2", len(res.PerExample))
	}
	for i, r := range res.PerExample {
		if r.JudgeApplied {
			t.Errorf("PerExample[%d].JudgeApplied = true, want false (Judge nil)", i)
		}
		if r.Judgement != (eval.Judgement{}) {
			t.Errorf("PerExample[%d].Judgement = %+v, want zero", i, r.Judgement)
		}
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
