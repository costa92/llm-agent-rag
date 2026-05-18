package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
)

// usageModel is a scripted model that reports token usage.
type usageModel struct {
	usage generate.Usage
}

func (m usageModel) Generate(_ context.Context, _ generate.Request) (generate.Response, error) {
	return generate.Response{Text: "Paris is the capital of France.", Usage: m.usage}, nil
}

func seedTokenSystem(t *testing.T, model generate.Model) *System {
	t.Helper()
	sys := New(Options{Model: model})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	return sys
}

func TestAskTokensFromReportedUsage(t *testing.T) {
	sys := seedTokenSystem(t, usageModel{usage: generate.Usage{PromptTokens: 120, CompletionTokens: 30}})
	ans, err := sys.Ask(context.Background(), "capital of France",
		AskOptions{Search: SearchOptions{Namespace: "geo", TopK: 1}})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	tok := ans.Diagnostics.Metrics.Tokens
	if tok.Estimated {
		t.Fatalf("Tokens.Estimated = true, want false for model-reported usage")
	}
	if tok.PromptTokens != 120 || tok.CompletionTokens != 30 {
		t.Fatalf("Tokens = %+v, want Prompt 120 / Completion 30", tok)
	}
	if tok.TotalTokens != 150 {
		t.Fatalf("Tokens.TotalTokens = %d, want 150 (filled from the two parts)", tok.TotalTokens)
	}
}

func TestAskTokensEstimatedWhenUsageAbsent(t *testing.T) {
	sys := seedTokenSystem(t, fakeModel{}) // fakeModel leaves Response.Usage zero
	ans, err := sys.Ask(context.Background(), "capital of France",
		AskOptions{Search: SearchOptions{Namespace: "geo", TopK: 1}})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	tok := ans.Diagnostics.Metrics.Tokens
	if !tok.Estimated {
		t.Fatalf("Tokens.Estimated = false, want true when the model reports no usage")
	}
	if tok.TotalTokens <= 0 {
		t.Fatalf("estimated Tokens.TotalTokens = %d, want > 0", tok.TotalTokens)
	}
	if tok.TotalTokens != tok.PromptTokens+tok.CompletionTokens {
		t.Fatalf("estimated TotalTokens %d != Prompt %d + Completion %d",
			tok.TotalTokens, tok.PromptTokens, tok.CompletionTokens)
	}
}
