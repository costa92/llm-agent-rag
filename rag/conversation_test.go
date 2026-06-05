package rag

import (
	"context"
	"errors"
	"testing"

	"github.com/costa92/llm-agent-rag/advanced"
	"github.com/costa92/llm-agent-rag/generate"
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
