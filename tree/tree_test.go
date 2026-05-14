package tree

import (
	"testing"

	"github.com/costa92/llm-agent-rag/ingest"
)

func TestBuildCreatesSectionHierarchy(t *testing.T) {
	doc := ingest.Document{
		ID:      "doc1",
		Title:   "Guide",
		Content: "# Cities\nParis is in France.\n## Travel\nMuseums and cafes.\n## History\nAncient capital notes.",
	}
	chunks := ingest.NewMarkdownSplitter(500, 50).Split(doc, 500)

	tree := Build(doc, chunks)
	if tree.Root == nil {
		t.Fatal("root is nil")
	}
	if tree.Root.ID != "doc1:root" {
		t.Fatalf("root id = %q, want doc1:root", tree.Root.ID)
	}
	sections := tree.Sections()
	if len(sections) < 3 {
		t.Fatalf("len(sections) = %d, want >= 3", len(sections))
	}
	leaves := tree.Leaves()
	if len(leaves) != len(chunks) {
		t.Fatalf("len(leaves) = %d, want %d", len(leaves), len(chunks))
	}
}

func TestFindLocatesSectionAndChunkNodes(t *testing.T) {
	doc := ingest.Document{
		ID:      "doc1",
		Title:   "Guide",
		Content: "# Cities\nParis is in France.\n## Travel\nMuseums and cafes.\n",
	}
	chunks := ingest.NewMarkdownSplitter(500, 50).Split(doc, 500)

	tree := Build(doc, chunks)
	section, ok := tree.Find("doc1:Cities/Travel")
	if !ok {
		t.Fatal("travel section not found")
	}
	if section.Heading != "Travel" {
		t.Fatalf("section heading = %q, want Travel", section.Heading)
	}
	chunk, ok := tree.Find("doc1:1")
	if !ok {
		t.Fatal("travel chunk not found")
	}
	if chunk.Content == "" {
		t.Fatal("chunk node content empty")
	}
}
