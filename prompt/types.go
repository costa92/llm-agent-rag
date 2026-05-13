package prompt

import "github.com/costa92/llm-agent-rag/store"

type RenderContext struct {
	Question  string
	Namespace string
	Hits      []store.Hit
	Metadata  map[string]any
}
