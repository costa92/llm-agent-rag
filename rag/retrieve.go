package rag

import (
	"context"
	"strings"
	"time"

	"github.com/costa92/llm-agent-rag/obs"
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
	// Resolve the obs.Counter: reuse the one Ask put on the context when
	// retrieve runs nested, otherwise create a fresh one so a standalone
	// Retrieve still records its own call counts. The before/after diff
	// gives retrieve's own counts either way.
	counter := obs.CounterFrom(ctx)
	if counter == nil {
		counter = obs.NewCounter()
		ctx = obs.WithCounter(ctx, counter)
	}
	before := counter.Counts()
	retrieveStart := time.Now()
	metrics := obs.Metrics{}
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
		EnableGraph:                  opts.EnableGraph,
		EnableTreeExpansion:          opts.EnableTreeExpansion,
		ExpansionDepth:               opts.ExpansionDepth,
	}
	preStart := time.Now()
	processed, err := s.pre.Process(ctx, req)
	preDuration := time.Since(preStart)
	if err != nil {
		return nil, retrievepolicy.Trace{}, err
	}
	effective := req
	effective.QueryVariants = append([]string(nil), processed.QueryVariants...)
	if len(effective.QueryVariants) > 0 {
		effective.Query = effective.QueryVariants[0]
	}
	retStart := time.Now()
	hits, trace, err := s.ret.Retrieve(ctx, effective)
	retDuration := time.Since(retStart)
	if err != nil {
		return nil, retrievepolicy.Trace{}, err
	}
	after := counter.Counts()
	metrics.Stages = []obs.StageTiming{
		{Stage: "preprocess", Duration: preDuration},
		{Stage: "retrieve", Duration: retDuration},
	}
	metrics.Calls = obs.CallCounts{
		Embed:    after.Embed - before.Embed,
		Generate: after.Generate - before.Generate,
	}
	metrics.TotalDuration = time.Since(retrieveStart)
	trace.Metrics = metrics
	if s.observer.OnRetrieve != nil {
		s.observer.OnRetrieve(ctx, trace)
	}
	return hits, trace, nil
}
