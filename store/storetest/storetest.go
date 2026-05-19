// Package storetest provides a shared conformance suite for store.Store
// implementations. Each backend wires it up with a single call:
//
//	storetest.RunConformance(t, func(t *testing.T) store.Store {
//	    return store.NewInMemoryStore(2)
//	}, storetest.WithDimensionStrict())
//
// The Factory is called once per subtest so each subtest sees a fresh,
// isolated store.
package storetest

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/store"
)

// Factory builds a fresh store.Store for one conformance subtest. The
// factory is responsible for any per-test setup (e.g. unique table name
// for backends with shared schema) and may register cleanup via t.Cleanup.
type Factory func(t *testing.T) store.Store

// Option toggles optional capability checks.
type Option func(*config)

type config struct {
	dimensionStrict bool
}

// WithDimensionStrict enables the dimension-mismatch test. Backends that
// enforce a fixed embedding dimension on Upsert should pass this option;
// backends without such a guard should omit it.
func WithDimensionStrict() Option { return func(c *config) { c.dimensionStrict = true } }

// RunConformance executes every conformance subtest against the factory.
func RunConformance(t *testing.T, factory Factory, opts ...Option) {
	t.Helper()
	cfg := config{}
	for _, opt := range opts {
		opt(&cfg)
	}

	t.Run("Upsert_and_Get_round_trip", func(t *testing.T) { testUpsertGet(t, factory) })
	t.Run("Search_returns_nearest_first", func(t *testing.T) { testSearchNearest(t, factory) })
	t.Run("Search_respects_namespace", func(t *testing.T) { testSearchNamespace(t, factory) })
	t.Run("Filter_narrows_results", func(t *testing.T) { testFilter(t, factory) })
	t.Run("Security_filter_intersects_with_caller_filter", func(t *testing.T) { testSecurityFilter(t, factory) })
	t.Run("List_returns_namespace_chunks", func(t *testing.T) { testList(t, factory) })
	t.Run("Get_on_missing_returns_ErrNotFound", func(t *testing.T) { testGetNotFound(t, factory) })
	t.Run("Remove_on_missing_returns_ErrNotFound", func(t *testing.T) { testRemoveNotFound(t, factory) })
	t.Run("Remove_deletes", func(t *testing.T) { testRemove(t, factory) })
	t.Run("RemoveByFilter_returns_count_and_removes", func(t *testing.T) { testRemoveByFilter(t, factory) })
	t.Run("Stats_reports_count_and_dim", func(t *testing.T) { testStats(t, factory) })
	if cfg.dimensionStrict {
		t.Run("Dimension_mismatch_returns_error", func(t *testing.T) { testDimensionMismatch(t, factory) })
	}
}

// RunLexicalConformance executes lexical-search conformance subtests against
// the factory's store. If the store does not implement store.LexicalSearcher
// the whole suite is skipped, so callers can invoke it unconditionally.
func RunLexicalConformance(t *testing.T, factory Factory) {
	t.Helper()
	if _, ok := factory(t).(store.LexicalSearcher); !ok {
		t.Skip("store does not implement store.LexicalSearcher")
	}
	t.Run("Lexical_keyword_match", func(t *testing.T) { testLexicalMatch(t, factory) })
	t.Run("Lexical_ranks_term_frequency", func(t *testing.T) { testLexicalTermFrequency(t, factory) })
	t.Run("Lexical_respects_namespace", func(t *testing.T) { testLexicalNamespace(t, factory) })
	t.Run("Lexical_security_filter_trims", func(t *testing.T) { testLexicalSecurityFilter(t, factory) })
	t.Run("Lexical_empty_query_returns_nothing", func(t *testing.T) { testLexicalEmpty(t, factory) })
}

// RunGraphConformance executes graph-storage conformance subtests against
// the factory's store. If the store does not implement store.GraphStore
// the whole suite is skipped, so callers can invoke it unconditionally.
func RunGraphConformance(t *testing.T, factory Factory) {
	t.Helper()
	if _, ok := factory(t).(store.GraphStore); !ok {
		t.Skip("store does not implement store.GraphStore")
	}
	t.Run("Upsert_then_Neighborhood_reaches_neighbors", func(t *testing.T) { testGraphUpsertNeighborhood(t, factory) })
	t.Run("Neighborhood_depth_is_hard_bounded", func(t *testing.T) { testGraphDepthBounded(t, factory) })
	t.Run("UpsertGraph_union_merges", func(t *testing.T) { testGraphUnionMerge(t, factory) })
	t.Run("RemoveGraphBySource_gcs_unreferenced", func(t *testing.T) { testGraphRemoveBySource(t, factory) })
	t.Run("FindEntities_resolves_by_name", func(t *testing.T) { testGraphFindEntities(t, factory) })
}

