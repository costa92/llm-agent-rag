package prompt

import "github.com/costa92/llm-agent-rag/store"

// RenderContext is the input a Template renders into a generation request.
type RenderContext struct {
	Question  string         // Question is the user query being answered.
	Namespace string         // Namespace is the namespace the retrieval ran against.
	Hits      []store.Hit    // Hits are the retrieved context chunks.
	Metadata  map[string]any // Metadata carries caller-supplied passthrough values.
}
