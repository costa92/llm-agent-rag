// Package agentic adds agentic retrieval patterns on top of the RAG
// pipeline. CorrectiveAsker is a self-correcting retrieval loop: it judges
// an answer's groundedness and, when grounding is low, reformulates the
// query and retries up to a bounded cap.
package agentic

import (
	"context"
	"errors"
	"strings"

	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/rag"
)

// Dependency errors returned by CorrectiveAsker.
var (
	ErrAskerRequired        = errors.New("agentic: Asker is required")
	ErrJudgeRequired        = errors.New("agentic: Judge is required")
	ErrReformulatorRequired = errors.New("agentic: Reformulator is required")
)

// QueryReformulator rewrites a question into a new retrieval query after a
// poorly-grounded answer.
type QueryReformulator interface {
	Reformulate(ctx context.Context, question string, prev rag.Answer) (string, error)
}

const reformulateSystemPrompt = "A previous answer was poorly grounded in " +
	"the retrieved context. Rewrite the user's question into a single, more " +
	"specific search query that would retrieve better supporting context. " +
	"Output only the rewritten query."

// LLMReformulator reformulates a query by prompting a generate.Model.
type LLMReformulator struct {
	Model generate.Model
}

// Reformulate prompts the model for a better retrieval query. A nil model
// or an empty response returns the original question unchanged.
func (r LLMReformulator) Reformulate(ctx context.Context, question string, prev rag.Answer) (string, error) {
	if r.Model == nil {
		return question, nil
	}
	resp, err := r.Model.Generate(ctx, generate.Request{
		SystemPrompt: reformulateSystemPrompt,
		Messages: []generate.Message{
			{Role: "user", Content: "Question: " + question + "\nPrevious answer: " + prev.Text},
		},
	})
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(resp.Text)
	if out == "" {
		return question, nil
	}
	return out, nil
}

// Attempt records one pass of the self-correcting loop.
type Attempt struct {
	Query           string
	Groundedness    float64
	AnswerRelevance float64
}

// Result is the outcome of a self-correcting ask: the best answer by
// groundedness, every attempt, and whether more than one attempt ran.
type Result struct {
	Answer    rag.Answer
	Attempts  []Attempt
	Corrected bool
}

// CorrectiveAsker runs the retrieve+generate pipeline, judges the answer's
// groundedness with the Phase 16 eval.Judge signal, and — when grounding
// is below MinGrounding — reformulates the query and retries, up to a
// bounded MaxRetries cap.
type CorrectiveAsker struct {
	Asker        eval.Asker
	Judge        eval.Judge
	Reformulator QueryReformulator
	MinGrounding float64 // grounding threshold; <= 0 → 0.5
	MaxRetries   int      // bounded retry cap; <= 0 → 2
}

// AskWithCorrection runs the self-correcting loop and returns the full
// Result. It makes at most MaxRetries+1 Asker.Ask calls and returns the
// best attempt by groundedness — never a worse later attempt.
func (a CorrectiveAsker) AskWithCorrection(ctx context.Context, question string, opts rag.AskOptions) (Result, error) {
	if a.Asker == nil {
		return Result{}, ErrAskerRequired
	}
	if a.Judge == nil {
		return Result{}, ErrJudgeRequired
	}
	if a.Reformulator == nil {
		return Result{}, ErrReformulatorRequired
	}
	minGrounding := a.MinGrounding
	if minGrounding <= 0 {
		minGrounding = 0.5
	}
	maxRetries := a.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 2
	}

	query := question
	var attempts []Attempt
	var best rag.Answer
	bestScore := -1.0

	for i := 0; i <= maxRetries; i++ {
		answer, err := a.Asker.Ask(ctx, query, opts)
		if err != nil {
			return Result{}, err
		}
		// Judge always against the original question — answer relevance is
		// to the user's real question, not the reformulated query.
		j, err := a.Judge.Judge(ctx, eval.JudgeRequest{
			Query:   question,
			Answer:  answer.Text,
			Context: hitContents(answer),
		})
		if err != nil {
			return Result{}, err
		}
		attempts = append(attempts, Attempt{
			Query:           query,
			Groundedness:    j.Groundedness,
			AnswerRelevance: j.AnswerRelevance,
		})
		if j.Groundedness > bestScore {
			bestScore = j.Groundedness
			best = answer
		}
		if j.Groundedness >= minGrounding || i == maxRetries {
			break
		}
		query, err = a.Reformulator.Reformulate(ctx, question, answer)
		if err != nil {
			return Result{}, err
		}
	}
	return Result{Answer: best, Attempts: attempts, Corrected: len(attempts) > 1}, nil
}

// Ask runs the self-correcting loop and returns just the best answer. It
// satisfies eval.Asker, so a CorrectiveAsker composes inside an
// eval.TriadEvaluator.
func (a CorrectiveAsker) Ask(ctx context.Context, question string, opts rag.AskOptions) (rag.Answer, error) {
	res, err := a.AskWithCorrection(ctx, question, opts)
	if err != nil {
		return rag.Answer{}, err
	}
	return res.Answer, nil
}

var _ eval.Asker = CorrectiveAsker{}

func hitContents(answer rag.Answer) []string {
	out := make([]string, 0, len(answer.Hits))
	for _, hit := range answer.Hits {
		out = append(out, hit.Chunk.Content)
	}
	return out
}
