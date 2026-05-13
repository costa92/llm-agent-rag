//go:build llmagent

package llmagent

import (
	"context"

	corellm "github.com/costa92/llm-agent/llm"
	"github.com/costa92/llm-agent-rag/generate"
)

type ModelAdapter struct {
	Inner corellm.ChatModel
}

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
