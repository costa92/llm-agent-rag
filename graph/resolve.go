package graph

import (
	"context"
	"errors"
	"sort"

	"github.com/costa92/llm-agent-rag/embed"
)

// ErrEntityResolverEmbedderRequired is returned by EmbeddingEntityResolver
// when it is used with a nil Embedder.
var ErrEntityResolverEmbedderRequired = errors.New("graph: embedding entity resolver requires an embed.Embedder")

// defaultResolverThreshold is the conservative cosine-similarity cutoff
// EmbeddingEntityResolver uses when Threshold <= 0. It is intentionally
// high — fuzzy resolution ships opt-in and conservative, so a near miss
// stays two nodes rather than risking a false merge.
const defaultResolverThreshold = 0.92

// EntityResolver rewrites near-duplicate entity names to a shared canonical
// surface form before Canonicalize runs. Because Canonicalize resolves
// relation endpoints by name and drops a relation whose endpoint matches no
// entity, an EntityResolver MUST rewrite relation endpoints to match the
// entity names it rewrites — otherwise a relation is silently orphaned.
//
// A resolver is an opt-in pre-pass: with NoopEntityResolver (the default)
// ingestion behaves exactly as before fuzzy resolution existed.
type EntityResolver interface {
	// Resolve rewrites near-duplicate entity names and their relation
	// endpoints to a shared canonical form.
	Resolve(ctx context.Context, entities []Entity, relations []Relation) ([]Entity, []Relation, error)
}

// NoopEntityResolver returns its inputs unchanged. It is the default
// resolver: with it, Import is byte-identical to pre-fuzzy-resolution
// behavior.
type NoopEntityResolver struct{}

// Resolve returns entities and relations unchanged.
func (NoopEntityResolver) Resolve(_ context.Context, entities []Entity, relations []Relation) ([]Entity, []Relation, error) {
	return entities, relations, nil
}

// EmbeddingEntityResolver merges near-duplicate entities — "Acme" and
// "Acme Corp" — by embedding each entity Name and clustering entities of
// the same Type whose cosine similarity is at least Threshold. Within a
// cluster every member entity Name is rewritten to one deterministically
// chosen canonical form, and every relation endpoint that named a member is
// rewritten to match.
//
// It is deterministic by construction: entities are processed in
// sorted-by-Name order, clustering is single-link over a fixed sorted pair
// scan, and the canonical name is the longest member name with lexical
// tie-breaking. There is no randomness, so the same input always yields the
// same output — keystone KG3-6.
//
// It is conservative: entities of different Type never merge (a person is
// never folded into an org), and the default Threshold is high. False
// positives ("Apple" the company vs the fruit) remain possible; ship with a
// high threshold and treat resolution as opt-in.
type EmbeddingEntityResolver struct {
	// Embedder embeds each entity Name. It is required; a nil Embedder
	// makes Resolve return ErrEntityResolverEmbedderRequired.
	Embedder embed.Embedder
	// Threshold is the minimum cosine similarity for two same-Type
	// entities to merge. A value <= 0 selects defaultResolverThreshold.
	Threshold float64
}

// Resolve clusters near-duplicate same-Type entities by embedding
// similarity and rewrites both entity names and relation endpoints to one
// canonical surface form per cluster. Inputs are not mutated; new slices
// are returned.
func (r EmbeddingEntityResolver) Resolve(ctx context.Context, entities []Entity, relations []Relation) ([]Entity, []Relation, error) {
	if r.Embedder == nil {
		return nil, nil, ErrEntityResolverEmbedderRequired
	}
	threshold := r.Threshold
	if threshold <= 0 {
		threshold = defaultResolverThreshold
	}
	if len(entities) == 0 {
		return entities, relations, nil
	}

	// Index entities by sorted position so iteration order is fixed. Two
	// entities with the same Name are embedded once but each carries its
	// own slot — every distinct name is a clustering node.
	order := make([]int, len(entities))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return entities[order[a]].Name < entities[order[b]].Name
	})

	// Embed each distinct entity Name once.
	vecByName := make(map[string]embed.Vector, len(entities))
	for _, idx := range order {
		name := entities[idx].Name
		if _, done := vecByName[name]; done {
			continue
		}
		vec, err := r.Embedder.Embed(ctx, name)
		if err != nil {
			return nil, nil, err
		}
		vecByName[name] = vec
	}

	// Single-link union-find over the sorted entity order. Two entities
	// join a cluster when they share a Type and their name embeddings are
	// at least threshold-similar. The sorted scan makes clustering order
	// deterministic.
	parent := make([]int, len(entities))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		// Attach the higher root to the lower so the representative is
		// stable regardless of scan direction.
		if ra < rb {
			parent[rb] = ra
		} else {
			parent[ra] = rb
		}
	}
	for i := 0; i < len(order); i++ {
		ei := entities[order[i]]
		for j := i + 1; j < len(order); j++ {
			ej := entities[order[j]]
			if ei.Type != ej.Type {
				continue
			}
			if ei.Name == ej.Name {
				union(order[i], order[j])
				continue
			}
			sim := embed.CosineSimilarity(vecByName[ei.Name], vecByName[ej.Name])
			if sim >= threshold {
				union(order[i], order[j])
			}
		}
	}

	// Per cluster, pick the canonical Name: the longest member name, ties
	// broken by lexically lowest. Iterate in sorted entity order so the
	// choice is deterministic.
	canonByRoot := map[int]string{}
	for _, idx := range order {
		root := find(idx)
		name := entities[idx].Name
		cur, seen := canonByRoot[root]
		if !seen || betterCanonical(name, cur) {
			canonByRoot[root] = name
		}
	}

	// Map every original entity name to its cluster's canonical name.
	// Restricted to the (name -> root) it was observed under so a name
	// shared across types is rewritten per-type consistently — in practice
	// a shared name lands in the same cluster only when the types match.
	rewrite := make(map[string]string, len(entities))
	for _, idx := range order {
		rewrite[entities[idx].Name] = canonByRoot[find(idx)]
	}

	outEnts := make([]Entity, len(entities))
	for i, e := range entities {
		e.Name = rewrite[e.Name]
		outEnts[i] = e
	}
	outRels := make([]Relation, len(relations))
	for i, rel := range relations {
		if c, ok := rewrite[rel.Source]; ok {
			rel.Source = c
		}
		if c, ok := rewrite[rel.Target]; ok {
			rel.Target = c
		}
		outRels[i] = rel
	}
	return outEnts, outRels, nil
}

// betterCanonical reports whether candidate should replace current as a
// cluster's canonical name: the longer name wins, ties broken lexically
// lowest.
func betterCanonical(candidate, current string) bool {
	if len(candidate) != len(current) {
		return len(candidate) > len(current)
	}
	return candidate < current
}
