package store

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/embed"
)

func TestInMemoryStoreNamespaceIsolation(t *testing.T) {
	s := NewInMemoryStore(2)
	err := s.Upsert(context.Background(), []StoredChunk{
		{ID: "a", Namespace: "n1", Vector: embed.Vector{1, 0}},
		{ID: "b", Namespace: "n2", Vector: embed.Vector{0, 1}},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}
	hits, err := s.Search(context.Background(), Query{Namespace: "n1", Vector: embed.Vector{1, 0}, TopK: 5})
	if err != nil {
		t.Fatalf("Search(): %v", err)
	}
	if len(hits) != 1 || hits[0].Chunk.ID != "a" {
		t.Fatalf("hits = %+v, want only a", hits)
	}
}
