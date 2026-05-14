package ingest

import (
	"reflect"
	"testing"
)

func TestMarkdownSplitterPreservesSectionMetadata(t *testing.T) {
	s := MarkdownSplitter{}
	doc := Document{
		ID: "doc1",
		Content: "# Cities\n" +
			"Paris is the capital of France.\n\n" +
			"## History\n" +
			"Paris has a long history.\n\n" +
			"## Travel\n" +
			"Paris has many museums.\n",
	}

	chunks := s.Split(doc, 500)
	if len(chunks) != 3 {
		t.Fatalf("len(chunks) = %d, want 3", len(chunks))
	}

	if chunks[0].Metadata[MetadataHeadingKey] != "Cities" {
		t.Fatalf("chunk0 heading = %v, want Cities", chunks[0].Metadata[MetadataHeadingKey])
	}
	if chunks[0].Metadata[MetadataHeadingLevelKey] != 1 {
		t.Fatalf("chunk0 heading_level = %v, want 1", chunks[0].Metadata[MetadataHeadingLevelKey])
	}

	if chunks[1].Metadata[MetadataHeadingKey] != "History" {
		t.Fatalf("chunk1 heading = %v, want History", chunks[1].Metadata[MetadataHeadingKey])
	}
	if got := chunks[1].Metadata[MetadataSectionPathKey]; !reflect.DeepEqual(got, []string{"Cities", "History"}) {
		t.Fatalf("chunk1 section_path = %#v, want [Cities History]", got)
	}

	if chunks[2].Metadata[MetadataHeadingKey] != "Travel" {
		t.Fatalf("chunk2 heading = %v, want Travel", chunks[2].Metadata[MetadataHeadingKey])
	}
	if got := chunks[2].Metadata[MetadataSectionPathKey]; !reflect.DeepEqual(got, []string{"Cities", "Travel"}) {
		t.Fatalf("chunk2 section_path = %#v, want [Cities Travel]", got)
	}
}

func TestMarkdownSplitterFallsBackToCharSplitterWithoutHeadings(t *testing.T) {
	s := MarkdownSplitter{}
	doc := Document{
		ID:      "doc1",
		Content: "Plain text without markdown headings.",
	}

	chunks := s.Split(doc, 500)
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}
	if _, ok := chunks[0].Metadata[MetadataHeadingKey]; ok {
		t.Fatalf("unexpected heading metadata in fallback chunk: %+v", chunks[0].Metadata)
	}
}
