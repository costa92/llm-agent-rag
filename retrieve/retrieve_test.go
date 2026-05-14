package retrieve

import (
	"context"
	"errors"
	"testing"

	"github.com/costa92/llm-agent-rag/advanced"
	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/store"
)

func TestNoopPreprocessorPreservesQuery(t *testing.T) {
	res, err := NoopPreprocessor{}.Process(context.Background(), Request{Query: "paris"})
	if err != nil {
		t.Fatalf("Process(): %v", err)
	}
	if len(res.QueryVariants) != 1 || res.QueryVariants[0] != "paris" {
		t.Fatalf("QueryVariants = %+v, want [paris]", res.QueryVariants)
	}
	if res.Trace.EffectiveQuery != "paris" {
		t.Fatalf("EffectiveQuery = %q, want paris", res.Trace.EffectiveQuery)
	}
}

type scriptedModel struct {
	resps []string
	err   error
	call  int
}

func (m *scriptedModel) Generate(_ context.Context, _ generate.Request) (generate.Response, error) {
	if m.err != nil {
		return generate.Response{}, m.err
	}
	if len(m.resps) == 0 {
		return generate.Response{}, nil
	}
	idx := m.call
	if idx >= len(m.resps) {
		idx = len(m.resps) - 1
	}
	m.call++
	return generate.Response{Text: m.resps[idx]}, nil
}

func TestLLMExpansionPreprocessorBuildsVariants(t *testing.T) {
	model := &scriptedModel{
		resps: []string{
			"capital of france",
			"paris france travel tips",
		},
	}
	pre := LLMExpansionPreprocessor{
		Model: model,
	}
	res, err := pre.Process(context.Background(), Request{
		Query:      "what is france capital",
		EnableMQE:  true,
		EnableHyDE: true,
		MQECount:   1,
	})
	if err != nil {
		t.Fatalf("Process(): %v", err)
	}
	if len(res.QueryVariants) != 3 {
		t.Fatalf("QueryVariants = %#v, want 3 variants", res.QueryVariants)
	}
	if res.QueryVariants[0] != "what is france capital" {
		t.Fatalf("first query variant = %q, want original query", res.QueryVariants[0])
	}
	if res.QueryVariants[1] != "capital of france" {
		t.Fatalf("second query variant = %q, want expansion", res.QueryVariants[1])
	}
	if res.QueryVariants[2] != "paris france travel tips" {
		t.Fatalf("third query variant = %q, want hypothetical", res.QueryVariants[2])
	}
	if res.Trace.EffectiveQuery != "what is france capital" {
		t.Fatalf("EffectiveQuery = %q, want original query", res.Trace.EffectiveQuery)
	}
}

func TestLLMExpansionPreprocessorRequiresModelWhenEnabled(t *testing.T) {
	_, err := LLMExpansionPreprocessor{}.Process(context.Background(), Request{
		Query:     "france capital",
		EnableMQE: true,
	})
	if !errors.Is(err, advanced.ErrModelRequired) {
		t.Fatalf("err = %v, want advanced.ErrModelRequired", err)
	}
}

type stubEmbedder struct{}

func (stubEmbedder) Embed(_ context.Context, _ string) (embed.Vector, error) {
	return embed.Vector{1, 0}, nil
}