// RunCommunityConformance executes community-storage conformance subtests
// against the factory's store. If the store does not implement
// store.CommunityStore the whole suite is skipped, so callers can invoke it
// unconditionally.
func RunCommunityConformance(t *testing.T, factory Factory) {
	t.Helper()
	if _, ok := factory(t).(store.CommunityStore); !ok {
		t.Skip("store does not implement store.CommunityStore")
	}
	t.Run("GraphSnapshot_round_trips_entities_and_relations", func(t *testing.T) { testCommunitySnapshot(t, factory) })
	t.Run("UpsertCommunities_then_Communities_round_trips", func(t *testing.T) { testCommunityRoundTrip(t, factory) })
	t.Run("UpsertCommunities_replaces_not_appends", func(t *testing.T) { testCommunityReplace(t, factory) })
	t.Run("Communities_are_namespace_isolated", func(t *testing.T) { testCommunityNamespaceIsolation(t, factory) })
	t.Run("Unknown_namespace_is_empty_no_error", func(t *testing.T) { testCommunityUnknownNamespace(t, factory) })
	t.Run("PutCommunityReport_then_CommunityReport_round_trips", func(t *testing.T) { testCommunityReportRoundTrip(t, factory) })
	t.Run("CommunityReport_unknown_id_is_miss_no_error", func(t *testing.T) { testCommunityReportMiss(t, factory) })
	t.Run("PutCommunityReport_overwrites", func(t *testing.T) { testCommunityReportOverwrite(t, factory) })
	t.Run("CommunityReports_are_namespace_isolated", func(t *testing.T) { testCommunityReportNamespaceIsolation(t, factory) })
}

func communityStore(t *testing.T, s store.Store) store.CommunityStore {
	t.Helper()
	cs, ok := s.(store.CommunityStore)
	if !ok {
		t.Fatalf("store does not implement store.CommunityStore")
	}
	return cs
}

// sampleCommunities builds a small two-level community hierarchy: two
// level-0 communities grouped under one level-1 community.
func sampleCommunities() []graph.Community {
	return []graph.Community{
		{
			ID:        "L1-t:a",
			Level:     1,
			ParentID:  "",
			EntityIDs: []string{"t:a", "t:b", "t:c", "t:d"},
		},
		{
			ID:          "L0-t:a",
			Level:       0,
			ParentID:    "L1-t:a",
			EntityIDs:   []string{"t:a", "t:b"},
			RelationIDs: []string{"t:a::r::t:b"},
		},
		{
			ID:          "L0-t:c",
			Level:       0,
			ParentID:    "L1-t:a",
			EntityIDs:   []string{"t:c", "t:d"},
			RelationIDs: []string{"t:c::r::t:d"},
		},
	}
}

func testCommunitySnapshot(t *testing.T, factory Factory) {
	s := factory(t)
	cs := communityStore(t, s)
	gs := graphStore(t, s)
	if err := gs.UpsertGraph(ctx(), "ns", chainGraph("c1")); err != nil {
		t.Fatalf("UpsertGraph: %v", err)
	}
	snap, err := cs.GraphSnapshot(ctx(), "ns")
	if err != nil {
		t.Fatalf("GraphSnapshot: %v", err)
	}
	if len(snap.Entities) != 4 {
		t.Fatalf("snapshot entities = %d, want 4", len(snap.Entities))
	}
	if len(snap.Relations) != 3 {
		t.Fatalf("snapshot relations = %d, want 3", len(snap.Relations))
	}
	// Deterministic, sorted-by-ID order.
	for i := 1; i < len(snap.Entities); i++ {
		if snap.Entities[i-1].ID >= snap.Entities[i].ID {
			t.Fatalf("snapshot entities not sorted by ID: %v", snap.Entities)
		}
	}
	for i := 1; i < len(snap.Relations); i++ {
		if snap.Relations[i-1].ID >= snap.Relations[i].ID {
			t.Fatalf("snapshot relations not sorted by ID: %v", snap.Relations)
		}
	}
	if snap.Entities[0].ID != "t:a" || snap.Entities[0].Name != "A" {
		t.Fatalf("snapshot first entity = %+v, want t:a/A", snap.Entities[0])
	}
}

