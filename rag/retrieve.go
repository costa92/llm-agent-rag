package rag

import (
	"context"
	"strings"

	retrievepolicy "github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

func (s *System) Retrieve(ctx context.Context, query string, opts SearchOptions) ([]store.Hit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, ErrEmptyQuery
	}
	req := retrievepolicy.Request{
		Query:           query,
		Namespace:       opts.Namespace,
		TopK:            opts.TopK,
		Filters:         opts.Filters,
		SecurityFilters: opts.SecurityFilters,
	}
	processed, err := s.pre.Process(ctx, req)
	if err != nil {
		return nil, err
	}
	effective := req
	if len(processed.QueryVariants) > 0 {
		effective.Query = processed.QueryVariants[0]
	}
	hits, _, err := s.ret.Retrieve(ctx, effective)
	if err != nil {
		return nil, err
	}
	return hits, nil
}
