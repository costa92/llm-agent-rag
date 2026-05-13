package store

import (
	"context"
	"sort"
	"sync"

	"github.com/costa92/llm-agent-rag/embed"
)

type InMemoryStore struct {
	mu  sync.RWMutex
	dim int
	all map[string]StoredChunk
}

func NewInMemoryStore(dim int) *InMemoryStore {
	if dim <= 0 {
		dim = 32
	}
	return &InMemoryStore{dim: dim, all: make(map[string]StoredChunk)}
}

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

func (s *InMemoryStore) Get(_ context.Context, id string) (StoredChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	chunk, ok := s.all[id]
	if !ok {
		return StoredChunk{}, ErrNotFound
	}
	return chunk, nil
}

func (s *InMemoryStore) Remove(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.all[id]; !ok {
		return ErrNotFound
	}
	delete(s.all, id)
	return nil
}

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
