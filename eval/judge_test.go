package eval_test

import (
	"context"
	"errors"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/generate"
)

// scriptedModel returns a fixed reply, so LLMJudge parsing is deterministic.
type scriptedModel struct {
	text string
}

func (m scriptedModel) Generate(_ context.Context, _ generate.Request) (generate.Response, error) {
	return generate.Response{Text: m.text}, nil
}

func TestLLMJudgeParsesCleanJSON(t *testing.T) {
	j := eval.LLMJudge{Model: scriptedModel{
		text: `{"groundedness": 0.9, "answer_relevance": 0.8, "rationale": "well supported"}`,
	}}
	got, err := j.Judge(context.Background(), eval.JudgeRequest{
		Query: "q", Answer: "a", Context: []string{"c"},
	})
	if err != nil {
		t.Fatalf("Judge(): %v", err)
	}
	if got.Groundedness != 0.9 || got.AnswerRelevance != 0.8 {
		t.Fatalf("got %+v, want groundedness 0.9 answer_relevance 0.8", got)
	}
	if got.Rationale != "well supported" {
		t.Fatalf("rationale = %q, want %q", got.Rationale, "well supported")
	}
}

func TestLLMJudgeExtractsJSONWrappedInProse(t *testing.T) {
	j := eval.LLMJudge{Model: scriptedModel{
		text: `Here is my evaluation: {"groundedness":0.5,"answer_relevance":0.6,"rationale":"partial"} done.`,
	}}
	got, err := j.Judge(context.Background(), eval.JudgeRequest{Query: "q", Answer: "a"})
	if err != nil {
		t.Fatalf("Judge(): %v", err)
	}
	if got.Groundedness != 0.5 || got.AnswerRelevance != 0.6 {
		t.Fatalf("got %+v, want groundedness 0.5 answer_relevance 0.6", got)
	}
}

func TestLLMJudgeClampsOutOfRangeScores(t *testing.T) {
	j := eval.LLMJudge{Model: scriptedModel{
		text: `{"groundedness":1.5,"answer_relevance":-0.3,"rationale":"x"}`,
	}}
	got, err := j.Judge(context.Background(), eval.JudgeRequest{Query: "q", Answer: "a"})
	if err != nil {
		t.Fatalf("Judge(): %v", err)
	}
	if got.Groundedness != 1.0 {
		t.Fatalf("Groundedness = %v, want clamped to 1.0", got.Groundedness)
	}
	if got.AnswerRelevance != 0.0 {
		t.Fatalf("AnswerRelevance = %v, want clamped to 0.0", got.AnswerRelevance)
	}
}

func TestLLMJudgeErrorsWhenNoJSON(t *testing.T) {
	j := eval.LLMJudge{Model: scriptedModel{text: "I cannot evaluate this answer."}}
	if _, err := j.Judge(context.Background(), eval.JudgeRequest{Query: "q", Answer: "a"}); err == nil {
		t.Fatalf("Judge() with no JSON in reply: want error")
	}
}

func TestLLMJudgeRequiresModel(t *testing.T) {
	_, err := eval.LLMJudge{}.Judge(context.Background(), eval.JudgeRequest{Query: "q", Answer: "a"})
	if !errors.Is(err, eval.ErrJudgeModelRequired) {
		t.Fatalf("err = %v, want ErrJudgeModelRequired", err)
	}
}
