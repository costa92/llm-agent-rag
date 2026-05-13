package store

import (
	"context"
	"errors"

	"github.com/costa92/llm-agent-rag/embed"
)

type Filter map[string]any

type Query struct {
	Namespace string
	Vector    embed.Vector
	TopK      int
	Filters   Filter
}

type Store interface {
	Upsert(ctx context.Context, chunks []StoredChunk) error
	Search(ctx context.Context, q Query) ([]Hit, error)
	Get(ctx context.Context, id string) (StoredChunk, error)
	Remove(ctx context.Context, id string) error
	Stats(ctx context.Context, namespace string) (Stats, error)
}

var ErrNotFound = errors.New("store: chunk not found")

var ErrDimensionMismatch = errors.New("store: vector dimension mismatch")
