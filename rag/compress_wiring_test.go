package rag

import (
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/compress"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/store"
)

// truncatingCompressor shortens every chunk's content to its first word so
// the test can observe that compression ran and was recorded.
type truncatingCompressor struct{}

func (truncatingCompressor) Compress(_ context.Context, _ string, hits []store.Hit) ([]store.Hit, error) {
	out := make([]store.Hit, len(hits))
	for i, h := range hits {
		if fields := strings.Fields(h.Chunk.Content); len(fields) > 0 {
			h.Chunk.Content = fields[0]
		}
		out[i] = h
	}
	return out, nil
}

func TestEffectiveCompressorDefaultsToNoop(t *testing.T) {
	sys := New(Options{})
	if _, ok := sys.effectiveCompressor().(compress.NoopCompressor); !ok {
		t.Fatalf("effectiveCompressor() = %T, want compress.NoopCompressor", sys.effectiveCompressor())
	}
	withC := New(Options{Compressor: truncatingCompressor{}})
	if _, ok := withC.effectiveCompressor().(truncatingCompressor); !ok {
		t.Fatalf("effectiveCompressor() = %T, want truncatingCompressor", withC.effectiveCompressor())
	}
}

func TestAskRecordsCompressedChunkIDsWhenEnabled(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Compressor: truncatingCompressor{}})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of France", AskOptions{
		Search: SearchOptions{Namespace: "geo", EnableCompression: true},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Diagnostics.CompressedChunkIDs) == 0 {
		t.Fatalf("CompressedChunkIDs empty, want the compressed chunk recorded")
	}
}

func TestAskNoCompressionWhenDisabled(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Compressor: truncatingCompressor{}})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of France", AskOptions{
		Search: SearchOptions{Namespace: "geo"}, // EnableCompression defaults false
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Diagnostics.CompressedChunkIDs) != 0 {
		t.Fatalf("CompressedChunkIDs = %v, want none when disabled", ans.Diagnostics.CompressedChunkIDs)
	}
}
