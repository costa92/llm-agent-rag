// Package generate defines the text-generation seam for the RAG pipeline.
// Model is the central interface — it takes a Request and returns a
// Response — and is the plug-point a caller fills with an LLM provider.
// Message, Request, Response, and Usage are the supporting value types.
package generate

import "context"

// Model generates text from a Request. It is the core generation seam: a
// caller plugs in an LLM provider by implementing it.
type Model interface {
	// Generate produces a Response for req.
	Generate(ctx context.Context, req Request) (Response, error)
}
