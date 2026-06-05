package rag

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/advanced"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
)

func TestLLMCondenserEmptyHistoryReturnsQuestion(t *testing.T) {
	c := LLMCondenser{Model: nil}
	got, err := c.Condense(context.Background(), nil, "what is the fee?")
	if err != nil {
		t.Fatalf("Condense(): %v", err)
	}
	if got != "what is the fee?" {
		t.Fatalf("got = %q, want question unchanged", got)
	}
}

func TestLLMCondenserRequiresModelWhenHistory(t *testing.T) {
	c := LLMCondenser{Model: nil}
	_, err := c.Condense(context.Background(), []generate.Message{{Role: "user", Content: "hi"}}, "and then?")
	if !errors.Is(err, advanced.ErrModelRequired) {
		t.Fatalf("err = %v, want advanced.ErrModelRequired", err)
	}
}

func TestPassthroughCondenserAlwaysReturnsQuestion(t *testing.T) {
	got, err := passthroughCondenser{}.Condense(
		context.Background(),
		[]generate.Message{{Role: "user", Content: "hi"}},
		"unchanged?",
	)
	if err != nil {
		t.Fatalf("Condense(): %v", err)
	}
	if got != "unchanged?" {
		t.Fatalf("got = %q, want unchanged", got)
	}
}

func TestEffectiveCondenserDefaults(t *testing.T) {
	noModel := New(Options{})
	if _, ok := noModel.effectiveCondenser().(passthroughCondenser); !ok {
		t.Fatalf("effectiveCondenser() = %T, want passthroughCondenser", noModel.effectiveCondenser())
	}
	withModel := New(Options{Model: fakeModel{}})
	if _, ok := withModel.effectiveCondenser().(LLMCondenser); !ok {
		t.Fatalf("effectiveCondenser() = %T, want LLMCondenser", withModel.effectiveCondenser())
	}
	override := New(Options{Model: fakeModel{}, QueryCondenser: passthroughCondenser{}})
	if _, ok := override.effectiveCondenser().(passthroughCondenser); !ok {
		t.Fatalf("effectiveCondenser() = %T, want overridden passthroughCondenser", override.effectiveCondenser())
	}
}

func TestAskConversationCondensesAndRecordsProvenance(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "HelloTalk membership annual fee"},
			{Text: "the annual fee is $99"},
		},
	}
	sys := New(Options{Model: model})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "HelloTalk membership annual fee is $99 per year."},
	}, ingest.ImportOptions{Namespace: "kb"}); err != nil {
		t.Fatalf("Import(): %v", err)
	}

	history := []generate.Message{
		{Role: "user", Content: "What perks does HelloTalk membership have?"},
		{Role: "assistant", Content: "Unlimited translation and advanced matching."},
	}
	ans, err := sys.AskConversation(context.Background(), history, "and the annual fee?", AskOptions{
		Search: SearchOptions{Namespace: "kb"},
	})
	if err != nil {
		t.Fatalf("AskConversation(): %v", err)
	}
	if ans.Diagnostics.OriginalQuestion != "and the annual fee?" {
		t.Fatalf("OriginalQuestion = %q, want original", ans.Diagnostics.OriginalQuestion)
	}
	if ans.Diagnostics.CondensedQuery != "HelloTalk membership annual fee" {
		t.Fatalf("CondensedQuery = %q, want standalone query", ans.Diagnostics.CondensedQuery)
	}
	if len(model.requests) < 2 {
		t.Fatalf("model calls = %d, want >= 2 (condense + answer)", len(model.requests))
	}
	condensePrompt := model.requests[0].Messages[0].Content
	if !strings.Contains(condensePrompt, "and the annual fee?") {
		t.Fatalf("condense prompt missing latest question: %q", condensePrompt)
	}
}

func TestAskConversationEmptyHistoryMatchesAsk(t *testing.T) {
	build := func() *System {
		sys := New(Options{Model: fakeModel{}})
		if _, err := sys.Import(context.Background(), []ingest.Document{
			{ID: "doc1", Content: "Paris is the capital of France."},
		}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
			t.Fatalf("Import(): %v", err)
		}
		return sys
	}
	opts := AskOptions{Search: SearchOptions{Namespace: "geo"}}

	askAns, err := build().Ask(context.Background(), "capital of France", opts)
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	convAns, err := build().AskConversation(context.Background(), nil, "capital of France", opts)
	if err != nil {
		t.Fatalf("AskConversation(): %v", err)
	}
	if convAns.Text != askAns.Text {
		t.Fatalf("AskConversation Text = %q, want Ask Text %q", convAns.Text, askAns.Text)
	}
	if len(convAns.Hits) != len(askAns.Hits) {
		t.Fatalf("AskConversation Hits = %d, want %d", len(convAns.Hits), len(askAns.Hits))
	}
}

func TestAskConversationRequiresModel(t *testing.T) {
	sys := New(Options{})
	_, err := sys.AskConversation(context.Background(), nil, "anything", AskOptions{})
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}