func testCommunityRoundTrip(t *testing.T, factory Factory) {
	cs := communityStore(t, factory(t))
	want := sampleCommunities()
	if err := cs.UpsertCommunities(ctx(), "ns", want); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}
	got, err := cs.Communities(ctx(), "ns")
	if err != nil {
		t.Fatalf("Communities: %v", err)
	}
	// Communities are returned sorted by ID; compare against the same order.
	wantSorted := append([]graph.Community(nil), want...)
	sort.Slice(wantSorted, func(i, j int) bool { return wantSorted[i].ID < wantSorted[j].ID })
	if !reflect.DeepEqual(got, wantSorted) {
		t.Fatalf("community round-trip mismatch:\n got %+v\nwant %+v", got, wantSorted)
	}
}

func testCommunityReplace(t *testing.T, factory Factory) {
	cs := communityStore(t, factory(t))
	if err := cs.UpsertCommunities(ctx(), "ns", sampleCommunities()); err != nil {
		t.Fatalf("UpsertCommunities #1: %v", err)
	}
	// A second upsert with a smaller set must replace, not append.
	replacement := []graph.Community{
		{ID: "L0-t:x", Level: 0, EntityIDs: []string{"t:x"}},
	}
	if err := cs.UpsertCommunities(ctx(), "ns", replacement); err != nil {
		t.Fatalf("UpsertCommunities #2: %v", err)
	}
	got, err := cs.Communities(ctx(), "ns")
	if err != nil {
		t.Fatalf("Communities: %v", err)
	}
	if len(got) != 1 || got[0].ID != "L0-t:x" {
		t.Fatalf("after replace: communities = %+v, want only L0-t:x (replace, not append)", got)
	}
}

func testCommunityNamespaceIsolation(t *testing.T, factory Factory) {
	cs := communityStore(t, factory(t))
	if err := cs.UpsertCommunities(ctx(), "alpha", sampleCommunities()); err != nil {
		t.Fatalf("UpsertCommunities alpha: %v", err)
	}
	beta := []graph.Community{{ID: "L0-t:z", Level: 0, EntityIDs: []string{"t:z"}}}
	if err := cs.UpsertCommunities(ctx(), "beta", beta); err != nil {
		t.Fatalf("UpsertCommunities beta: %v", err)
	}
	gotAlpha, err := cs.Communities(ctx(), "alpha")
	if err != nil {
		t.Fatalf("Communities alpha: %v", err)
	}
	if len(gotAlpha) != 3 {
		t.Fatalf("alpha communities = %d, want 3 (beta must not leak in)", len(gotAlpha))
	}
	gotBeta, err := cs.Communities(ctx(), "beta")
	if err != nil {
		t.Fatalf("Communities beta: %v", err)
	}
	if len(gotBeta) != 1 || gotBeta[0].ID != "L0-t:z" {
		t.Fatalf("beta communities = %+v, want only L0-t:z", gotBeta)
	}
}

func testCommunityUnknownNamespace(t *testing.T, factory Factory) {
	cs := communityStore(t, factory(t))
	snap, err := cs.GraphSnapshot(ctx(), "missing")
	if err != nil {
		t.Fatalf("GraphSnapshot unknown ns: %v", err)
	}
	if len(snap.Entities) != 0 || len(snap.Relations) != 0 {
		t.Fatalf("GraphSnapshot unknown ns = %+v, want empty graph", snap)
	}
	comms, err := cs.Communities(ctx(), "missing")
	if err != nil {
		t.Fatalf("Communities unknown ns: %v", err)
	}
	if comms != nil {
		t.Fatalf("Communities unknown ns = %+v, want nil", comms)
	}
}

// sampleReport builds a CommunityReport for use in the report round-trip
// conformance subtests.
func sampleReport(communityID string) graph.CommunityReport {
	return graph.CommunityReport{
		CommunityID: communityID,
		Title:       "Early Computing Pioneers",
		Summary:     "A community of people and machines central to the dawn of computing.",
		ContentHash: "deadbeefcafe",
	}
}

