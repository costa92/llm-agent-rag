package compress

import (
	"context"
	"fmt"
	"strings"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/store"
)

// AbstractiveCompressor replaces each chunk's Content with an LLM-generated,
// query-focused summary. It achieves higher compression than the extractive
// compressor, but the summary is model-generated text, not verbatim source —
// so it carries a hallucination risk and one model call per chunk.
type AbstractiveCompressor struct {
	Model generate.Model // Model produces the per-chunk summary.
}

// Compress summarizes each chunk toward the query. An empty model reply
// leaves that chunk's content unchanged.
func (c AbstractiveCompressor) Compress(ctx context.Context, query string, hits []store.Hit) ([]store.Hit, error) {
	if c.Model == nil {
		return nil, ErrModelRequired
	}
	out := make([]store.Hit, len(hits))
	for i, h := range hits {
		prompt := fmt.Sprintf(`Summarize the passage below, keeping only what is relevant to the query. Be concise. Output only the summary, no commentary.

Query: %s

Passage:
%s`, query, h.Chunk.Content)
		resp, err := c.Model.Generate(ctx, generate.Request{
			Messages: []generate.Message{{Role: "user", Content: prompt}},
		})
		if err != nil {
			return nil, err
		}
		if summary := strings.TrimSpace(resp.Text); summary != "" {
			h.Chunk.Content = summary
		}
		out[i] = h
	}
	return out, nil
}
