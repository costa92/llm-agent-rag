package rag

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/store"
)

// Grader scores individual evidence chunks for relevance to a query and
// support for an answer. Inspired by Self-RAG / RAGLab [ISREL] / [ISSUP]
// reflection tokens, it is the per-chunk equivalent of an LLM judge.
//
// Both scoring methods return a float in [0.0, 1.0], a short human reason,
// and an error. Implementations should be fail-open: prefer returning a
// conservative score (e.g. 0.5) with a parse note than an error.
//
// Compatibility note: this interface is additive — it is not consumed by
// any existing seam and only takes effect when wired through
// ReflectionOptions.EnableChunkGrading.
type Grader interface {
	// ScoreRelevance scores how relevant a hit is to a query.
	ScoreRelevance(ctx context.Context, query string, hit store.Hit) (score float64, reason string, err error)
	// ScoreSupport scores how well a hit supports a generated answer.
	ScoreSupport(ctx context.Context, answer string, hit store.Hit) (score float64, reason string, err error)
}

// NoopGrader is a deterministic Grader that returns the neutral 0.5 score
// for every chunk. It is the safe default when grading is wired but no
// real Grader is configured.
type NoopGrader struct{}

// ScoreRelevance returns the neutral 0.5 for any input.
func (NoopGrader) ScoreRelevance(_ context.Context, _ string, _ store.Hit) (float64, string, error) {
	return 0.5, "noop-grader: neutral relevance", nil
}

// ScoreSupport returns the neutral 0.5 for any input.
func (NoopGrader) ScoreSupport(_ context.Context, _ string, _ store.Hit) (float64, string, error) {
	return 0.5, "noop-grader: neutral support", nil
}

// PromptGrader scores hits by asking a generate.Model to emit a numeric
// score in [0.0, 1.0]. Inspired by Self-RAG's [ISREL] / [ISSUP] reflection
// tokens. On parse failure it falls open with 0.5 and the raw reply text
// as the reason — a missing or malformed score never errors the call.
type PromptGrader struct {
	// Model is the underlying generation model used to score chunks. A nil
	// Model surfaces an error so callers can detect misconfiguration.
	Model generate.Model
}

// ScoreRelevance asks Model to score how relevant the chunk is to query.
func (p PromptGrader) ScoreRelevance(ctx context.Context, query string, hit store.Hit) (float64, string, error) {
	if p.Model == nil {
		return 0, "", fmt.Errorf("rag: PromptGrader.Model is nil")
	}
	prompt := graderRelevancePrompt(query, hit)
	return p.score(ctx, prompt)
}

// ScoreSupport asks Model to score how well the chunk supports answer.
func (p PromptGrader) ScoreSupport(ctx context.Context, answer string, hit store.Hit) (float64, string, error) {
	if p.Model == nil {
		return 0, "", fmt.Errorf("rag: PromptGrader.Model is nil")
	}
	prompt := graderSupportPrompt(answer, hit)
	return p.score(ctx, prompt)
}

// score issues one chat turn and parses the numeric score from the reply.
// Fail-open: on parse error, return 0.5 + the raw text as reason.
func (p PromptGrader) score(ctx context.Context, prompt string) (float64, string, error) {
	req := generate.Request{
		SystemPrompt: "You score a retrieved evidence chunk for the RAG pipeline. Reply with a single line: score=<float between 0 and 1>.",
		Messages: []generate.Message{{
			Role:    "user",
			Content: prompt,
		}},
	}
	resp, err := p.Model.Generate(ctx, req)
	if err != nil {
		return 0, "", err
	}
	score, ok := parseGraderScore(resp.Text)
	if !ok {
		return 0.5, "parse-failed: " + strings.TrimSpace(resp.Text), nil
	}
	return score, strings.TrimSpace(resp.Text), nil
}

// graderRelevancePrompt renders the per-chunk relevance prompt.
func graderRelevancePrompt(query string, hit store.Hit) string {
	return fmt.Sprintf(
		"Query: %s\nChunk ID: %s\nChunk content: %s\n\nScore how relevant the chunk is to the query on a 0.0-1.0 scale. Reply with: score=<float>",
		query,
		hit.Chunk.ID,
		hit.Chunk.Content,
	)
}

// graderSupportPrompt renders the per-chunk answer-support prompt.
func graderSupportPrompt(answer string, hit store.Hit) string {
	return fmt.Sprintf(
		"Answer: %s\nChunk ID: %s\nChunk content: %s\n\nScore how well the chunk supports the answer on a 0.0-1.0 scale. Reply with: score=<float>",
		answer,
		hit.Chunk.ID,
		hit.Chunk.Content,
	)
}

// parseGraderScore extracts a float in [0,1] from a "score=<float>" line.
// It is tolerant of leading/trailing whitespace and mixed case and clamps
// the parsed value to [0,1]. It returns ok=false when no score= line is
// present or the value cannot be parsed — the caller then fails open.
func parseGraderScore(text string) (float64, bool) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(key), "score") {
			continue
		}
		v := strings.TrimSpace(value)
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, false
		}
		if f < 0 {
			f = 0
		}
		if f > 1 {
			f = 1
		}
		return f, true
	}
	return 0, false
}
