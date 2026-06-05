package advanced

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
)

type scriptedModel struct {
	resp string
	err  error
}

func (m scriptedModel) Generate(_ context.Context, _ generate.Request) (generate.Response, error) {
	if m.err != nil {
		return generate.Response{}, m.err
	}
	return generate.Response{Text: m.resp}, nil
}

func TestExpandQueryDedupesAndStripsNumbering(t *testing.T) {
	got, err := ExpandQuery(context.Background(), scriptedModel{
		resp: "1. go dependency management\n- Go modules\nmodule system\n\nmodule system",
	}, "go modules", 3)
	if err != nil {
		t.Fatalf("ExpandQuery(): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0] != "go dependency management" || got[1] != "module system" {
		t.Fatalf("got = %#v", got)
	}
}

func TestExpandQueryRequiresModel(t *testing.T) {
	_, err := ExpandQuery(context.Background(), nil, "go modules", 2)
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

func TestGenerateHypotheticalTrimsWhitespace(t *testing.T) {
	got, err := GenerateHypothetical(context.Background(), scriptedModel{
		resp: "  Go modules manage dependencies.  \n",
	}, "what are go modules?")
	if err != nil {
		t.Fatalf("GenerateHypothetical(): %v", err)
	}
	if !strings.Contains(got, "modules") {
		t.Fatalf("got = %q", got)
	}
	if strings.HasPrefix(got, " ") || strings.HasSuffix(got, "\n") {
		t.Fatalf("got not trimmed: %q", got)
	}
}

func TestGenerateStepBackTrimsWhitespace(t *testing.T) {
	got, err := GenerateStepBack(context.Background(), scriptedModel{
		resp: "  How is the user level system designed?  \n",
	}, "how many points does Lv5 need?")
	if err != nil {
		t.Fatalf("GenerateStepBack(): %v", err)
	}
	if !strings.Contains(got, "level system") {
		t.Fatalf("got = %q", got)
	}
	if strings.HasPrefix(got, " ") || strings.HasSuffix(got, "\n") {
		t.Fatalf("got not trimmed: %q", got)
	}
}

func TestGenerateStepBackRequiresModel(t *testing.T) {
	_, err := GenerateStepBack(context.Background(), nil, "anything")
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

func TestCondenseQueryEmptyHistoryReturnsQuestionWithoutModel(t *testing.T) {
	got, err := CondenseQuery(context.Background(), nil, nil, "what is the annual fee?")
	if err != nil {
		t.Fatalf("CondenseQuery(): %v", err)
	}
	if got != "what is the annual fee?" {
		t.Fatalf("got = %q, want question unchanged", got)
	}
}

func TestCondenseQueryRewritesWithHistory(t *testing.T) {
	history := []generate.Message{
		{Role: "user", Content: "What perks does HelloTalk membership have?"},
		{Role: "assistant", Content: "Members get unlimited translation and advanced matching."},
	}
	got, err := CondenseQuery(context.Background(), scriptedModel{
		resp: "  How much is the HelloTalk membership annual fee?  \n",
	}, history, "and the annual fee?")
	if err != nil {
		t.Fatalf("CondenseQuery(): %v", err)
	}
	if got != "How much is the HelloTalk membership annual fee?" {
		t.Fatalf("got = %q, want trimmed standalone query", got)
	}
}

func TestCondenseQueryRequiresModelWhenHistoryPresent(t *testing.T) {
	history := []generate.Message{{Role: "user", Content: "hi"}}
	_, err := CondenseQuery(context.Background(), nil, history, "and then?")
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

func TestCondenseQueryEmptyModelReplyFallsBackToQuestion(t *testing.T) {
	history := []generate.Message{{Role: "user", Content: "hi"}}
	got, err := CondenseQuery(context.Background(), scriptedModel{resp: "   \n"}, history, "and then?")
	if err != nil {
		t.Fatalf("CondenseQuery(): %v", err)
	}
	if got != "and then?" {
		t.Fatalf("got = %q, want fallback to original question", got)
	}
}
