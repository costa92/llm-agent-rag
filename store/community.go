package store

import (
	"context"
	"sort"

	"github.com/costa92/llm-agent-rag/graph"
)

var _ CommunityStore = (*InMemoryStore)(nil)

// GraphSnapshot returns the full stored graph for a namespace — every
// entity and relation, in deterministic (sorted-by-ID) order. It is the
// input community detection needs. An unknown namespace yields an empty
// graph and no error. The returned graph is a deep copy: mutating it does
// not touch stored state.
func (s *InMemoryStore) GraphSnapshot(_ context.Context, namespace string) (graph.Graph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ng := s.graphs[namespace]
	if ng == nil {
		return graph.Graph{}, nil
	}

	entIDs := make([]string, 0, len(ng.entities))
	for id := range ng.entities {
		entIDs = append(entIDs, id)
	}
	sort.Strings(entIDs)
	entities := make([]graph.Entity, 0, len(entIDs))
	for _, id := range entIDs {
		entities = append(entities, copyEntity(ng.entities[id]))
	}

	relIDs := make([]string, 0, len(ng.relations))
	for id := range ng.relations {
		relIDs = append(relIDs, id)
	}
	sort.Strings(relIDs)
	relations := make([]graph.Relation, 0, len(relIDs))
	for _, id := range relIDs {
		relations = append(relations, copyRelation(ng.relations[id]))
	}

	return graph.Graph{Entities: entities, Relations: relations}, nil
}

// UpsertCommunities replaces the namespace's community set. Detection
// always produces the full set for a namespace, so this is replace-all —
// re-detection on re-ingest reconciles by overwriting. A deep copy is
// stored so a later mutation of the caller's slice cannot reach into the
// store.
func (s *InMemoryStore) UpsertCommunities(_ context.Context, namespace string, communities []graph.Community) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(communities) == 0 {
		delete(s.communities, namespace)
		return nil
	}
	s.communities[namespace] = copyCommunities(communities)
	return nil
}

// Communities returns the namespace's stored community set, sorted by
// community ID. An unknown namespace yields nil and no error. The result
// is a deep copy — the caller cannot mutate stored state through it.
func (s *InMemoryStore) Communities(_ context.Context, namespace string) ([]graph.Community, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stored := s.communities[namespace]
	if len(stored) == 0 {
		return nil, nil
	}
	out := copyCommunities(stored)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// PutCommunityReport persists report under (namespace, report.CommunityID),
// overwriting any existing report for that community. The report is a value
// type with only string fields, so no deep copy is needed.
func (s *InMemoryStore) PutCommunityReport(_ context.Context, namespace string, report graph.CommunityReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.reports[namespace]
	if ns == nil {
		ns = make(map[string]graph.CommunityReport)
		s.reports[namespace] = ns
	}
	ns[report.CommunityID] = report
	return nil
}

// CommunityReport returns the stored report for a community. The bool is
// false (and the error nil) for an unknown community ID — a cache miss.
func (s *InMemoryStore) CommunityReport(_ context.Context, namespace, communityID string) (graph.CommunityReport, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	report, ok := s.reports[namespace][communityID]
	return report, ok, nil
}

// copyCommunities deep-copies a community slice, including each community's
// EntityIDs and RelationIDs, so stored state is fully isolated from caller
// state in both directions.
func copyCommunities(in []graph.Community) []graph.Community {
	out := make([]graph.Community, len(in))
	for i, c := range in {
		c.EntityIDs = copyStrings(c.EntityIDs)
		c.RelationIDs = copyStrings(c.RelationIDs)
		out[i] = c
	}
	return out
}

// copyEntity deep-copies an entity's slice and map fields.
func copyEntity(e graph.Entity) graph.Entity {
	e.SourceChunkIDs = copyStrings(e.SourceChunkIDs)
	if e.Metadata != nil {
		md := make(map[string]any, len(e.Metadata))
		for k, v := range e.Metadata {
			md[k] = v
		}
		e.Metadata = md
	}
	return e
}

// copyRelation deep-copies a relation's slice fields.
func copyRelation(r graph.Relation) graph.Relation {
	r.SourceChunkIDs = copyStrings(r.SourceChunkIDs)
	return r
}

// copyStrings returns a copy of s, preserving a nil input as nil.
func copyStrings(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}
