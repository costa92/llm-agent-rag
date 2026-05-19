package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/retrieve"
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

// TestAskSurfacesGraphPaths verifies that a path-mode retrieve.GraphRetriever
// wired into a System surfaces its ranked paths and evidence subgraph through
// Answer.Diagnostics.GraphTrace — the new v0.9 GraphTrace fields ride through
// rag.Diagnostics for free.
func TestAskSurfacesGraphPaths(t *testing.T) {
	st := store.NewInMemoryStore(2)
	ctx := context.Background()
	if err := st.Upsert(ctx, []store.StoredChunk{
		{ID: "c1", Namespace: "ns", Content: "alpha text", Vector: embed.Vector{0, 0}},
		{ID: "c2", Namespace: "ns", Content: "bravo text", Vector: embed.Vector{0, 0}},
		{ID: "c3", Namespace: "ns", Content: "charlie text", Vector: embed.Vector{0, 0}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	g := graph.Graph{
		Entities: []graph.Entity{
			{ID: "t:alpha", Name: "Alpha", Type: "t", SourceChunkIDs: []string{"c1"}},
			{ID: "t:bravo", Name: "Bravo", Type: "t", SourceChunkIDs: []string{"c2"}},
			{ID: "t:charlie", Name: "Charlie", Type: "t", SourceChunkIDs: []string{"c3"}},
		},
		Relations: []graph.Relation{
			{ID: "t:alpha::r::t:bravo", Source: "t:alpha", Target: "t:bravo", Relation: "r", SourceChunkIDs: []string{"c1"}, Weight: 1},
			{ID: "t:bravo::r::t:charlie", Source: "t:bravo", Target: "t:charlie", Relation: "r", SourceChunkIDs: []string{"c2"}, Weight: 1},
		},
	}
	if err := st.UpsertGraph(ctx, "ns", g); err != nil {
		t.Fatalf("UpsertGraph: %v", err)
	}

	sys := New(Options{
		Model: fakeModel{},
		Store: st,
		Retriever: retrieve.GraphRetriever{
			Store:      st,
			MaxDepth:   2,
			PathRanker: graph.WeightedPathRanker{},
		},
	})
	// "Alpha Charlie" links to the two chain endpoints, so the retriever
	// ranks the simple path connecting them.
	ans, err := sys.Ask(ctx, "Alpha Charlie", AskOptions{
		Search: SearchOptions{Namespace: "ns", TopK: 10},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	gt := ans.Diagnostics.GraphTrace
	if len(gt.Paths) == 0 {
		t.Fatalf("Diagnostics.GraphTrace.Paths empty — path mode did not surface")
	}
	if gt.EvidenceSubgraph == nil {
		t.Fatalf("Diagnostics.GraphTrace.EvidenceSubgraph is nil")
	}
	if len(gt.EvidenceSubgraph.Entities) != 3 {
		t.Fatalf("EvidenceSubgraph has %d entities, want 3", len(gt.EvidenceSubgraph.Entities))
	}
	p := gt.Paths[0]
	if len(p.EntityIDs) < 2 || p.EntityIDs[0] != "t:alpha" ||
		p.EntityIDs[len(p.EntityIDs)-1] != "t:charlie" {
		t.Fatalf("top ranked path = %v, want one connecting t:alpha .. t:charlie", p.EntityIDs)
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
