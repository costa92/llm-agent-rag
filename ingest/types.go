package ingest

type Document struct {
	ID       string
	Title    string
	Content  string
	Metadata map[string]any
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
	Documents int
	Chunks    int
	ChunkIDs  []string
}
