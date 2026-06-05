package compress

import (
	"context"
	"errors"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/store"
)

type stubModel struct {
	resp string
}

func (m stubModel) Generate(_ context.Context, _ generate.Request) (generate.Response, error) {
	return generate.Response{Text: m.resp}, nil
}

func TestAbstractiveCompressorReplacesContentWithSummary(t *testing.T) {
	c := AbstractiveCompressor{Model: stubModel{resp: "  short summary.  "}}
	in := []store.Hit{{
		Chunk: store.StoredChunk{ID: "a", Content: "a very long passage about many things"},
		Score: 0.7,
	}}
	out, err := c.Compress(context.Background(), "query", in)
	if err != nil {
		t.Fatalf("Compress(): %v", err)
	}
	if out[0].Chunk.Content != "short summary." {
		t.Fatalf("Content = %q, want trimmed summary", out[0].Chunk.Content)
	}
	if out[0].Chunk.ID != "a" || out[0].Score != 0.7 {
		t.Fatalf("provenance not preserved: %#v", out[0])
	}
}

func TestAbstractiveCompressorEmptySummaryKeepsOriginal(t *testing.T) {
	c := AbstractiveCompressor{Model: stubModel{resp: "   "}}
	in := []store.Hit{hit("a", "original content", 0.5)}
	out, err := c.Compress(context.Background(), "query", in)
	if err != nil {
		t.Fatalf("Compress(): %v", err)
	}
	if out[0].Chunk.Content != "original content" {
		t.Fatalf("Content = %q, want original kept on empty summary", out[0].Chunk.Content)
	}
}

func TestAbstractiveCompressorRequiresModel(t *testing.T) {
	_, err := AbstractiveCompressor{}.Compress(context.Background(), "query", nil)
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}
