package eval_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/rag"
)

// atomicScriptedAsker is a thread-safe variant of scriptedAsker for the
// v1.4.0 parallel benchmark tests. It records every (question, opts)
// it sees under a sync.Mutex and returns a hand-built rag.Answer keyed
// by question. Safe for concurrent use by multiple goroutines.
type atomicScriptedAsker struct {
	mu       sync.Mutex
	byQuery  map[string]rag.Answer
	calls    []string
	lastOpts []rag.AskOptions
	errBy    map[string]error
}

func (a *atomicScriptedAsker) Ask(_ context.Context, q string, opts rag.AskOptions) (rag.Answer, error) {
	a.mu.Lock()
	a.calls = append(a.calls, q)
	a.lastOpts = append(a.lastOpts, opts)
	ans, ok := a.byQuery[q]
	var err error
	if a.errBy != nil {
		err = a.errBy[q]
	}
	a.mu.Unlock()
	if err != nil {
		return rag.Answer{}, err
	}
	if !ok {
		return rag.Answer{}, nil
	}
	return ans, nil
}

// atomicScriptedJudge is a thread-safe deterministic judge. It returns
// the verdict keyed by query (or a zero-value Judgement if missing).
// Errors are looked up by query too. Safe for concurrent use.
type atomicScriptedJudge struct {
	mu        sync.Mutex
	verdictBy map[string]eval.Judgement
	errBy     map[string]error
	calls     int32
}

func (j *atomicScriptedJudge) Judge(_ context.Context, req eval.JudgeRequest) (eval.Judgement, error) {
	atomic.AddInt32(&j.calls, 1)
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.errBy != nil {
		if err := j.errBy[req.Query]; err != nil {
			return eval.Judgement{}, err
		}
	}
	if j.verdictBy != nil {
		return j.verdictBy[req.Query], nil
	}
	return eval.Judgement{}, nil
}

// sleepInjectingAsker sleeps a configured duration inside Ask and
// tracks the peak number of concurrent Ask calls observed across its
// lifetime. Used to assert bounded-fan-out invariants in the parallel
// benchmark tests, mirroring rag/active_parallel_test.go's
// instrumentedRetriever (rag/active_parallel_test.go:23-68).
type sleepInjectingAsker struct {
	mu       sync.Mutex
	sleepFor map[string]time.Duration
	byQuery  map[string]rag.Answer
	errBy    map[string]error
	active   int32
	peak     int32
}

func (a *sleepInjectingAsker) Ask(_ context.Context, q string, _ rag.AskOptions) (rag.Answer, error) {
	cur := atomic.AddInt32(&a.active, 1)
	defer atomic.AddInt32(&a.active, -1)
	for {
		old := atomic.LoadInt32(&a.peak)
		if cur <= old || atomic.CompareAndSwapInt32(&a.peak, old, cur) {
			break
		}
	}
	a.mu.Lock()
	d, hasSleep := a.sleepFor[q]
	if !hasSleep {
		d = 30 * time.Millisecond
	}
	ans := a.byQuery[q]
	var err error
	if a.errBy != nil {
		err = a.errBy[q]
	}
	a.mu.Unlock()
	if d > 0 {
		time.Sleep(d)
	}
	if err != nil {
		return rag.Answer{}, err
	}
	if ans.Text == "" {
		ans.Text = q
	}
	return ans, nil
}

// peakActive returns the peak number of concurrent Ask calls observed.
// Thread-safe.
func (a *sleepInjectingAsker) peakActive() int32 { return atomic.LoadInt32(&a.peak) }

// sleepingDataset builds a dataset of n examples named q1..qN, each
// with the given sleep duration. Used by parallel concurrency tests.
func sleepingDataset(n int, _ time.Duration) eval.AnswerDataset {
	exs := make([]eval.AnswerExample, 0, n)
	for i := 1; i <= n; i++ {
		exs = append(exs, eval.AnswerExample{
			Example: eval.Example{Query: fmt.Sprintf("q%d", i)},
		})
	}
	return eval.AnswerDataset{Name: "sleep", TopK: 3, Examples: exs}
}

func newSleepingAsker(n int, d time.Duration) *sleepInjectingAsker {
	sleep := make(map[string]time.Duration, n)
	by := make(map[string]rag.Answer, n)
	for i := 1; i <= n; i++ {
		q := fmt.Sprintf("q%d", i)
		sleep[q] = d
		by[q] = rag.Answer{Text: q}
	}
	return &sleepInjectingAsker{sleepFor: sleep, byQuery: by}
}

