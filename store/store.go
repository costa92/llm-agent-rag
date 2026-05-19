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

// CommunityStore is an optional capability a Store may implement to persist
// a namespace's detected community hierarchy and to read back the whole
// namespace graph (the input community detection needs). It is a sibling of
// GraphStore — consumers type-assert for it and degrade gracefully when a
// store does not implement it, exactly like LexicalSearcher and GraphStore.
// Adding it leaves the v0.7 GraphStore interface byte-identical. The
// community set is per-namespace.
type CommunityStore interface {
	// GraphSnapshot returns the full stored graph for a namespace —
	// entities and relations in deterministic order. It is the input to
	// community detection. An unknown namespace yields an empty graph and
	// no error.
	GraphSnapshot(ctx context.Context, namespace string) (graph.Graph, error)
	// UpsertCommunities replaces the namespace's community set with
	// communities (replace-all: detection always produces the full set).
	UpsertCommunities(ctx context.Context, namespace string, communities []graph.Community) error
	// Communities returns the namespace's stored community set, sorted by
	// community ID. An unknown namespace yields nil and no error.
	Communities(ctx context.Context, namespace string) ([]graph.Community, error)
	// PutCommunityReport persists report under (namespace, report.CommunityID),
	// overwriting any existing report for that community. It is the report
	// cache's write side — community summaries are generated lazily at query
	// time and cached here.
	PutCommunityReport(ctx context.Context, namespace string, report graph.CommunityReport) error
	// CommunityReport returns the stored report for a community. The bool is
	// false (and the error nil) when no report has been persisted for that
	// community ID — a cache miss, not an error.
	CommunityReport(ctx context.Context, namespace, communityID string) (graph.CommunityReport, bool, error)
}

var ErrNotFound = errors.New("store: chunk not found")

var ErrDimensionMismatch = errors.New("store: vector dimension mismatch")
