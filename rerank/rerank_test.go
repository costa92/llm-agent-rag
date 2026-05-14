package rerank

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/store"
)

func TestNoopRerankerPreservesOrder(t *testing.T) {
	hits := []store.Hit{
		{Chunk: store.StoredChunk{ID: "a"}, Score: 0.2},
		{Chunk: store.StoredChunk{ID: "b"}, Score: 0.1},
	}
	got, trace, err := NoopReranker{}.Rerank(context.Background(), Request{
		Query: "paris",
		Hits:  hits,
	})
	if err != nil {
		t.Fatalf("Rerank(): %v", err)
	}
	if got[0].Chunk.ID != "a" || got[1].Chunk.ID != "b" {
		t.Fatalf("got = %+v, want original order", got)
	}
	if len(trace.OutputChunkIDs) != 2 || trace.OutputChunkIDs[0] != "a" {
		t.Fatalf("trace = %+v, want preserved ids", trace)
	}
}

func TestHeuristicRerankerPromotesLexicalMatch(t *testing.T) {
	hits := []store.Hit{
		{Chunk: store.StoredChunk{ID: "a", Content: "general travel guide"}, Score: 0.8},
		{Chunk: store.StoredChunk{ID: "b", Content: "Paris is the capital of France"}, Score: 0.7},
	}
	got, trace, err := HeuristicReranker{}.Rerank(context.Background(), Request{
		Query: "capital of france",
		Hits:  hits,
	})
	if err != nil {
		t.Fatalf("Rerank(): %v", err)
	}
	if got[0].Chunk.ID != "b" {
		t.Fatalf("top hit = %s, want b", got[0].Chunk.ID)
	}
	if trace.OutputChunkIDs[0] != "b" {
		t.Fatalf("trace = %+v, want b promoted", trace)
	}
}
