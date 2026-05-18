package rerank

import (
	"context"
	"errors"
	"testing"

	"github.com/costa92/llm-agent-rag/store"
)

func TestNoopRerankerPreservesOrder(t *testing.T) {
	hits := []store.Hit{
		{Chunk: store.StoredChunk{ID: "a"}, Score: 0.2},
		{Chunk: store.StoredChunk{ID: "b"}, Score: 0.1},
	}
	got, trace, err := NoopReranker{}.Rerank(context.Background(), Request{
		Query: "paris",
		Hits:  hits,
	})
	if err != nil {
		t.Fatalf("Rerank(): %v", err)
	}
	if got[0].Chunk.ID != "a" || got[1].Chunk.ID != "b" {
		t.Fatalf("got = %+v, want original order", got)
	}
	if len(trace.OutputChunkIDs) != 2 || trace.OutputChunkIDs[0] != "a" {
		t.Fatalf("trace = %+v, want preserved ids", trace)
	}
}

func TestHeuristicRerankerPromotesLexicalMatch(t *testing.T) {
	hits := []store.Hit{
		{Chunk: store.StoredChunk{ID: "a", Content: "general travel guide"}, Score: 0.8},
		{Chunk: store.StoredChunk{ID: "b", Content: "Paris is the capital of France"}, Score: 0.7},
	}
	got, trace, err := HeuristicReranker{}.Rerank(context.Background(), Request{
		Query: "capital of france",
		Hits:  hits,
	})
	if err != nil {
		t.Fatalf("Rerank(): %v", err)
	}
	if got[0].Chunk.ID != "b" {
		t.Fatalf("top hit = %s, want b", got[0].Chunk.ID)
	}
	if trace.OutputChunkIDs[0] != "b" {
		t.Fatalf("trace = %+v, want b promoted", trace)
	}
}

func findScore(scores []RerankScore, id string) (RerankScore, bool) {
	for _, s := range scores {
		if s.ChunkID == id {
			return s, true
		}
	}
	return RerankScore{}, false
}

func TestNoopRerankerScoresHaveZeroDeltas(t *testing.T) {
	hits := []store.Hit{
		{Chunk: store.StoredChunk{ID: "a"}, Score: 0.2},
		{Chunk: store.StoredChunk{ID: "b"}, Score: 0.1},
	}
	_, trace, err := NoopReranker{}.Rerank(context.Background(), Request{Query: "q", Hits: hits})
	if err != nil {
		t.Fatalf("Rerank(): %v", err)
	}
	if len(trace.Scores) != 2 {
		t.Fatalf("Scores len = %d, want 2", len(trace.Scores))
	}
	for _, s := range trace.Scores {
		if s.RankDelta != 0 {
			t.Fatalf("score %+v: want RankDelta 0 for pass-through rerank", s)
		}
		if s.InputScore != s.OutputScore {
			t.Fatalf("score %+v: want InputScore == OutputScore", s)
		}
		if s.InputRank != s.OutputRank {
			t.Fatalf("score %+v: want InputRank == OutputRank", s)
		}
	}
}

func TestHeuristicRerankerTraceScoresShowPromotion(t *testing.T) {
	hits := []store.Hit{
		{Chunk: store.StoredChunk{ID: "a", Content: "general travel guide"}, Score: 0.8},
		{Chunk: store.StoredChunk{ID: "b", Content: "Paris is the capital of France"}, Score: 0.7},
	}
	_, trace, err := HeuristicReranker{}.Rerank(context.Background(), Request{
		Query: "capital of france",
		Hits:  hits,
	})
	if err != nil {
		t.Fatalf("Rerank(): %v", err)
	}
	b, ok := findScore(trace.Scores, "b")
	if !ok {
		t.Fatalf("Scores = %+v, want an entry for b", trace.Scores)
	}
	if b.InputRank != 2 || b.OutputRank != 1 {
		t.Fatalf("b score = %+v, want InputRank 2, OutputRank 1", b)
	}
	if b.RankDelta != 1 {
		t.Fatalf("b RankDelta = %d, want 1 (promoted one place)", b.RankDelta)
	}
	if b.OutputScore <= b.InputScore {
		t.Fatalf("b score = %+v, want OutputScore > InputScore after lexical boost", b)
	}
}

// reverseScorer scores documents so input order is reversed: the last
// document gets the highest score.
type reverseScorer struct{}

func (reverseScorer) Score(_ context.Context, _ string, documents []string) ([]float64, error) {
	scores := make([]float64, len(documents))
	for i := range documents {
		scores[i] = float64(i)
	}
	return scores, nil
}

func TestModelRerankerReordersByModelScore(t *testing.T) {
	hits := []store.Hit{
		{Chunk: store.StoredChunk{ID: "a", Content: "first"}, Score: 0.9},
		{Chunk: store.StoredChunk{ID: "b", Content: "second"}, Score: 0.8},
		{Chunk: store.StoredChunk{ID: "c", Content: "third"}, Score: 0.7},
	}
	out, trace, err := ModelReranker{Model: reverseScorer{}}.Rerank(context.Background(), Request{
		Query: "q", Hits: hits,
	})
	if err != nil {
		t.Fatalf("Rerank(): %v", err)
	}
	if len(out) != 3 || out[0].Chunk.ID != "c" || out[2].Chunk.ID != "a" {
		t.Fatalf("out = %+v, want reversed order c, b, a", out)
	}
	c, ok := findScore(trace.Scores, "c")
	if !ok || c.InputRank != 3 || c.OutputRank != 1 || c.RankDelta != 2 {
		t.Fatalf("c score = %+v (found=%v), want InputRank 3, OutputRank 1, RankDelta 2", c, ok)
	}
}

func TestModelRerankerTopNTruncates(t *testing.T) {
	hits := []store.Hit{
		{Chunk: store.StoredChunk{ID: "a"}},
		{Chunk: store.StoredChunk{ID: "b"}},
		{Chunk: store.StoredChunk{ID: "c"}},
	}
	out, _, err := ModelReranker{Model: reverseScorer{}, TopN: 2}.Rerank(context.Background(), Request{
		Query: "q", Hits: hits,
	})
	if err != nil {
		t.Fatalf("Rerank(): %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("out len = %d, want 2 (TopN truncation)", len(out))
	}
}

func TestModelRerankerRequiresModel(t *testing.T) {
	_, _, err := ModelReranker{}.Rerank(context.Background(), Request{Query: "q"})
	if !errors.Is(err, ErrScoringModelRequired) {
		t.Fatalf("err = %v, want ErrScoringModelRequired", err)
	}
}
