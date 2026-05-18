package rag

import (
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/pack"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

type fakeModel struct{}

func (fakeModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	return generate.Response{Text: req.Messages[0].Content}, nil
}

func TestSystemImportRetrieveAsk(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is in France."},
		{ID: "doc2", Content: "Berlin is in Germany."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	hits, err := sys.Retrieve(context.Background(), "Paris France", SearchOptions{Namespace: "geo", TopK: 1})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("len(hits) = %d, want 1", len(hits))
	}
	ans, err := sys.Ask(context.Background(), "Where is Paris?", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Text == "" || len(ans.Hits) != 1 || len(ans.Prompt.Messages) != 1 {
		t.Fatalf("Answer = %+v", ans)
	}
	if len(ans.Citations) != 1 {
		t.Fatalf("len(ans.Citations) = %d, want 1", len(ans.Citations))
	}
	if ans.Diagnostics.HitCount != 1 {
		t.Fatalf("ans.Diagnostics.HitCount = %d, want 1", ans.Diagnostics.HitCount)
	}
	if ans.Trace.Question != "Where is Paris?" {
		t.Fatalf("ans.Trace.Question = %q, want original question", ans.Trace.Question)
	}
}

func TestSystemImportFrom(t *testing.T) {
	sys := New(Options{})
	_, err := sys.ImportFrom(context.Background(), ingest.StaticSource(
		ingest.Document{ID: "doc1", Content: "hello world"},
	), ingest.ImportOptions{})
	if err != nil {
		t.Fatalf("ImportFrom(): %v", err)
	}
}

func TestAskRequiresModel(t *testing.T) {
	sys := New(Options{})
	_, err := sys.Ask(context.Background(), "q", AskOptions{})
	if err != ErrModelRequired {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

func TestSystemRetrieveSecurityFilters(t *testing.T) {
	sys := New(Options{})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "Paris is in France.",
			Metadata: map[string]any{
				"tenant": "a",
			},
		},
		{
			ID:      "doc2",
			Content: "Paris travel guide.",
			Metadata: map[string]any{
				"tenant": "b",
			},
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	hits, err := sys.Retrieve(context.Background(), "Paris", SearchOptions{
		Namespace: "geo",
		TopK:      5,
		SecurityFilters: map[string]any{
			"tenant": "a",
		},
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("len(hits) = %d, want 1", len(hits))
	}
	if hits[0].Chunk.Metadata["tenant"] != "a" {
		t.Fatalf("hit tenant = %v, want a", hits[0].Chunk.Metadata["tenant"])
	}
}

func TestAskCarriesTraceAndFilters(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "Paris is in France.",
			Metadata: map[string]any{
				"tenant": "a",
				"lang":   "en",
			},
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "Where is Paris?", AskOptions{
		Search: SearchOptions{
			Namespace: "geo",
			TopK:      3,
			Filters: map[string]any{
				"lang": "en",
			},
			SecurityFilters: map[string]any{
				"tenant": "a",
			},
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Trace.Namespace != "geo" {
		t.Fatalf("ans.Trace.Namespace = %q, want geo", ans.Trace.Namespace)
	}
	if ans.Trace.TopK != 3 {
		t.Fatalf("ans.Trace.TopK = %d, want 3", ans.Trace.TopK)
	}
	if ans.Trace.Filters["lang"] != "en" {
		t.Fatalf("ans.Trace.Filters = %+v, want lang=en", ans.Trace.Filters)
	}
	if ans.Trace.SecurityFilters["tenant"] != "a" {
		t.Fatalf("ans.Trace.SecurityFilters = %+v, want tenant=a", ans.Trace.SecurityFilters)
	}
	if len(ans.Trace.SelectedChunkIDs) != 1 {
		t.Fatalf("len(ans.Trace.SelectedChunkIDs) = %d, want 1", len(ans.Trace.SelectedChunkIDs))
	}
}

func TestImportPreservesLineageMetadataIntoStore(t *testing.T) {
	mem := store.NewInMemoryStore(32)
	sys := New(Options{Store: mem})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:               "doc1",
			Content:          "Paris is in France.",
			SourceID:         "knowledge-base",
			Version:          "2026-05-14",
			Checksum:         "sha256:abc",
			EmbeddingVersion: "hash-32-v1",
			Metadata: map[string]any{
				"lang": "en",
			},
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	chunk, err := mem.Get(context.Background(), "doc1:0")
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if chunk.Metadata[ingest.MetadataSourceIDKey] != "knowledge-base" {
		t.Fatalf("source_id = %v, want knowledge-base", chunk.Metadata[ingest.MetadataSourceIDKey])
	}
	if chunk.Metadata[ingest.MetadataVersionKey] != "2026-05-14" {
		t.Fatalf("version = %v, want 2026-05-14", chunk.Metadata[ingest.MetadataVersionKey])
	}
	if chunk.Metadata[ingest.MetadataChecksumKey] != "sha256:abc" {
		t.Fatalf("checksum = %v, want sha256:abc", chunk.Metadata[ingest.MetadataChecksumKey])
	}
	if chunk.Metadata[ingest.MetadataEmbeddingVersionKey] != "hash-32-v1" {
		t.Fatalf("embedding_version = %v, want hash-32-v1", chunk.Metadata[ingest.MetadataEmbeddingVersionKey])
	}
	if chunk.Metadata["lang"] != "en" {
		t.Fatalf("lang = %v, want en", chunk.Metadata["lang"])
	}
}

func TestImportReplaceSourceRemovesPreviousChunks(t *testing.T) {
	mem := store.NewInMemoryStore(32)
	sys := New(Options{Store: mem})

	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:       "doc1",
			Content:  "Old alpha content",
			SourceID: "source-alpha",
		},
	}, ingest.ImportOptions{Namespace: "docs"})
	if err != nil {
		t.Fatalf("first Import(): %v", err)
	}

	_, err = sys.Import(context.Background(), []ingest.Document{
		{
			ID:       "doc2",
			Content:  "New alpha content",
			SourceID: "source-alpha",
		},
	}, ingest.ImportOptions{Namespace: "docs", ReplaceSource: true})
	if err != nil {
		t.Fatalf("second Import(): %v", err)
	}

	if _, err := mem.Get(context.Background(), "doc1:0"); err == nil {
		t.Fatalf("old chunk still exists after ReplaceSource import")
	}
	chunk, err := mem.Get(context.Background(), "doc2:0")
	if err != nil {
		t.Fatalf("Get(new chunk): %v", err)
	}
	if chunk.Metadata[ingest.MetadataSourceIDKey] != "source-alpha" {
		t.Fatalf("source_id = %v, want source-alpha", chunk.Metadata[ingest.MetadataSourceIDKey])
	}
}

type rewritePreprocessor struct{}

func (rewritePreprocessor) Process(_ context.Context, req retrieve.Request) (retrieve.PreprocessResult, error) {
	return retrieve.PreprocessResult{
		QueryVariants: []string{"france capital"},
		Trace: retrieve.Trace{
			OriginalQuery:  req.Query,
			EffectiveQuery: "france capital",
			QueryVariants:  []string{"france capital"},
		},
	}, nil
}

func TestRetrieveUsesConfiguredPreprocessor(t *testing.T) {
	sys := New(Options{Preprocessor: rewritePreprocessor{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
		{ID: "doc2", Content: "Berlin is the capital of Germany."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	hits, err := sys.Retrieve(context.Background(), "what is the capital of france", SearchOptions{
		Namespace: "geo",
		TopK:      1,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("len(hits) = %d, want 1", len(hits))
	}
	if hits[0].Chunk.DocID != "doc1" {
		t.Fatalf("top hit doc = %s, want doc1", hits[0].Chunk.DocID)
	}
}

func TestAskReranksAndPacksContext(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "general travel guide"},
		{ID: "doc2", Content: "Paris is the capital of France and has museums cafes boulevards"},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{
			Namespace:    "geo",
			TopK:         2,
			EnableRerank: true,
		},
		MaxTokens: 10,
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Trace.RerankedChunkIDs) == 0 || ans.Trace.RerankedChunkIDs[0] != "doc2:0" {
		t.Fatalf("reranked ids = %#v, want doc2 first", ans.Trace.RerankedChunkIDs)
	}
	if len(ans.Diagnostics.PromptChunkIDs) == 0 || ans.Diagnostics.PromptChunkIDs[0] != "doc2:0" {
		t.Fatalf("prompt chunk ids = %#v, want doc2 included", ans.Diagnostics.PromptChunkIDs)
	}
	if !strings.Contains(ans.Prompt.Messages[0].Content, "doc2:0") {
		t.Fatalf("prompt missing packed chunk doc2: %q", ans.Prompt.Messages[0].Content)
	}
}

func TestAskPopulatesRerankScores(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "general travel guide"},
		{ID: "doc2", Content: "Paris is the capital of France and has museums cafes"},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	withRerank, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:    SearchOptions{Namespace: "geo", TopK: 2, EnableRerank: true},
		MaxTokens: 10,
	})
	if err != nil {
		t.Fatalf("Ask(rerank on): %v", err)
	}
	if len(withRerank.Diagnostics.RerankScores) == 0 {
		t.Fatalf("RerankScores empty, want per-hit rerank detail when EnableRerank is true")
	}
	for _, s := range withRerank.Diagnostics.RerankScores {
		if s.ChunkID == "" || s.OutputRank == 0 {
			t.Fatalf("rerank score %+v incomplete", s)
		}
	}
	noRerank, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 2},
	})
	if err != nil {
		t.Fatalf("Ask(rerank off): %v", err)
	}
	if noRerank.Diagnostics.RerankScores != nil {
		t.Fatalf("RerankScores = %+v, want nil when EnableRerank is false", noRerank.Diagnostics.RerankScores)
	}
}

func TestAskCanUseCustomPacker(t *testing.T) {
	sys := New(Options{
		Model:  fakeModel{},
		Packer: fixedPacker{},
	})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
		{ID: "doc2", Content: "Berlin is the capital of Germany."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "Where is Paris?", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 2},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Hits) != 1 || ans.Hits[0].Chunk.ID != "doc1:0" {
		t.Fatalf("hits = %+v, want only doc1:0", ans.Hits)
	}
	if len(ans.Trace.DroppedChunkIDs) == 0 || ans.Trace.DroppedChunkIDs[0] != "doc2:0" {
		t.Fatalf("dropped ids = %#v, want doc2 dropped", ans.Trace.DroppedChunkIDs)
	}
}

