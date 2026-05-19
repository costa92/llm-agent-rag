package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/store"
)

// plainStore wraps a store.Store, exposing only the store.Store method
// set — it deliberately does not implement store.GraphStore.
type plainStore struct{ store.Store }

func TestImportExtractsGraph(t *testing.T) {
	sys := New(Options{
		Model: fakeModel{},
		EntityExtractor: graph.DictionaryEntityExtractor{Terms: map[string]string{
			"Paris":  "city",
			"France": "country",
			"Berlin": "city",
		}},
	})
	res, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
		{ID: "doc2", Content: "Berlin is a city; France borders Germany."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Graph == nil {
		t.Fatalf("ImportResult.Graph is nil, want an extracted graph")
	}
	got := map[string]bool{}
	for _, e := range res.Graph.Entities {
		got[e.ID] = true
	}
	for _, id := range []string{"city:paris", "country:france", "city:berlin"} {
		if !got[id] {
			t.Fatalf("graph missing entity %q: %+v", id, res.Graph.Entities)
		}
	}
	for _, e := range res.Graph.Entities {
		if e.ID == "country:france" && len(e.SourceChunkIDs) < 2 {
			t.Fatalf("France provenance = %v, want >= 2 chunks (it appears in both docs)", e.SourceChunkIDs)
		}
	}
}

func TestImportNoExtractorLeavesGraphNil(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	res, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Graph != nil {
		t.Fatalf("ImportResult.Graph = %+v, want nil without an extractor", res.Graph)
	}
}

func TestImportPersistsGraphToStore(t *testing.T) {
	st := store.NewInMemoryStore(32)
	sys := New(Options{
		Model: fakeModel{},
		Store: st,
		EntityExtractor: graph.DictionaryEntityExtractor{Terms: map[string]string{
			"Paris": "city", "France": "country",
		}},
	})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	found, err := st.FindEntities(context.Background(), "geo", []string{"Paris", "France"})
	if err != nil {
		t.Fatalf("FindEntities: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("store graph has %d of [Paris, France], want 2 (graph persisted)", len(found))
	}
}

func TestImportReingestReconcilesGraph(t *testing.T) {
	st := store.NewInMemoryStore(32)
	sys := New(Options{
		Model: fakeModel{},
		Store: st,
		EntityExtractor: graph.DictionaryEntityExtractor{Terms: map[string]string{
			"Paris": "city", "Berlin": "city",
		}},
	})
	ctx := context.Background()
	if _, err := sys.Import(ctx, []ingest.Document{
		{ID: "doc1", SourceID: "src1", Content: "Paris is lovely."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import #1: %v", err)
	}
	if got, _ := st.FindEntities(ctx, "geo", []string{"Paris"}); len(got) != 1 {
		t.Fatalf("after import #1: Paris should be in the graph")
	}
	// Re-ingest the same source; new content drops Paris, adds Berlin.
	if _, err := sys.Import(ctx, []ingest.Document{
		{ID: "doc1", SourceID: "src1", Content: "Berlin is lovely."},
	}, ingest.ImportOptions{Namespace: "geo", ReplaceSource: true}); err != nil {
		t.Fatalf("Import #2 (re-ingest): %v", err)
	}
	if got, _ := st.FindEntities(ctx, "geo", []string{"Paris"}); len(got) != 0 {
		t.Fatalf("after re-ingest: Paris still in graph, want it reconciled away: %+v", got)
	}
	if got, _ := st.FindEntities(ctx, "geo", []string{"Berlin"}); len(got) != 1 {
		t.Fatalf("after re-ingest: Berlin should be in the graph")
	}
}

func TestImportNoGraphStoreDegradesGracefully(t *testing.T) {
	st := plainStore{Store: store.NewInMemoryStore(32)}
	sys := New(Options{
		Model:           fakeModel{},
		Store:           st,
		EntityExtractor: graph.DictionaryEntityExtractor{Terms: map[string]string{"Paris": "city"}},
	})
	res, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import with a non-GraphStore store: %v", err)
	}
	if res.Graph == nil {
		t.Fatalf("res.Graph is nil — the graph should still be produced even when the store cannot persist it")
	}
}
