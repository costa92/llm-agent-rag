package store

import (
	"context"
	"reflect"
	"sort"
	"sync"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/graph"
)

// InMemoryStore is the built-in process-local Store. It also implements the
// LexicalSearcher, GraphStore, and CommunityStore capability interfaces, and
// is safe for concurrent use.
type InMemoryStore struct {
	mu          sync.RWMutex
	dim         int
	all         map[string]StoredChunk
	graphs      map[string]*nsGraph                         // namespace -> entity/relation graph
	communities map[string][]graph.Community                // namespace -> detected community hierarchy
	reports     map[string]map[string]graph.CommunityReport // namespace -> communityID -> report
}

// NewInMemoryStore returns an empty InMemoryStore with the given embedding
// dimension. A dim <= 0 selects a sane default (32).
func NewInMemoryStore(dim int) *InMemoryStore {
	if dim <= 0 {
		dim = 32
	}
	return &InMemoryStore{
		dim:         dim,
		all:         make(map[string]StoredChunk),
		graphs:      make(map[string]*nsGraph),
		communities: make(map[string][]graph.Community),
		reports:     make(map[string]map[string]graph.CommunityReport),
	}
}

// Upsert inserts or replaces chunks by ID, rejecting any with a vector whose
// length does not match the store dimension.
func (s *InMemoryStore) Upsert(_ context.Context, chunks []StoredChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, chunk := range chunks {
		if len(chunk.Vector) != s.dim {
			return ErrDimensionMismatch
		}
		s.all[chunk.ID] = chunk
	}
	return nil
}

// Search returns the top-K chunks ranked by cosine similarity to q.Vector.
func (s *InMemoryStore) Search(_ context.Context, q Query) ([]Hit, error) {
	if len(q.Vector) != s.dim {
		return nil, ErrDimensionMismatch
	}
	if q.TopK <= 0 {
		q.TopK = 5
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	hits := make([]Hit, 0, len(s.all))
	for _, chunk := range s.all {
		if q.Namespace != "" && chunk.Namespace != q.Namespace {
			continue
		}
		if !matchesFilters(chunk.Metadata, q.Filters) {
			continue
		}
		if !matchesFilters(chunk.Metadata, q.SecurityFilters) {
			continue
		}
		hits = append(hits, Hit{
			Chunk: chunk,
			Score: embed.CosineSimilarity(q.Vector, chunk.Vector),
		})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > q.TopK {
		hits = hits[:q.TopK]
	}
	return hits, nil
}

// Get returns the chunk with the given ID, or ErrNotFound.
func (s *InMemoryStore) Get(_ context.Context, id string) (StoredChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	chunk, ok := s.all[id]
	if !ok {
		return StoredChunk{}, ErrNotFound
	}
	return chunk, nil
}

// List returns every chunk in a namespace matching the given filters.
func (s *InMemoryStore) List(_ context.Context, namespace string, filters Filter, securityFilters Filter) ([]StoredChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]StoredChunk, 0, len(s.all))
	for _, chunk := range s.all {
		if namespace != "" && chunk.Namespace != namespace {
			continue
		}
		if !matchesFilters(chunk.Metadata, filters) {
			continue
		}
		if !matchesFilters(chunk.Metadata, securityFilters) {
			continue
		}
		out = append(out, chunk)
	}
	return out, nil
}

// Remove deletes the chunk with the given ID, or returns ErrNotFound.
func (s *InMemoryStore) Remove(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.all[id]; !ok {
		return ErrNotFound
	}
	delete(s.all, id)
	return nil
}

// RemoveByFilter deletes every chunk in a namespace matching filters and
// returns the number removed.
func (s *InMemoryStore) RemoveByFilter(_ context.Context, namespace string, filters Filter) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for id, chunk := range s.all {
		if namespace != "" && chunk.Namespace != namespace {
			continue
		}
		if !matchesFilters(chunk.Metadata, filters) {
			continue
		}
		delete(s.all, id)
		removed++
	}
	return removed, nil
}

// Stats returns chunk-count and dimension statistics for a namespace.
func (s *InMemoryStore) Stats(_ context.Context, namespace string) (Stats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, chunk := range s.all {
		if namespace == "" || chunk.Namespace == namespace {
			count++
		}
	}
	return Stats{Count: count, Dim: s.dim}, nil
}

func matchesFilters(metadata map[string]any, filters Filter) bool {
	if len(filters) == 0 {
		return true
	}
	if len(metadata) == 0 {
		return false
	}
	for key, want := range filters {
		got, ok := metadata[key]
		if !ok {
			return false
		}
		if !reflect.DeepEqual(got, want) {
			return false
		}
	}
	return true
}
