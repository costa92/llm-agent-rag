package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/store"
)

// TestImportDetectsCommunities verifies that, with a store implementing
// store.CommunityStore and an Options.CommunityDetector, Import detects a
// community hierarchy after persisting the graph: ImportResult.Graph.
// Communities is populated and the store's Communities(ns) matches.
func TestImportDetectsCommunities(t *testing.T) {
	st := store.NewInMemoryStore(32)
	sys := New(Options{
		Model: fakeModel{},
		Store: st,
		EntityExtractor: graph.DictionaryEntityExtractor{Terms: map[string]string{
			"Paris":  "city",
			"France": "country",
			"Berlin": "city",
			"Spain":  "country",
		}},
		CommunityDetector: graph.LouvainDetector{},
	})
	ctx := context.Background()
	res, err := sys.Import(ctx, []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
		{ID: "doc2", Content: "Berlin is a city in Spain."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Graph == nil {
		t.Fatalf("ImportResult.Graph is nil, want an extracted graph")
	}
	if len(res.Graph.Communities) == 0 {
		t.Fatalf("ImportResult.Graph.Communities is empty, want a detected hierarchy")
	}
	// The store must hold the same set the result reports.
	stored, err := st.Communities(ctx, "geo")
	if err != nil {
		t.Fatalf("Communities: %v", err)
	}
	if len(stored) != len(res.Graph.Communities) {
		t.Fatalf("store has %d communities, ImportResult reports %d",
			len(stored), len(res.Graph.Communities))
	}
	for i := range stored {
		if stored[i].ID != res.Graph.Communities[i].ID {
			t.Fatalf("community %d: store ID %q != result ID %q",
				i, stored[i].ID, res.Graph.Communities[i].ID)
		}
	}
}

// TestImportReingestRedetectsCommunities verifies KG3-7: a ReplaceSource
// re-ingest re-detects the whole namespace's communities. After the
// re-ingest the stored set reflects the new graph — it is not stale and
// not the old set appended to.
func TestImportReingestRedetectsCommunities(t *testing.T) {
	st := store.NewInMemoryStore(32)
	sys := New(Options{
		Model: fakeModel{},
		Store: st,
		EntityExtractor: graph.DictionaryEntityExtractor{Terms: map[string]string{
			"Paris":  "city",
			"France": "country",
			"Berlin": "city",
			"Spain":  "country",
		}},
		CommunityDetector: graph.LouvainDetector{},
	})
	ctx := context.Background()
	if _, err := sys.Import(ctx, []ingest.Document{
		{ID: "doc1", SourceID: "src1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import #1: %v", err)
	}
	before, err := st.Communities(ctx, "geo")
	if err != nil {
		t.Fatalf("Communities before: %v", err)
	}
	if len(before) == 0 {
		t.Fatalf("after import #1: expected detected communities")
	}
	if !communitiesMention(before, "city:paris") {
		t.Fatalf("after import #1: communities should mention city:paris, got %+v", before)
	}

	// Re-ingest the same source with new content: Paris/France drop out,
	// Berlin/Spain take their place.
	if _, err := sys.Import(ctx, []ingest.Document{
		{ID: "doc1", SourceID: "src1", Content: "Berlin is a city in Spain."},
	}, ingest.ImportOptions{Namespace: "geo", ReplaceSource: true}); err != nil {
		t.Fatalf("Import #2 (re-ingest): %v", err)
	}
	after, err := st.Communities(ctx, "geo")
	if err != nil {
		t.Fatalf("Communities after: %v", err)
	}
	if communitiesMention(after, "city:paris") {
		t.Fatalf("after re-ingest: communities still mention city:paris — stale, want re-detected: %+v", after)
	}
	if !communitiesMention(after, "city:berlin") {
		t.Fatalf("after re-ingest: communities should mention city:berlin (re-detected): %+v", after)
	}
	// Re-detection is replace-all: the namespace must not accumulate the old
	// entity set on top of the new one.
	for _, c := range after {
		for _, id := range c.EntityIDs {
			if id == "city:paris" || id == "country:france" {
				t.Fatalf("after re-ingest: community %s still carries stale entity %q: %+v", c.ID, id, after)
			}
		}
	}
}

// TestImportNoDetectorLeavesCommunitiesEmpty verifies graceful degradation:
// with a CommunityStore but no Options.CommunityDetector, Import runs with
// no detection and no error.
func TestImportNoDetectorLeavesCommunitiesEmpty(t *testing.T) {
	st := store.NewInMemoryStore(32)
	sys := New(Options{
		Model: fakeModel{},
		Store: st,
		EntityExtractor: graph.DictionaryEntityExtractor{Terms: map[string]string{
			"Paris": "city",
		}},
		// No CommunityDetector configured.
	})
	ctx := context.Background()
	res, err := sys.Import(ctx, []ingest.Document{
		{ID: "doc1", Content: "Paris."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import without a detector: %v", err)
	}
	if res.Graph != nil && len(res.Graph.Communities) != 0 {
		t.Fatalf("res.Graph.Communities = %+v, want empty without a detector", res.Graph.Communities)
	}
	stored, err := st.Communities(ctx, "geo")
	if err != nil {
		t.Fatalf("Communities: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("store has %d communities, want 0 without a detector", len(stored))
	}
}

// TestImportNoCommunityStoreDegradesGracefully verifies graceful degradation
// when the store does not implement store.CommunityStore: detection is
// skipped, no error, the graph is still produced.
func TestImportNoCommunityStoreDegradesGracefully(t *testing.T) {
	st := plainStore{Store: store.NewInMemoryStore(32)}
	sys := New(Options{
		Model:             fakeModel{},
		Store:             st,
		EntityExtractor:   graph.DictionaryEntityExtractor{Terms: map[string]string{"Paris": "city"}},
		CommunityDetector: graph.LouvainDetector{},
	})
	res, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import with a non-CommunityStore store: %v", err)
	}
	if res.Graph == nil {
		t.Fatalf("res.Graph is nil — the graph should still be produced even without a CommunityStore")
	}
	if len(res.Graph.Communities) != 0 {
		t.Fatalf("res.Graph.Communities = %+v, want empty without a CommunityStore", res.Graph.Communities)
	}
}

// communitiesMention reports whether any community in cs carries the given
// entity ID among its members.
func communitiesMention(cs []graph.Community, entityID string) bool {
	for _, c := range cs {
		for _, id := range c.EntityIDs {
			if id == entityID {
				return true
			}
		}
	}
	return false
}
