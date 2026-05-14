package ingest

import "testing"

func TestCharSplitterSingleChunk(t *testing.T) {
	s := CharSplitter{}
	chunks := s.Split(Document{ID: "doc1", Content: "hello world"}, 500)
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}
	if chunks[0].ID != "doc1:0" {
		t.Fatalf("ID = %q", chunks[0].ID)
	}
}

func TestCharSplitterStableChunkIDs(t *testing.T) {
	s := CharSplitter{}
	doc := Document{ID: "doc42", Content: "a b c d e f g h i j k l m n o p q r s t"}
	chunks := s.Split(doc, 10)
	if len(chunks) < 2 {
		t.Fatalf("len(chunks) = %d, want >= 2", len(chunks))
	}
	for i, chunk := range chunks {
		want := "doc42:" + string(rune('0'+i))
		if chunk.ID != want {
			t.Fatalf("chunk[%d].ID = %q, want %q", i, chunk.ID, want)
		}
	}
}

func TestCharSplitterPropagatesLineageMetadata(t *testing.T) {
	s := CharSplitter{}
	chunks := s.Split(Document{
		ID:               "doc1",
		Content:          "hello world",
		SourceID:         "kb-1",
		Version:          "v3",
		Checksum:         "abc123",
		EmbeddingVersion: "embed-v2",
		Metadata: map[string]any{
			"lang": "en",
		},
	}, 500)
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}
	md := chunks[0].Metadata
	if md[MetadataSourceIDKey] != "kb-1" {
		t.Fatalf("source_id = %v, want kb-1", md[MetadataSourceIDKey])
	}
	if md[MetadataVersionKey] != "v3" {
		t.Fatalf("version = %v, want v3", md[MetadataVersionKey])
	}
	if md[MetadataChecksumKey] != "abc123" {
		t.Fatalf("checksum = %v, want abc123", md[MetadataChecksumKey])
	}
	if md[MetadataEmbeddingVersionKey] != "embed-v2" {
		t.Fatalf("embedding_version = %v, want embed-v2", md[MetadataEmbeddingVersionKey])
	}
	if md["lang"] != "en" {
		t.Fatalf("lang = %v, want en", md["lang"])
	}
}
