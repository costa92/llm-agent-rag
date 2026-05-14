package rerank

import (
	"context"
	"sort"
	"strings"

	"github.com/costa92/llm-agent-rag/store"
)

type Request struct {
	Query string
	Hits  []store.Hit
}

type Trace struct {
	InputChunkIDs  []string
	OutputChunkIDs []string
}

type Reranker interface {
	Rerank(ctx context.Context, req Request) ([]store.Hit, Trace, error)
}

type NoopReranker struct{}

func (NoopReranker) Rerank(_ context.Context, req Request) ([]store.Hit, Trace, error) {
	hits := append([]store.Hit(nil), req.Hits...)
	return hits, Trace{
		InputChunkIDs:  chunkIDs(req.Hits),
		OutputChunkIDs: chunkIDs(hits),
	}, nil
}

type HeuristicReranker struct{}

func (HeuristicReranker) Rerank(_ context.Context, req Request) ([]store.Hit, Trace, error) {
	type scoredHit struct {
		hit   store.Hit
		score float64
		order int
	}
	tokens := tokenize(req.Query)
	scored := make([]scoredHit, 0, len(req.Hits))
	for i, hit := range req.Hits {
		scored = append(scored, scoredHit{
			hit:   hit,
			score: hit.Score + lexicalBoost(tokens, hit.Chunk.Title+" "+hit.Chunk.Content),
			order: i,
		})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].order < scored[j].order
		}
		return scored[i].score > scored[j].score
	})
	out := make([]store.Hit, 0, len(scored))
	for _, item := range scored {
		hit := item.hit
		hit.Score = item.score
		out = append(out, hit)
	}
	return out, Trace{
		InputChunkIDs:  chunkIDs(req.Hits),
		OutputChunkIDs: chunkIDs(out),
	}, nil
}

func chunkIDs(hits []store.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.Chunk.ID)
	}
	return out
}

func tokenize(text string) []string {
	fields := strings.Fields(strings.ToLower(text))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, ".,;:!?()[]{}\"'")
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func lexicalBoost(queryTokens []string, text string) float64 {
	if len(queryTokens) == 0 {
		return 0
	}
	counts := make(map[string]int)
	for _, token := range tokenize(text) {
		counts[token]++
	}
	var matches int
	for _, token := range queryTokens {
		if counts[token] > 0 {
			matches++
		}
	}
	return float64(matches) * 0.05
}
