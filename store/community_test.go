package store_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/store"
	"github.com/costa92/llm-agent-rag/store/storetest"
)

func TestInMemoryStoreCommunityConformance(t *testing.T) {
	storetest.RunCommunityConformance(t, func(t *testing.T) store.Store {
		return store.NewInMemoryStore(2)
	})
}

// TestCommunityDetectedHierarchyRoundTrip drives a real LouvainDetector over
// a graph snapshot read out of the store, then persists the detected
// hierarchy and reads it back — it must survive byte-identically.
func TestCommunityDetectedHierarchyRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore(2)

	// Two triangles joined by a single bridge edge — a graph with a clear
	// two-cluster community structure for Louvain to find.
	g := graph.Graph{
		Entities: []graph.Entity{
			{ID: "t:a", Name: "A", Type: "t", SourceChunkIDs: []string{"c1"}},
			{ID: "t:b", Name: "B", Type: "t", SourceChunkIDs: []string{"c1"}},
			{ID: "t:c", Name: "C", Type: "t", SourceChunkIDs: []string{"c1"}},
			{ID: "t:d", Name: "D", Type: "t", SourceChunkIDs: []string{"c1"}},
			{ID: "t:e", Name: "E", Type: "t", SourceChunkIDs: []string{"c1"}},
			{ID: "t:f", Name: "F", Type: "t", SourceChunkIDs: []string{"c1"}},
		},
		Relations: []graph.Relation{
			{ID: "t:a::r::t:b", Source: "t:a", Target: "t:b", Relation: "r", SourceChunkIDs: []string{"c1"}, Weight: 1},
			{ID: "t:b::r::t:c", Source: "t:b", Target: "t:c", Relation: "r", SourceChunkIDs: []string{"c1"}, Weight: 1},
			{ID: "t:c::r::t:a", Source: "t:c", Target: "t:a", Relation: "r", SourceChunkIDs: []string{"c1"}, Weight: 1},
			{ID: "t:d::r::t:e", Source: "t:d", Target: "t:e", Relation: "r", SourceChunkIDs: []string{"c1"}, Weight: 1},
			{ID: "t:e::r::t:f", Source: "t:e", Target: "t:f", Relation: "r", SourceChunkIDs: []string{"c1"}, Weight: 1},
			{ID: "t:f::r::t:d", Source: "t:f", Target: "t:d", Relation: "r", SourceChunkIDs: []string{"c1"}, Weight: 1},
			{ID: "t:c::r::t:d", Source: "t:c", Target: "t:d", Relation: "r", SourceChunkIDs: []string{"c1"}, Weight: 1},
		},
	}

	gs, ok := store.Store(s).(store.GraphStore)
	if !ok {
		t.Fatalf("InMemoryStore does not implement store.GraphStore")
	}
	if err := gs.UpsertGraph(ctx, "ns", g); err != nil {
		t.Fatalf("UpsertGraph: %v", err)
	}

	cs, ok := store.Store(s).(store.CommunityStore)
	if !ok {
		t.Fatalf("InMemoryStore does not implement store.CommunityStore")
	}

	// Read the whole-graph snapshot back out and run Louvain over it.
	snap, err := cs.GraphSnapshot(ctx, "ns")
	if err != nil {
		t.Fatalf("GraphSnapshot: %v", err)
	}
	detected, err := graph.LouvainDetector{}.Detect(ctx, snap)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(detected) == 0 {
		t.Fatalf("Louvain detected no communities for a clustered graph")
	}

	// Persist and read back — the detected hierarchy must survive unchanged.
	if err := cs.UpsertCommunities(ctx, "ns", detected); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}
	got, err := cs.Communities(ctx, "ns")
	if err != nil {
		t.Fatalf("Communities: %v", err)
	}

	// Detect already returns communities sorted by ID, and Communities
	// returns them sorted by ID — so a direct DeepEqual must hold.
	if !reflect.DeepEqual(got, detected) {
		t.Fatalf("detected hierarchy not round-tripped:\n got %+v\nwant %+v", got, detected)
	}

	// A re-detect of the same snapshot is deterministic, and persisting it
	// again replaces in place — the stored set stays identical.
	redetected, err := graph.LouvainDetector{}.Detect(ctx, snap)
	if err != nil {
		t.Fatalf("Detect (re-run): %v", err)
	}
	if !reflect.DeepEqual(redetected, detected) {
		t.Fatalf("Louvain not deterministic across runs")
	}
}

// TestCommunityUpsertDoesNotAliasCallerSlice verifies the store deep-copies
// on UpsertCommunities — a later mutation of the caller's slice must not
// reach into stored state.
func TestCommunityUpsertDoesNotAliasCallerSlice(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore(2)
	cs, ok := store.Store(s).(store.CommunityStore)
	if !ok {
		t.Fatalf("InMemoryStore does not implement store.CommunityStore")
	}

	in := []graph.Community{
		{ID: "L0-t:a", Level: 0, EntityIDs: []string{"t:a", "t:b"}},
	}
	if err := cs.UpsertCommunities(ctx, "ns", in); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}
	// Mutate the caller's slice after the upsert.
	in[0].ID = "MUTATED"
	in[0].EntityIDs[0] = "MUTATED"

	got, err := cs.Communities(ctx, "ns")
	if err != nil {
		t.Fatalf("Communities: %v", err)
	}
	if len(got) != 1 || got[0].ID != "L0-t:a" {
		t.Fatalf("stored community aliased caller slice: %+v", got)
	}
	if got[0].EntityIDs[0] != "t:a" {
		t.Fatalf("stored EntityIDs aliased caller slice: %+v", got[0].EntityIDs)
	}

	// And the returned slice must not alias stored state either.
	got[0].EntityIDs[0] = "MUTATED"
	again, err := cs.Communities(ctx, "ns")
	if err != nil {
		t.Fatalf("Communities (re-read): %v", err)
	}
	if again[0].EntityIDs[0] != "t:a" {
		t.Fatalf("returned slice aliased stored state: %+v", again[0].EntityIDs)
	}
}
