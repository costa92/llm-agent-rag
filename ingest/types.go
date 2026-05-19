package ingest

import (
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/guard"
	"github.com/costa92/llm-agent-rag/obs"
)

type Document struct {
	ID               string
	Title            string
	Content          string
	SourceID         string
	Version          string
	Checksum         string
	EmbeddingVersion string
	Metadata         map[string]any
}

type Chunk struct {
	ID       string
	DocID    string
	Index    int
	Total    int
	Title    string
	Content  string
	Metadata map[string]any
}

type ImportResult struct {
	Documents  int
	Chunks     int
	ChunkIDs   []string
	Metrics    obs.Metrics
	Redactions []guard.Redaction
	Graph      *graph.Graph
}
