package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/ingest"
)

// TestAskOptions_MaxTotalTokens_ZeroValueIsUnlimited asserts the field
// compiles in a keyed literal and the zero value is unlimited (no
// abort behavior is wired yet in this commit).
func TestAskOptions_MaxTotalTokens_ZeroValueIsUnlimited(t *testing.T) {
	opts := AskOptions{
		Search:         SearchOptions{Namespace: "geo", TopK: 1},
		MaxTotalTokens: 0,
	}
	if opts.MaxTotalTokens != 0 {
		t.Fatalf("MaxTotalTokens = %d, want 0", opts.MaxTotalTokens)
	}
}

// TestAskOptions_MaxTotalTokens_Plumbing_WithBudgetSet asserts Ask runs
// to completion under a sufficient budget (still no enforcement). This
// pins that the v1.7.0 ctx plumbing does not regress when MaxTotalTokens
// is set to a non-zero value but the budget is never exceeded.
func TestAskOptions_MaxTotalTokens_Plumbing_WithBudgetSet(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of France", AskOptions{
		Search:         SearchOptions{Namespace: "geo", TopK: 1},
		MaxTotalTokens: 1_000_000, // generous budget: never tripped
	})
	if err != nil {
		t.Fatalf("Ask with high budget: %v", err)
	}
	if ans.Text == "" {
		t.Fatalf("Ask returned empty answer text")
	}
}
