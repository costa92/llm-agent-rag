// Package obs holds the cost-and-latency types shared across the RAG
// pipeline. It is a leaf package — it imports only the standard library —
// so any package (rag, retrieve, ingest) can embed obs.Metrics without an
// import cycle.
package obs

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// StageTiming is the wall-clock duration of one named stage of a RAG
// operation, e.g. "retrieve", "rerank", "pack", "generate", "embed".
type StageTiming struct {
	Stage    string        // Stage is the stage name, e.g. "retrieve" or "generate".
	Duration time.Duration // Duration is the stage's wall-clock time.
}

// CallCounts counts external model calls made during one RAG operation.
type CallCounts struct {
	Embed    int // Embed is the number of embedding calls.
	Generate int // Generate is the number of generation calls.
}

// TokenUsage is the token accounting for one RAG operation. Estimated is
// true when the counts were derived from a token counter rather than
// reported by the model. Populated by token accounting (slice 17-02).
type TokenUsage struct {
	PromptTokens     int  // PromptTokens is the number of prompt tokens.
	CompletionTokens int  // CompletionTokens is the number of completion tokens.
	TotalTokens      int  // TotalTokens is the combined token count.
	Estimated        bool // Estimated is true when counts came from a token counter, not the model.
}

// StageTokenUsage is the token accounting for one named generation stage
// (e.g. "ask", "reflection_decision", "grader", "planner") of a top-level
// rag operation. It is the per-Generate audit counterpart of Metrics.Tokens
// — Metrics.Tokens reports the answer-leg's deriveTokenUsage(req, resp);
// StageTokenUsage records every Generate call the operation made.
//
// The sum across a Metrics.StageTokenUsage slice is NOT equal to
// Metrics.Tokens — they aggregate different things (cross-stage audit vs.
// answer-leg accounting). Both are reported independently and additively.
type StageTokenUsage struct {
	Stage string     // Stage is the named generation stage (e.g. "ask").
	Usage TokenUsage // Usage is the token accounting for that Generate call.
}

// Metrics is the cost-and-latency record for one RAG operation. Stages are
// in execution order; TotalDuration is the end-to-end wall clock.
type Metrics struct {
	TotalDuration    time.Duration     // TotalDuration is the end-to-end wall clock.
	Stages           []StageTiming     // Stages are the per-stage timings in execution order.
	Calls            CallCounts        // Calls is the external model-call accounting.
	Tokens           TokenUsage        // Tokens is the token accounting for the operation.
	StageTokenUsage  []StageTokenUsage // StageTokenUsage is the per-Generate token audit, in call order.
}

// Counter accumulates model-call counts for one operation. It is safe for
// concurrent use. Attach it to a context with WithCounter; instrumented
// embedders and models increment whichever Counter rides the call context.
type Counter struct {
	embed    atomic.Int64
	generate atomic.Int64
}

// NewCounter returns a zeroed Counter.
func NewCounter() *Counter { return &Counter{} }

// AddEmbed records n embedding calls. A nil Counter is a no-op, so callers
// may pass the result of CounterFrom straight through.
func (c *Counter) AddEmbed(n int) {
	if c != nil {
		c.embed.Add(int64(n))
	}
}

// AddGenerate records n generation calls. A nil Counter is a no-op.
func (c *Counter) AddGenerate(n int) {
	if c != nil {
		c.generate.Add(int64(n))
	}
}

// Counts returns the calls recorded so far. A nil Counter returns zero.
func (c *Counter) Counts() CallCounts {
	if c == nil {
		return CallCounts{}
	}
	return CallCounts{
		Embed:    int(c.embed.Load()),
		Generate: int(c.generate.Load()),
	}
}

