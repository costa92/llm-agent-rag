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
