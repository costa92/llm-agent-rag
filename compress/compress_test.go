package compress

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/store"
)

func hit(id, content string, score float64) store.Hit {
	return store.Hit{Chunk: store.StoredChunk{ID: id, Content: content}, Score: score}
}

func TestNoopCompressorReturnsHitsUnchanged(t *testing.T) {
	in := []store.Hit{hit("a", "first sentence. second sentence.", 0.9)}
	out, err := NoopCompressor{}.Compress(context.Background(), "query", in)
	if err != nil {
		t.Fatalf("Compress(): %v", err)
	}
	if len(out) != 1 || out[0].Chunk.Content != "first sentence. second sentence." {
		t.Fatalf("out = %#v, want unchanged", out)
	}
}
