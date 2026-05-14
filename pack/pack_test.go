package pack

import (
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/store"
)

func TestSimpleCounterCountsEnglishWords(t *testing.T) {
	if got := (SimpleCounter{}).Count("Paris is the capital of France"); got <= 0 {
		t.Fatalf("Count() = %d, want positive", got)
	}
}

func TestGreedyTokenPackerDropsOverflowingChunks(t *testing.T) {
	result, err := GreedyTokenPacker{}.Pack(context.Background(), Request{
		Question:  "Where is Paris?",
		MaxTokens: 12,
		Hits: []store.Hit{
			{Chunk: store.StoredChunk{ID: "a", Content: "Paris is the capital of France."}},
			{Chunk: store.StoredChunk{ID: "b", Content: "Berlin is the capital of Germany."}},
		},
	})
	if err != nil {
		t.Fatalf("Pack(): %v", err)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("len(result.Hits) = %d, want 1", len(result.Hits))
	}
	if result.Trace.SelectedChunkIDs[0] != "a" {
		t.Fatalf("selected = %#v, want only a", result.Trace.SelectedChunkIDs)
	}
	if len(result.Trace.DroppedChunkIDs) == 0 || result.Trace.DroppedChunkIDs[0] != "b" {
		t.Fatalf("dropped = %#v, want b dropped", result.Trace.DroppedChunkIDs)
	}
}

func TestGreedyTokenPackerTruncatesLastChunk(t *testing.T) {
	result, err := GreedyTokenPacker{}.Pack(context.Background(), Request{
		Question:  "Paris?",
		MaxTokens: 10,
		Hits: []store.Hit{
			{Chunk: store.StoredChunk{ID: "a", Content: "Paris museums cafes boulevards river gardens monuments history."}},
		},
	})
	if err != nil {
		t.Fatalf("Pack(): %v", err)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("len(result.Hits) = %d, want 1", len(result.Hits))
	}
	if !strings.Contains(result.Hits[0].Chunk.Content, "[truncated]") {
		t.Fatalf("content = %q, want truncation marker", result.Hits[0].Chunk.Content)
	}
	if len(result.Trace.TruncatedChunkIDs) != 1 || result.Trace.TruncatedChunkIDs[0] != "a" {
		t.Fatalf("trace = %+v, want a truncated", result.Trace)
	}
}
