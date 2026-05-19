// Package prompt defines the prompt-template seam for answer generation.
// Template is the central interface — it renders a RenderContext into a
// generate.Request — and DefaultQATemplate is the built-in QA template a
// caller may replace with a custom one.
package prompt

import (
	"context"

	"github.com/costa92/llm-agent-rag/generate"
)

// Template renders a RenderContext into a generate.Request. It is the
// prompt-template seam: a caller swaps in a custom prompt by implementing it.
type Template interface {
	// Render builds the generation request for the given context.
	Render(ctx context.Context, rc RenderContext) (generate.Request, error)
}