func TestDenseRetrieverUsesStoreContract(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:        "a",
			Namespace: "docs",
			Vector:    embed.Vector{1, 0},
			Metadata: map[string]any{
				"lang": "en",
			},
		},
		{
			ID:        "b",
			Namespace: "docs",
			Vector:    embed.Vector{0.9, 0.1},
			Metadata: map[string]any{
				"lang": "fr",
			},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := DenseRetriever{Embedder: stubEmbedder{}, Store: mem}
	hits, trace, err := r.Retrieve(context.Background(), Request{
		Query:     "paris",
		Namespace: "docs",
		TopK:      5,
		Filters: map[string]any{
			"lang": "en",
		},
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) != 1 || hits[0].Chunk.ID != "a" {
		t.Fatalf("hits = %+v, want only a", hits)
	}
	if trace.EffectiveQuery != "paris" {
		t.Fatalf("trace = %+v, want effective query paris", trace)
	}
}

func TestLexicalRetrieverUsesContentOverlap(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:        "a",
			Namespace: "docs",
			Vector:    embed.Vector{1, 0},
			Content:   "Paris travel guide for museums",
		},
		{
			ID:        "b",
			Namespace: "docs",
			Vector:    embed.Vector{0.9, 0.1},
			Content:   "Berlin public transit manual",
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := LexicalRetriever{Store: mem}
	hits, _, err := r.Retrieve(context.Background(), Request{
		Query:     "paris museums",
		Namespace: "docs",
		TopK:      5,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) != 1 || hits[0].Chunk.ID != "a" {
		t.Fatalf("hits = %+v, want only a", hits)
	}
}

func TestHybridRetrieverFusesDenseAndLexical(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:        "a",
			Namespace: "docs",
			Vector:    embed.Vector{1, 0},
			Content:   "Paris travel guide for museums",
		},
		{
			ID:        "b",
			Namespace: "docs",
			Vector:    embed.Vector{0.95, 0.05},
			Content:   "France capital overview",
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := HybridRetriever{
		Dense:   DenseRetriever{Embedder: stubEmbedder{}, Store: mem},
		Lexical: LexicalRetriever{Store: mem},
	}
	hits, _, err := r.Retrieve(context.Background(), Request{
		Query:     "paris museums",
		Namespace: "docs",
		TopK:      5,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("expected fused hits, got none")
	}
	foundA := false
	for _, hit := range hits {
		if hit.Chunk.ID == "a" {
			foundA = true
			break
		}
	}
	if !foundA {
		t.Fatalf("hits = %+v, want fused result set to include lexical match a", hits)
	}
}

type recordingRetriever struct {
	queries []string
	hitsByQ map[string][]store.Hit
}

func (r *recordingRetriever) Retrieve(_ context.Context, req Request) ([]store.Hit, Trace, error) {
	r.queries = append(r.queries, req.Query)
	return append([]store.Hit(nil), r.hitsByQ[req.Query]...), Trace{
		OriginalQuery:  req.Query,
		EffectiveQuery: req.Query,
		QueryVariants:  []string{req.Query},
	}, nil
}

func TestVariantRetrieverMergesExpandedQueries(t *testing.T) {
	base := &recordingRetriever{
		hitsByQ: map[string][]store.Hit{
			"france capital": {
				{Chunk: store.StoredChunk{ID: "doc1"}, Score: 0.5},
			},
			"capital of france": {
				{Chunk: store.StoredChunk{ID: "doc1"}, Score: 0.7},
				{Chunk: store.StoredChunk{ID: "doc2"}, Score: 0.6},
			},
			"paris overview": {
				{Chunk: store.StoredChunk{ID: "doc3"}, Score: 0.9},
			},
		},
	}
	r := VariantRetriever{Base: base}
	hits, trace, err := r.Retrieve(context.Background(), Request{
		Query:         "france capital",
		TopK:          2,
		QueryVariants: []string{"france capital", "capital of france", "paris overview"},
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(base.queries) != 3 {
		t.Fatalf("base queries = %#v, want 3 invocations", base.queries)
	}
	if len(hits) != 2 {
		t.Fatalf("len(hits) = %d, want 2", len(hits))
	}
	if hits[0].Chunk.ID != "doc3" || hits[1].Chunk.ID != "doc1" {
		t.Fatalf("hits = %+v, want doc3 then doc1", hits)
	}
	if trace.QueryVariants[1] != "capital of france" {
		t.Fatalf("trace variants = %#v, want preserved variants", trace.QueryVariants)
	}
}
