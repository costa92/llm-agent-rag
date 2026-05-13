package prompt

import (
	"context"
	"fmt"
	"strings"

	"github.com/costa92/llm-agent-rag/generate"
)

type DefaultQATemplate struct {
	SystemPrompt string
	Instructions string
}

func (t DefaultQATemplate) Render(_ context.Context, rc RenderContext) (generate.Request, error) {
	system := t.SystemPrompt
	if system == "" {
		system = "You answer questions using retrieved context."
	}
	instructions := t.Instructions
	if instructions == "" {
		instructions = "Use the context below to answer the question. Cite chunk IDs in [brackets] when relevant."
	}
	var b strings.Builder
	b.WriteString(instructions)
	b.WriteString("\n\nContext:\n")
	for _, hit := range rc.Hits {
		fmt.Fprintf(&b, "[%s] %s\n\n", hit.Chunk.ID, hit.Chunk.Content)
	}
	fmt.Fprintf(&b, "Question: %s", rc.Question)
	return generate.Request{
		SystemPrompt: system,
		Messages: []generate.Message{{
			Role:    "user",
			Content: b.String(),
		}},
		Metadata: rc.Metadata,
	}, nil
}
