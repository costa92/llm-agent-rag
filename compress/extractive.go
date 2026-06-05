package compress

import (
	"context"
	"sort"
	"strings"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/store"
)

// ExtractiveCompressor keeps only the sentences in each chunk most relevant
// to the query, scored by embedding cosine similarity. It never rewrites
// text, so kept content stays verbatim source and citations remain exact.
type ExtractiveCompressor struct {
	Embedder     embed.Embedder // Embedder scores sentences against the query.
	MaxSentences int            // MaxSentences kept per chunk; <= 0 defaults to 2.
}

// Compress keeps the top-MaxSentences sentences per chunk by relevance to
// query, preserving their original order. Chunks with no more than
// MaxSentences sentences are returned whole.
func (c ExtractiveCompressor) Compress(ctx context.Context, query string, hits []store.Hit) ([]store.Hit, error) {
	if c.Embedder == nil {
		return nil, ErrEmbedderRequired
	}
	maxSentences := c.MaxSentences
	if maxSentences <= 0 {
		maxSentences = 2
	}
	qv, err := c.Embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]store.Hit, len(hits))
	for i, h := range hits {
		sentences := splitSentences(h.Chunk.Content)
		if len(sentences) <= maxSentences {
			out[i] = h
			continue
		}
		type scored struct {
			idx   int
			text  string
			score float64
		}
		ranked := make([]scored, 0, len(sentences))
		for j, s := range sentences {
			sv, err := c.Embedder.Embed(ctx, s)
			if err != nil {
				return nil, err
			}
			ranked = append(ranked, scored{idx: j, text: s, score: embed.CosineSimilarity(qv, sv)})
		}
		sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].score > ranked[b].score })
		kept := ranked[:maxSentences]
		sort.SliceStable(kept, func(a, b int) bool { return kept[a].idx < kept[b].idx })
		parts := make([]string, len(kept))
		for k, s := range kept {
			parts[k] = s.text
		}
		h.Chunk.Content = strings.Join(parts, " ")
		out[i] = h
	}
	return out, nil
}

// splitSentences splits text on '.', '!', '?' terminators, keeping each
// terminator with its sentence. Whitespace-only fragments are dropped.
func splitSentences(text string) []string {
	var out []string
	var b strings.Builder
	for _, r := range text {
		b.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			if s := strings.TrimSpace(b.String()); s != "" {
				out = append(out, s)
			}
			b.Reset()
		}
	}
	if s := strings.TrimSpace(b.String()); s != "" {
		out = append(out, s)
	}
	return out
}
