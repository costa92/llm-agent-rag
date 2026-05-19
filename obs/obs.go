// Package obs holds the cost-and-latency types shared across the RAG
// pipeline. It is a leaf package — it imports only the standard library —
// so any package (rag, retrieve, ingest) can embed obs.Metrics without an
// import cycle.
package obs

import (
	"context"
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

// Metrics is the cost-and-latency record for one RAG operation. Stages are
// in execution order; TotalDuration is the end-to-end wall clock.
type Metrics struct {
	TotalDuration time.Duration // TotalDuration is the end-to-end wall clock.
	Stages        []StageTiming // Stages are the per-stage timings in execution order.
	Calls         CallCounts    // Calls is the external model-call accounting.
	Tokens        TokenUsage    // Tokens is the token accounting for the operation.
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

type counterKey struct{}

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
