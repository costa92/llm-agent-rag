package store

import "github.com/costa92/llm-agent-rag/embed"

// StoredChunk is one chunk of content as held by a Store, together with its
// embedding vector and document/section provenance.
type StoredChunk struct {
	ID           string         // ID is the chunk's unique identifier.
	Namespace    string         // Namespace is the partition the chunk belongs to.
	DocID        string         // DocID is the ID of the source document.
	Title        string         // Title is the source document title.
	SectionID    string         // SectionID identifies the chunk's section within the document.
	SectionPath  []string       // SectionPath is the heading breadcrumb to the chunk.
	Heading      string         // Heading is the chunk's immediate section heading.
	HeadingLevel int            // HeadingLevel is the depth of Heading (1 = top level).
	Content      string         // Content is the chunk text.
	Vector       embed.Vector   // Vector is the chunk's embedding.
	Metadata     map[string]any // Metadata carries arbitrary per-chunk key/value pairs.
}

// Hit is one search result: a stored chunk and its relevance score.
type Hit struct {
	Chunk StoredChunk // Chunk is the matched stored chunk.
	Score float64     // Score is the chunk's relevance to the query.
}

// Stats reports namespace-level storage statistics.
type Stats struct {
	Count int // Count is the number of chunks in the namespace.
	Dim   int // Dim is the embedding dimension of stored vectors.
}
