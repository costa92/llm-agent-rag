package obs

import (
	"context"
	"sync"
	"testing"
)

func TestCounterTallies(t *testing.T) {
	c := NewCounter()
	c.AddEmbed(2)
	c.AddEmbed(1)
	c.AddGenerate(1)
	if got := c.Counts(); got.Embed != 3 || got.Generate != 1 {
		t.Fatalf("Counts() = %+v, want {Embed:3 Generate:1}", got)
	}
}

func TestCounterFromAbsentIsNilSafe(t *testing.T) {
	if c := CounterFrom(context.Background()); c != nil {
		t.Fatalf("CounterFrom(bare ctx) = %v, want nil", c)
	}
	var c *Counter // nil Counter methods must not panic
	c.AddEmbed(1)
	c.AddGenerate(1)
	if got := c.Counts(); got != (CallCounts{}) {
		t.Fatalf("nil Counter Counts() = %+v, want zero", got)
	}
}

func TestWithCounterRoundTrip(t *testing.T) {
	c := NewCounter()
	ctx := WithCounter(context.Background(), c)
	if CounterFrom(ctx) != c {
		t.Fatalf("CounterFrom did not return the attached counter")
	}
	CounterFrom(ctx).AddEmbed(5)
	if c.Counts().Embed != 5 {
		t.Fatalf("increment via CounterFrom not visible on original counter")
	}
}

func TestStageTokenUsageAccumulatorThreadSafe(t *testing.T) {
	acc := NewStageUsageAccumulator()
	const goroutines = 1000
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			acc.Append("ask", TokenUsage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3})
		}()
	}
	wg.Wait()
	snap := acc.Snapshot()
	if len(snap) != goroutines {
		t.Fatalf("Snapshot len = %d, want %d", len(snap), goroutines)
	}
	for i, e := range snap {
		if e.Stage != "ask" || e.Usage.TotalTokens != 3 {
			t.Fatalf("entry[%d] = %+v, want stage=ask total=3", i, e)
		}
	}
}

func TestStageUsageFromMissingReturnsNilSafe(t *testing.T) {
	if a := StageUsageFrom(context.Background()); a != nil {
		t.Fatalf("StageUsageFrom(bare ctx) = %v, want nil", a)
	}
	var a *StageUsageAccumulator
	// nil accumulator methods must not panic
	a.Append("any", TokenUsage{})
	if snap := a.Snapshot(); snap != nil {
		t.Fatalf("nil accumulator Snapshot() = %v, want nil", snap)
	}
}

func TestWithStageUsageRoundTrip(t *testing.T) {
	a := NewStageUsageAccumulator()
	ctx := WithStageUsage(context.Background(), a)
	if got := StageUsageFrom(ctx); got != a {
		t.Fatalf("StageUsageFrom did not return the attached accumulator")
	}
	StageUsageFrom(ctx).Append("ask", TokenUsage{TotalTokens: 7})
	snap := a.Snapshot()
	if len(snap) != 1 || snap[0].Stage != "ask" || snap[0].Usage.TotalTokens != 7 {
		t.Fatalf("append via StageUsageFrom not visible on original: %+v", snap)
	}
}

// --- v1.7.0 C2 TotalSoFar tests ----------------------------------------

// TestStageUsageAccumulator_TotalSoFar_SumsTotalTokens asserts the
// returned int is the sum of entries[i].Usage.TotalTokens.
func TestStageUsageAccumulator_TotalSoFar_SumsTotalTokens(t *testing.T) {
	acc := NewStageUsageAccumulator()
	if got := acc.TotalSoFar(); got != 0 {
		t.Fatalf("empty TotalSoFar = %d, want 0", got)
	}
	acc.Append("ask", TokenUsage{TotalTokens: 10})
	acc.Append("reflection_decision", TokenUsage{TotalTokens: 5})
	acc.Append("grader", TokenUsage{TotalTokens: 7})
	if got := acc.TotalSoFar(); got != 22 {
		t.Fatalf("TotalSoFar = %d, want 22", got)
	}
}

// TestStageUsageAccumulator_TotalSoFar_NilSafe asserts a nil accumulator
// returns 0 without panicking.
func TestStageUsageAccumulator_TotalSoFar_NilSafe(t *testing.T) {
	var a *StageUsageAccumulator
	if got := a.TotalSoFar(); got != 0 {
		t.Fatalf("nil TotalSoFar = %d, want 0", got)
	}
}

// TestStageUsageAccumulator_TotalSoFar_ConcurrentSafe pummels TotalSoFar
// concurrently with Append. The race detector must remain clean.
func TestStageUsageAccumulator_TotalSoFar_ConcurrentSafe(t *testing.T) {
	acc := NewStageUsageAccumulator()
	const goroutines = 200
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			acc.Append("ask", TokenUsage{TotalTokens: 1})
		}()
		go func() {
			defer wg.Done()
			_ = acc.TotalSoFar()
		}()
	}
	wg.Wait()
	if got := acc.TotalSoFar(); got != goroutines {
		t.Fatalf("final TotalSoFar = %d, want %d", got, goroutines)
	}
}

// --- v1.7.0 C2 WithTokenBudget / TokenBudgetFrom tests ----------------

// TestTokenBudget_ZeroFromBareCtx asserts a bare context returns 0.
func TestTokenBudget_ZeroFromBareCtx(t *testing.T) {
	if got := TokenBudgetFrom(context.Background()); got != 0 {
		t.Fatalf("TokenBudgetFrom(bare ctx) = %d, want 0", got)
	}
}

// TestTokenBudget_RoundtripsThroughContext asserts a positive budget
// installed with WithTokenBudget reads back via TokenBudgetFrom.
func TestTokenBudget_RoundtripsThroughContext(t *testing.T) {
	ctx := WithTokenBudget(context.Background(), 1000)
	if got := TokenBudgetFrom(ctx); got != 1000 {
		t.Fatalf("TokenBudgetFrom = %d, want 1000", got)
	}
}

// TestTokenBudget_NonPositiveIgnored asserts WithTokenBudget(ctx, 0) and
// WithTokenBudget(ctx, -5) both return ctx unchanged (TokenBudgetFrom
// continues to read 0).
func TestTokenBudget_NonPositiveIgnored(t *testing.T) {
	base := context.Background()
	for _, max := range []int{0, -5} {
		ctx := WithTokenBudget(base, max)
		if got := TokenBudgetFrom(ctx); got != 0 {
			t.Errorf("WithTokenBudget(ctx, %d) -> TokenBudgetFrom = %d, want 0", max, got)
		}
	}
}

func TestMetricsStageTokenUsageZeroValue(t *testing.T) {
	var m Metrics
	if m.StageTokenUsage != nil {
		t.Fatalf("zero Metrics.StageTokenUsage = %v, want nil", m.StageTokenUsage)
	}
	if len(m.StageTokenUsage) != 0 {
		t.Fatalf("zero Metrics.StageTokenUsage len = %d, want 0", len(m.StageTokenUsage))
	}
}
