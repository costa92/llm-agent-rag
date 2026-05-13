package rag

import (
	"context"
	"strings"

	"github.com/costa92/llm-agent-rag/store"
)

func (s *System) Retrieve(ctx context.Context, query string, opts SearchOptions) ([]store.Hit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, ErrEmptyQuery
	}
	vec, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	return s.store.Search(ctx, store.Query{
		Namespace: opts.Namespace,
		Vector:    vec,
		TopK:      opts.TopK,
		Filters:   opts.Filters,
	})
}
