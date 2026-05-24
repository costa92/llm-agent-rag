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

func TestMetricsStageTokenUsageZeroValue(t *testing.T) {
	var m Metrics
	if m.StageTokenUsage != nil {
		t.Fatalf("zero Metrics.StageTokenUsage = %v, want nil", m.StageTokenUsage)
	}
	if len(m.StageTokenUsage) != 0 {
		t.Fatalf("zero Metrics.StageTokenUsage len = %d, want 0", len(m.StageTokenUsage))
	}
}
