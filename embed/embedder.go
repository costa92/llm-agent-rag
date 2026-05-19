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
