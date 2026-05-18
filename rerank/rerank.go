package rerank

import (
	"context"
	"errors"
	"fmt"
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
	Scores         []RerankScore
}

// RerankScore records one chunk's score and rank before and after a rerank
// pass. RankDelta is InputRank - OutputRank: positive means the chunk was
// promoted. An InputRank of 0 means the chunk was not in the rerank input.
type RerankScore struct {
	ChunkID     string
	InputScore  float64
	OutputScore float64
	InputRank   int
	OutputRank  int
	RankDelta   int
}

// buildScores pairs each output hit with its pre-rerank score and rank,
// producing per-hit explainability for a rerank pass.
func buildScores(input, output []store.Hit) []RerankScore {
	type inMeta struct {
		score float64
		rank  int
	}
	in := make(map[string]inMeta, len(input))
	for i, hit := range input {
		if _, ok := in[hit.Chunk.ID]; ok {
			continue
		}
		in[hit.Chunk.ID] = inMeta{score: hit.Score, rank: i + 1}
	}
	scores := make([]RerankScore, 0, len(output))
	for i, hit := range output {
		meta := in[hit.Chunk.ID]
		rs := RerankScore{
			ChunkID:     hit.Chunk.ID,
			InputScore:  meta.score,
			OutputScore: hit.Score,
			InputRank:   meta.rank,
			OutputRank:  i + 1,
		}
		if meta.rank != 0 {
			rs.RankDelta = meta.rank - rs.OutputRank
		}
		scores = append(scores, rs)
	}
	return scores
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
		Scores:         buildScores(req.Hits, hits),
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
			score: hit.Score + lexicalBoost(tokens, hitDocument(hit)),
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
		Scores:         buildScores(req.Hits, out),
	}, nil
}

// ErrScoringModelRequired is returned by ModelReranker when no ScoringModel
// is configured.
var ErrScoringModelRequired = errors.New("rerank: scoring model required")

// ScoringModel scores how well each document answers the query. The returned
// slice is parallel to documents — one score per document. It is the
// abstract seam ModelReranker depends on; concrete implementations may call
// a cross-encoder or a hosted rerank API.
type ScoringModel interface {
	Score(ctx context.Context, query string, documents []string) ([]float64, error)
}

// ModelReranker reranks hits by a ScoringModel's relevance scores. It is a
// drop-in rerank.Reranker. The rag.System default stays the network-free
// HeuristicReranker, so ModelReranker is opt-in via rag.Options.Reranker.
type ModelReranker struct {
	Model ScoringModel
	// TopN, if > 0, truncates the reranked output to TopN hits.
	TopN int
}

func (r ModelReranker) Rerank(ctx context.Context, req Request) ([]store.Hit, Trace, error) {
	if r.Model == nil {
		return nil, Trace{}, ErrScoringModelRequired
	}
	docs := make([]string, len(req.Hits))
	for i, hit := range req.Hits {
		docs[i] = hitDocument(hit)
	}
	scores, err := r.Model.Score(ctx, req.Query, docs)
	if err != nil {
		return nil, Trace{}, err
	}
	if len(scores) != len(req.Hits) {
		return nil, Trace{}, fmt.Errorf("rerank: scoring model returned %d scores for %d hits", len(scores), len(req.Hits))
	}
	type scoredHit struct {
		hit   store.Hit
		score float64
		order int
	}
	scored := make([]scoredHit, len(req.Hits))
	for i, hit := range req.Hits {
		scored[i] = scoredHit{hit: hit, score: scores[i], order: i}
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
	if r.TopN > 0 && len(out) > r.TopN {
		out = out[:r.TopN]
	}
	return out, Trace{
		InputChunkIDs:  chunkIDs(req.Hits),
		OutputChunkIDs: chunkIDs(out),
		Scores:         buildScores(req.Hits, out),
	}, nil
}

// hitDocument builds the document text shown to a reranker — the structured
// composition of a chunk's title, content, heading, and section path.
func hitDocument(hit store.Hit) string {
	return hit.Chunk.Title + " " + hit.Chunk.Content + " " + hit.Chunk.Heading + " " + strings.Join(hit.Chunk.SectionPath, " ")
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