func testCommunityReportRoundTrip(t *testing.T, factory Factory) {
	cs := communityStore(t, factory(t))
	want := sampleReport("L0-t:a")
	if err := cs.PutCommunityReport(ctx(), "ns", want); err != nil {
		t.Fatalf("PutCommunityReport: %v", err)
	}
	got, found, err := cs.CommunityReport(ctx(), "ns", "L0-t:a")
	if err != nil {
		t.Fatalf("CommunityReport: %v", err)
	}
	if !found {
		t.Fatalf("CommunityReport: found = false, want true")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("report round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func testCommunityReportMiss(t *testing.T, factory Factory) {
	cs := communityStore(t, factory(t))
	got, found, err := cs.CommunityReport(ctx(), "ns", "nonexistent")
	if err != nil {
		t.Fatalf("CommunityReport unknown id: %v", err)
	}
	if found {
		t.Fatalf("CommunityReport unknown id: found = true, want false (a cache miss)")
	}
	if !reflect.DeepEqual(got, graph.CommunityReport{}) {
		t.Fatalf("CommunityReport unknown id = %+v, want zero value", got)
	}
}

func testCommunityReportOverwrite(t *testing.T, factory Factory) {
	cs := communityStore(t, factory(t))
	if err := cs.PutCommunityReport(ctx(), "ns", sampleReport("L0-t:a")); err != nil {
		t.Fatalf("PutCommunityReport #1: %v", err)
	}
	updated := graph.CommunityReport{
		CommunityID: "L0-t:a",
		Title:       "Revised Title",
		Summary:     "A revised summary after re-detection.",
		ContentHash: "feedface0000",
	}
	if err := cs.PutCommunityReport(ctx(), "ns", updated); err != nil {
		t.Fatalf("PutCommunityReport #2: %v", err)
	}
	got, found, err := cs.CommunityReport(ctx(), "ns", "L0-t:a")
	if err != nil {
		t.Fatalf("CommunityReport: %v", err)
	}
	if !found {
		t.Fatalf("CommunityReport: found = false, want true")
	}
	if !reflect.DeepEqual(got, updated) {
		t.Fatalf("second Put did not overwrite:\n got %+v\nwant %+v", got, updated)
	}
}

func testCommunityReportNamespaceIsolation(t *testing.T, factory Factory) {
	cs := communityStore(t, factory(t))
	if err := cs.PutCommunityReport(ctx(), "alpha", sampleReport("L0-t:a")); err != nil {
		t.Fatalf("PutCommunityReport alpha: %v", err)
	}
	// The same community ID under a different namespace must not collide.
	_, found, err := cs.CommunityReport(ctx(), "beta", "L0-t:a")
	if err != nil {
		t.Fatalf("CommunityReport beta: %v", err)
	}
	if found {
		t.Fatalf("CommunityReport beta: found = true, want false (alpha must not leak in)")
	}
	got, found, err := cs.CommunityReport(ctx(), "alpha", "L0-t:a")
	if err != nil {
		t.Fatalf("CommunityReport alpha: %v", err)
	}
	if !found || got.Title != "Early Computing Pioneers" {
		t.Fatalf("CommunityReport alpha = %+v (found=%v), want the alpha report", got, found)
	}
}

func graphStore(t *testing.T, s store.Store) store.GraphStore {
	t.Helper()
	gs, ok := s.(store.GraphStore)
	if !ok {
		t.Fatalf("store does not implement store.GraphStore")
	}
	return gs
}

// chainGraph builds a 4-entity chain A-B-C-D with all provenance from
// chunkID.
func chainGraph(chunkID string) graph.Graph {
	return graph.Graph{
		Entities: []graph.Entity{
			{ID: "t:a", Name: "A", Type: "t", SourceChunkIDs: []string{chunkID}},
			{ID: "t:b", Name: "B", Type: "t", SourceChunkIDs: []string{chunkID}},
			{ID: "t:c", Name: "C", Type: "t", SourceChunkIDs: []string{chunkID}},
			{ID: "t:d", Name: "D", Type: "t", SourceChunkIDs: []string{chunkID}},
		},
		Relations: []graph.Relation{
			{ID: "t:a::r::t:b", Source: "t:a", Target: "t:b", Relation: "r", SourceChunkIDs: []string{chunkID}, Weight: 1},
			{ID: "t:b::r::t:c", Source: "t:b", Target: "t:c", Relation: "r", SourceChunkIDs: []string{chunkID}, Weight: 1},
			{ID: "t:c::r::t:d", Source: "t:c", Target: "t:d", Relation: "r", SourceChunkIDs: []string{chunkID}, Weight: 1},
		},
	}
}

func testGraphUpsertNeighborhood(t *testing.T, factory Factory) {
	gs := graphStore(t, factory(t))
	if err := gs.UpsertGraph(ctx(), "ns", chainGraph("c1")); err != nil {
		t.Fatalf("UpsertGraph: %v", err)
	}
	sub, err := gs.Neighborhood(ctx(), "ns", []string{"t:a"}, 1)
	if err != nil {
		t.Fatalf("Neighborhood: %v", err)
	}
	if sub.Depth["t:a"] != 0 {
		t.Fatalf("seed A depth = %d, want 0", sub.Depth["t:a"])
	}
	if sub.Depth["t:b"] != 1 {
		t.Fatalf("neighbor B depth = %d, want 1", sub.Depth["t:b"])
	}
	if _, ok := sub.Depth["t:c"]; ok {
		t.Fatalf("C reached at depth 1, want only A and B")
	}
}

func testGraphDepthBounded(t *testing.T, factory Factory) {
	gs := graphStore(t, factory(t))
	if err := gs.UpsertGraph(ctx(), "ns", chainGraph("c1")); err != nil {
		t.Fatalf("UpsertGraph: %v", err)
	}
	sub, err := gs.Neighborhood(ctx(), "ns", []string{"t:a"}, 5) // requests 5; hard cap 2
	if err != nil {
		t.Fatalf("Neighborhood: %v", err)
	}
	for id, hop := range sub.Depth {
		if hop > 2 {
			t.Fatalf("entity %s reached at hop %d, want <= 2 (hard cap)", id, hop)
		}
	}
	if _, ok := sub.Depth["t:d"]; ok {
		t.Fatalf("D (3 hops from A) reached despite the depth-2 cap")
	}
}

func testGraphUnionMerge(t *testing.T, factory Factory) {
	gs := graphStore(t, factory(t))
	if err := gs.UpsertGraph(ctx(), "ns", chainGraph("c1")); err != nil {
		t.Fatalf("UpsertGraph #1: %v", err)
	}
	g2 := graph.Graph{Entities: []graph.Entity{
		{ID: "t:a", Name: "A", Type: "t", SourceChunkIDs: []string{"c2"}},
	}}
	if err := gs.UpsertGraph(ctx(), "ns", g2); err != nil {
		t.Fatalf("UpsertGraph #2: %v", err)
	}
	found, err := gs.FindEntities(ctx(), "ns", []string{"A"})
	if err != nil {
		t.Fatalf("FindEntities: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("FindEntities(A) = %d entities, want 1 (merged, not duplicated)", len(found))
	}
	if len(found[0].SourceChunkIDs) != 2 {
		t.Fatalf("A provenance = %v, want c1 and c2 unioned", found[0].SourceChunkIDs)
	}
}

func testGraphRemoveBySource(t *testing.T, factory Factory) {
	gs := graphStore(t, factory(t))
	g := graph.Graph{Entities: []graph.Entity{
		{ID: "t:a", Name: "A", Type: "t", SourceChunkIDs: []string{"c1"}},
		{ID: "t:b", Name: "B", Type: "t", SourceChunkIDs: []string{"c1", "c2"}},
	}}
	if err := gs.UpsertGraph(ctx(), "ns", g); err != nil {
		t.Fatalf("UpsertGraph: %v", err)
	}
	if err := gs.RemoveGraphBySource(ctx(), "ns", []string{"c1"}); err != nil {
		t.Fatalf("RemoveGraphBySource: %v", err)
	}
	found, err := gs.FindEntities(ctx(), "ns", []string{"A", "B"})
	if err != nil {
		t.Fatalf("FindEntities: %v", err)
	}
	if len(found) != 1 || found[0].ID != "t:b" {
		t.Fatalf("after removing c1: entities = %+v, want only B (A garbage-collected)", found)
	}
	if len(found[0].SourceChunkIDs) != 1 || found[0].SourceChunkIDs[0] != "c2" {
		t.Fatalf("B provenance after removal = %v, want [c2]", found[0].SourceChunkIDs)
	}
}

func testGraphFindEntities(t *testing.T, factory Factory) {
	gs := graphStore(t, factory(t))
	if err := gs.UpsertGraph(ctx(), "ns", chainGraph("c1")); err != nil {
		t.Fatalf("UpsertGraph: %v", err)
	}
	found, err := gs.FindEntities(ctx(), "ns", []string{"b", "MISSING"})
	if err != nil {
		t.Fatalf("FindEntities: %v", err)
	}
	if len(found) != 1 || found[0].ID != "t:b" {
		t.Fatalf("FindEntities([b, MISSING]) = %+v, want just B (case-insensitive)", found)
	}
}

func ctx() context.Context { return context.Background() }

func mustLexical(t *testing.T, s store.Store, q store.Query) []store.Hit {
	t.Helper()
	ls, ok := s.(store.LexicalSearcher)
	if !ok {
		t.Fatalf("store does not implement store.LexicalSearcher")
	}
	hits, err := ls.LexicalSearch(ctx(), q)
	if err != nil {
		t.Fatalf("LexicalSearch: %v", err)
	}
	return hits
}

func mustUpsert(t *testing.T, s store.Store, chunks []store.StoredChunk) {
	t.Helper()
	if err := s.Upsert(ctx(), chunks); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
}

func testUpsertGet(t *testing.T, factory Factory) {
	s := factory(t)
	want := store.StoredChunk{
		ID:           "u1",
		Namespace:    "alpha",
		DocID:        "doc1",
		Title:        "Title",
		SectionID:    "alpha:doc1:Intro",
		SectionPath:  []string{"Intro"},
		Heading:      "Intro",
		HeadingLevel: 1,
		Content:      "hello world",
		Vector:       embed.Vector{1, 0},
		Metadata:     map[string]any{"lang": "en"},
	}
	mustUpsert(t, s, []store.StoredChunk{want})
	got, err := s.Get(ctx(), "u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != want.ID || got.Namespace != want.Namespace || got.Content != want.Content {
		t.Fatalf("Get: got %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(got.SectionPath, want.SectionPath) {
		t.Fatalf("SectionPath: got %#v, want %#v", got.SectionPath, want.SectionPath)
	}
	if got.HeadingLevel != want.HeadingLevel {
		t.Fatalf("HeadingLevel: got %d, want %d", got.HeadingLevel, want.HeadingLevel)
	}
	if got.Metadata["lang"] != "en" {
		t.Fatalf("Metadata round-trip: got %v, want lang=en", got.Metadata)
	}
}

func testSearchNearest(t *testing.T, factory Factory) {
	s := factory(t)
	mustUpsert(t, s, []store.StoredChunk{
		{ID: "near", Namespace: "n", DocID: "d", Content: "near", Vector: embed.Vector{1, 0}},
		{ID: "far", Namespace: "n", DocID: "d", Content: "far", Vector: embed.Vector{0, 1}},
	})
	hits, err := s.Search(ctx(), store.Query{Namespace: "n", Vector: embed.Vector{1, 0}, TopK: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits len = %d, want 2", len(hits))
	}
	if hits[0].Chunk.ID != "near" {
		t.Fatalf("top hit = %q, want near", hits[0].Chunk.ID)
	}
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("scores not descending: %v", hits)
	}
}

func testSearchNamespace(t *testing.T, factory Factory) {
	s := factory(t)
	mustUpsert(t, s, []store.StoredChunk{
		{ID: "a1", Namespace: "a", DocID: "d", Content: "a", Vector: embed.Vector{1, 0}},
		{ID: "b1", Namespace: "b", DocID: "d", Content: "b", Vector: embed.Vector{1, 0}},
	})
	hits, err := s.Search(ctx(), store.Query{Namespace: "a", Vector: embed.Vector{1, 0}, TopK: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, hit := range hits {
		if hit.Chunk.Namespace != "a" {
			t.Fatalf("hit %+v leaked from namespace %q", hit, hit.Chunk.Namespace)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("hits len = %d, want 1 (only namespace a)", len(hits))
	}
}

func testFilter(t *testing.T, factory Factory) {
	s := factory(t)
	mustUpsert(t, s, []store.StoredChunk{
		{ID: "en", Namespace: "n", DocID: "d", Content: "english", Vector: embed.Vector{1, 0}, Metadata: map[string]any{"lang": "en"}},
		{ID: "zh", Namespace: "n", DocID: "d", Content: "chinese", Vector: embed.Vector{1, 0}, Metadata: map[string]any{"lang": "zh"}},
	})
	hits, err := s.Search(ctx(), store.Query{
		Namespace: "n", Vector: embed.Vector{1, 0}, TopK: 5,
		Filters: store.Filter{"lang": "en"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Chunk.ID != "en" {
		t.Fatalf("filtered hits = %+v, want only en", hits)
	}
}

func testSecurityFilter(t *testing.T, factory Factory) {
	s := factory(t)
	mustUpsert(t, s, []store.StoredChunk{
		{ID: "tA", Namespace: "n", DocID: "d", Content: "a", Vector: embed.Vector{1, 0}, Metadata: map[string]any{"tenant": "a"}},
		{ID: "tB", Namespace: "n", DocID: "d", Content: "b", Vector: embed.Vector{0.9, 0.1}, Metadata: map[string]any{"tenant": "b"}},
	})
	// Caller filter "tenant=b" intersected with SecurityFilter "tenant=a"
	// must yield zero hits — security filter is an AND, not an override.
	hits, err := s.Search(ctx(), store.Query{
		Namespace: "n", Vector: embed.Vector{1, 0}, TopK: 5,
		Filters:         store.Filter{"tenant": "b"},
		SecurityFilters: store.Filter{"tenant": "a"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("conflicting filters returned %+v, want zero hits", hits)
	}
	// Security filter alone narrows to tenant a.
	hits, err = s.Search(ctx(), store.Query{
		Namespace: "n", Vector: embed.Vector{1, 0}, TopK: 5,
		SecurityFilters: store.Filter{"tenant": "a"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Chunk.ID != "tA" {
		t.Fatalf("security-only hits = %+v, want only tA", hits)
	}
}

func testList(t *testing.T, factory Factory) {
	s := factory(t)
	mustUpsert(t, s, []store.StoredChunk{
		{ID: "a", Namespace: "n", DocID: "d", Content: "a", Vector: embed.Vector{1, 0}, Metadata: map[string]any{"k": 1}},
		{ID: "b", Namespace: "n", DocID: "d", Content: "b", Vector: embed.Vector{1, 0}, Metadata: map[string]any{"k": 2}},
		{ID: "other", Namespace: "x", DocID: "d", Content: "x", Vector: embed.Vector{1, 0}},
	})
	all, err := s.List(ctx(), "n", nil, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List(n) returned %d chunks, want 2", len(all))
	}
	filtered, err := s.List(ctx(), "n", store.Filter{"k": 1}, nil)
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != "a" {
		t.Fatalf("List filtered = %+v, want only a", filtered)
	}
}

func testGetNotFound(t *testing.T, factory Factory) {
	s := factory(t)
	_, err := s.Get(ctx(), "missing")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get missing err = %v, want store.ErrNotFound", err)
	}
}

func testRemoveNotFound(t *testing.T, factory Factory) {
	s := factory(t)
	if err := s.Remove(ctx(), "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Remove missing err = %v, want store.ErrNotFound", err)
	}
}

func testRemove(t *testing.T, factory Factory) {
	s := factory(t)
	mustUpsert(t, s, []store.StoredChunk{
		{ID: "r1", Namespace: "n", DocID: "d", Content: "x", Vector: embed.Vector{1, 0}},
	})
	if err := s.Remove(ctx(), "r1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := s.Get(ctx(), "r1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get after Remove err = %v, want store.ErrNotFound", err)
	}
}

func testRemoveByFilter(t *testing.T, factory Factory) {
	s := factory(t)
	mustUpsert(t, s, []store.StoredChunk{
		{ID: "k1", Namespace: "n", DocID: "d", Vector: embed.Vector{1, 0}, Metadata: map[string]any{"kind": "drop"}},
		{ID: "k2", Namespace: "n", DocID: "d", Vector: embed.Vector{1, 0}, Metadata: map[string]any{"kind": "drop"}},
		{ID: "k3", Namespace: "n", DocID: "d", Vector: embed.Vector{1, 0}, Metadata: map[string]any{"kind": "keep"}},
	})
	count, err := s.RemoveByFilter(ctx(), "n", store.Filter{"kind": "drop"})
	if err != nil {
		t.Fatalf("RemoveByFilter: %v", err)
	}
	if count != 2 {
		t.Fatalf("RemoveByFilter count = %d, want 2", count)
	}
	if _, err := s.Get(ctx(), "k1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("k1 should be removed: %v", err)
	}
	if _, err := s.Get(ctx(), "k3"); err != nil {
		t.Fatalf("k3 should remain: %v", err)
	}
}

func testStats(t *testing.T, factory Factory) {
	s := factory(t)
	mustUpsert(t, s, []store.StoredChunk{
		{ID: "s1", Namespace: "n", DocID: "d", Vector: embed.Vector{1, 0}},
		{ID: "s2", Namespace: "n", DocID: "d", Vector: embed.Vector{0, 1}},
	})
	stats, err := s.Stats(ctx(), "n")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Count != 2 {
		t.Fatalf("Stats Count = %d, want 2", stats.Count)
	}
	if stats.Dim != 2 {
		t.Fatalf("Stats Dim = %d, want 2", stats.Dim)
	}
}

func testDimensionMismatch(t *testing.T, factory Factory) {
	s := factory(t)
	err := s.Upsert(ctx(), []store.StoredChunk{
		{ID: "bad", Namespace: "n", DocID: "d", Vector: embed.Vector{1, 0, 0}}, // 3-dim into 2-dim store
	})
	if !errors.Is(err, store.ErrDimensionMismatch) {
		t.Fatalf("Upsert with bad dim err = %v, want store.ErrDimensionMismatch", err)
	}
}

func testLexicalMatch(t *testing.T, factory Factory) {
	s := factory(t)
	mustUpsert(t, s, []store.StoredChunk{
		{ID: "db-doc", Namespace: "n", DocID: "d", Content: "database replication and sharding strategy", Vector: embed.Vector{1, 0}},
		{ID: "fe-doc", Namespace: "n", DocID: "d", Content: "frontend rendering pipeline tutorial", Vector: embed.Vector{0, 1}},
	})
	hits := mustLexical(t, s, store.Query{Namespace: "n", Text: "replication", TopK: 5})
	if len(hits) != 1 || hits[0].Chunk.ID != "db-doc" {
		t.Fatalf("lexical hits = %+v, want only db-doc", hits)
	}
}

func testLexicalTermFrequency(t *testing.T, factory Factory) {
	s := factory(t)
	mustUpsert(t, s, []store.StoredChunk{
		{ID: "heavy", Namespace: "n", DocID: "d", Content: "kafka kafka kafka kafka streaming", Vector: embed.Vector{1, 0}},
		{ID: "light", Namespace: "n", DocID: "d", Content: "kafka introduction guide notes", Vector: embed.Vector{0, 1}},
	})
	hits := mustLexical(t, s, store.Query{Namespace: "n", Text: "kafka", TopK: 5})
	if len(hits) != 2 {
		t.Fatalf("lexical hits = %+v, want both chunks", hits)
	}
	if hits[0].Chunk.ID != "heavy" {
		t.Fatalf("top hit = %q, want heavy (more occurrences should rank higher)", hits[0].Chunk.ID)
	}
}

func testLexicalNamespace(t *testing.T, factory Factory) {
	s := factory(t)
	mustUpsert(t, s, []store.StoredChunk{
		{ID: "ns-a", Namespace: "a", DocID: "d", Content: "elasticsearch cluster configuration", Vector: embed.Vector{1, 0}},
		{ID: "ns-b", Namespace: "b", DocID: "d", Content: "elasticsearch cluster configuration", Vector: embed.Vector{1, 0}},
	})
	hits := mustLexical(t, s, store.Query{Namespace: "a", Text: "elasticsearch", TopK: 5})
	for _, hit := range hits {
		if hit.Chunk.Namespace != "a" {
			t.Fatalf("hit %+v leaked from namespace %q", hit, hit.Chunk.Namespace)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("lexical hits len = %d, want 1 (only namespace a)", len(hits))
	}
}

func testLexicalSecurityFilter(t *testing.T, factory Factory) {
	s := factory(t)
	mustUpsert(t, s, []store.StoredChunk{
		{ID: "sec-a", Namespace: "n", DocID: "d", Content: "postgres performance tuning", Vector: embed.Vector{1, 0}, Metadata: map[string]any{"tenant": "a"}},
		{ID: "sec-b", Namespace: "n", DocID: "d", Content: "postgres performance tuning", Vector: embed.Vector{1, 0}, Metadata: map[string]any{"tenant": "b"}},
	})
	hits := mustLexical(t, s, store.Query{
		Namespace:       "n",
		Text:            "postgres",
		TopK:            5,
		SecurityFilters: store.Filter{"tenant": "a"},
	})
	if len(hits) != 1 || hits[0].Chunk.ID != "sec-a" {
		t.Fatalf("security-filtered lexical hits = %+v, want only sec-a", hits)
	}
}

func testLexicalEmpty(t *testing.T, factory Factory) {
	s := factory(t)
	mustUpsert(t, s, []store.StoredChunk{
		{ID: "a", Namespace: "n", DocID: "d", Content: "kafka streaming pipeline", Vector: embed.Vector{1, 0}},
	})
	hits := mustLexical(t, s, store.Query{Namespace: "n", Text: "", TopK: 5})
	if len(hits) != 0 {
		t.Fatalf("empty-query lexical hits = %+v, want none", hits)
	}
}
