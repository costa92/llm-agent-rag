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

// TestGraphRetrieverCommunityIDs verifies that, when the store is a
// store.CommunityStore with detected communities, GraphRetriever.Retrieve
// records the IDs of the communities its reached entities belong to —
// sorted and deduped.
func TestGraphRetrieverCommunityIDs(t *testing.T) {
	st := graphFixture(t)
	// Two communities partitioning the chain: {alpha,bravo} and {charlie}.
	if err := st.UpsertCommunities(context.Background(), "ns", []graph.Community{
		{ID: "comm-1", Level: 0, EntityIDs: []string{"t:alpha", "t:bravo"}},
		{ID: "comm-0", Level: 0, EntityIDs: []string{"t:charlie"}},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}

	r := GraphRetriever{Store: st, MaxDepth: 2}
	_, trace, err := r.Retrieve(context.Background(), Request{Query: "Alpha", Namespace: "ns", TopK: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	// The traversal reaches all three entities, spanning both communities.
	want := []string{"comm-0", "comm-1"}
	if got := trace.Graph.CommunityIDs; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Trace.Graph.CommunityIDs = %v, want %v (sorted, deduped)", got, want)
	}
}

// TestGraphRetrieverCommunityIDsNil verifies graceful degradation: a store
// that is a GraphStore but carries no detected communities yields a nil
// CommunityIDs — no behavior change.
func TestGraphRetrieverCommunityIDsNil(t *testing.T) {
	st := graphFixture(t) // a GraphStore, but UpsertCommunities was never called
	r := GraphRetriever{Store: st, MaxDepth: 2}
	_, trace, err := r.Retrieve(context.Background(), Request{Query: "Alpha", Namespace: "ns", TopK: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if trace.Graph.CommunityIDs != nil {
		t.Fatalf("Trace.Graph.CommunityIDs = %v, want nil (no communities detected)", trace.Graph.CommunityIDs)
	}
}

// TestGraphRetrieverPathMode verifies opt-in path mode: with a non-nil
// PathRanker and a query linking to two connected entities, Retrieve
// populates Trace.Graph.Paths and a non-nil EvidenceSubgraph — while the
// chunk []store.Hit stays byte-identical to the path-mode-off run.
func TestGraphRetrieverPathMode(t *testing.T) {
	st := graphFixture(t)
	// "Alpha Charlie" links to the two endpoint entities of the chain,
	// so there is exactly one seed pair {t:alpha, t:charlie}.
	req := Request{Query: "Alpha Charlie", Namespace: "ns", TopK: 10}

	off := GraphRetriever{Store: st, MaxDepth: 2}
	offHits, offTrace, err := off.Retrieve(context.Background(), req)
	if err != nil {
		t.Fatalf("Retrieve (path mode off): %v", err)
	}
	if offTrace.Graph.Paths != nil || offTrace.Graph.EvidenceSubgraph != nil {
		t.Fatalf("path mode off: Paths/EvidenceSubgraph not nil (Paths=%v, Evidence=%v)",
			offTrace.Graph.Paths, offTrace.Graph.EvidenceSubgraph)
	}

	on := GraphRetriever{Store: st, MaxDepth: 2, PathRanker: graph.WeightedPathRanker{}}
	onHits, onTrace, err := on.Retrieve(context.Background(), req)
	if err != nil {
		t.Fatalf("Retrieve (path mode on): %v", err)
	}
	if len(onTrace.Graph.Paths) == 0 {
		t.Fatalf("path mode on: Trace.Graph.Paths empty, want >= 1 ranked path")
	}
	if onTrace.Graph.EvidenceSubgraph == nil {
		t.Fatalf("path mode on: Trace.Graph.EvidenceSubgraph is nil")
	}
	if len(onTrace.Graph.EvidenceSubgraph.Entities) != 3 {
		t.Fatalf("EvidenceSubgraph has %d entities, want 3 (the traversed neighborhood)",
			len(onTrace.Graph.EvidenceSubgraph.Entities))
	}
	// The ranked path must connect the two seeds Alpha .. Charlie.
	p := onTrace.Graph.Paths[0]
	if len(p.EntityIDs) < 2 || p.EntityIDs[0] != "t:alpha" ||
		p.EntityIDs[len(p.EntityIDs)-1] != "t:charlie" {
		t.Fatalf("ranked path = %v, want one connecting t:alpha .. t:charlie", p.EntityIDs)
	}

	// KG4-4: path mode must not disturb the chunk hits — byte-identical.
	if len(onHits) != len(offHits) {
		t.Fatalf("hit count differs: path mode on=%d, off=%d", len(onHits), len(offHits))
	}
	for i := range onHits {
		if onHits[i].Chunk.ID != offHits[i].Chunk.ID || onHits[i].Score != offHits[i].Score {
			t.Fatalf("hit %d differs between path modes: on=%+v off=%+v",
				i, onHits[i], offHits[i])
		}
	}
	// The rest of the trace is identical too.
	if onTrace.Graph.MaxHop != offTrace.Graph.MaxHop ||
		len(onTrace.Graph.SeedEntityIDs) != len(offTrace.Graph.SeedEntityIDs) ||
		len(onTrace.Graph.ReachedEntityIDs) != len(offTrace.Graph.ReachedEntityIDs) {
		t.Fatalf("path mode disturbed the v0.7/v0.8 trace fields")
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
