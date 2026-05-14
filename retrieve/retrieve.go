package retrieve

import (
	"context"
	"sort"
	"strings"

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

type LexicalRetriever struct {
	Store store.Store
}

func (r LexicalRetriever) Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error) {
	chunks, err := r.Store.List(ctx, req.Namespace, store.Filter(req.Filters), store.Filter(req.SecurityFilters))
	if err != nil {
		return nil, Trace{}, err
	}
	tokens := tokenize(req.Query)
	hits := make([]store.Hit, 0, len(chunks))
	for _, chunk := range chunks {
		score := lexicalScore(tokens, chunk.Content)
		if score <= 0 {
			continue
		}
		hits = append(hits, store.Hit{
			Chunk: chunk,
			Score: score,
		})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if req.TopK > 0 && len(hits) > req.TopK {
		hits = hits[:req.TopK]
	}
	return hits, Trace{
		OriginalQuery: req.Query,
		EffectiveQuery: req.Query,
		QueryVariants: []string{req.Query},
	}, nil
}

type HybridRetriever struct {
	Dense   Retriever
	Lexical Retriever
}

func (r HybridRetriever) Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error) {
	denseHits, denseTrace, err := r.Dense.Retrieve(ctx, req)
	if err != nil {
		return nil, Trace{}, err
	}
	lexHits, _, err := r.Lexical.Retrieve(ctx, req)
	if err != nil {
		return nil, Trace{}, err
	}

	fused := make(map[string]store.Hit, len(denseHits)+len(lexHits))
	rrfScores := make(map[string]float64, len(denseHits)+len(lexHits))

	apply := func(hits []store.Hit) {
		for i, hit := range hits {
			if _, ok := fused[hit.Chunk.ID]; !ok {
				fused[hit.Chunk.ID] = hit
			}
			rrfScores[hit.Chunk.ID] += 1.0 / float64(i+1+60)
		}
	}
	apply(denseHits)
	apply(lexHits)

	out := make([]store.Hit, 0, len(fused))
	for id, hit := range fused {
		hit.Score = rrfScores[id]
		out = append(out, hit)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if req.TopK > 0 && len(out) > req.TopK {
		out = out[:req.TopK]
	}
	return out, Trace{
		OriginalQuery: req.Query,
		EffectiveQuery: denseTrace.EffectiveQuery,
		QueryVariants: []string{req.Query},
	}, nil
}

func tokenize(text string) []string {
	fields := strings.Fields(strings.ToLower(text))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, ".,;:!?()[]{}\"'")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func lexicalScore(tokens []string, content string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	contentTokens := tokenize(content)
	if len(contentTokens) == 0 {
		return 0
	}
	counts := make(map[string]int, len(contentTokens))
	for _, tok := range contentTokens {
		counts[tok]++
	}
	var score float64
	for _, tok := range tokens {
		if counts[tok] > 0 {
			score += 1
		}
	}
	return score
}
