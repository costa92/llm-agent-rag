package rag

import (
	"context"
	"strings"

	retrievepolicy "github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

func (s *System) Retrieve(ctx context.Context, query string, opts SearchOptions) ([]store.Hit, error) {
	hits, _, err := s.retrieve(ctx, query, opts)
	return hits, err
}

func (s *System) retrieve(ctx context.Context, query string, opts SearchOptions) ([]store.Hit, retrievepolicy.Trace, error) {
	if strings.TrimSpace(query) == "" {
		return nil, retrievepolicy.Trace{}, ErrEmptyQuery
	}
	req := retrievepolicy.Request{
		Query:                        query,
		Namespace:                    opts.Namespace,
		TopK:                         opts.TopK,
		Filters:                      opts.Filters,
		SecurityFilters:              opts.SecurityFilters,
		RoutePath:                    append([]string(nil), opts.RoutePath...),
		EnableAutoRoute:              opts.EnableAutoRoute,
		AutoRouteMinScore:            opts.AutoRouteMinScore,
		AutoRouteMaxCandidates:       opts.AutoRouteMaxCandidates,
		AutoRouteConfidenceThreshold: opts.AutoRouteConfidenceThreshold,
		AutoRouteFanout:              opts.AutoRouteFanout,
		AutoRouteConfidenceGap:       opts.AutoRouteConfidenceGap,
		EnableMQE:                    opts.EnableMQE,
		EnableHyDE:                   opts.EnableHyDE,
		MQECount:                     opts.MQECount,
		EnableStructure:              opts.EnableStructure,
		EnableTreeExpansion:          opts.EnableTreeExpansion,
		ExpansionDepth:               opts.ExpansionDepth,
	}
	processed, err := s.pre.Process(ctx, req)
	if err != nil {
		return nil, retrievepolicy.Trace{}, err
	}
	effective := req
	effective.QueryVariants = append([]string(nil), processed.QueryVariants...)
	if len(effective.QueryVariants) > 0 {
		effective.Query = effective.QueryVariants[0]
	}
	hits, trace, err := s.ret.Retrieve(ctx, effective)
	if err != nil {
		return nil, retrievepolicy.Trace{}, err
	}
	if s.observer.OnRetrieve != nil {
		s.observer.OnRetrieve(ctx, trace)
	}
	return hits, trace, nil
}
