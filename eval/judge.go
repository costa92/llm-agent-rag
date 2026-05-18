package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/costa92/llm-agent-rag/generate"
)

// ErrJudgeModelRequired is returned by LLMJudge when no model is configured.
var ErrJudgeModelRequired = errors.New("eval: judge model required")

// JudgeRequest is one answer to be scored against its query and the context
// it was supposed to be grounded in.
type JudgeRequest struct {
	Query   string
	Answer  string
	Context []string
}

// Judgement scores a generated answer. Groundedness and AnswerRelevance are
// in [0,1]; higher is better.
type Judgement struct {
	Groundedness    float64
	AnswerRelevance float64
	Rationale       string
}

// Judge scores a generated answer for groundedness and answer relevance —
// legs 2 and 3 of the RAG Triad. Implementations may be heuristic or
// model-backed; callers supply their own so eval stays vendor-neutral.
type Judge interface {
	Judge(ctx context.Context, req JudgeRequest) (Judgement, error)
}

// LLMJudge is an LLM-as-judge: it scores an answer by prompting a
// generate.Model and parsing a JSON judgement from the reply.
type LLMJudge struct {
	Model generate.Model
}

const judgeSystemPrompt = `You are a strict evaluator of retrieval-augmented answers. ` +
	`Score the answer on two axes, each from 0.0 to 1.0:
- groundedness: how fully the answer is supported by the provided context passages (1.0 = every claim is supported, 0.0 = unsupported or contradicted).
- answer_relevance: how directly the answer addresses the question (1.0 = fully addresses it, 0.0 = off-topic).
Reply with ONLY a JSON object: {"groundedness": <number>, "answer_relevance": <number>, "rationale": "<short explanation>"}.`

// Judge implements the Judge interface.
func (j LLMJudge) Judge(ctx context.Context, req JudgeRequest) (Judgement, error) {
	if j.Model == nil {
		return Judgement{}, ErrJudgeModelRequired
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Question:\n%s\n\nAnswer:\n%s\n\nContext passages:\n", req.Query, req.Answer)
	if len(req.Context) == 0 {
		b.WriteString("(no context provided)\n")
	}
	for i, passage := range req.Context {
		fmt.Fprintf(&b, "[%d] %s\n", i+1, passage)
	}
	resp, err := j.Model.Generate(ctx, generate.Request{
		SystemPrompt: judgeSystemPrompt,
		Messages:     []generate.Message{{Role: "user", Content: b.String()}},
	})
	if err != nil {
		return Judgement{}, err
	}
	return parseJudgement(resp.Text)
}

type wireJudgement struct {
	Groundedness    float64 `json:"groundedness"`
	AnswerRelevance float64 `json:"answer_relevance"`
	Rationale       string  `json:"rationale"`
}

// parseJudgement extracts a JSON judgement from a model reply. Parsing is
// lenient: models often wrap the JSON object in prose, so the first '{'
// through the last '}' is taken as the object. Scores are clamped to [0,1].
func parseJudgement(text string) (Judgement, error) {
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end < 0 || end < start {
		return Judgement{}, fmt.Errorf("eval: judge response contains no JSON object: %q", text)
	}
	var w wireJudgement
	if err := json.Unmarshal([]byte(text[start:end+1]), &w); err != nil {
		return Judgement{}, fmt.Errorf("eval: parse judge response: %w", err)
	}
	return Judgement{
		Groundedness:    clamp01(w.Groundedness),
		AnswerRelevance: clamp01(w.AnswerRelevance),
		Rationale:       w.Rationale,
	}, nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
