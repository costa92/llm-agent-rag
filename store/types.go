package store

import "github.com/costa92/llm-agent-rag/embed"

type StoredChunk struct {
	ID        string
	Namespace string
	DocID     string
	Title     string
	Content   string
	Vector    embed.Vector
	Metadata  map[string]any
}

type Hit struct {
	Chunk StoredChunk
	Score float64
}

type Stats struct {
	Count int
	Dim   int
}
