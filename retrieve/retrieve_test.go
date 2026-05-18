package retrieve

import (
	"context"
	"errors"
	"strings"
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

func TestLexicalRetrieverRanksByBM25(t *testing.T) {
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

func indexOfHit(hits []store.Hit, id string) int {
	for i, h := range hits {
		if h.Chunk.ID == id {
			return i
		}
	}
	return -1
}

func TestLexicalRetrieverScoresTermFrequency(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{ID: "tf-high", Namespace: "docs", Vector: embed.Vector{1, 0}, Content: "kafka kafka kafka streaming pipeline"},
		{ID: "tf-low", Namespace: "docs", Vector: embed.Vector{1, 0}, Content: "kafka introduction overview guide notes"},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}
	hits, _, err := LexicalRetriever{Store: mem}.Retrieve(context.Background(), Request{
		Query: "kafka", Namespace: "docs", TopK: 5,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) != 2 || hits[0].Chunk.ID != "tf-high" {
		t.Fatalf("hits = %+v, want tf-high ranked first by term frequency", hits)
	}
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("scores = %v / %v, want repeated-term chunk to score higher", hits[0].Score, hits[1].Score)
	}
}

func TestLexicalRetrieverDownweightsCommonTerms(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{ID: "rare-doc", Namespace: "docs", Vector: embed.Vector{1, 0}, Content: "quasar alpha beta gamma"},
		{ID: "common-doc", Namespace: "docs", Vector: embed.Vector{1, 0}, Content: "common alpha beta gamma"},
		{ID: "filler-1", Namespace: "docs", Vector: embed.Vector{1, 0}, Content: "common delta epsilon zeta"},
		{ID: "filler-2", Namespace: "docs", Vector: embed.Vector{1, 0}, Content: "common eta theta iota"},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}
	hits, _, err := LexicalRetriever{Store: mem}.Retrieve(context.Background(), Request{
		Query: "quasar common", Namespace: "docs", TopK: 5,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	rare, common := indexOfHit(hits, "rare-doc"), indexOfHit(hits, "common-doc")
	if rare == -1 || common == -1 {
		t.Fatalf("hits = %+v, want both rare-doc and common-doc present", hits)
	}
	if rare >= common {
		t.Fatalf("rare-doc at %d, common-doc at %d, want rare term to outrank ubiquitous term", rare, common)
	}
}

func TestLexicalRetrieverNormalizesByLength(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{ID: "short", Namespace: "docs", Vector: embed.Vector{1, 0}, Content: "vector search index"},
		{ID: "long", Namespace: "docs", Vector: embed.Vector{1, 0}, Content: "vector search index plus lots of additional unrelated padding words here now today"},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}
	hits, _, err := LexicalRetriever{Store: mem}.Retrieve(context.Background(), Request{
		Query: "vector", Namespace: "docs", TopK: 5,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) != 2 || hits[0].Chunk.ID != "short" {
		t.Fatalf("hits = %+v, want short focused chunk ranked first", hits)
	}
}

func TestLexicalRetrieverEmptyQuery(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{ID: "a", Namespace: "docs", Vector: embed.Vector{1, 0}, Content: "kafka streaming pipeline"},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}
	hits, _, err := LexicalRetriever{Store: mem}.Retrieve(context.Background(), Request{
		Query: "", Namespace: "docs", TopK: 5,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %+v, want no hits for empty query", hits)
	}
}

type lexicalSearcherStub struct {
	*store.InMemoryStore
	lexHits []store.Hit
	called  bool
}

func (s *lexicalSearcherStub) LexicalSearch(_ context.Context, _ store.Query) ([]store.Hit, error) {
	s.called = true
	return append([]store.Hit(nil), s.lexHits...), nil
}

func TestLexicalRetrieverDelegatesToLexicalSearcher(t *testing.T) {
	stub := &lexicalSearcherStub{
		InMemoryStore: store.NewInMemoryStore(2),
		lexHits:       []store.Hit{{Chunk: store.StoredChunk{ID: "sentinel", Namespace: "docs"}, Score: 9.9}},
	}
	hits, _, err := LexicalRetriever{Store: stub}.Retrieve(context.Background(), Request{
		Query: "anything", Namespace: "docs", TopK: 5,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if !stub.called {
		t.Fatalf("LexicalSearch was not called; retriever did not use the LexicalSearcher capability")
	}
	if len(hits) != 1 || hits[0].Chunk.ID != "sentinel" {
		t.Fatalf("hits = %+v, want the sentinel hit from LexicalSearch", hits)
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

func TestStructureRetrieverExpandsMatchedSectionToLeafChunks(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:          "doc1:0",
			Namespace:   "docs",
			DocID:       "doc1",
			Title:       "Cities",
			SectionID:   "docs:doc1:Travel",
			SectionPath: []string{"Cities", "Travel"},
			Heading:     "Travel",
			Content:     "Museums and cafes in Paris.",
			Vector:      embed.Vector{1, 0},
		},
		{
			ID:          "doc1:1",
			Namespace:   "docs",
			DocID:       "doc1",
			Title:       "Cities",
			SectionID:   "docs:doc1:Travel Tips",
			SectionPath: []string{"Cities", "Travel", "Travel Tips"},
			Heading:     "Travel Tips",
			Content:     "Book early and use the metro.",
			Vector:      embed.Vector{1, 0},
		},
		{
			ID:          "doc1:2",
			Namespace:   "docs",
			DocID:       "doc1",
			Title:       "Cities",
			SectionID:   "docs:doc1:History",
			SectionPath: []string{"Cities", "History"},
			Heading:     "History",
			Content:     "Ancient capital notes.",
			Vector:      embed.Vector{1, 0},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := StructureRetriever{Store: mem}
	hits, trace, err := r.Retrieve(context.Background(), Request{
		Query:               "travel",
		Namespace:           "docs",
		TopK:                5,
		EnableTreeExpansion: true,
		ExpansionDepth:      3,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("len(hits) = %d, want at least 2 expanded hits", len(hits))
	}
	foundNested := false
	for _, hit := range hits {
		if hit.Chunk.ID == "doc1:1" {
			foundNested = true
			break
		}
	}
	if !foundNested {
		t.Fatalf("hits = %+v, want nested leaf doc1:1 from matched section expansion", hits)
	}
	if len(trace.MatchedSections) == 0 || trace.MatchedSections[0] != "docs:doc1:Travel" {
		t.Fatalf("matched sections = %#v, want travel section", trace.MatchedSections)
	}
	if len(trace.SearchPath) == 0 || !strings.Contains(trace.SearchPath[0], "Travel") {
		t.Fatalf("search path = %#v, want travel path", trace.SearchPath)
	}
	if len(trace.ExpandedChunkIDs) < 2 {
		t.Fatalf("expanded chunk ids = %#v, want expanded leaf ids", trace.ExpandedChunkIDs)
	}
}

func TestLexicalAndDenseRespectRoutePath(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:          "doc1:0",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:Travel",
			SectionPath: []string{"Cities", "Travel"},
			Content:     "Paris travel museums guide",
			Vector:      embed.Vector{1, 0},
		},
		{
			ID:          "doc1:1",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:History",
			SectionPath: []string{"Cities", "History"},
			Content:     "Paris history ancient guide",
			Vector:      embed.Vector{1, 0},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	lex := LexicalRetriever{Store: mem}
	lexHits, lexTrace, err := lex.Retrieve(context.Background(), Request{
		Query:     "paris guide",
		Namespace: "docs",
		TopK:      5,
		RoutePath: []string{"Cities", "Travel"},
	})
	if err != nil {
		t.Fatalf("Lexical Retrieve(): %v", err)
	}
	if len(lexHits) != 1 || lexHits[0].Chunk.ID != "doc1:0" {
		t.Fatalf("lex hits = %+v, want only travel subtree hit", lexHits)
	}
	if !pathEqualsFold(lexTrace.RoutePath, []string{"Cities", "Travel"}) {
		t.Fatalf("lex trace route path = %#v, want travel path", lexTrace.RoutePath)
	}

	dense := DenseRetriever{Embedder: stubEmbedder{}, Store: mem}
	denseHits, denseTrace, err := dense.Retrieve(context.Background(), Request{
		Query:     "paris guide",
		Namespace: "docs",
		TopK:      5,
		RoutePath: []string{"Cities", "Travel"},
	})
	if err != nil {
		t.Fatalf("Dense Retrieve(): %v", err)
	}
	if len(denseHits) != 1 || denseHits[0].Chunk.ID != "doc1:0" {
		t.Fatalf("dense hits = %+v, want only travel subtree hit", denseHits)
	}
	if !pathEqualsFold(denseTrace.RoutePath, []string{"Cities", "Travel"}) {
		t.Fatalf("dense trace route path = %#v, want travel path", denseTrace.RoutePath)
	}
}

func TestLexicalAutoRouteSelectsMatchingSectionPath(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:          "doc1:0",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:Travel",
			SectionPath: []string{"Cities", "Travel"},
			Heading:     "Travel",
			Content:     "Museums and cafes in Paris.",
			Vector:      embed.Vector{1, 0},
		},
		{
			ID:          "doc1:1",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:History",
			SectionPath: []string{"Cities", "History"},
			Heading:     "History",
			Content:     "Ancient capital notes.",
			Vector:      embed.Vector{1, 0},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := LexicalRetriever{Store: mem}
	hits, trace, err := r.Retrieve(context.Background(), Request{
		Query:             "travel museums",
		Namespace:         "docs",
		TopK:              5,
		EnableAutoRoute:   true,
		AutoRouteMinScore: 2,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if !pathEqualsFold(trace.RoutePath, []string{"Cities", "Travel"}) {
		t.Fatalf("route path = %#v, want travel subtree", trace.RoutePath)
	}
	if !pathEqualsFold(trace.AutoRoutePath, []string{"Cities", "Travel"}) {
		t.Fatalf("auto route path = %#v, want travel subtree", trace.AutoRoutePath)
	}
	if len(trace.AutoRouteCandidates) == 0 {
		t.Fatalf("auto route candidates = %#v, want non-empty", trace.AutoRouteCandidates)
	}
	if !pathEqualsFold(trace.AutoRouteCandidates[0].Path, []string{"Cities", "Travel"}) {
		t.Fatalf("top auto route candidate = %#v, want travel subtree", trace.AutoRouteCandidates[0].Path)
	}
	if trace.AutoRouteCandidates[0].Confidence <= 0 {
		t.Fatalf("top auto route candidate confidence = %v, want > 0", trace.AutoRouteCandidates[0].Confidence)
	}
	if len(trace.AutoRouteCandidates[0].Signals) == 0 {
		t.Fatalf("top auto route candidate signals = %#v, want non-empty", trace.AutoRouteCandidates[0].Signals)
	}
	if len(hits) != 1 || hits[0].Chunk.ID != "doc1:0" {
		t.Fatalf("hits = %+v, want only travel hit", hits)
	}
}

func TestVariantRetrieverMergesAutoRouteCandidatesAcrossQueries(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:          "doc1:0",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:Travel",
			SectionPath: []string{"Cities", "Travel"},
			Heading:     "Travel",
			Content:     "Museums and cafes in Paris.",
			Vector:      embed.Vector{1, 0},
		},
		{
			ID:          "doc1:1",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:History",
			SectionPath: []string{"Cities", "History"},
			Heading:     "History",
			Content:     "Ancient capital history notes.",
			Vector:      embed.Vector{1, 0},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := VariantRetriever{
		Base: LexicalRetriever{Store: mem},
	}
	hits, trace, err := r.Retrieve(context.Background(), Request{
		Query:                  "travel history",
		Namespace:              "docs",
		TopK:                   5,
		EnableAutoRoute:        true,
		AutoRouteMinScore:      2,
		AutoRouteMaxCandidates: 2,
		QueryVariants:          []string{"travel museums", "history notes"},
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected hits")
	}
	if len(trace.AutoRouteCandidates) < 2 {
		t.Fatalf("auto route candidates = %#v, want merged candidates", trace.AutoRouteCandidates)
	}
	if trace.AutoRouteCandidates[0].Confidence <= 0 || trace.AutoRouteCandidates[0].Confidence > 1 {
		t.Fatalf("top auto route candidate confidence = %v, want normalized range", trace.AutoRouteCandidates[0].Confidence)
	}
	if !pathEqualsFold(trace.AutoRouteCandidates[0].Path, []string{"Cities", "Travel"}) &&
		!pathEqualsFold(trace.AutoRouteCandidates[0].Path, []string{"Cities", "History"}) {
		t.Fatalf("unexpected top candidate = %#v", trace.AutoRouteCandidates[0].Path)
	}
	foundTravel := false
	foundHistory := false
	for _, candidate := range trace.AutoRouteCandidates {
		if pathEqualsFold(candidate.Path, []string{"Cities", "Travel"}) {
			foundTravel = true
		}
		if pathEqualsFold(candidate.Path, []string{"Cities", "History"}) {
			foundHistory = true
		}
	}
	if !foundTravel || !foundHistory {
		t.Fatalf("auto route candidates = %#v, want travel and history", trace.AutoRouteCandidates)
	}
}

func TestVariantRetrieverCanFanoutAcrossTopRouteCandidates(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:          "doc1:0",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:Travel",
			SectionPath: []string{"Cities", "Travel"},
			Heading:     "Travel",
			Content:     "Museums and cafes in Paris.",
			Vector:      embed.Vector{1, 0},
		},
		{
			ID:          "doc1:1",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:History",
			SectionPath: []string{"Cities", "History"},
			Heading:     "History",
			Content:     "History museums and capital notes.",
			Vector:      embed.Vector{1, 0},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := VariantRetriever{Base: LexicalRetriever{Store: mem}}
	hits, trace, err := r.Retrieve(context.Background(), Request{
		Query:                        "travel history museums",
		Namespace:                    "docs",
		TopK:                         5,
		EnableAutoRoute:              true,
		AutoRouteMinScore:            2,
		AutoRouteMaxCandidates:       2,
		AutoRouteConfidenceThreshold: 0.5,
		AutoRouteFanout:              2,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("hits = %+v, want fanout results from two routes", hits)
	}
	foundTravel := false
	foundHistory := false
	for _, hit := range hits {
		if pathEqualsFold(hit.Chunk.SectionPath, []string{"Cities", "Travel"}) {
			foundTravel = true
		}
		if pathEqualsFold(hit.Chunk.SectionPath, []string{"Cities", "History"}) {
			foundHistory = true
		}
	}
	if !foundTravel || !foundHistory {
		t.Fatalf("hits = %+v, want travel and history route hits", hits)
	}
	if len(trace.AutoRouteCandidates) < 2 {
		t.Fatalf("auto route candidates = %#v, want retained fanout candidates", trace.AutoRouteCandidates)
	}
	if trace.RoutePolicy.Mode != "fanout" {
		t.Fatalf("route policy mode = %q, want fanout", trace.RoutePolicy.Mode)
	}
	if trace.RoutePolicy.SelectedCount != 2 {
		t.Fatalf("route policy selected count = %d, want 2", trace.RoutePolicy.SelectedCount)
	}
	if len(trace.RoutePolicy.Rationale) == 0 {
		t.Fatalf("route policy rationale = %#v, want non-empty", trace.RoutePolicy.Rationale)
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

func TestVariantRetrieverConvergesWhenConfidenceGapDominates(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:          "doc1:0",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:Travel",
			SectionPath: []string{"Cities", "Travel"},
			Heading:     "Travel museums",
			Content:     "Travel museums.",
			Vector:      embed.Vector{1, 0},
		},
		{
			ID:          "doc1:1",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:History-museums",
			SectionPath: []string{"Cities", "History-museums"},
			Heading:     "Old history",
			Content:     "Old history of museums.",
			Vector:      embed.Vector{1, 0},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := VariantRetriever{Base: LexicalRetriever{Store: mem}}
	hits, trace, err := r.Retrieve(context.Background(), Request{
		Query:                        "travel museums",
		Namespace:                    "docs",
		TopK:                         5,
		EnableAutoRoute:              true,
		AutoRouteMinScore:            2,
		AutoRouteMaxCandidates:       2,
		AutoRouteConfidenceThreshold: 0.3,
		AutoRouteFanout:              2,
		AutoRouteConfidenceGap:       0.4,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if trace.RoutePolicy.Mode != "converged" {
		t.Fatalf("route policy mode = %q, want converged", trace.RoutePolicy.Mode)
	}
	if trace.RoutePolicy.SelectedCount != 1 {
		t.Fatalf("route policy selected count = %d, want 1", trace.RoutePolicy.SelectedCount)
	}
	if trace.RoutePolicy.Gap <= 0 {
		t.Fatalf("route policy gap = %v, want > 0", trace.RoutePolicy.Gap)
	}
	if trace.RoutePolicy.ConfidenceGap != 0.4 {
		t.Fatalf("route policy confidence gap = %v, want 0.4", trace.RoutePolicy.ConfidenceGap)
	}
	hasConverged := false
	for _, line := range trace.RoutePolicy.Rationale {
		if strings.HasPrefix(line, "converged:") {
			hasConverged = true
			break
		}
	}
	if !hasConverged {
		t.Fatalf("route policy rationale = %#v, want converged: entry", trace.RoutePolicy.Rationale)
	}
	for _, hit := range hits {
		if pathEqualsFold(hit.Chunk.SectionPath, []string{"Cities", "History-museums"}) {
			t.Fatalf("hits = %+v, history route should not be queried when converged", hits)
		}
	}
}

func TestVariantRetrieverFansOutWhenConfidenceGapIsSmall(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:          "doc1:0",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:Travel",
			SectionPath: []string{"Cities", "Travel"},
			Heading:     "Museums cafes",
			Content:     "Museums and cafes.",
			Vector:      embed.Vector{1, 0},
		},
		{
			ID:          "doc1:1",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:History",
			SectionPath: []string{"Cities", "History"},
			Heading:     "Museums cafes",
			Content:     "Museums and cafes near history sites.",
			Vector:      embed.Vector{1, 0},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := VariantRetriever{Base: LexicalRetriever{Store: mem}}
	hits, trace, err := r.Retrieve(context.Background(), Request{
		Query:                        "museums cafes travel",
		Namespace:                    "docs",
		TopK:                         5,
		EnableAutoRoute:              true,
		AutoRouteMinScore:            2,
		AutoRouteMaxCandidates:       2,
		AutoRouteConfidenceThreshold: 0.3,
		AutoRouteFanout:              2,
		AutoRouteConfidenceGap:       0.5,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if trace.RoutePolicy.Mode != "fanout" {
		t.Fatalf("route policy mode = %q, want fanout", trace.RoutePolicy.Mode)
	}
	if trace.RoutePolicy.SelectedCount != 2 {
		t.Fatalf("route policy selected count = %d, want 2", trace.RoutePolicy.SelectedCount)
	}
	if trace.RoutePolicy.Gap <= 0 {
		t.Fatalf("route policy gap = %v, want > 0", trace.RoutePolicy.Gap)
	}
	if trace.RoutePolicy.Gap >= 0.5 {
		t.Fatalf("route policy gap = %v, want < threshold 0.5", trace.RoutePolicy.Gap)
	}
	hasFanout := false
	for _, line := range trace.RoutePolicy.Rationale {
		if strings.HasPrefix(line, "fanout:") {
			hasFanout = true
			break
		}
	}
	if !hasFanout {
		t.Fatalf("route policy rationale = %#v, want fanout: entry", trace.RoutePolicy.Rationale)
	}
	foundTravel := false
	foundHistory := false
	for _, hit := range hits {
		if pathEqualsFold(hit.Chunk.SectionPath, []string{"Cities", "Travel"}) {
			foundTravel = true
		}
		if pathEqualsFold(hit.Chunk.SectionPath, []string{"Cities", "History"}) {
			foundHistory = true
		}
	}
	if !foundTravel || !foundHistory {
		t.Fatalf("hits = %+v, want both travel and history hits when fanning out", hits)
	}
}

func TestVariantRetrieverConfidenceGapZeroIsOptOut(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:          "doc1:0",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:Travel",
			SectionPath: []string{"Cities", "Travel"},
			Heading:     "Travel museums",
			Content:     "Travel museums.",
			Vector:      embed.Vector{1, 0},
		},
		{
			ID:          "doc1:1",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:History-museums",
			SectionPath: []string{"Cities", "History-museums"},
			Heading:     "Old history",
			Content:     "Old history of museums.",
			Vector:      embed.Vector{1, 0},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := VariantRetriever{Base: LexicalRetriever{Store: mem}}
	_, trace, err := r.Retrieve(context.Background(), Request{
		Query:                        "travel museums",
		Namespace:                    "docs",
		TopK:                         5,
		EnableAutoRoute:              true,
		AutoRouteMinScore:            2,
		AutoRouteMaxCandidates:       2,
		AutoRouteConfidenceThreshold: 0.3,
		AutoRouteFanout:              2,
		AutoRouteConfidenceGap:       0,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if trace.RoutePolicy.Mode != "fanout" {
		t.Fatalf("route policy mode = %q, want fanout (gap opt-out)", trace.RoutePolicy.Mode)
	}
	if trace.RoutePolicy.ConfidenceGap != 0 {
		t.Fatalf("route policy confidence gap = %v, want 0 (opt-out)", trace.RoutePolicy.ConfidenceGap)
	}
	if trace.RoutePolicy.Gap != 0 {
		t.Fatalf("route policy gap = %v, want 0 when gap policy inactive", trace.RoutePolicy.Gap)
	}
	for _, line := range trace.RoutePolicy.Rationale {
		if strings.HasPrefix(line, "converged:") || strings.HasPrefix(line, "fanout:") {
			t.Fatalf("route policy rationale = %#v, gap-policy lines should be absent when opted out", trace.RoutePolicy.Rationale)
		}
	}
}

func TestVariantRetrieverTrajectoryReflectsConvergedRoute(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:          "doc1:0",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:Travel",
			SectionPath: []string{"Cities", "Travel"},
			Heading:     "Travel museums",
			Content:     "Travel museums.",
			Vector:      embed.Vector{1, 0},
		},
		{
			ID:          "doc1:1",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:History-museums",
			SectionPath: []string{"Cities", "History-museums"},
			Heading:     "Old history",
			Content:     "Old history of museums.",
			Vector:      embed.Vector{1, 0},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := VariantRetriever{Base: LexicalRetriever{Store: mem}}
	_, trace, err := r.Retrieve(context.Background(), Request{
		Query:                        "travel museums",
		Namespace:                    "docs",
		TopK:                         5,
		EnableAutoRoute:              true,
		AutoRouteMinScore:            2,
		AutoRouteMaxCandidates:       2,
		AutoRouteConfidenceThreshold: 0.3,
		AutoRouteFanout:              2,
		AutoRouteConfidenceGap:       0.4,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(trace.SearchTrajectory) != 1 {
		t.Fatalf("trajectory len = %d, want 1 (converged)", len(trace.SearchTrajectory))
	}
	step := trace.SearchTrajectory[0]
	if step.Mode != "converged" {
		t.Fatalf("trajectory step mode = %q, want converged", step.Mode)
	}
	if !pathEqualsFold(step.Route, []string{"Cities", "Travel"}) {
		t.Fatalf("trajectory route = %#v, want Travel route", step.Route)
	}
	if step.HitCount == 0 || len(step.HitIDs) != step.HitCount {
		t.Fatalf("trajectory hit count = %d ids=%#v, want non-zero matching", step.HitCount, step.HitIDs)
	}
	for _, id := range step.HitIDs {
		if id == "doc1:1" {
			t.Fatalf("trajectory hit ids = %#v, history chunk should not appear when converged", step.HitIDs)
		}
	}
}

func TestVariantRetrieverTrajectoryReflectsFanoutPerRoute(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:          "doc1:0",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:Travel",
			SectionPath: []string{"Cities", "Travel"},
			Heading:     "Museums cafes",
			Content:     "Museums and cafes.",
			Vector:      embed.Vector{1, 0},
		},
		{
			ID:          "doc1:1",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:History",
			SectionPath: []string{"Cities", "History"},
			Heading:     "Museums cafes",
			Content:     "Museums and cafes near history sites.",
			Vector:      embed.Vector{1, 0},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := VariantRetriever{Base: LexicalRetriever{Store: mem}}
	_, trace, err := r.Retrieve(context.Background(), Request{
		Query:                        "museums cafes travel",
		Namespace:                    "docs",
		TopK:                         5,
		EnableAutoRoute:              true,
		AutoRouteMinScore:            2,
		AutoRouteMaxCandidates:       2,
		AutoRouteConfidenceThreshold: 0.3,
		AutoRouteFanout:              2,
		AutoRouteConfidenceGap:       0.5,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(trace.SearchTrajectory) != 2 {
		t.Fatalf("trajectory len = %d, want 2 (fanout)", len(trace.SearchTrajectory))
	}
	travelStep := -1
	historyStep := -1
	for i, step := range trace.SearchTrajectory {
		if pathEqualsFold(step.Route, []string{"Cities", "Travel"}) {
			travelStep = i
		}
		if pathEqualsFold(step.Route, []string{"Cities", "History"}) {
			historyStep = i
		}
	}
	if travelStep < 0 || historyStep < 0 {
		t.Fatalf("trajectory = %#v, want one step per route", trace.SearchTrajectory)
	}
	for _, step := range trace.SearchTrajectory {
		if step.Mode != "fanout" {
			t.Fatalf("trajectory step mode = %q, want fanout", step.Mode)
		}
		if step.HitCount == 0 {
			t.Fatalf("trajectory step %v has zero hits", step.Route)
		}
		// per-route HitIDs must not bleed: the travel step should not contain
		// the history chunk and vice versa.
		for _, id := range step.HitIDs {
			isTravelChunk := id == "doc1:0"
			isHistoryChunk := id == "doc1:1"
			if pathEqualsFold(step.Route, []string{"Cities", "Travel"}) && isHistoryChunk {
				t.Fatalf("travel trajectory step contained history chunk %q", id)
			}
			if pathEqualsFold(step.Route, []string{"Cities", "History"}) && isTravelChunk {
				t.Fatalf("history trajectory step contained travel chunk %q", id)
			}
		}
	}
}

func TestVariantRetrieverTrajectoryRecordsSinglePathRun(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:          "doc1:0",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:Travel",
			SectionPath: []string{"Cities", "Travel"},
			Heading:     "Travel museums",
			Content:     "Travel museums.",
			Vector:      embed.Vector{1, 0},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := VariantRetriever{Base: LexicalRetriever{Store: mem}}
	hits, trace, err := r.Retrieve(context.Background(), Request{
		Query:                  "travel museums",
		Namespace:              "docs",
		TopK:                   5,
		EnableAutoRoute:        true,
		AutoRouteMinScore:      2,
		AutoRouteMaxCandidates: 1,
		AutoRouteFanout:        1,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(trace.SearchTrajectory) != 1 {
		t.Fatalf("trajectory len = %d, want 1 (single path)", len(trace.SearchTrajectory))
	}
	step := trace.SearchTrajectory[0]
	if step.Mode != "single" {
		t.Fatalf("trajectory step mode = %q, want single", step.Mode)
	}
	if step.HitCount != len(hits) {
		t.Fatalf("trajectory hit count = %d, want %d (matches returned hits)", step.HitCount, len(hits))
	}
}

func TestVariantRetrieverDefaultPlannerMatchesNilPlanner(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:          "doc1:0",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:Travel",
			SectionPath: []string{"Cities", "Travel"},
			Heading:     "Travel museums",
			Content:     "Travel museums.",
			Vector:      embed.Vector{1, 0},
		},
		{
			ID:          "doc1:1",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:History-museums",
			SectionPath: []string{"Cities", "History-museums"},
			Heading:     "Old history",
			Content:     "Old history of museums.",
			Vector:      embed.Vector{1, 0},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	req := Request{
		Query:                        "travel museums",
		Namespace:                    "docs",
		TopK:                         5,
		EnableAutoRoute:              true,
		AutoRouteMinScore:            2,
		AutoRouteMaxCandidates:       2,
		AutoRouteConfidenceThreshold: 0.3,
		AutoRouteFanout:              2,
		AutoRouteConfidenceGap:       0.4,
	}

	nilPlanner := VariantRetriever{Base: LexicalRetriever{Store: mem}}
	hitsNil, traceNil, err := nilPlanner.Retrieve(context.Background(), req)
	if err != nil {
		t.Fatalf("nil planner Retrieve(): %v", err)
	}
	gapAware := VariantRetriever{Base: LexicalRetriever{Store: mem}, Planner: GapAwareSectionPlanner{}}
	hitsExplicit, traceExplicit, err := gapAware.Retrieve(context.Background(), req)
	if err != nil {
		t.Fatalf("explicit planner Retrieve(): %v", err)
	}
	if traceNil.RoutePolicy.Mode != traceExplicit.RoutePolicy.Mode {
		t.Fatalf("mode mismatch: nil=%q explicit=%q", traceNil.RoutePolicy.Mode, traceExplicit.RoutePolicy.Mode)
	}
	if traceNil.RoutePolicy.SelectedCount != traceExplicit.RoutePolicy.SelectedCount {
		t.Fatalf("selected mismatch: nil=%d explicit=%d", traceNil.RoutePolicy.SelectedCount, traceExplicit.RoutePolicy.SelectedCount)
	}
	if traceNil.RoutePolicy.Gap != traceExplicit.RoutePolicy.Gap {
		t.Fatalf("gap mismatch: nil=%v explicit=%v", traceNil.RoutePolicy.Gap, traceExplicit.RoutePolicy.Gap)
	}
	if len(hitsNil) != len(hitsExplicit) {
		t.Fatalf("hit count mismatch: nil=%d explicit=%d", len(hitsNil), len(hitsExplicit))
	}
	for i := range hitsNil {
		if hitsNil[i].Chunk.ID != hitsExplicit[i].Chunk.ID {
			t.Fatalf("hit %d id mismatch: nil=%q explicit=%q", i, hitsNil[i].Chunk.ID, hitsExplicit[i].Chunk.ID)
		}
	}
}

type forcedSinglePlanner struct{}

func (forcedSinglePlanner) Plan(_ context.Context, _ Request, candidates []RouteCandidate) (SectionPlannerDecision, error) {
	if len(candidates) == 0 {
		return SectionPlannerDecision{
			Mode:      "single",
			Fanout:    1,
			Rationale: []string{"custom planner: no candidates"},
		}, nil
	}
	selected := []RouteCandidate{candidates[0]}
	markSelectedCandidates(selected, 1)
	return SectionPlannerDecision{
		Selected:  selected,
		Mode:      "single",
		Fanout:    1,
		Rationale: []string{"custom planner: forced single"},
	}, nil
}

func TestVariantRetrieverHonorsCustomPlanner(t *testing.T) {
	mem := store.NewInMemoryStore(2)
	err := mem.Upsert(context.Background(), []store.StoredChunk{
		{
			ID:          "doc1:0",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:Travel",
			SectionPath: []string{"Cities", "Travel"},
			Heading:     "Museums cafes",
			Content:     "Museums and cafes.",
			Vector:      embed.Vector{1, 0},
		},
		{
			ID:          "doc1:1",
			Namespace:   "docs",
			DocID:       "doc1",
			SectionID:   "docs:doc1:History",
			SectionPath: []string{"Cities", "History"},
			Heading:     "Museums cafes",
			Content:     "Museums and cafes near history sites.",
			Vector:      embed.Vector{1, 0},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	r := VariantRetriever{Base: LexicalRetriever{Store: mem}, Planner: forcedSinglePlanner{}}
	_, trace, err := r.Retrieve(context.Background(), Request{
		Query:                        "museums cafes travel",
		Namespace:                    "docs",
		TopK:                         5,
		EnableAutoRoute:              true,
		AutoRouteMinScore:            2,
		AutoRouteMaxCandidates:       2,
		AutoRouteConfidenceThreshold: 0.3,
		AutoRouteFanout:              2,
		AutoRouteConfidenceGap:       0.5,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if trace.RoutePolicy.Mode != "single" {
		t.Fatalf("route policy mode = %q, want single (forced)", trace.RoutePolicy.Mode)
	}
	if trace.RoutePolicy.SelectedCount != 1 {
		t.Fatalf("route policy selected count = %d, want 1", trace.RoutePolicy.SelectedCount)
	}
	foundCustom := false
	for _, line := range trace.RoutePolicy.Rationale {
		if strings.Contains(line, "custom planner: forced single") {
			foundCustom = true
			break
		}
	}
	if !foundCustom {
		t.Fatalf("route policy rationale = %#v, want custom planner line", trace.RoutePolicy.Rationale)
	}
	if len(trace.SearchTrajectory) != 1 {
		t.Fatalf("trajectory len = %d, want 1 (custom planner forces single route execution)", len(trace.SearchTrajectory))
	}
}

func hitList(ids ...string) []store.Hit {
	hits := make([]store.Hit, len(ids))
	for i, id := range ids {
		hits[i] = store.Hit{Chunk: store.StoredChunk{ID: id}, Score: float64(len(ids) - i)}
	}
	return hits
}

func TestHybridRetrieverFusionAttribution(t *testing.T) {
	const q = "fusion query"
	dense := &recordingRetriever{hitsByQ: map[string][]store.Hit{q: hitList("c1", "c2")}}
	lexical := &recordingRetriever{hitsByQ: map[string][]store.Hit{q: hitList("c2", "c3")}}
	r := HybridRetriever{Dense: dense, Lexical: lexical}
	_, trace, err := r.Retrieve(context.Background(), Request{Query: q, TopK: 10})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(trace.Fusion) != 3 {
		t.Fatalf("Fusion len = %d, want 3", len(trace.Fusion))
	}
	if trace.Fusion[0].ChunkID != "c2" {
		t.Fatalf("Fusion[0] = %q, want c2 (ranked by both signals)", trace.Fusion[0].ChunkID)
	}
	byID := map[string]FusionAttribution{}
	for _, f := range trace.Fusion {
		byID[f.ChunkID] = f
	}
	if byID["c2"].DenseRank != 2 || byID["c2"].LexicalRank != 1 || byID["c2"].StructureRank != 0 {
		t.Fatalf("c2 attribution = %+v, want dense=2 lexical=1 structure=0", byID["c2"])
	}
	if byID["c1"].DenseRank != 1 || byID["c1"].LexicalRank != 0 {
		t.Fatalf("c1 attribution = %+v, want dense=1 lexical=0", byID["c1"])
	}
	if byID["c3"].DenseRank != 0 || byID["c3"].LexicalRank != 2 {
		t.Fatalf("c3 attribution = %+v, want dense=0 lexical=2", byID["c3"])
	}
	if byID["c2"].RRFScore <= byID["c1"].RRFScore || byID["c2"].RRFScore <= byID["c3"].RRFScore {
		t.Fatalf("multi-signal chunk c2 should outscore single-signal chunks: %+v", byID)
	}
}

func TestHybridRetrieverRRFConstantDefaultPreservesBehavior(t *testing.T) {
	const q = "rrf query"
	run := func(k float64) []store.Hit {
		dense := &recordingRetriever{hitsByQ: map[string][]store.Hit{q: hitList("a", "b", "c")}}
		lexical := &recordingRetriever{hitsByQ: map[string][]store.Hit{q: hitList("b", "d")}}
		out, _, err := HybridRetriever{Dense: dense, Lexical: lexical, RRFConstant: k}.Retrieve(
			context.Background(), Request{Query: q, TopK: 10})
		if err != nil {
			t.Fatalf("Retrieve(k=%v): %v", k, err)
		}
		return out
	}
	def, explicit := run(0), run(60)
	if len(def) != len(explicit) {
		t.Fatalf("lengths differ: %d vs %d", len(def), len(explicit))
	}
	for i := range def {
		if def[i].Chunk.ID != explicit[i].Chunk.ID || def[i].Score != explicit[i].Score {
			t.Fatalf("hit %d differs: %+v vs %+v (zero RRFConstant must equal 60)", i, def[i], explicit[i])
		}
	}
}

func TestHybridRetrieverRRFConstantConfigurable(t *testing.T) {
	const q = "configurable query"
	build := func(k float64) []store.Hit {
		dense := &recordingRetriever{hitsByQ: map[string][]store.Hit{q: hitList("x", "p", "q", "r", "y")}}
		lexical := &recordingRetriever{hitsByQ: map[string][]store.Hit{q: hitList("a", "b", "c", "d", "y")}}
		out, _, err := HybridRetriever{Dense: dense, Lexical: lexical, RRFConstant: k}.Retrieve(
			context.Background(), Request{Query: q, TopK: 10})
		if err != nil {
			t.Fatalf("Retrieve(k=%v): %v", k, err)
		}
		return out
	}
	// Default k=60: y (rank 5 in both signals) outranks x (rank 1 in one).
	def := build(0)
	if indexOfHit(def, "y") >= indexOfHit(def, "x") {
		t.Fatalf("k=60: want y before x, got %+v", def)
	}
	// Small k=1: the rank-1 vs rank-5 gap dominates, so x outranks y.
	small := build(1)
	if indexOfHit(small, "x") >= indexOfHit(small, "y") {
		t.Fatalf("k=1: want x before y, got %+v", small)
	}
}
