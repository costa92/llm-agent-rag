package store

import (
	"context"
	"errors"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/graph"
)

type Filter map[string]any

type Query struct {
	Namespace       string
	Text            string
	Vector          embed.Vector
	TopK            int
	Filters         Filter
	SecurityFilters Filter
}

type Store interface {
	Upsert(ctx context.Context, chunks []StoredChunk) error
	Search(ctx context.Context, q Query) ([]Hit, error)
	List(ctx context.Context, namespace string, filters Filter, securityFilters Filter) ([]StoredChunk, error)
	Get(ctx context.Context, id string) (StoredChunk, error)
	Remove(ctx context.Context, id string) error
	RemoveByFilter(ctx context.Context, namespace string, filters Filter) (int, error)
	Stats(ctx context.Context, namespace string) (Stats, error)
}

// LexicalSearcher is an optional capability a Store may implement to run
// keyword/full-text ranking natively. Retrieval type-asserts for it and
// falls back to an in-process scan when a store does not implement it.
type LexicalSearcher interface {
	LexicalSearch(ctx context.Context, q Query) ([]Hit, error)
}

// GraphStore is an optional capability a Store may implement to persist and
// traverse an entity/relation graph. Consumers type-assert for it and
// degrade gracefully when a store does not implement it — the same
// contract as LexicalSearcher. The graph is per-namespace.
type GraphStore interface {
	// UpsertGraph union-merges g into the namespace's graph: entities and
	// relations merge by ID, unioning provenance.
	UpsertGraph(ctx context.Context, namespace string, g graph.Graph) error
	// RemoveGraphBySource removes the given chunk IDs from every entity's
	// and relation's provenance, garbage-collecting any left unreferenced.
	RemoveGraphBySource(ctx context.Context, namespace string, chunkIDs []string) error
	// Neighborhood traverses from the seed entity IDs out to depth hops
	// (hard-capped at 2) and returns the reached subgraph.
	Neighborhood(ctx context.Context, namespace string, seedIDs []string, depth int) (graph.Subgraph, error)
	// FindEntities returns entities whose name matches any of names.
	FindEntities(ctx context.Context, namespace string, names []string) ([]graph.Entity, error)
}

var ErrNotFound = errors.New("store: chunk not found")

var ErrDimensionMismatch = errors.New("store: vector dimension mismatch")
