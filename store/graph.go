package store

import (
	"context"
	"sort"
	"strings"

	"github.com/costa92/llm-agent-rag/graph"
)

// Graph-traversal bounds (KG-7): traversal never exceeds maxGraphDepth
// hops, and each hop expands at most maxGraphFanout neighbors per entity.
const (
	maxGraphDepth  = 2
	maxGraphFanout = 64
)

var _ GraphStore = (*InMemoryStore)(nil)

// nsGraph is the in-memory entity/relation graph for one namespace.
type nsGraph struct {
	entities  map[string]graph.Entity
	relations map[string]graph.Relation
}

// graphNS returns the namespace's graph, creating it if absent. The caller
// must hold s.mu for writing.
func (s *InMemoryStore) graphNS(namespace string) *nsGraph {
	g := s.graphs[namespace]
	if g == nil {
		g = &nsGraph{
			entities:  map[string]graph.Entity{},
			relations: map[string]graph.Relation{},
		}
		s.graphs[namespace] = g
	}
	return g
}

// UpsertGraph union-merges g into the namespace's graph.
func (s *InMemoryStore) UpsertGraph(_ context.Context, namespace string, g graph.Graph) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ng := s.graphNS(namespace)
	for _, e := range g.Entities {
		ng.entities[e.ID] = mergeEntity(ng.entities[e.ID], e)
	}
	for _, r := range g.Relations {
		ng.relations[r.ID] = mergeRelation(ng.relations[r.ID], r)
	}
	return nil
}

// RemoveGraphBySource drops the given chunk IDs from every entity's and
// relation's provenance, garbage-collecting any left unreferenced.
func (s *InMemoryStore) RemoveGraphBySource(_ context.Context, namespace string, chunkIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ng := s.graphs[namespace]
	if ng == nil {
		return nil
	}
	drop := make(map[string]bool, len(chunkIDs))
	for _, id := range chunkIDs {
		drop[id] = true
	}
	for id, e := range ng.entities {
		e.SourceChunkIDs = removeFrom(e.SourceChunkIDs, drop)
		if len(e.SourceChunkIDs) == 0 {
			delete(ng.entities, id)
		} else {
			ng.entities[id] = e
		}
	}
	for id, r := range ng.relations {
		r.SourceChunkIDs = removeFrom(r.SourceChunkIDs, drop)
		if len(r.SourceChunkIDs) == 0 {
			delete(ng.relations, id)
		} else {
			ng.relations[id] = r
		}
	}
	return nil
}

// Neighborhood traverses from the seed entity IDs out to depth hops
// (clamped to [0, maxGraphDepth]) and returns the reached subgraph.
func (s *InMemoryStore) Neighborhood(_ context.Context, namespace string, seedIDs []string, depth int) (graph.Subgraph, error) {
	if depth < 0 {
		depth = 0
	}
	if depth > maxGraphDepth {
		depth = maxGraphDepth
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	sub := graph.Subgraph{Depth: map[string]int{}}
	ng := s.graphs[namespace]
	if ng == nil {
		return sub, nil
	}

	// undirected adjacency, deterministically ordered
	adj := map[string][]string{}
	for _, r := range ng.relations {
		adj[r.Source] = append(adj[r.Source], r.Target)
		adj[r.Target] = append(adj[r.Target], r.Source)
	}
	for id := range adj {
		sort.Strings(adj[id])
	}

	reached := map[string]int{}
	var frontier []string
	for _, id := range seedIDs {
		if _, ok := ng.entities[id]; !ok {
			continue
		}
		if _, seen := reached[id]; !seen {
			reached[id] = 0
			frontier = append(frontier, id)
		}
	}
	sort.Strings(frontier)
	for hop := 1; hop <= depth && len(frontier) > 0; hop++ {
		var next []string
		for _, id := range frontier {
			neighbors := adj[id]
			if len(neighbors) > maxGraphFanout {
				neighbors = neighbors[:maxGraphFanout]
			}
			for _, n := range neighbors {
				if _, ok := ng.entities[n]; !ok {
					continue
				}
				if _, seen := reached[n]; seen {
					continue
				}
				reached[n] = hop
				next = append(next, n)
			}
		}
		sort.Strings(next)
		frontier = next
	}

	ids := make([]string, 0, len(reached))
	for id := range reached {
		ids = append(ids, id)
		sub.Depth[id] = reached[id]
	}
	sort.Strings(ids)
	for _, id := range ids {
		sub.Entities = append(sub.Entities, ng.entities[id])
	}
	relIDs := make([]string, 0)
	for id, r := range ng.relations {
		if _, ok := reached[r.Source]; !ok {
			continue
		}
		if _, ok := reached[r.Target]; !ok {
			continue
		}
		relIDs = append(relIDs, id)
	}
	sort.Strings(relIDs)
	for _, id := range relIDs {
		sub.Relations = append(sub.Relations, ng.relations[id])
	}
	return sub, nil
}

// FindEntities returns entities whose name matches any of names
// (case-folded exact match).
func (s *InMemoryStore) FindEntities(_ context.Context, namespace string, names []string) ([]graph.Entity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ng := s.graphs[namespace]
	if ng == nil {
		return nil, nil
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[graph.NormalizeName(n)] = true
	}
	ids := make([]string, 0, len(ng.entities))
	for id := range ng.entities {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []graph.Entity
	for _, id := range ids {
		e := ng.entities[id]
		if want[graph.NormalizeName(e.Name)] {
			out = append(out, e)
		}
	}
	return out, nil
}

func mergeEntity(existing, incoming graph.Entity) graph.Entity {
	if existing.ID == "" {
		return incoming
	}
	existing.SourceChunkIDs = mergeStrings(existing.SourceChunkIDs, incoming.SourceChunkIDs)
	existing.Description = mergeDescription(existing.Description, incoming.Description)
	return existing
}

func mergeRelation(existing, incoming graph.Relation) graph.Relation {
	if existing.ID == "" {
		return incoming
	}
	existing.SourceChunkIDs = mergeStrings(existing.SourceChunkIDs, incoming.SourceChunkIDs)
	existing.Description = mergeDescription(existing.Description, incoming.Description)
	existing.Weight += incoming.Weight
	return existing
}

func mergeStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, group := range [][]string{a, b} {
		for _, s := range group {
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// mergeDescription concatenates two descriptions, deduping the "; "-joined
// parts so repeated upserts of the same content do not grow unbounded.
func mergeDescription(a, b string) string {
	seen := map[string]bool{}
	var parts []string
	for _, src := range []string{a, b} {
		for _, p := range strings.Split(src, "; ") {
			p = strings.TrimSpace(p)
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "; ")
}

func removeFrom(values []string, drop map[string]bool) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !drop[v] {
			out = append(out, v)
		}
	}
	return out
}