func TestStructureAwareRetrieveAndAskTrace(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Splitter: ingest.NewMarkdownSplitter(500, 50)})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "# Cities\nParis is in France.\n## Travel\nMuseums and cafes.\n## History\nAncient capital notes.",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	hits, err := sys.Retrieve(context.Background(), "travel paris", SearchOptions{
		Namespace:       "geo",
		TopK:            2,
		EnableStructure: true,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected structure-aware hits")
	}
	if len(hits[0].Chunk.SectionPath) == 0 {
		t.Fatalf("top hit missing section path: %+v", hits[0].Chunk)
	}
	foundTravel := false
	for _, hit := range hits {
		if strings.Join(hit.Chunk.SectionPath, " > ") == "Cities > Travel" {
			foundTravel = true
			break
		}
	}
	if !foundTravel {
		t.Fatalf("structure hits missing travel section: %+v", hits)
	}

	ans, err := sys.Ask(context.Background(), "Where should I travel in Paris?", AskOptions{
		Search: SearchOptions{
			Namespace:           "geo",
			TopK:                3,
			EnableStructure:     true,
			EnableTreeExpansion: true,
			ExpansionDepth:      3,
			EnableRerank:        true,
		},
		MaxTokens: 50,
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Trace.MatchedSections) == 0 {
		t.Fatalf("matched sections = %#v, want non-empty", ans.Trace.MatchedSections)
	}
	if len(ans.Trace.SearchPath) == 0 {
		t.Fatalf("search path = %#v, want non-empty", ans.Trace.SearchPath)
	}
	if len(ans.Trace.ExpandedSections) == 0 {
		t.Fatalf("expanded sections = %#v, want non-empty", ans.Trace.ExpandedSections)
	}
	if len(ans.Trace.ExpandedChunkIDs) == 0 {
		t.Fatalf("expanded chunk ids = %#v, want non-empty", ans.Trace.ExpandedChunkIDs)
	}
	if len(ans.Diagnostics.ExpandedChunkIDs) == 0 {
		t.Fatalf("diagnostics expanded chunk ids = %#v, want non-empty", ans.Diagnostics.ExpandedChunkIDs)
	}
	if len(ans.Citations) == 0 || len(ans.Citations[0].SectionPath) == 0 {
		t.Fatalf("citations = %+v, want section path", ans.Citations)
	}
	if !strings.Contains(ans.Prompt.Messages[0].Content, "Cities >") {
		t.Fatalf("prompt missing structured path prefix: %q", ans.Prompt.Messages[0].Content)
	}
}

func TestAskCarriesRoutePathAndConstrainsSubtree(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Splitter: ingest.NewMarkdownSplitter(500, 50)})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "# Cities\nParis overview.\n## Travel\nMuseums and cafes.\n## History\nAncient capital notes.",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "Paris notes", AskOptions{
		Search: SearchOptions{
			Namespace:           "geo",
			TopK:                3,
			RoutePath:           []string{"Cities", "Travel"},
			EnableStructure:     true,
			EnableTreeExpansion: true,
			ExpansionDepth:      3,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if !pathEquals(ans.Trace.RoutePath, []string{"Cities", "Travel"}) {
		t.Fatalf("trace route path = %#v, want travel route", ans.Trace.RoutePath)
	}
	for _, hit := range ans.Hits {
		if strings.Join(hit.Chunk.SectionPath, " > ") != "Cities > Travel" {
			t.Fatalf("hit path = %q, want only travel subtree", strings.Join(hit.Chunk.SectionPath, " > "))
		}
	}
}

func TestAskCarriesAutoRoutePathAndConstrainsSubtree(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Splitter: ingest.NewMarkdownSplitter(500, 50)})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "# Cities\nParis overview.\n## Travel\nMuseums and cafes.\n## History\nAncient capital notes.",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "travel museums", AskOptions{
		Search: SearchOptions{
			Namespace:           "geo",
			TopK:                3,
			EnableAutoRoute:     true,
			AutoRouteMinScore:   2,
			EnableStructure:     true,
			EnableTreeExpansion: true,
			ExpansionDepth:      3,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if !pathEquals(ans.Trace.RoutePath, []string{"Cities", "Travel"}) {
		t.Fatalf("trace route path = %#v, want travel route", ans.Trace.RoutePath)
	}
	if !pathEquals(ans.Trace.AutoRoutePath, []string{"Cities", "Travel"}) {
		t.Fatalf("trace auto route path = %#v, want travel route", ans.Trace.AutoRoutePath)
	}
	if len(ans.Trace.AutoRouteCandidates) == 0 {
		t.Fatalf("trace auto route candidates = %#v, want non-empty", ans.Trace.AutoRouteCandidates)
	}
	if len(ans.Diagnostics.AutoRouteCandidates) == 0 {
		t.Fatalf("diagnostics auto route candidates = %#v, want non-empty", ans.Diagnostics.AutoRouteCandidates)
	}
	for _, hit := range ans.Hits {
		if strings.Join(hit.Chunk.SectionPath, " > ") != "Cities > Travel" {
			t.Fatalf("hit path = %q, want only auto-routed travel subtree", strings.Join(hit.Chunk.SectionPath, " > "))
		}
	}
}

func TestAskCarriesMergedAutoRouteCandidatesAcrossVariants(t *testing.T) {
	sys := New(Options{
		Model:        fakeModel{},
		Splitter:     ingest.NewMarkdownSplitter(500, 50),
		Preprocessor: rewriteVariantPreprocessor{},
	})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "# Cities\nParis overview.\n## Travel\nMuseums and cafes.\n## History\nAncient capital notes.",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "route planner", AskOptions{
		Search: SearchOptions{
			Namespace:              "geo",
			TopK:                   3,
			EnableAutoRoute:        true,
			AutoRouteMinScore:      2,
			AutoRouteMaxCandidates: 2,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Trace.AutoRouteCandidates) < 2 {
		t.Fatalf("trace auto route candidates = %#v, want merged candidates", ans.Trace.AutoRouteCandidates)
	}
}

func TestAskCanFanoutAcrossTopRouteCandidates(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Splitter: ingest.NewMarkdownSplitter(500, 50)})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "# Cities\nParis overview.\n## Travel\nMuseums and cafes.\n## History\nHistory museums and capital notes.",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "travel history museums", AskOptions{
		Search: SearchOptions{
			Namespace:                    "geo",
			TopK:                         5,
			EnableAutoRoute:              true,
			AutoRouteMinScore:            2,
			AutoRouteMaxCandidates:       2,
			AutoRouteConfidenceThreshold: 0.5,
			AutoRouteFanout:              2,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	foundTravel := false
	foundHistory := false
	for _, hit := range ans.Hits {
		if pathEquals(hit.Chunk.SectionPath, []string{"Cities", "Travel"}) {
			foundTravel = true
		}
		if pathEquals(hit.Chunk.SectionPath, []string{"Cities", "History"}) {
			foundHistory = true
		}
	}
	if !foundTravel || !foundHistory {
		t.Fatalf("hits = %+v, want travel and history fanout hits", ans.Hits)
	}
	if ans.Trace.RoutePolicy.Mode != "fanout" {
		t.Fatalf("trace route policy mode = %q, want fanout", ans.Trace.RoutePolicy.Mode)
	}
	if ans.Trace.RoutePolicy.SelectedCount != 2 {
		t.Fatalf("trace route policy selected count = %d, want 2", ans.Trace.RoutePolicy.SelectedCount)
	}
	if len(ans.Trace.RoutePolicy.Rationale) == 0 {
		t.Fatalf("trace route policy rationale = %#v, want non-empty", ans.Trace.RoutePolicy.Rationale)
	}
	if ans.Diagnostics.RoutePolicy.Mode != "fanout" {
		t.Fatalf("diagnostics route policy mode = %q, want fanout", ans.Diagnostics.RoutePolicy.Mode)
	}
}

func TestAskConvergesWhenConfidenceGapDominates(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Splitter: ingest.NewMarkdownSplitter(500, 50)})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "# Cities\nParis overview.\n## Travel museums\nWalking tour notes.\n## History museums\nLegacy notes.",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "travel museums", AskOptions{
		Search: SearchOptions{
			Namespace:                    "geo",
			TopK:                         5,
			EnableAutoRoute:              true,
			AutoRouteMinScore:            2,
			AutoRouteMaxCandidates:       2,
			AutoRouteConfidenceThreshold: 0.4,
			AutoRouteFanout:              2,
			AutoRouteConfidenceGap:       0.3,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Trace.RoutePolicy.Mode != "converged" {
		t.Fatalf("trace route policy mode = %q, want converged", ans.Trace.RoutePolicy.Mode)
	}
	if ans.Trace.RoutePolicy.SelectedCount != 1 {
		t.Fatalf("trace route policy selected count = %d, want 1", ans.Trace.RoutePolicy.SelectedCount)
	}
	if ans.Trace.RoutePolicy.ConfidenceGap != 0.3 {
		t.Fatalf("trace route policy confidence gap = %v, want 0.3", ans.Trace.RoutePolicy.ConfidenceGap)
	}
	if ans.Trace.RoutePolicy.Gap <= 0 {
		t.Fatalf("trace route policy gap = %v, want > 0", ans.Trace.RoutePolicy.Gap)
	}
	hasConverged := false
	for _, line := range ans.Trace.RoutePolicy.Rationale {
		if strings.HasPrefix(line, "converged:") {
			hasConverged = true
			break
		}
	}
	if !hasConverged {
		t.Fatalf("trace route policy rationale = %#v, want converged: entry", ans.Trace.RoutePolicy.Rationale)
	}
	if ans.Diagnostics.RoutePolicy.Mode != "converged" {
		t.Fatalf("diagnostics route policy mode = %q, want converged", ans.Diagnostics.RoutePolicy.Mode)
	}
	if ans.Diagnostics.RoutePolicy.Gap != ans.Trace.RoutePolicy.Gap {
		t.Fatalf("diagnostics gap = %v, want trace gap %v", ans.Diagnostics.RoutePolicy.Gap, ans.Trace.RoutePolicy.Gap)
	}
	for _, hit := range ans.Hits {
		if pathEquals(hit.Chunk.SectionPath, []string{"Cities", "History museums"}) {
			t.Fatalf("hits = %+v, history museums route should not be queried when converged", ans.Hits)
		}
	}
}

func TestAskExposesPerRouteSearchTrajectory(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Splitter: ingest.NewMarkdownSplitter(500, 50)})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "# Cities\nParis overview.\n## Travel\nMuseums and cafes.\n## History\nHistory museums and capital notes.",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "travel history museums", AskOptions{
		Search: SearchOptions{
			Namespace:                    "geo",
			TopK:                         5,
			EnableAutoRoute:              true,
			AutoRouteMinScore:            2,
			AutoRouteMaxCandidates:       2,
			AutoRouteConfidenceThreshold: 0.5,
			AutoRouteFanout:              2,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Trace.SearchTrajectory) != 2 {
		t.Fatalf("trace search trajectory len = %d, want 2 fanout steps", len(ans.Trace.SearchTrajectory))
	}
	if len(ans.Diagnostics.SearchTrajectory) != len(ans.Trace.SearchTrajectory) {
		t.Fatalf("diagnostics trajectory len = %d, want %d", len(ans.Diagnostics.SearchTrajectory), len(ans.Trace.SearchTrajectory))
	}
	for i, step := range ans.Trace.SearchTrajectory {
		if step.HitCount == 0 {
			t.Fatalf("trajectory step %d route=%v has zero hits", i, step.Route)
		}
		if len(step.HitIDs) != step.HitCount {
			t.Fatalf("trajectory step %d hit count = %d but ids = %#v", i, step.HitCount, step.HitIDs)
		}
		if step.Mode != "fanout" {
			t.Fatalf("trajectory step %d mode = %q, want fanout", i, step.Mode)
		}
		mirror := ans.Diagnostics.SearchTrajectory[i]
		if mirror.HitCount != step.HitCount {
			t.Fatalf("diagnostics trajectory step %d hit count = %d, trace had %d", i, mirror.HitCount, step.HitCount)
		}
		if !pathEquals(mirror.Route, step.Route) {
			t.Fatalf("diagnostics trajectory step %d route = %#v, trace had %#v", i, mirror.Route, step.Route)
		}
	}
}

type fixedPacker struct{}

type rewriteVariantPreprocessor struct{}

func (rewriteVariantPreprocessor) Process(_ context.Context, req retrieve.Request) (retrieve.PreprocessResult, error) {
	return retrieve.PreprocessResult{
		QueryVariants: []string{"travel museums", "history notes"},
		Trace: retrieve.Trace{
			OriginalQuery:  req.Query,
			EffectiveQuery: "travel museums",
			QueryVariants:  []string{"travel museums", "history notes"},
		},
	}, nil
}

func (fixedPacker) Pack(_ context.Context, req pack.Request) (pack.Result, error) {
	selected := make([]store.Hit, 0, 1)
	if len(req.Hits) > 0 {
		selected = append(selected, req.Hits[0])
	}
	dropped := make([]string, 0, len(req.Hits))
	for _, hit := range req.Hits[1:] {
		dropped = append(dropped, hit.Chunk.ID)
	}
	return pack.Result{
		Hits: selected,
		Trace: pack.Trace{
			SelectedChunkIDs: chunkIDs(selected),
			DroppedChunkIDs:  dropped,
		},
	}, nil
}

func pathEquals(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
