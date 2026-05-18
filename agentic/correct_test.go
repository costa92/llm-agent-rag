package agentic_test

import (
	"context"
	"errors"
	"testing"

	"github.com/costa92/llm-agent-rag/agentic"
	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/rag"
	"github.com/costa92/llm-agent-rag/store"
)

// stubAsker returns answers keyed by query and records every call.
type stubAsker struct {
	byQuery map[string]rag.Answer
	calls   []string
}

func (s *stubAsker) Ask(_ context.Context, question string, _ rag.AskOptions) (rag.Answer, error) {
	s.calls = append(s.calls, question)
	return s.byQuery[question], nil
}

// stubJudge returns a deterministic groundedness keyed by answer text.
type stubJudge struct {
	byAnswer map[string]float64
}

func (j stubJudge) Judge(_ context.Context, req eval.JudgeRequest) (eval.Judgement, error) {
	return eval.Judgement{Groundedness: j.byAnswer[req.Answer], AnswerRelevance: 1}, nil
}

// stubReformulator always returns a fixed next query.
type stubReformulator struct{ next string }

func (r stubReformulator) Reformulate(_ context.Context, _ string, _ rag.Answer) (string, error) {
	return r.next, nil
}

func answerWith(text string) rag.Answer {
	return rag.Answer{Text: text, Hits: []store.Hit{{Chunk: store.StoredChunk{Content: "context"}}}}
}

func TestCorrectiveAskerCorrects(t *testing.T) {
	asker := &stubAsker{byQuery: map[string]rag.Answer{
		"original":     answerWith("weak answer"),
		"reformulated": answerWith("strong answer"),
	}}
	ca := agentic.CorrectiveAsker{
		Asker:        asker,
		Judge:        stubJudge{byAnswer: map[string]float64{"weak answer": 0.2, "strong answer": 0.9}},
		Reformulator: stubReformulator{next: "reformulated"},
		MinGrounding: 0.5,
		MaxRetries:   2,
	}
	res, err := ca.AskWithCorrection(context.Background(), "original", rag.AskOptions{})
	if err != nil {
		t.Fatalf("AskWithCorrection: %v", err)
	}
	if res.Answer.Text != "strong answer" {
		t.Fatalf("Answer = %q, want 'strong answer'", res.Answer.Text)
	}
	if len(res.Attempts) != 2 {
		t.Fatalf("Attempts = %d, want 2", len(res.Attempts))
	}
	if !res.Corrected {
		t.Fatalf("Corrected = false, want true")
	}
}

func TestCorrectiveAskerRespectsRetryCap(t *testing.T) {
	asker := &stubAsker{byQuery: map[string]rag.Answer{
		"original": answerWith("low1"),
		"reform":   answerWith("low2"),
	}}
	ca := agentic.CorrectiveAsker{
		Asker:        asker,
		Judge:        stubJudge{byAnswer: map[string]float64{"low1": 0.1, "low2": 0.2}},
		Reformulator: stubReformulator{next: "reform"},
		MinGrounding: 0.9,
		MaxRetries:   2,
	}
	res, err := ca.AskWithCorrection(context.Background(), "original", rag.AskOptions{})
	if err != nil {
		t.Fatalf("AskWithCorrection: %v", err)
	}
	if len(res.Attempts) != 3 {
		t.Fatalf("Attempts = %d, want 3 (MaxRetries 2 + 1)", len(res.Attempts))
	}
	if len(asker.calls) != 3 {
		t.Fatalf("Asker.Ask called %d times, want 3 (bounded cap)", len(asker.calls))
	}
	if res.Answer.Text != "low2" {
		t.Fatalf("Answer = %q, want best attempt 'low2' (0.2 > 0.1)", res.Answer.Text)
	}
}

func TestCorrectiveAskerNoCorrectionNeeded(t *testing.T) {
	asker := &stubAsker{byQuery: map[string]rag.Answer{"original": answerWith("good")}}
	ca := agentic.CorrectiveAsker{
		Asker:        asker,
		Judge:        stubJudge{byAnswer: map[string]float64{"good": 0.95}},
		Reformulator: stubReformulator{next: "unused"},
		MinGrounding: 0.5,
		MaxRetries:   2,
	}
	res, err := ca.AskWithCorrection(context.Background(), "original", rag.AskOptions{})
	if err != nil {
		t.Fatalf("AskWithCorrection: %v", err)
	}
	if len(res.Attempts) != 1 {
		t.Fatalf("Attempts = %d, want 1 (no retry)", len(res.Attempts))
	}
	if res.Corrected {
		t.Fatalf("Corrected = true, want false")
	}
	if res.Answer.Text != "good" {
		t.Fatalf("Answer = %q, want 'good'", res.Answer.Text)
	}
}

func TestCorrectiveAskerRequiresDependencies(t *testing.T) {
	asker := &stubAsker{byQuery: map[string]rag.Answer{}}
	judge := stubJudge{}
	reform := stubReformulator{}
	ctx := context.Background()

	if _, err := (agentic.CorrectiveAsker{Judge: judge, Reformulator: reform}).
		AskWithCorrection(ctx, "q", rag.AskOptions{}); !errors.Is(err, agentic.ErrAskerRequired) {
		t.Fatalf("nil Asker → %v, want ErrAskerRequired", err)
	}
	if _, err := (agentic.CorrectiveAsker{Asker: asker, Reformulator: reform}).
		AskWithCorrection(ctx, "q", rag.AskOptions{}); !errors.Is(err, agentic.ErrJudgeRequired) {
		t.Fatalf("nil Judge → %v, want ErrJudgeRequired", err)
	}
	if _, err := (agentic.CorrectiveAsker{Asker: asker, Judge: judge}).
		AskWithCorrection(ctx, "q", rag.AskOptions{}); !errors.Is(err, agentic.ErrReformulatorRequired) {
		t.Fatalf("nil Reformulator → %v, want ErrReformulatorRequired", err)
	}
}

type scriptedModel struct{ text string }

func (m scriptedModel) Generate(_ context.Context, _ generate.Request) (generate.Response, error) {
	return generate.Response{Text: m.text}, nil
}

func TestLLMReformulator(t *testing.T) {
	out, err := (agentic.LLMReformulator{Model: scriptedModel{text: "  better query  "}}).
		Reformulate(context.Background(), "orig", rag.Answer{Text: "weak"})
	if err != nil {
		t.Fatalf("Reformulate: %v", err)
	}
	if out != "better query" {
		t.Fatalf("Reformulate = %q, want 'better query' (trimmed)", out)
	}
	out, err = (agentic.LLMReformulator{}).Reformulate(context.Background(), "orig", rag.Answer{})
	if err != nil {
		t.Fatalf("Reformulate (nil model): %v", err)
	}
	if out != "orig" {
		t.Fatalf("nil-model Reformulate = %q, want 'orig'", out)
	}
}
