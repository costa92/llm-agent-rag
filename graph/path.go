package graph

import (
	"sort"
	"strings"
)

// RankedPath is one scored simple path between two entities within a
// Subgraph. EntityIDs is the traversal in order — path[0]..path[n];
// RelationIDs holds the n edges between consecutive entities. Score is a
// deterministic composite — higher means a stronger connecting path.
type RankedPath struct {
	EntityIDs   []string // ordered: path[0]..path[n], the traversal
	RelationIDs []string // ordered edges between consecutive entities
	Score       float64  // deterministic composite; higher = stronger
}

// PathRanker ranks simple paths within a Subgraph between seed entity
// pairs. Implementations are deterministic: the same Subgraph and the same
// seedPairs always yield the same []RankedPath, byte-for-byte.
type PathRanker interface {
	RankPaths(sub Subgraph, seedPairs [][2]string) []RankedPath
}

// maxPathLen caps the number of edges in an enumerated path. It is the
// graph-package path-length bound — graph cannot import store, so it
// carries its own const here, matching store's maxGraphDepth (2).
const maxPathLen = 2

// defaultLengthDecay is the WeightedPathRanker length-decay used when the
// configured LengthDecay is non-positive.
const defaultLengthDecay = 0.5

// provenanceBonus is the small score multiplier added once per pair of
// consecutive relations that share at least one SourceChunkID — a
// co-attested (provenance-overlapping) path scores above a scattered one.
const provenanceBonus = 0.1

// WeightedPathRanker is the default deterministic, pure-stdlib PathRanker.
// For each seed pair it enumerates every simple path (no repeated entity)
// of up to maxPathLen edges via a bounded DFS over the Subgraph's relations
// — relations treated undirected for connectivity — and scores each path by
// a composite of three signals already present in the graph:
//
//   - length: LengthDecay^(edges-1) — shorter paths score higher;
//   - edge weight: the product of edgeWeight over the path's relations;
//   - provenance overlap: a small bonus per consecutive relation pair that
//     shares a SourceChunkID — co-attested evidence ranks above scattered.
//
// The returned []RankedPath is sorted by Score descending, ties broken by
// the joined EntityIDs sequence — a total, reproducible order. Every map is
// drained into a sorted slice before iteration and the DFS visits
// neighbors in sorted entity-ID order, so ranking is fully deterministic
// (keystones KG4-4, KG4-6).
type WeightedPathRanker struct {
	// LengthDecay scales the per-extra-edge length penalty. Values in
	// (0,1) make longer paths score progressively lower; LengthDecay <= 0
	// is treated as defaultLengthDecay (0.5).
	LengthDecay float64
}

// pathEdge is a directed adjacency entry: the neighbor entity ID and the
// relation connecting to it.
type pathEdge struct {
	to    string
	relID string
}

// RankPaths implements PathRanker. It is deterministic — see the type doc.
func (r WeightedPathRanker) RankPaths(sub Subgraph, seedPairs [][2]string) []RankedPath {
	decay := r.LengthDecay
	if decay <= 0 {
		decay = defaultLengthDecay
	}

	known := make(map[string]bool, len(sub.Entities))
	for _, e := range sub.Entities {
		known[e.ID] = true
	}

	// Undirected adjacency over the subgraph's relations. A self-loop or a
	// relation whose endpoint is not a reached entity is skipped.
	adj := make(map[string][]pathEdge, len(sub.Entities))
	relByID := make(map[string]Relation, len(sub.Relations))
	for _, rel := range sub.Relations {
		relByID[rel.ID] = rel
		if rel.Source == rel.Target || !known[rel.Source] || !known[rel.Target] {
			continue
		}
		adj[rel.Source] = append(adj[rel.Source], pathEdge{to: rel.Target, relID: rel.ID})
		adj[rel.Target] = append(adj[rel.Target], pathEdge{to: rel.Source, relID: rel.ID})
	}
	// Sort each adjacency list so the DFS visits neighbors in a total,
	// reproducible order — by neighbor ID, then relation ID.
	for id := range adj {
		edges := adj[id]
		sort.Slice(edges, func(i, j int) bool {
			if edges[i].to != edges[j].to {
				return edges[i].to < edges[j].to
			}
			return edges[i].relID < edges[j].relID
		})
		adj[id] = edges
	}

	var out []RankedPath
	for _, pair := range seedPairs {
		from, to := pair[0], pair[1]
		if from == "" || to == "" || from == to {
			continue
		}
		if !known[from] || !known[to] {
			continue
		}
		visited := map[string]bool{from: true}
		enumPaths(adj, relByID, from, to, []string{from}, nil, visited, decay, &out)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return pathKey(out[i].EntityIDs) < pathKey(out[j].EntityIDs)
	})
	return out
}

