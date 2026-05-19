//go:build llmagent

// Package llmagent is the build-tagged integration adapter between this SDK
// and the core github.com/costa92/llm-agent framework. Built only under the
// `llmagent` tag, it provides ModelAdapter — which adapts a core
// corellm.ChatModel to the generate.Model seam — and AsTool, which exposes a
// rag.System as an agents.Tool for the core agent framework.
package llmagent

import (
	"context"

	"github.com/costa92/llm-agent-rag/generate"
	corellm "github.com/costa92/llm-agent/llm"
)

// ModelAdapter adapts a core llm-agent corellm.ChatModel to the
// generate.Model seam, so a core chat model can drive this SDK's pipeline.
type ModelAdapter struct {
	Inner corellm.ChatModel // Inner is the wrapped core chat model.
}

// Generate satisfies generate.Model by delegating to the wrapped
// corellm.ChatModel.
func (a ModelAdapter) Generate(ctx context.Context, req generate.Request) (generate.Response, error) {
	msgs := make([]corellm.Message, 0, len(req.Messages))
	for _, msg := range req.Messages {
		msgs = append(msgs, corellm.Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	resp, err := a.Inner.Generate(ctx, corellm.Request{
		SystemPrompt: req.SystemPrompt,
		Messages:     msgs,
		Metadata:     req.Metadata,
	})
	if err != nil {
		return generate.Response{}, err
	}
	return generate.Response{Text: resp.Text}, nil
}