// TestAnswerBenchmarkParallelismZeroUsesSequentialPath asserts that
// Parallelism=0 collapses to the sequential loop (no concurrency).
func TestAnswerBenchmarkParallelismZeroUsesSequentialPath(t *testing.T) {
	asker := newSleepingAsker(3, 30*time.Millisecond)
	ds := sleepingDataset(3, 30*time.Millisecond)
	bench := eval.AnswerBenchmark{Asker: asker, Parallelism: 0}
	if _, err := bench.Run(context.Background(), ds); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := asker.peakActive(); got != 1 {
		t.Fatalf("peakActive = %d, want 1 (Parallelism=0 → sequential)", got)
	}
}

// TestAnswerBenchmarkParallelismOneUsesSequentialPath asserts that
// Parallelism=1 explicitly also collapses to sequential.
func TestAnswerBenchmarkParallelismOneUsesSequentialPath(t *testing.T) {
	asker := newSleepingAsker(3, 30*time.Millisecond)
	ds := sleepingDataset(3, 30*time.Millisecond)
	bench := eval.AnswerBenchmark{Asker: asker, Parallelism: 1}
	if _, err := bench.Run(context.Background(), ds); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := asker.peakActive(); got != 1 {
		t.Fatalf("peakActive = %d, want 1 (Parallelism=1 → sequential)", got)
	}
}

// TestAnswerBenchmarkParallelismNegativeCoercesToSequential asserts that
// any negative Parallelism (Q4) coerces to sequential without error.
func TestAnswerBenchmarkParallelismNegativeCoercesToSequential(t *testing.T) {
	asker := newSleepingAsker(3, 30*time.Millisecond)
	ds := sleepingDataset(3, 30*time.Millisecond)
	bench := eval.AnswerBenchmark{Asker: asker, Parallelism: -3}
	if _, err := bench.Run(context.Background(), ds); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := asker.peakActive(); got != 1 {
		t.Fatalf("peakActive = %d, want 1 (Parallelism=-3 → sequential)", got)
	}
}

// TestAnswerBenchmarkParallelismActuallyOverlaps asserts that under
// Parallelism=3 with three sleeping examples, at least two Ask calls
// run concurrently — i.e. the worker pool actually fans out.
func TestAnswerBenchmarkParallelismActuallyOverlaps(t *testing.T) {
	asker := newSleepingAsker(3, 50*time.Millisecond)
	ds := sleepingDataset(3, 50*time.Millisecond)
	bench := eval.AnswerBenchmark{Asker: asker, Parallelism: 3}
	if _, err := bench.Run(context.Background(), ds); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := asker.peakActive(); got < 2 {
		t.Fatalf("peakActive = %d, want >= 2 (Parallelism=3 should overlap)", got)
	}
}

// TestAnswerBenchmarkParallelismCapsAtConfiguredValue asserts the
// semaphore caps in-flight work at Parallelism, even when the dataset
// is larger.
func TestAnswerBenchmarkParallelismCapsAtConfiguredValue(t *testing.T) {
	asker := newSleepingAsker(4, 40*time.Millisecond)
	ds := sleepingDataset(4, 40*time.Millisecond)
	bench := eval.AnswerBenchmark{Asker: asker, Parallelism: 2}
	if _, err := bench.Run(context.Background(), ds); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := asker.peakActive(); got > 2 {
		t.Fatalf("peakActive = %d, want <= 2 (Parallelism=2 cap)", got)
	}
	if got := asker.peakActive(); got < 2 {
		t.Fatalf("peakActive = %d, want >= 2 (some overlap expected)", got)
	}
}

// TestAnswerBenchmarkParallelPerExampleOrderMatchesDataset asserts that
// PerExample[i] corresponds to dataset.Examples[i] regardless of the
// completion order under parallel dispatch — exercise with sleeps that
// make completion order != dispatch order.
func TestAnswerBenchmarkParallelPerExampleOrderMatchesDataset(t *testing.T) {
	asker := &sleepInjectingAsker{
		sleepFor: map[string]time.Duration{
			"q1": 80 * time.Millisecond, // finishes last
			"q2": 20 * time.Millisecond,
			"q3": 50 * time.Millisecond,
			"q4": 10 * time.Millisecond, // finishes first
		},
		byQuery: map[string]rag.Answer{
			"q1": {Text: "ans1"},
			"q2": {Text: "ans2"},
			"q3": {Text: "ans3"},
			"q4": {Text: "ans4"},
		},
	}
	ds := sleepingDataset(4, 0)
	bench := eval.AnswerBenchmark{Asker: asker, Parallelism: 4}
	res, err := bench.Run(context.Background(), ds)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.PerExample) != 4 {
		t.Fatalf("PerExample len = %d, want 4", len(res.PerExample))
	}
	for i, want := range []string{"q1", "q2", "q3", "q4"} {
		if got := res.PerExample[i].Example.Query; got != want {
			t.Errorf("PerExample[%d].Example.Query = %q, want %q (order must match dataset)", i, got, want)
		}
		if got := res.PerExample[i].Answer; got != "ans"+fmt.Sprintf("%d", i+1) {
			t.Errorf("PerExample[%d].Answer = %q, want ans%d", i, got, i+1)
		}
	}
}

