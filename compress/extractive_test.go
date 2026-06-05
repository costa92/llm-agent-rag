package compress

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/store"
)

// keywordEmbedder embeds text as [containsParis, 1]. The constant second
// dimension keeps every vector non-zero so cosine similarity is never NaN.
type keywordEmbedder struct{}

func (keywordEmbedder) Embed(_ context.Context, text string) (embed.Vector, error) {
	contains := float32(0)
	if strings.Contains(strings.ToLower(text), "paris") {
		contains = 1
	}
	return embed.Vector{contains, 1}, nil
}

func (keywordEmbedder) Dimension() int { return 2 }

func TestExtractiveCompressorKeepsMostRelevantSentence(t *testing.T) {
	c := ExtractiveCompressor{Embedder: keywordEmbedder{}, MaxSentences: 1}
	in := []store.Hit{{
		Chunk: store.StoredChunk{
			ID:       "a",
			Content:  "Berlin is large. Paris is the capital of France. Rome is old.",
			Metadata: map[string]any{"k": "v"},
		},
		Score: 0.9,
	}}
	out, err := c.Compress(context.Background(), "paris", in)
	if err != nil {
		t.Fatalf("Compress(): %v", err)
	}
	if out[0].Chunk.Content != "Paris is the capital of France." {
		t.Fatalf("Content = %q, want only the Paris sentence", out[0].Chunk.Content)
	}
	if out[0].Chunk.ID != "a" || out[0].Score != 0.9 || out[0].Chunk.Metadata["k"] != "v" {
		t.Fatalf("provenance not preserved: %#v", out[0])
	}
	// Original input must not be mutated.
	if in[0].Chunk.Content != "Berlin is large. Paris is the capital of France. Rome is old." {
		t.Fatalf("input mutated: %q", in[0].Chunk.Content)
	}
}

func TestExtractiveCompressorKeepsShortChunksWhole(t *testing.T) {
	c := ExtractiveCompressor{Embedder: keywordEmbedder{}, MaxSentences: 2}
	in := []store.Hit{hit("a", "Only one sentence here.", 0.5)}
	out, err := c.Compress(context.Background(), "paris", in)
	if err != nil {
		t.Fatalf("Compress(): %v", err)
	}
	if out[0].Chunk.Content != "Only one sentence here." {
		t.Fatalf("Content = %q, want unchanged (<= MaxSentences)", out[0].Chunk.Content)
	}
}

func TestExtractiveCompressorRequiresEmbedder(t *testing.T) {
	_, err := ExtractiveCompressor{}.Compress(context.Background(), "paris", nil)
	if !errors.Is(err, ErrEmbedderRequired) {
		t.Fatalf("err = %v, want ErrEmbedderRequired", err)
	}
}