// StageUsageAccumulator accumulates per-Generate StageTokenUsage entries
// for one top-level Ask/AskGlobal/AskDrift call. It is safe for concurrent
// use — the v1.2.1 ParallelFollowups and v1.4.0 AnswerBenchmark.Parallelism
// paths may fire Generate calls from multiple goroutines, and the
// accumulator absorbs that fan-out under a sync.Mutex.
//
// Attach an accumulator to a context with WithStageUsage; instrumented
// counting models append to whichever accumulator rides the call context.
// A nil *StageUsageAccumulator is a no-op, so callers may pass the result
// of StageUsageFrom straight through.
type StageUsageAccumulator struct {
	mu      sync.Mutex
	entries []StageTokenUsage
}

// NewStageUsageAccumulator returns a zeroed StageUsageAccumulator.
func NewStageUsageAccumulator() *StageUsageAccumulator { return &StageUsageAccumulator{} }

// Append records one StageTokenUsage entry. A nil accumulator is a no-op.
func (a *StageUsageAccumulator) Append(stage string, usage TokenUsage) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.entries = append(a.entries, StageTokenUsage{Stage: stage, Usage: usage})
	a.mu.Unlock()
}

// TotalSoFar returns the running sum of entries[i].Usage.TotalTokens
// across every Append call so far. A nil accumulator returns 0. The sum
// is computed under the same sync.Mutex that guards Append/Snapshot, so
// it is safe to call concurrently with Append.
//
// v1.7.0: used by the rag-side cumulative token budget check in
// countingModel.Generate after each successful Append.
func (a *StageUsageAccumulator) TotalSoFar() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	total := 0
	for _, e := range a.entries {
		total += e.Usage.TotalTokens
	}
	return total
}

// Snapshot returns a defensive copy of the entries recorded so far, in the
// order Append was called. A nil accumulator returns nil.
func (a *StageUsageAccumulator) Snapshot() []StageTokenUsage {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.entries) == 0 {
		return nil
	}
	out := make([]StageTokenUsage, len(a.entries))
	copy(out, a.entries)
	return out
}

type counterKey struct{}
type stageUsageKey struct{}
type tokenBudgetKey struct{}

// WithTokenBudget returns a context carrying a cumulative-token budget of
// max. Instrumented counting models (rag.countingModel) consult this
// budget after each successful Generate to short-circuit further work
// when StageUsageAccumulator.TotalSoFar() exceeds max.
//
// A non-positive max (<= 0) is ignored and ctx is returned unchanged —
// 0 means "unlimited" (preserves v1.6.0 behavior when MaxTotalTokens is
// the zero value).
//
// v1.7.0.
func WithTokenBudget(ctx context.Context, max int) context.Context {
	if max <= 0 {
		return ctx
	}
	return context.WithValue(ctx, tokenBudgetKey{}, max)
}

// TokenBudgetFrom returns the cumulative-token budget on ctx, or 0 when
// none is attached. 0 means "unlimited" — callers should compare with
// `if budget > 0 && total > budget { ... }`.
//
// v1.7.0.
func TokenBudgetFrom(ctx context.Context) int {
	b, _ := ctx.Value(tokenBudgetKey{}).(int)
	return b
}

// WithCounter returns a context carrying c. Instrumented embedders and
// models increment it on each call.
func WithCounter(ctx context.Context, c *Counter) context.Context {
	return context.WithValue(ctx, counterKey{}, c)
}

// CounterFrom returns the Counter on ctx, or nil if none is attached. The
// result is safe to use directly — AddEmbed/AddGenerate are nil-safe.
func CounterFrom(ctx context.Context) *Counter {
	c, _ := ctx.Value(counterKey{}).(*Counter)
	return c
}

// WithStageUsage returns a context carrying a. Instrumented counting
// models append per-Generate StageTokenUsage entries to it.
func WithStageUsage(ctx context.Context, a *StageUsageAccumulator) context.Context {
	return context.WithValue(ctx, stageUsageKey{}, a)
}

// StageUsageFrom returns the StageUsageAccumulator on ctx, or nil if none
// is attached. The result is safe to use directly — Append and Snapshot are
// nil-safe.
func StageUsageFrom(ctx context.Context) *StageUsageAccumulator {
	a, _ := ctx.Value(stageUsageKey{}).(*StageUsageAccumulator)
	return a
}
