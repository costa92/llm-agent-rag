// Package rerank re-scores retrieved candidates before they are packed
// into the answer context. Reranker is the central seam — HeuristicReranker,
// ModelReranker, and NoopReranker are the built-ins — and ScoringModel is
// the model-scoring seam (HTTPScoringModel calls an external rerank API).
package rerank

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/costa92/llm-agent-rag/store"
)

// Request is a rerank request: a query and the hits to re-score.
type Request struct {
	Query string      // Query is the question the hits are scored against.
	Hits  []store.Hit // Hits are the retrieved candidates to rerank.
}

// Trace records a rerank pass: the input and output ordering and per-chunk
// scores.
type Trace struct {
	InputChunkIDs  []string      // InputChunkIDs are the chunk IDs in pre-rerank order.
	OutputChunkIDs []string      // OutputChunkIDs are the chunk IDs in post-rerank order.
	Scores         []RerankScore // Scores is the per-chunk before/after explainability.
}

// RerankScore records one chunk's score and rank before and after a rerank
// pass. RankDelta is InputRank - OutputRank: positive means the chunk was
// promoted. An InputRank of 0 means the chunk was not in the rerank input.
type RerankScore struct {
	ChunkID     string  // ChunkID identifies the chunk this score describes.
	InputScore  float64 // InputScore is the chunk's score before reranking.
	OutputScore float64 // OutputScore is the chunk's score after reranking.
	InputRank   int     // InputRank is the chunk's 1-based rank before reranking; 0 if absent from the input.
	OutputRank  int     // OutputRank is the chunk's 1-based rank after reranking.
	RankDelta   int     // RankDelta is InputRank - OutputRank; positive means promoted.
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

// Reranker re-scores retrieved candidates before they are packed. It is the
// reranking seam: the built-ins are HeuristicReranker, ModelReranker, and
// NoopReranker.
type Reranker interface {
	// Rerank re-orders req.Hits and returns the new order with a Trace.
	Rerank(ctx context.Context, req Request) ([]store.Hit, Trace, error)
}

// NoopReranker is a Reranker that returns its input unchanged.
type NoopReranker struct{}

// Rerank returns req.Hits unchanged.
func (NoopReranker) Rerank(_ context.Context, req Request) ([]store.Hit, Trace, error) {
	hits := append([]store.Hit(nil), req.Hits...)
	return hits, Trace{
		InputChunkIDs:  chunkIDs(req.Hits),
		OutputChunkIDs: chunkIDs(hits),
		Scores:         buildScores(req.Hits, hits),
	}, nil
}

// HeuristicReranker is the network-free default Reranker. It re-scores hits
// by combining the retrieval score with a lexical query-overlap boost.
type HeuristicReranker struct{}

// Rerank re-orders req.Hits by retrieval score plus a lexical-overlap boost.
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
	// Score returns one relevance score per document, parallel to documents.
	Score(ctx context.Context, query string, documents []string) ([]float64, error)
}

// ModelReranker reranks hits by a ScoringModel's relevance scores. It is a
// drop-in rerank.Reranker. The rag.System default stays the network-free
// HeuristicReranker, so ModelReranker is opt-in via rag.Options.Reranker.
type ModelReranker struct {
	Model ScoringModel // Model scores query/document relevance.
	// TopN, if > 0, truncates the reranked output to TopN hits.
	TopN int
}

// Rerank re-orders req.Hits by the ScoringModel's relevance scores.
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
