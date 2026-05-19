// Package graph builds and holds a knowledge graph — entities and typed
// relations extracted from ingested documents — for GraphRAG retrieval.
// It is a near-leaf package: it imports only the standard library, the
// generate seam (for the LLM-backed extractor), and the embed seam (for
// the embedding-similarity entity resolver). Both seams are themselves
// stdlib-only leaf packages, so graph adds no third-party dependency.
package graph

import "context"

// Entity is a node: a salient real-world thing named in the corpus.
type Entity struct {
	ID             string // canonical id; empty until Canonicalize assigns it
	Name           string
	Type           string // free-form label: "person", "org", "place", ...
	Description    string
	SourceChunkIDs []string // provenance — the chunks it was extracted from
	Metadata       map[string]any
}

// Relation is a directed, typed edge between two entities. Source and
// Target hold entity names as extracted, and canonical entity IDs after
// Canonicalize resolves them.
type Relation struct {
	ID             string
	Source         string
	Target         string
	Relation       string // the edge label
	Description    string
	SourceChunkIDs []string
	Weight         float64
}

// Graph is a canonicalized set of entities and relations.
type Graph struct {
	Entities  []Entity
	Relations []Relation
	// Communities is the optional detected community hierarchy. It is
	// additive — a zero-value Graph behaves exactly as in v0.7. A
	// CommunityDetector populates it; see community.go.
	Communities []Community
}

// Subgraph is the result of a neighborhood traversal: the reached
// entities, the relations among them, and each entity's hop distance from
// the nearest seed (seeds = 0).
type Subgraph struct {
	Entities  []Entity
	Relations []Relation
	Depth     map[string]int
}

// EntityExtractor turns one chunk's text into raw (pre-canonical) graph
// primitives. Implementations may be LLM-backed or deterministic; callers
// supply their own so the package stays vendor-neutral.
type EntityExtractor interface {
	Extract(ctx context.Context, chunkID, text string) ([]Entity, []Relation, error)
}
