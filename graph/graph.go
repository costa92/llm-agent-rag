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
	ID             string         // ID is the canonical id; empty until Canonicalize assigns it.
	Name           string         // Name is the entity's surface name.
	Type           string         // Type is a free-form label: "person", "org", "place", ...
	Description    string         // Description is a short description of the entity.
	SourceChunkIDs []string       // SourceChunkIDs is provenance — the chunks it was extracted from.
	Metadata       map[string]any // Metadata carries arbitrary per-entity key/value pairs.
}

// Relation is a directed, typed edge between two entities. Source and
// Target hold entity names as extracted, and canonical entity IDs after
// Canonicalize resolves them.
type Relation struct {
	ID             string   // ID is the relation's unique identifier.
	Source         string   // Source is the source entity (name pre-Canonicalize, ID after).
	Target         string   // Target is the target entity (name pre-Canonicalize, ID after).
	Relation       string   // Relation is the edge label.
	Description    string   // Description is a short description of the relation.
	SourceChunkIDs []string // SourceChunkIDs is provenance — the chunks it was extracted from.
	Weight         float64  // Weight is the relation's strength or confidence.
}

// Graph is a canonicalized set of entities and relations.
type Graph struct {
	Entities  []Entity   // Entities are the graph's canonicalized entities.
	Relations []Relation // Relations are the graph's canonicalized relations.
	// Communities is the optional detected community hierarchy. It is
	// additive — a zero-value Graph behaves exactly as in v0.7. A
	// CommunityDetector populates it; see community.go.
	Communities []Community
}

// Subgraph is the result of a neighborhood traversal: the reached
// entities, the relations among them, and each entity's hop distance from
// the nearest seed (seeds = 0).
type Subgraph struct {
	Entities  []Entity       // Entities are the reached entities.
	Relations []Relation     // Relations are the relations among the reached entities.
	Depth     map[string]int // Depth maps each entity ID to its hop distance from the nearest seed.
}

// EntityExtractor turns one chunk's text into raw (pre-canonical) graph
// primitives. Implementations may be LLM-backed or deterministic; callers
// supply their own so the package stays vendor-neutral.
type EntityExtractor interface {
	// Extract turns one chunk's text into raw entities and relations.
	Extract(ctx context.Context, chunkID, text string) ([]Entity, []Relation, error)
}
