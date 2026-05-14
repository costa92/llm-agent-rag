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
