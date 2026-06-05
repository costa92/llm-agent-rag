// Package compress shrinks retrieved chunks to query-relevant content. It
// runs between rerank and pack in the answer pipeline. Compressor is the
// seam; NoopCompressor, ExtractiveCompressor, and AbstractiveCompressor are
// the built-in implementations.
package compress

import (
	"context"

	"github.com/costa92/llm-agent-rag/store"
)

// Compressor shrinks each hit's Content to the material relevant to query.
// Implementations preserve Chunk.ID, Score, and Metadata so an answer's
// citation provenance is unaffected.
type Compressor interface {
	Compress(ctx context.Context, query string, hits []store.Hit) ([]store.Hit, error)
}

// NoopCompressor returns hits unchanged. It is the default compressor, so an
// unconfigured pipeline behaves exactly as before.
type NoopCompressor struct{}

// Compress returns hits unchanged.
func (NoopCompressor) Compress(_ context.Context, _ string, hits []store.Hit) ([]store.Hit, error) {
	return hits, nil
}
