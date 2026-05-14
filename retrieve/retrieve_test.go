package retrieve

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/store"
)

func TestNoopPreprocessorPreservesQuery(t *testing.T) {
	res, err := NoopPreprocessor{}.Process(context.Background(), Request{Query: "paris"})
	if err != nil {
		t.Fatalf("Process(): %v", err)
	}
	if len(res.QueryVariants) != 1 || res.QueryVariants[0] != "paris" {
		t.Fatalf("QueryVariants = %+v, want [paris]", res.QueryVariants)
	}
	if res.Trace.EffectiveQuery != "paris" {
		t.Fatalf("EffectiveQuery = %q, want paris", res.Trace.EffectiveQuery)
	}
}

type stubEmbedder struct{}

func (stubEmbedder) Embed(_ context.Context, _ string) (embed.Vector, error) {
	return embed.Vector{1, 0}, nil
}

func TestDenseRetrieverUsesStoreContract(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:        "a",
			Namespace: "docs",
			Vector:    embed.Vector{1, 0},
			Metadata: map[string]any{
				"lang": "en",
			},
		},
		{
			ID:        "b",
			Namespace: "docs",
			Vector:    embed.Vector{0.9, 0.1},
			Metadata: map[string]any{
				"lang": "fr",
			},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := DenseRetriever{Embedder: stubEmbedder{}, Store: mem}
	hits, trace, err := r.Retrieve(context.Background(), Request{
		Query:     "paris",
		Namespace: "docs",
		TopK:      5,
		Filters: map[string]any{
			"lang": "en",
		},
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) != 1 || hits[0].Chunk.ID != "a" {
		t.Fatalf("hits = %+v, want only a", hits)
	}
	if trace.EffectiveQuery != "paris" {
		t.Fatalf("trace = %+v, want effective query paris", trace)
	}
}
