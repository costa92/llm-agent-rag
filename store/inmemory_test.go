package store

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/embed"
)

func TestInMemoryStoreNamespaceIsolation(t *testing.T) {
	s := NewInMemoryStore(2)
	err := s.Upsert(context.Background(), []StoredChunk{
		{ID: "a", Namespace: "n1", Vector: embed.Vector{1, 0}},
		{ID: "b", Namespace: "n2", Vector: embed.Vector{0, 1}},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}
	hits, err := s.Search(context.Background(), Query{Namespace: "n1", Vector: embed.Vector{1, 0}, TopK: 5})
	if err != nil {
		t.Fatalf("Search(): %v", err)
	}
	if len(hits) != 1 || hits[0].Chunk.ID != "a" {
		t.Fatalf("hits = %+v, want only a", hits)
	}
}

func TestInMemoryStoreMetadataFilters(t *testing.T) {
	s := NewInMemoryStore(2)
	err := s.Upsert(context.Background(), []StoredChunk{
		{
			ID:        "paris-guide",
			Namespace: "docs",
			Vector:    embed.Vector{1, 0},
			Metadata: map[string]any{
				"lang":   "en",
				"source": "guide",
				"tier":   1,
			},
		},
		{
			ID:        "paris-faq",
			Namespace: "docs",
			Vector:    embed.Vector{0.9, 0.1},
			Metadata: map[string]any{
				"lang":   "fr",
				"source": "faq",
				"tier":   2,
			},
		},
		{
			ID:        "berlin-guide",
			Namespace: "docs",
			Vector:    embed.Vector{0.8, 0.2},
			Metadata: map[string]any{
				"lang":   "en",
				"source": "guide",
				"tier":   2,
			},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	hits, err := s.Search(context.Background(), Query{
		Namespace: "docs",
		Vector:    embed.Vector{1, 0},
		TopK:      5,
		Filters: Filter{
			"lang":   "en",
			"source": "guide",
		},
	})
	if err != nil {
		t.Fatalf("Search(): %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("len(hits) = %d, want 2", len(hits))
	}
	if hits[0].Chunk.ID != "paris-guide" || hits[1].Chunk.ID != "berlin-guide" {
		t.Fatalf("hits = %+v, want filtered english guide docs in score order", hits)
	}
}

func TestInMemoryStoreMetadataFiltersRequireExactMatch(t *testing.T) {
	s := NewInMemoryStore(2)
	err := s.Upsert(context.Background(), []StoredChunk{
		{
			ID:        "doc-a",
			Namespace: "docs",
			Vector:    embed.Vector{1, 0},
			Metadata: map[string]any{
				"tier": 1,
			},
		},
		{
			ID:        "doc-b",
			Namespace: "docs",
			Vector:    embed.Vector{0.9, 0.1},
			Metadata: map[string]any{
				"tier": 2,
			},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	hits, err := s.Search(context.Background(), Query{
		Namespace: "docs",
		Vector:    embed.Vector{1, 0},
		TopK:      5,
		Filters: Filter{
			"tier": 2,
		},
	})
	if err != nil {
		t.Fatalf("Search(): %v", err)
	}
	if len(hits) != 1 || hits[0].Chunk.ID != "doc-b" {
		t.Fatalf("hits = %+v, want only doc-b", hits)
	}
}

func TestInMemoryStoreMetadataFiltersMissingKeyExcludesChunk(t *testing.T) {
	s := NewInMemoryStore(2)
	err := s.Upsert(context.Background(), []StoredChunk{
		{
			ID:        "with-lang",
			Namespace: "docs",
			Vector:    embed.Vector{1, 0},
			Metadata: map[string]any{
				"lang": "en",
			},
		},
		{
			ID:        "without-lang",
			Namespace: "docs",
			Vector:    embed.Vector{0.9, 0.1},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	hits, err := s.Search(context.Background(), Query{
		Namespace: "docs",
		Vector:    embed.Vector{1, 0},
		TopK:      5,
		Filters: Filter{
			"lang": "en",
		},
	})
	if err != nil {
		t.Fatalf("Search(): %v", err)
	}
	if len(hits) != 1 || hits[0].Chunk.ID != "with-lang" {
		t.Fatalf("hits = %+v, want only with-lang", hits)
	}
}

func TestInMemoryStoreSecurityFiltersCannotBeBypassed(t *testing.T) {
	s := NewInMemoryStore(2)
	err := s.Upsert(context.Background(), []StoredChunk{
		{
			ID:        "tenant-a-public",
			Namespace: "docs",
			Vector:    embed.Vector{1, 0},
			Metadata: map[string]any{
				"tenant": "a",
				"scope":  "public",
			},
		},
		{
			ID:        "tenant-b-public",
			Namespace: "docs",
			Vector:    embed.Vector{0.95, 0.05},
			Metadata: map[string]any{
				"tenant": "b",
				"scope":  "public",
			},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	hits, err := s.Search(context.Background(), Query{
		Namespace: "docs",
		Vector:    embed.Vector{1, 0},
		TopK:      5,
		Filters: Filter{
			"scope": "public",
		},
		SecurityFilters: Filter{
			"tenant": "a",
		},
	})
	if err != nil {
		t.Fatalf("Search(): %v", err)
	}
	if len(hits) != 1 || hits[0].Chunk.ID != "tenant-a-public" {
		t.Fatalf("hits = %+v, want only tenant-a-public", hits)
	}
}

func TestInMemoryStoreSecurityFiltersOverrideConflictingCallerIntent(t *testing.T) {
	s := NewInMemoryStore(2)
	err := s.Upsert(context.Background(), []StoredChunk{
		{
			ID:        "tenant-a",
			Namespace: "docs",
			Vector:    embed.Vector{1, 0},
			Metadata: map[string]any{
				"tenant": "a",
			},
		},
		{
			ID:        "tenant-b",
			Namespace: "docs",
			Vector:    embed.Vector{0.9, 0.1},
			Metadata: map[string]any{
				"tenant": "b",
			},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	hits, err := s.Search(context.Background(), Query{
		Namespace: "docs",
		Vector:    embed.Vector{1, 0},
		TopK:      5,
		Filters: Filter{
			"tenant": "b",
		},
		SecurityFilters: Filter{
			"tenant": "a",
		},
	})
	if err != nil {
		t.Fatalf("Search(): %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %+v, want no hits because security and caller filters conflict", hits)
	}
}

func TestInMemoryStoreRemoveByFilter(t *testing.T) {
	s := NewInMemoryStore(2)
	err := s.Upsert(context.Background(), []StoredChunk{
		{
			ID:        "a1",
			Namespace: "docs",
			Vector:    embed.Vector{1, 0},
			Metadata: map[string]any{
				"source_id": "alpha",
			},
		},
		{
			ID:        "a2",
			Namespace: "docs",
			Vector:    embed.Vector{0.9, 0.1},
			Metadata: map[string]any{
				"source_id": "alpha",
			},
		},
		{
			ID:        "b1",
			Namespace: "docs",
			Vector:    embed.Vector{0.8, 0.2},
			Metadata: map[string]any{
				"source_id": "beta",
			},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	removed, err := s.RemoveByFilter(context.Background(), "docs", Filter{
		"source_id": "alpha",
	})
	if err != nil {
		t.Fatalf("RemoveByFilter(): %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}

	stats, err := s.Stats(context.Background(), "docs")
	if err != nil {
		t.Fatalf("Stats(): %v", err)
	}
	if stats.Count != 1 {
		t.Fatalf("stats.Count = %d, want 1", stats.Count)
	}
}

func TestInMemoryStoreListHonorsFilters(t *testing.T) {
	s := NewInMemoryStore(2)
	err := s.Upsert(context.Background(), []StoredChunk{
		{
			ID:        "a",
			Namespace: "docs",
			Vector:    embed.Vector{1, 0},
			Content:   "Paris travel guide",
			Metadata: map[string]any{
				"lang": "en",
			},
		},
		{
			ID:        "b",
			Namespace: "docs",
			Vector:    embed.Vector{0.9, 0.1},
			Content:   "Guide de Paris",
			Metadata: map[string]any{
				"lang": "fr",
			},
		},
	})
	if err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	chunks, err := s.List(context.Background(), "docs", Filter{"lang": "en"}, nil)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(chunks) != 1 || chunks[0].ID != "a" {
		t.Fatalf("chunks = %+v, want only a", chunks)
	}
}
