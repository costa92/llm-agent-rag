// Package advanced provides stateless, LLM-backed query-transformation
// helpers that reshape a query before retrieval to improve recall, such as
// multi-query expansion and hypothetical-document (HyDE) generation. Each
// helper takes a generate.Model and is a standalone function, not a pipeline
// stage.
package advanced

import (
	"context"
	"fmt"
	"strings"

	"github.com/costa92/llm-agent-rag/generate"
)

// ExpandQuery asks the model to rewrite query into n semantically equivalent
// alternatives. Empty and duplicate lines are removed.
func ExpandQuery(ctx context.Context, model generate.Model, query string, n int) ([]string, error) {
	if model == nil {
		return nil, ErrModelRequired
	}
	prompt := fmt.Sprintf(`Rewrite the user's search query into %d semantically-equivalent alternatives.
Each alternative on its own line, no numbering, no commentary.

Query: %s`, n, query)

	resp, err := model.Generate(ctx, generate.Request{
		Messages: []generate.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{strings.ToLower(query): true}
	out := make([]string, 0, n)
	for _, line := range strings.Split(resp.Text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "0123456789.-* \t")
		if line == "" {
			continue
		}
		key := strings.ToLower(line)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, line)
		if len(out) >= n {
			break
		}
	}
	return out, nil
}

// GenerateHypothetical asks the model to synthesize a short hypothetical
// answer suitable for embedding-driven recall.
func GenerateHypothetical(ctx context.Context, model generate.Model, query string) (string, error) {
	if model == nil {
		return "", ErrModelRequired
	}
	prompt := fmt.Sprintf(`Write a short hypothetical answer (2-3 sentences) that would directly answer the question below. Do not say "I don't know" — invent plausible-sounding content; this output is used to find similar real documents, not shown to the user.

Question: %s`, query)

	resp, err := model.Generate(ctx, generate.Request{
		Messages: []generate.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Text), nil
}

// GenerateStepBack asks the model to abstract query into a more general,
// higher-level question that surfaces background knowledge. The result is
// retrieved alongside the original query (step-back prompting).
func GenerateStepBack(ctx context.Context, model generate.Model, query string) (string, error) {
	if model == nil {
		return "", ErrModelRequired
	}
	prompt := fmt.Sprintf(`Generate a more general, higher-level version of the question below that retrieves useful background knowledge. Keep the original intent. Output only the rewritten question, no numbering, no commentary.

Question: %s`, query)

	resp, err := model.Generate(ctx, generate.Request{
		Messages: []generate.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Text), nil
}

// CondenseQuery rewrites question into a standalone retrieval query using the
// conversation history, resolving pronouns and omitted context. With empty
// history it returns question unchanged without calling the model. An empty
// model reply also falls back to the original question.
func CondenseQuery(ctx context.Context, model generate.Model, history []generate.Message, question string) (string, error) {
	if len(history) == 0 {
		return question, nil
	}
	if model == nil {
		return "", ErrModelRequired
	}
	var hist strings.Builder
	for _, m := range history {
		hist.WriteString(m.Role)
		hist.WriteString(": ")
		hist.WriteString(m.Content)
		hist.WriteString("\n")
	}
	prompt := fmt.Sprintf(`Given the conversation history, rewrite the latest question into a standalone, complete retrieval query that resolves any pronouns or omitted context. If no rewrite is needed, return the question unchanged. Output only the rewritten question, no commentary.

Conversation history:
%sLatest question: %s`, hist.String(), question)

	resp, err := model.Generate(ctx, generate.Request{
		Messages: []generate.Message{{Role: "user", Content: prompt}},
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
