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

// keep unused imports happy for tests that come in later commits
var (
	_ = errors.New
	_ = sort.Slice
)
