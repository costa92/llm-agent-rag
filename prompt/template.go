package prompt

import (
	"context"

	"github.com/costa92/llm-agent-rag/generate"
)

type Template interface {
	Render(ctx context.Context, rc RenderContext) (generate.Request, error)
}