// enumPaths is the bounded DFS. It extends the current simple path (curEnts
// / curRels, visited tracks membership) one edge at a time, never revisiting
// an entity, and emits a scored RankedPath every time it reaches dst within
// maxPathLen edges. Neighbors are visited in the pre-sorted adjacency order.
func enumPaths(
	adj map[string][]pathEdge,
	relByID map[string]Relation,
	cur, dst string,
	curEnts, curRels []string,
	visited map[string]bool,
	decay float64,
	out *[]RankedPath,
) {
	if len(curRels) >= maxPathLen {
		return
	}
	for _, e := range adj[cur] {
		if visited[e.to] {
			continue
		}
		nextEnts := append(append([]string(nil), curEnts...), e.to)
		nextRels := append(append([]string(nil), curRels...), e.relID)
		if e.to == dst {
			*out = append(*out, RankedPath{
				EntityIDs:   nextEnts,
				RelationIDs: nextRels,
				Score:       scorePath(nextRels, relByID, decay),
			})
			// dst reached — do not extend through it (simple path).
			continue
		}
		visited[e.to] = true
		enumPaths(adj, relByID, e.to, dst, nextEnts, nextRels, visited, decay, out)
		delete(visited, e.to)
	}
}

// scorePath computes the deterministic composite score for a path given its
// ordered relation IDs: a length-decay factor times the product of edge
// weights, times one (1 + provenanceBonus) factor per consecutive relation
// pair that shares a SourceChunkID.
func scorePath(relIDs []string, relByID map[string]Relation, decay float64) float64 {
	edges := len(relIDs)
	if edges == 0 {
		return 0
	}

	// length: decay^(edges-1) — a single edge has no penalty.
	score := 1.0
	for i := 0; i < edges-1; i++ {
		score *= decay
	}

	// edge weight: product of edgeWeight over the path's relations.
	for _, id := range relIDs {
		score *= edgeWeight(relByID[id])
	}

	// provenance overlap: bonus per consecutive relation pair co-attested
	// by a shared SourceChunkID.
	for i := 0; i+1 < edges; i++ {
		if shareChunk(relByID[relIDs[i]], relByID[relIDs[i+1]]) {
			score *= 1 + provenanceBonus
		}
	}
	return score
}

// shareChunk reports whether two relations cite at least one common
// SourceChunkID — evidence that the two hops are co-attested.
func shareChunk(a, b Relation) bool {
	if len(a.SourceChunkIDs) == 0 || len(b.SourceChunkIDs) == 0 {
		return false
	}
	seen := make(map[string]bool, len(a.SourceChunkIDs))
	for _, c := range a.SourceChunkIDs {
		seen[c] = true
	}
	for _, c := range b.SourceChunkIDs {
		if seen[c] {
			return true
		}
	}
	return false
}

// pathKey joins an entity-ID sequence into a single comparable string for
// the total tie-break. The "\x00" separator never appears in an entity ID,
// so distinct sequences never collide.
func pathKey(entityIDs []string) string {
	return strings.Join(entityIDs, "\x00")
}
