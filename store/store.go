// Package store defines the storage-backend seam for the RAG pipeline.
// Store is the core interface that every backend implements; CommunityStore,
// GraphStore, and LexicalSearcher are opt-in capability interfaces a backend
// may additionally satisfy and callers type-assert for. InMemoryStore is the
// built-in backend; sister-repo backends prove conformance via store/storetest.
package store

import (
	"context"
	"errors"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/graph"
)

// Filter is a metadata equality filter — a chunk matches when every key in
// the map equals the chunk's metadata value for that key.
type Filter map[string]any

// Query is a vector-search request against a Store.
type Query struct {
	Namespace       string       // Namespace scopes the search to one namespace.
	Text            string       // Text is the raw query string (used by lexical search).
	Vector          embed.Vector // Vector is the query embedding (used by vector search).
	TopK            int          // TopK caps the number of hits returned.
	Filters         Filter       // Filters restricts results by chunk metadata.
	SecurityFilters Filter       // SecurityFilters applies caller-enforced access control.
}

// Store is the core storage seam: every retrieval backend implements it.
// Backends prove conformance via the store/storetest suite.
type Store interface {
	// Upsert inserts or replaces the given chunks by ID.
	Upsert(ctx context.Context, chunks []StoredChunk) error
	// Search returns the top-K hits for q ranked by vector similarity.
	Search(ctx context.Context, q Query) ([]Hit, error)
	// List returns the chunks in a namespace matching the given filters.
	List(ctx context.Context, namespace string, filters Filter, securityFilters Filter) ([]StoredChunk, error)
	// Get returns the chunk with the given ID, or ErrNotFound.
	Get(ctx context.Context, id string) (StoredChunk, error)
	// Remove deletes the chunk with the given ID.
	Remove(ctx context.Context, id string) error
	// RemoveByFilter deletes every chunk in a namespace matching filters and
	// returns the number removed.
	RemoveByFilter(ctx context.Context, namespace string, filters Filter) (int, error)
	// Stats returns chunk-count and dimension statistics for a namespace.
	Stats(ctx context.Context, namespace string) (Stats, error)
}

// LexicalSearcher is an optional capability a Store may implement to run
// keyword/full-text ranking natively. Retrieval type-asserts for it and
// falls back to an in-process scan when a store does not implement it.
type LexicalSearcher interface {
	// LexicalSearch runs keyword/full-text ranking for q natively.
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

// ErrNotFound is returned by Store.Get when no chunk has the requested ID.
var ErrNotFound = errors.New("store: chunk not found")

// ErrDimensionMismatch is returned when a chunk or query vector does not
// match the store's configured embedding dimension.
var ErrDimensionMismatch = errors.New("store: vector dimension mismatch")
