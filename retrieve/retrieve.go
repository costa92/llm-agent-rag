package retrieve

import (
	"context"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/store"
)

type Request struct {
	Query           string
	Namespace       string
	TopK            int
	Filters         map[string]any
	SecurityFilters map[string]any
}

type PreprocessResult struct {
	QueryVariants []string
	Trace         Trace
}

type Trace struct {
	OriginalQuery string
	EffectiveQuery string
	QueryVariants []string
}

type QueryPreprocessor interface {
	Process(ctx context.Context, req Request) (PreprocessResult, error)
}

type NoopPreprocessor struct{}

func (NoopPreprocessor) Process(_ context.Context, req Request) (PreprocessResult, error) {
	return PreprocessResult{
		QueryVariants: []string{req.Query},
		Trace: Trace{
			OriginalQuery: req.Query,
			EffectiveQuery: req.Query,
			QueryVariants: []string{req.Query},
		},
	}, nil
}

type QueryEmbedder interface {
	Embed(ctx context.Context, text string) (embed.Vector, error)
}

type Retriever interface {
	Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error)
}

type DenseRetriever struct {
	Embedder QueryEmbedder
	Store    store.Store
}

func (r DenseRetriever) Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error) {
	vec, err := r.Embedder.Embed(ctx, req.Query)
	if err != nil {
		return nil, Trace{}, err
	}
	hits, err := r.Store.Search(ctx, store.Query{
		Namespace:       req.Namespace,
		Vector:          vec,
		TopK:            req.TopK,
		Filters:         store.Filter(req.Filters),
		SecurityFilters: store.Filter(req.SecurityFilters),
	})
	if err != nil {
		return nil, Trace{}, err
	}
	return hits, Trace{
		OriginalQuery: req.Query,
		EffectiveQuery: req.Query,
		QueryVariants: []string{req.Query},
	}, nil
}
