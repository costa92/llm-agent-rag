// Package embed defines the embedding-backend seam for the RAG pipeline.
// Embedder is the central interface — it turns text into a Vector — and
// every retriever and store that needs vectors depends on it. HashEmbedder
// is the deterministic default implementation used for tests and offline
// runs; CosineSimilarity is the vector-comparison helper.
package embed

import "context"

// Embedder turns text into a Vector. It is the embedding-backend seam: every
// retriever and store that needs vectors depends on it, and a caller plugs in
// a real embedding model by implementing it.
type Embedder interface {
	// Embed returns the embedding vector for text.
	Embed(ctx context.Context, text string) (Vector, error)
	// Dimension reports the fixed length of vectors this Embedder produces.
	Dimension() int
}

// BatchEmbedder is an optional sibling capability for embedders that can
// embed many texts in a single round-trip — useful for bulk-import
// workflows where N sequential single-text Embed calls would be
// wasteful. The returned slice has the same length as texts and matches
// it positionally: out[i] is the embedding of texts[i].
//
// Added in v1.0.2 (P1-16). Plain Embedder callers see no behavior
// change — the rag.Importer type-asserts to BatchEmbedder and falls
// back to per-chunk Embed when the assertion fails, so v1 callers that
// only implement Embedder continue to work unchanged.
//
// Callers can type-assert to gain the batch fast path:
//
//	if be, ok := e.(BatchEmbedder); ok {
//	    vecs, err := be.EmbedBatch(ctx, texts)
//	    // ...
//	}
type BatchEmbedder interface {
	// EmbedBatch returns embeddings for texts in input order: out[i]
	// is the embedding of texts[i]. Implementations must preserve
	// context cancellation — returning ctx.Err() when ctx is done.
	EmbedBatch(ctx context.Context, texts []string) ([]Vector, error)
}