// countingFinishAsker is a sleeping asker that counts completed Ask
// calls regardless of success/error. It also lets us error on a specific
// query while other workers are still in flight, then assert every
// dispatched goroutine eventually wakes up.
type countingFinishAsker struct {
	sleepInjectingAsker
	finished int32
}

func (a *countingFinishAsker) Ask(ctx context.Context, q string, opts rag.AskOptions) (rag.Answer, error) {
	defer atomic.AddInt32(&a.finished, 1)
	return a.sleepInjectingAsker.Ask(ctx, q, opts)
}

func newCountingAsker(n int, d time.Duration) *countingFinishAsker {
	sleep := make(map[string]time.Duration, n)
	by := make(map[string]rag.Answer, n)
	for i := 1; i <= n; i++ {
		q := fmt.Sprintf("q%d", i)
		sleep[q] = d
		by[q] = rag.Answer{Text: q}
	}
	return &countingFinishAsker{
		sleepInjectingAsker: sleepInjectingAsker{sleepFor: sleep, byQuery: by},
	}
}

// TestAnswerBenchmarkParallelAskerErrorReturnsWrappedFirstError asserts
// that when an Ask error fires under parallel dispatch, the returned
// error names the failing query and wraps the inner error message.
func TestAnswerBenchmarkParallelAskerErrorReturnsWrappedFirstError(t *testing.T) {
	asker := &sleepInjectingAsker{
		sleepFor: map[string]time.Duration{
			"q1": 50 * time.Millisecond,
			"q2": 10 * time.Millisecond, // errors first
			"q3": 50 * time.Millisecond,
		},
		byQuery: map[string]rag.Answer{
			"q1": {Text: "ans1"},
			"q3": {Text: "ans3"},
		},
		errBy: map[string]error{"q2": errors.New("ask-boom")},
	}
	ds := sleepingDataset(3, 0)
	bench := eval.AnswerBenchmark{Asker: asker, Parallelism: 3}
	_, err := bench.Run(context.Background(), ds)
	if err == nil {
		t.Fatalf("Run: want error")
	}
	if !contains(err.Error(), "q2") {
		t.Fatalf("error %q does not mention failing query q2", err)
	}
	if !contains(err.Error(), "ask-boom") {
		t.Fatalf("error %q does not wrap inner error", err)
	}
	if !contains(err.Error(), "eval: ask") {
		t.Fatalf("error %q does not carry 'eval: ask' prefix", err)
	}
}

// TestAnswerBenchmarkParallelJudgeErrorReturnsWrapped asserts the
// Judge-side error is wrapped with the "eval: judge %q" prefix under
// parallel dispatch.
func TestAnswerBenchmarkParallelJudgeErrorReturnsWrapped(t *testing.T) {
	asker := newSleepingAsker(3, 30*time.Millisecond)
	judge := &atomicScriptedJudge{
		verdictBy: map[string]eval.Judgement{
			"q1": {Groundedness: 0.5, AnswerRelevance: 0.5},
			"q2": {Groundedness: 0.5, AnswerRelevance: 0.5},
			"q3": {Groundedness: 0.5, AnswerRelevance: 0.5},
		},
		errBy: map[string]error{"q1": errors.New("judge-boom")},
	}
	ds := sleepingDataset(3, 0)
	bench := eval.AnswerBenchmark{Asker: asker, Judge: judge, Parallelism: 3}
	_, err := bench.Run(context.Background(), ds)
	if err == nil {
		t.Fatalf("Run: want error")
	}
	if !contains(err.Error(), `eval: judge "q1"`) {
		t.Fatalf("error %q missing 'eval: judge \"q1\"' prefix", err)
	}
	if !contains(err.Error(), "judge-boom") {
		t.Fatalf("error %q does not wrap inner error", err)
	}
}

// TestAnswerBenchmarkParallelOtherWorkersFinish asserts that when a
// goroutine errors mid-batch, the in-flight siblings complete naturally
// (no context cancellation). All four dispatched goroutines must record
// a finish. Race-detector clean is the second half of this gate.
func TestAnswerBenchmarkParallelOtherWorkersFinish(t *testing.T) {
	asker := newCountingAsker(4, 40*time.Millisecond)
	asker.errBy = map[string]error{"q2": errors.New("transient")}
	ds := sleepingDataset(4, 0)
	bench := eval.AnswerBenchmark{Asker: asker, Parallelism: 4}
	_, err := bench.Run(context.Background(), ds)
	if err == nil {
		t.Fatalf("Run: want error")
	}
	got := atomic.LoadInt32(&asker.finished)
	if got != 4 {
		t.Fatalf("finished = %d, want 4 (no context cancellation; all workers complete)", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// keep unused imports happy for tests that come in later commits
var (
	_ = sort.Slice
)
