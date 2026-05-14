package retrieve

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/costa92/llm-agent-rag/advanced"
	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/store"
)

type Request struct {
	Query           string
	Namespace       string
	TopK            int
	Filters         map[string]any
	SecurityFilters map[string]any
	EnableMQE       bool
	EnableHyDE      bool
	MQECount        int
	QueryVariants   []string
}

type PreprocessResult struct {
	QueryVariants []string
	Trace         Trace
}

type Trace struct {
	OriginalQuery  string
	EffectiveQuery string
	QueryVariants  []string
}

type QueryPreprocessor interface {
	Process(ctx context.Context, req Request) (PreprocessResult, error)
}

type NoopPreprocessor struct{}

func (NoopPreprocessor) Process(_ context.Context, req Request) (PreprocessResult, error) {
	return PreprocessResult{
		QueryVariants: []string{req.Query},
		Trace: Trace{
			OriginalQuery:  req.Query,
			EffectiveQuery: req.Query,
			QueryVariants:  []string{req.Query},
		},
	}, nil
}

type LLMExpansionPreprocessor struct {
	Model generate.Model
}

func (p LLMExpansionPreprocessor) Process(ctx context.Context, req Request) (PreprocessResult, error) {
	variants := uniqueQueries(req.Query)
	if req.EnableMQE {
		if p.Model == nil {
			return PreprocessResult{}, advanced.ErrModelRequired
		}
		count := req.MQECount
		if count <= 0 {
			count = 3
		}
		expansions, err := advanced.ExpandQuery(ctx, p.Model, req.Query, count)
		if err != nil {
			return PreprocessResult{}, err
		}
		variants = appendUniqueQueries(variants, expansions...)
	}
	if req.EnableHyDE {
		if p.Model == nil {
			return PreprocessResult{}, advanced.ErrModelRequired
		}
		hypo, err := advanced.GenerateHypothetical(ctx, p.Model, req.Query)
		if err != nil {
			return PreprocessResult{}, err
		}
		variants = appendUniqueQueries(variants, hypo)
	}
	if len(variants) == 0 {
		variants = []string{req.Query}
	}
	return PreprocessResult{
		QueryVariants: variants,
		Trace: Trace{
			OriginalQuery:  req.Query,
			EffectiveQuery: variants[0],
			QueryVariants:  append([]string(nil), variants...),
		},
	}, nil
}

type QueryEmbedder interface {
	Embed(ctx context.Context, text string) (embed.Vector, error)
}

type Retriever interface {
	Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error)
}

var ErrBaseRetrieverRequired = errors.New("retrieve: base retriever required")

type VariantRetriever struct {
	Base Retriever
}

func (r VariantRetriever) Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error) {
	if r.Base == nil {
		return nil, Trace{}, ErrBaseRetrieverRequired
	}
	variants := req.QueryVariants
	if len(variants) == 0 {
		variants = []string{req.Query}
	}
	type rankedHit struct {
		hit   store.Hit
		order int
	}
	merged := make(map[string]rankedHit)
	nextOrder := 0
	for _, query := range variants {
		subReq := req
		subReq.Query = query
		subReq.QueryVariants = nil
		hits, _, err := r.Base.Retrieve(ctx, subReq)
		if err != nil {
			return nil, Trace{}, err
		}
		for _, hit := range hits {
			prev, ok := merged[hit.Chunk.ID]
			if !ok {
				merged[hit.Chunk.ID] = rankedHit{hit: hit, order: nextOrder}
				nextOrder++
				continue
			}
			if hit.Score > prev.hit.Score {
				prev.hit = hit
				merged[hit.Chunk.ID] = prev
			}
		}
	}
	out := make([]rankedHit, 0, len(merged))
	for _, hit := range merged {
		out = append(out, hit)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].hit.Score == out[j].hit.Score {
			return out[i].order < out[j].order
		}
		return out[i].hit.Score > out[j].hit.Score
	})
	limit := len(out)
	if req.TopK > 0 && limit > req.TopK {
		limit = req.TopK
	}
	hits := make([]store.Hit, 0, limit)
	for _, hit := range out[:limit] {
		hits = append(hits, hit.hit)
	}
	return hits, Trace{
		OriginalQuery:  req.Query,
		EffectiveQuery: variants[0],
		QueryVariants:  append([]string(nil), variants...),
	}, nil
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
		OriginalQuery:  req.Query,
		EffectiveQuery: req.Query,
		QueryVariants:  []string{req.Query},
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
		OriginalQuery:  req.Query,
		EffectiveQuery: req.Query,
		QueryVariants:  []string{req.Query},
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
		OriginalQuery:  req.Query,
		EffectiveQuery: denseTrace.EffectiveQuery,
		QueryVariants:  append([]string(nil), denseTrace.QueryVariants...),
	}, nil
}

func uniqueQueries(query string) []string {
	return appendUniqueQueries(nil, query)
}

func appendUniqueQueries(dst []string, queries ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(queries))
	out := make([]string, 0, len(dst)+len(queries))
	for _, query := range dst {
		trimmed := strings.TrimSpace(query)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	for _, query := range queries {
		trimmed := strings.TrimSpace(query)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
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
