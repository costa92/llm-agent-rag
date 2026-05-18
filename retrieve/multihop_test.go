package retrieve

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/store"
)

type hopStubRetriever struct {
	byQuery map[string][]store.Hit
	calls   []string
}

func (s *hopStubRetriever) Retrieve(_ context.Context, req Request) ([]store.Hit, Trace, error) {
	s.calls = append(s.calls, req.Query)
	return s.byQuery[req.Query], Trace{OriginalQuery: req.Query}, nil
}

func mkHit(id string, score float64) store.Hit {
	return store.Hit{Chunk: store.StoredChunk{ID: id}, Score: score}
}

func TestHeuristicDecomposer(t *testing.T) {
	d := HeuristicDecomposer{}
	subs, _ := d.Decompose(context.Background(), "capital of France and population of France")
	if len(subs) != 2 {
		t.Fatalf("compound query decomposed to %v, want 2 sub-queries", subs)
	}
	subs, _ = d.Decompose(context.Background(), "what is the capital of France")
	if len(subs) != 1 || subs[0] != "what is the capital of France" {
		t.Fatalf("simple query decomposed to %v, want [<query>]", subs)
	}
}

func TestLLMDecomposer(t *testing.T) {
	model := &scriptedModel{resps: []string{"capital of France\npopulation of France"}}
	subs, err := (LLMDecomposer{Model: model}).Decompose(context.Background(), "compound q")
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	if len(subs) != 2 || subs[0] != "capital of France" || subs[1] != "population of France" {
		t.Fatalf("LLMDecomposer parsed %v, want 2 sub-queries", subs)
	}
	subs, _ = (LLMDecomposer{}).Decompose(context.Background(), "no model query")
	if len(subs) != 1 || subs[0] != "no model query" {
		t.Fatalf("nil-model Decompose → %v, want [<query>]", subs)
	}
}

func TestMultiHopRetrieverMergesSubRetrievals(t *testing.T) {
	base := &hopStubRetriever{byQuery: map[string][]store.Hit{
		"capital of France":    {mkHit("c1", 0.9), mkHit("shared", 0.4)},
		"population of France": {mkHit("p1", 0.8), mkHit("shared", 0.7)},
	}}
	mhr := MultiHopRetriever{Base: base, Decomposer: HeuristicDecomposer{}}
	hits, trace, err := mhr.Retrieve(context.Background(), Request{
		Query: "capital of France and population of France",
		TopK:  10,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(base.calls) != 2 {
		t.Fatalf("Base called %d times, want 2 (one per sub-query)", len(base.calls))
	}
	if len(hits) != 3 {
		t.Fatalf("merged hits = %d, want 3 (c1, p1, shared deduped)", len(hits))
	}
	for _, h := range hits {
		if h.Chunk.ID == "shared" && h.Score != 0.7 {
			t.Fatalf("shared hit score = %v, want 0.7 (max across hops)", h.Score)
		}
	}
	if len(trace.Hops) != 2 || trace.Hops[0].HitCount != 2 || trace.Hops[1].HitCount != 2 {
		t.Fatalf("Trace.Hops = %+v, want 2 hops with 2 hits each", trace.Hops)
	}
}

func TestMultiHopRetrieverTopK(t *testing.T) {
	base := &hopStubRetriever{byQuery: map[string][]store.Hit{
		"alpha": {mkHit("h1", 0.9), mkHit("h2", 0.5)},
		"bravo": {mkHit("h3", 0.7)},
	}}
	mhr := MultiHopRetriever{Base: base, Decomposer: HeuristicDecomposer{}}
	hits, _, err := mhr.Retrieve(context.Background(), Request{Query: "alpha and bravo", TopK: 2})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2 (TopK)", len(hits))
	}
	if hits[0].Chunk.ID != "h1" || hits[1].Chunk.ID != "h3" {
		t.Fatalf("hits not score-sorted: got %s, %s", hits[0].Chunk.ID, hits[1].Chunk.ID)
	}
}

func TestMultiHopRetrieverSingleHopPassThrough(t *testing.T) {
	base := &hopStubRetriever{byQuery: map[string][]store.Hit{
		"what is the capital of France": {mkHit("c1", 0.9)},
	}}
	mhr := MultiHopRetriever{Base: base, Decomposer: HeuristicDecomposer{}}
	if _, _, err := mhr.Retrieve(context.Background(), Request{
		Query: "what is the capital of France", TopK: 5,
	}); err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(base.calls) != 1 {
		t.Fatalf("non-compound query → %d Base calls, want 1", len(base.calls))
	}
}

func TestMultiHopRetrieverNilBase(t *testing.T) {
	_, _, err := (MultiHopRetriever{}).Retrieve(context.Background(), Request{Query: "x"})
	if err != ErrBaseRetrieverRequired {
		t.Fatalf("nil base → err %v, want ErrBaseRetrieverRequired", err)
	}
}
