package rag

import (
	"context"

	"github.com/costa92/llm-agent-rag/prompt"
)

func (s *System) Ask(ctx context.Context, question string, opts AskOptions) (Answer, error) {
	if s.model == nil {
		return Answer{}, ErrModelRequired
	}
	hits, err := s.Retrieve(ctx, question, opts.Search)
	if err != nil {
		return Answer{}, err
	}
	tpl := opts.Template
	if tpl == nil {
		tpl = s.template
	}
	req, err := tpl.Render(ctx, prompt.RenderContext{
		Question:  question,
		Namespace: opts.Search.Namespace,
		Hits:      hits,
		Metadata:  opts.Metadata,
	})
	if err != nil {
		return Answer{}, err
	}
	resp, err := s.model.Generate(ctx, req)
	if err != nil {
		return Answer{}, err
	}
	return Answer{Text: resp.Text, Hits: hits, Prompt: req}, nil
}
