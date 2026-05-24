package eval_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/obs"
)

// recordingJudgeModel is a local fixture for the eval-side cost observer
// tests. It returns a canned generate.Response (or error) and records each
// request. Safe for concurrent use via sync.Mutex.
type recordingJudgeModel struct {
	mu       sync.Mutex
	requests []generate.Request
	resp     generate.Response
	err      error
}

func (m *recordingJudgeModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	if m.err != nil {
		return generate.Response{}, m.err
	}
	return m.resp, nil
}

// evalRecorder is a thread-safe ledger of GenerateUsageHook events for
// eval-side cost-observer tests.
type evalRecorder struct {
	mu      sync.Mutex
	entries []struct {
		Stage string
		Usage obs.TokenUsage
	}
}

func (r *evalRecorder) Hook(_ context.Context, stage string, usage obs.TokenUsage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, struct {
		Stage string
		Usage obs.TokenUsage
	}{Stage: stage, Usage: usage})
}

func (r *evalRecorder) Snapshot() []struct {
	Stage string
	Usage obs.TokenUsage
} {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]struct {
		Stage string
		Usage obs.TokenUsage
	}, len(r.entries))
	copy(out, r.entries)
	return out
}

func TestCostObservingJudgeFiresHookOnSuccess(t *testing.T) {
	rec := &evalRecorder{}
	inner := eval.LLMJudge{
		Model: &recordingJudgeModel{
			resp: generate.Response{
				Text: `{"groundedness": 0.9, "answer_relevance": 0.8, "rationale": "ok"}`,
				Usage: generate.Usage{
					PromptTokens:     12,
					CompletionTokens: 8,
					TotalTokens:      20,
				},
			},
		},
	}
	j := eval.NewCostObservingJudge(inner, rec.Hook)
	verdict, err := j.Judge(context.Background(), eval.JudgeRequest{
		Query:   "q",
		Answer:  "a",
		Context: []string{"c1", "c2"},
	})
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if verdict.Groundedness != 0.9 || verdict.AnswerRelevance != 0.8 {
		t.Fatalf("verdict = %+v, want g=0.9 a=0.8", verdict)
	}
	snap := rec.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Snapshot len = %d, want 1: %+v", len(snap), snap)
	}
	if snap[0].Stage != "judge_eval" {
		t.Fatalf("Stage = %q, want %q", snap[0].Stage, "judge_eval")
	}
	if snap[0].Usage.TotalTokens != 20 {
		t.Fatalf("Usage.TotalTokens = %d, want 20", snap[0].Usage.TotalTokens)
	}
}

func TestCostObservingJudgeSkipsHookOnError(t *testing.T) {
	rec := &evalRecorder{}
	inner := eval.LLMJudge{
		Model: &recordingJudgeModel{
			err: errors.New("boom"),
		},
	}
	j := eval.NewCostObservingJudge(inner, rec.Hook)
	if _, err := j.Judge(context.Background(), eval.JudgeRequest{Query: "q"}); err == nil {
		t.Fatalf("Judge err = nil, want non-nil")
	}
	if snap := rec.Snapshot(); len(snap) != 0 {
		t.Fatalf("hook fired on error path: %+v", snap)
	}
}

func TestCostObservingJudgeNilHookSafe(t *testing.T) {
	inner := eval.LLMJudge{
		Model: &recordingJudgeModel{
			resp: generate.Response{
				Text: `{"groundedness": 0.5, "answer_relevance": 0.5}`,
			},
		},
	}
	j := eval.NewCostObservingJudge(inner, nil)
	if _, err := j.Judge(context.Background(), eval.JudgeRequest{Query: "q"}); err != nil {
		t.Fatalf("Judge with nil hook: %v", err)
	}
}
