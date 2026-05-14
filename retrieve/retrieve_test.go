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

func TestLexicalRetrieverUsesContentOverlap(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:        "a",
			Namespace: "docs",
			Vector:    embed.Vector{1, 0},
			Content:   "Paris travel guide for museums",
		},
		{
			ID:        "b",
			Namespace: "docs",
			Vector:    embed.Vector{0.9, 0.1},
			Content:   "Berlin public transit manual",
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := LexicalRetriever{Store: mem}
	hits, _, err := r.Retrieve(context.Background(), Request{
		Query:     "paris museums",
		Namespace: "docs",
		TopK:      5,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) != 1 || hits[0].Chunk.ID != "a" {
		t.Fatalf("hits = %+v, want only a", hits)
	}
}

func TestHybridRetrieverFusesDenseAndLexical(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:        "a",
			Namespace: "docs",
			Vector:    embed.Vector{1, 0},
			Content:   "Paris travel guide for museums",
		},
		{
			ID:        "b",
			Namespace: "docs",
			Vector:    embed.Vector{0.95, 0.05},
			Content:   "France capital overview",
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := HybridRetriever{
		Dense:   DenseRetriever{Embedder: stubEmbedder{}, Store: mem},
		Lexical: LexicalRetriever{Store: mem},
	}
	hits, _, err := r.Retrieve(context.Background(), Request{
		Query:     "paris museums",
		Namespace: "docs",
		TopK:      5,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("expected fused hits, got none")
	}
	foundA := false
	for _, hit := range hits {
		if hit.Chunk.ID == "a" {
			foundA = true
			break
		}
	}
	if !foundA {
		t.Fatalf("hits = %+v, want fused result set to include lexical match a", hits)
	}
}
