package rag

import (
	"context"
	"errors"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/store"
)

// TestAsk_BudgetExceeded_ReturnsZeroAnswer_AndTypedError asserts that
// Ask returns (Answer{}, *BudgetExceededError) when MaxTotalTokens is
// exceeded by the answer-stage Generate.
func TestAsk_BudgetExceeded_ReturnsZeroAnswer_AndTypedError(t *testing.T) {
	model := &scriptedUsageModel{
		responses: []generate.Response{
			{Text: "an answer", Usage: generate.Usage{PromptTokens: 80, CompletionTokens: 40, TotalTokens: 120}},
		},
	}
	sys := New(Options{Model: model})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of France", AskOptions{
		Search:         SearchOptions{Namespace: "geo", TopK: 1},
		MaxTotalTokens: 50,
	})
	if err == nil {
		t.Fatalf("Ask: want budget error")
	}
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("err type = %T, want *BudgetExceededError", err)
	}
	if ans.Text != "" {
		t.Errorf("ans.Text = %q, want empty Answer on budget abort", ans.Text)
	}
	if len(ans.Hits) != 0 {
		t.Errorf("ans.Hits len = %d, want 0 on budget abort", len(ans.Hits))
	}
	if !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Errorf("errors.Is to sentinel = false; want true")
	}
}

// TestAsk_BudgetExceeded_PartialDiagnostics_CarriesStageTokenUsage
// asserts the PartialDiagnostics carries the StageTokenUsage snapshot
// for the one Generate call that tripped the budget.
func TestAsk_BudgetExceeded_PartialDiagnostics_CarriesStageTokenUsage(t *testing.T) {
	model := &scriptedUsageModel{
		responses: []generate.Response{
			{Text: "an answer", Usage: generate.Usage{TotalTokens: 200}},
		},
	}
	sys := New(Options{Model: model})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	_, err := sys.Ask(context.Background(), "capital of France", AskOptions{
		Search:         SearchOptions{Namespace: "geo", TopK: 1},
		MaxTotalTokens: 50,
	})
	if err == nil {
		t.Fatalf("Ask: want budget error")
	}
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("err type = %T, want *BudgetExceededError", err)
	}
	stages := budgetErr.PartialDiagnostics.Metrics.StageTokenUsage
	if len(stages) != 1 {
		t.Fatalf("PartialDiagnostics.StageTokenUsage len = %d, want 1 (overage call recorded): %+v", len(stages), stages)
	}
	if stages[0].Stage != StageAsk {
		t.Errorf("StageTokenUsage[0].Stage = %q, want %q", stages[0].Stage, StageAsk)
	}
	if stages[0].Usage.TotalTokens != 200 {
		t.Errorf("StageTokenUsage[0].TotalTokens = %d, want 200", stages[0].Usage.TotalTokens)
	}
}

// TestAsk_BudgetExceeded_PartialDiagnostics_CarriesPartialReflectionRounds
// asserts that rule-mode reflection with MaxRounds=3 and a budget that
// allows exactly 2 rounds returns PartialDiagnostics with 2 reflection
// RoundDetails recorded.
func TestAsk_BudgetExceeded_PartialDiagnostics_CarriesPartialReflectionRounds(t *testing.T) {
	model := &scriptedUsageModel{
		responses: []generate.Response{
			// Round 1 answer: 50 tokens
			{Text: "round 1", Usage: generate.Usage{TotalTokens: 50}},
			// Round 2 answer: 50 tokens (cumulative 100 → still <= 150)
			{Text: "round 2", Usage: generate.Usage{TotalTokens: 50}},
			// Round 3 answer: 100 tokens (cumulative 200 → > 150 → trip)
			{Text: "round 3", Usage: generate.Usage{TotalTokens: 100}},
		},
	}
	sys := New(Options{
		Model: model,
		Retriever: orderedResultRetriever{
			results: map[string][]store.Hit{
				"capital of france": {
					orderedHit("docA", "doc1", 0.9, "alpha"),
				},
			},
		},
		Packer: orderedAllPacker{},
	})
	_, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:         SearchOptions{Namespace: "geo", TopK: 1},
		Template:       promptRoutingTemplate{},
		MaxTotalTokens: 150,
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        3,
			MinHits:          99, // never satisfied → loop until MaxRounds
			MinScore:         99,
			MinUniqueDocs:    99,
			RequireCitations: false,
		},
	})
	if err == nil {
		t.Fatalf("Ask: want budget error")
	}
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("err type = %T, want *BudgetExceededError", err)
	}
	got := len(budgetErr.PartialDiagnostics.Reflection.RoundDetails)
	if got != 2 {
		t.Fatalf("PartialDiagnostics.Reflection.RoundDetails len = %d, want 2 (budget allowed 2 of 3 rounds): rounds=%+v", got, budgetErr.PartialDiagnostics.Reflection.RoundDetails)
	}
}

// TestAsk_BudgetExceeded_PartialDiagnostics_HasCallCounts asserts the
// embed/generate counters are populated in the PartialDiagnostics.
func TestAsk_BudgetExceeded_PartialDiagnostics_HasCallCounts(t *testing.T) {
	model := &scriptedUsageModel{
		responses: []generate.Response{
			{Text: "answer", Usage: generate.Usage{TotalTokens: 100}},
		},
	}
	sys := New(Options{Model: model})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	_, err := sys.Ask(context.Background(), "capital", AskOptions{
		Search:         SearchOptions{Namespace: "geo", TopK: 1},
		MaxTotalTokens: 50,
	})
	if err == nil {
		t.Fatalf("Ask: want budget error")
	}
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("err type = %T, want *BudgetExceededError", err)
	}
	calls := budgetErr.PartialDiagnostics.Metrics.Calls
	if calls.Generate < 1 {
		t.Errorf("PartialDiagnostics.Calls.Generate = %d, want >= 1", calls.Generate)
	}
	if calls.Embed < 1 {
		t.Errorf("PartialDiagnostics.Calls.Embed = %d, want >= 1 (query embed)", calls.Embed)
	}
}

// TestAsk_NoBudget_UnaffectedByMaxTotalTokensZero asserts MaxTotalTokens
// == 0 leaves Ask byte-for-byte equivalent to the v1.6.0 path — a clean
// (Answer{...}, nil) return regardless of usage size.
func TestAsk_NoBudget_UnaffectedByMaxTotalTokensZero(t *testing.T) {
	model := &scriptedUsageModel{
		responses: []generate.Response{
			{Text: "answer", Usage: generate.Usage{TotalTokens: 999_999}},
		},
	}
	sys := New(Options{Model: model})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital", AskOptions{
		Search:         SearchOptions{Namespace: "geo", TopK: 1},
		MaxTotalTokens: 0,
	})
	if err != nil {
		t.Fatalf("Ask with zero budget: %v", err)
	}
	if ans.Text == "" {
		t.Fatalf("Ask returned empty answer despite no budget")
	}
}
