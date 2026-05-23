package rag

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/ingest"
)

// importPerTextEmbedder is an embed.Embedder that records each per-text
// Embed call. It does NOT implement embed.BatchEmbedder, so Import must
// fall back to the per-chunk loop.
type importPerTextEmbedder struct {
	dim        int
	embedCalls int64
	embedTexts []string
}

func (c *importPerTextEmbedder) Embed(_ context.Context, text string) (embed.Vector, error) {
	atomic.AddInt64(&c.embedCalls, 1)
	c.embedTexts = append(c.embedTexts, text)
	v := make(embed.Vector, c.dim)
	if c.dim > 0 {
		v[0] = float32(len(text))
	}
	return v, nil
}

func (c *importPerTextEmbedder) Dimension() int { return c.dim }

// importBatchEmbedder is an embed.Embedder that also implements
// embed.BatchEmbedder. Import must use the batch fast path: one
// EmbedBatch call per Import for N chunks, zero per-text Embed calls.
type importBatchEmbedder struct {
	dim             int
	embedCalls      int64
	embedBatchCalls int64
	batchedTexts    [][]string
	batchErr        error
}

func (c *importBatchEmbedder) Embed(_ context.Context, text string) (embed.Vector, error) {
	atomic.AddInt64(&c.embedCalls, 1)
	v := make(embed.Vector, c.dim)
	if c.dim > 0 {
		v[0] = float32(len(text))
	}
	return v, nil
}

func (c *importBatchEmbedder) Dimension() int { return c.dim }

func (c *importBatchEmbedder) EmbedBatch(ctx context.Context, texts []string) ([]embed.Vector, error) {
	atomic.AddInt64(&c.embedBatchCalls, 1)
	c.batchedTexts = append(c.batchedTexts, append([]string(nil), texts...))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.batchErr != nil {
		return nil, c.batchErr
	}
	out := make([]embed.Vector, len(texts))
	for i, t := range texts {
		v := make(embed.Vector, c.dim)
		if c.dim > 0 {
			v[0] = float32(len(t))
		}
		out[i] = v
	}
	return out, nil
}

// TestImportUsesBatchEmbedderWhenAvailable asserts that when the
// configured embedder satisfies embed.BatchEmbedder, Import calls
// EmbedBatch exactly once with all chunk texts, and makes zero per-text
// Embed calls. This is the v1.0.2 batch fast path.
func TestImportUsesBatchEmbedderWhenAvailable(t *testing.T) {
	be := &importBatchEmbedder{dim: 8}
	sys := New(Options{Embedder: be})
	res, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is in France."},
		{ID: "doc2", Content: "Berlin is in Germany."},
		{ID: "doc3", Content: "Tokyo is in Japan."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Chunks != 3 {
		t.Fatalf("res.Chunks = %d, want 3", res.Chunks)
	}
	if got := atomic.LoadInt64(&be.embedBatchCalls); got != 1 {
		t.Fatalf("EmbedBatch calls = %d, want exactly 1", got)
	}
	if got := atomic.LoadInt64(&be.embedCalls); got != 0 {
		t.Fatalf("Embed calls = %d, want 0 (batch path must not fall through)", got)
	}
	if len(be.batchedTexts) != 1 || len(be.batchedTexts[0]) != 3 {
		t.Fatalf("batched texts = %v, want one batch of 3 texts", be.batchedTexts)
	}
}

// TestImportFallsBackToSingleEmbedWhenBatchUnavailable is the v1
// safety pin: an embedder that only implements Embedder (no
// EmbedBatch) must still work, with exactly one Embed call per chunk.
// This guards against regressing the existing v1 contract.
func TestImportFallsBackToSingleEmbedWhenBatchUnavailable(t *testing.T) {
	e := &importPerTextEmbedder{dim: 8}
	sys := New(Options{Embedder: e})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is in France."},
		{ID: "doc2", Content: "Berlin is in Germany."},
		{ID: "doc3", Content: "Tokyo is in Japan."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got := atomic.LoadInt64(&e.embedCalls); got != 3 {
		t.Fatalf("Embed calls = %d, want 3 (one per chunk, fallback path)", got)
	}
}

// TestImportBatchEmbedderErrorAborts asserts that a batch-embedder
// error propagates out of Import wrapped with the "rag:" / "embed"
// shape, matching the per-chunk error wrapping convention so existing
// error handling does not silently regress.
func TestImportBatchEmbedderErrorAborts(t *testing.T) {
	wantErr := errors.New("upstream embed failure")
	be := &importBatchEmbedder{dim: 8, batchErr: wantErr}
	sys := New(Options{Embedder: be})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "hello"},
	}, ingest.ImportOptions{Namespace: "kb"})
	if err == nil {
		t.Fatal("Import err = nil, want batch embed error")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("errors.Is(err, wantErr) = false, err = %v", err)
	}
	if !strings.Contains(err.Error(), "rag:") || !strings.Contains(err.Error(), "embed") {
		t.Fatalf("err = %q, want rag: ... embed wrap shape matching per-chunk error", err.Error())
	}
}
