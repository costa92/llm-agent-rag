package ingest

import (
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/guard"
	"github.com/costa92/llm-agent-rag/obs"
)

// Document is one source document to be ingested.
type Document struct {
	ID               string         // ID uniquely identifies the document.
	Title            string         // Title is the document title.
	Content          string         // Content is the full document text.
	SourceID         string         // SourceID identifies the upstream source the document came from.
	Version          string         // Version is the document's source version, if any.
	Checksum         string         // Checksum is a content checksum used to detect changes.
	EmbeddingVersion string         // EmbeddingVersion records which embedding model produced its chunks.
	Metadata         map[string]any // Metadata carries arbitrary per-document key/value pairs.
}

// Chunk is one splittable unit of a Document, ready for embedding and storage.
type Chunk struct {
	ID       string         // ID uniquely identifies the chunk.
	DocID    string         // DocID is the ID of the source document.
	Index    int            // Index is the chunk's zero-based position within the document.
	Total    int            // Total is the number of chunks the document split into.
	Title    string         // Title is the source document title.
	Content  string         // Content is the chunk text.
	Metadata map[string]any // Metadata carries arbitrary per-chunk key/value pairs.
}

// ImportResult summarizes the outcome of one ingest run.
type ImportResult struct {
	Documents  int               // Documents is the number of documents imported.
	Chunks     int               // Chunks is the number of chunks produced.
	ChunkIDs   []string          // ChunkIDs are the IDs of every produced chunk.
	Metrics    obs.Metrics       // Metrics is the cost-and-latency record for the run.
	Redactions []guard.Redaction // Redactions records PII redactions applied during ingest.
	Graph      *graph.Graph      // Graph is the entity graph extracted during ingest, if any.
}
