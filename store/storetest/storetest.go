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
	"testing"

	"github.com/costa92/llm-agent-rag/embed"
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

func ctx() context.Context { return context.Background() }

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
