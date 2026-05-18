package obs

import (
	"context"
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
