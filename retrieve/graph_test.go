package retrieve

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/store"
)

// nonGraphStore is a store.Store that is deliberately not a
// store.GraphStore.
type nonGraphStore struct{ store.Store }

// graphFixture builds an in-memory store with three chunks and a chain
// graph Alpha-Bravo-Charlie whose entities' provenance points at them.
func graphFixture(t *testing.T) *store.InMemoryStore {
	t.Helper()
	st := store.NewInMemoryStore(2)
	if err := st.Upsert(context.Background(), []store.StoredChunk{
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
	if err := st.UpsertGraph(context.Background(), "ns", g); err != nil {
		t.Fatalf("UpsertGraph: %v", err)
	}
	return st
}

func TestLexicalEntityLinker(t *testing.T) {
	st := graphFixture(t)
	ents, err := LexicalEntityLinker{}.Link(context.Background(), "tell me about Alpha", "ns", st)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(ents) != 1 || ents[0].ID != "t:alpha" {
		t.Fatalf("Link(Alpha) = %+v, want [t:alpha]", ents)
	}
}

func TestGraphRetriever(t *testing.T) {
	st := graphFixture(t)
	r := GraphRetriever{Store: st, MaxDepth: 2}
	hits, trace, err := r.Retrieve(context.Background(), Request{Query: "Alpha", Namespace: "ns", TopK: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("hits = %d, want 3 (c1 seed, c2 1-hop, c3 2-hop)", len(hits))
	}
	if hits[0].Chunk.ID != "c1" {
		t.Fatalf("hits[0] = %s, want c1 (seed entity's chunk ranks highest)", hits[0].Chunk.ID)
	}
	if !(hits[0].Score > hits[1].Score && hits[1].Score > hits[2].Score) {
		t.Fatalf("scores not proximity-decayed: %v / %v / %v", hits[0].Score, hits[1].Score, hits[2].Score)
	}
	if len(trace.Graph.SeedEntityIDs) != 1 || trace.Graph.SeedEntityIDs[0] != "t:alpha" {
		t.Fatalf("Trace.Graph.SeedEntityIDs = %v, want [t:alpha]", trace.Graph.SeedEntityIDs)
	}
	if len(trace.Graph.ReachedEntityIDs) != 3 {
		t.Fatalf("Trace.Graph.ReachedEntityIDs = %v, want 3 reached", trace.Graph.ReachedEntityIDs)
	}
	if trace.Graph.MaxHop != 2 {
		t.Fatalf("Trace.Graph.MaxHop = %d, want 2", trace.Graph.MaxHop)
	}
}

func TestGraphRetrieverNonGraphStore(t *testing.T) {
	r := GraphRetriever{Store: nonGraphStore{Store: store.NewInMemoryStore(2)}}
	hits, _, err := r.Retrieve(context.Background(), Request{Query: "Alpha", Namespace: "ns", TopK: 10})
	if err != nil {
		t.Fatalf("Retrieve over a non-GraphStore: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %d, want 0 (a non-GraphStore yields no graph hits)", len(hits))
	}
}

func TestHybridRetrieverFusesGraphSignal(t *testing.T) {
	st := graphFixture(t)
	empty := &hopStubRetriever{byQuery: map[string][]store.Hit{}}
	hr := HybridRetriever{
		Dense:   empty,
		Lexical: empty,
		Graph:   GraphRetriever{Store: st, MaxDepth: 2},
	}
	hits, trace, err := hr.Retrieve(context.Background(), Request{
		Query: "Alpha", Namespace: "ns", TopK: 10, EnableGraph: true,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("no hits — the graph signal did not fuse in")
	}
	graphRanked := false
	for _, f := range trace.Fusion {
		if f.GraphRank > 0 {
			graphRanked = true
		}
	}
	if !graphRanked {
		t.Fatalf("no FusionAttribution has GraphRank > 0 — graph signal not attributed")
	}
	if len(trace.Graph.SeedEntityIDs) == 0 {
		t.Fatalf("Trace.Graph not carried through HybridRetriever")
	}
}

func TestHybridRetrieverGraphOff(t *testing.T) {
	st := graphFixture(t)
	empty := &hopStubRetriever{byQuery: map[string][]store.Hit{}}
	hr := HybridRetriever{
		Dense:   empty,
		Lexical: empty,
		Graph:   GraphRetriever{Store: st, MaxDepth: 2},
	}
	hits, trace, err := hr.Retrieve(context.Background(), Request{
		Query: "Alpha", Namespace: "ns", TopK: 10, EnableGraph: false,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %d with EnableGraph off and empty dense/lexical, want 0", len(hits))
	}
	if len(trace.Graph.SeedEntityIDs) != 0 {
		t.Fatalf("Trace.Graph populated with EnableGraph off")
	}
}
