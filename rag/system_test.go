package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
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
