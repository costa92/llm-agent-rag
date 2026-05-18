package store

import (
	"context"
	"errors"

	"github.com/costa92/llm-agent-rag/embed"
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

var ErrNotFound = errors.New("store: chunk not found")

var ErrDimensionMismatch = errors.New("store: vector dimension mismatch")
