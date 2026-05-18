package rag

import (
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/guard"
	"github.com/costa92/llm-agent-rag/ingest"
)

func TestImportRedactsPII(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Redactor: guard.NewPIIRedactor()})
	res, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Reach me at alice@example.com for the Paris trip."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	hits, err := sys.Retrieve(context.Background(), "Paris trip",
		SearchOptions{Namespace: "geo", TopK: 1})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("no hits")
	}
	if strings.Contains(hits[0].Chunk.Content, "alice@example.com") {
		t.Fatalf("raw email stored: %q", hits[0].Chunk.Content)
	}
	if !strings.Contains(hits[0].Chunk.Content, "[REDACTED:EMAIL]") {
		t.Fatalf("placeholder missing from stored chunk: %q", hits[0].Chunk.Content)
	}
	if len(res.Redactions) != 1 || res.Redactions[0].Kind != "email" || res.Redactions[0].Count != 1 {
		t.Fatalf("ImportResult.Redactions = %+v, want one email redaction", res.Redactions)
	}
}

func TestImportNoRedactorKeepsContent(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	res, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Reach me at alice@example.com."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(res.Redactions) != 0 {
		t.Fatalf("Redactions non-empty without a redactor: %+v", res.Redactions)
	}
	hits, err := sys.Retrieve(context.Background(), "Reach me",
		SearchOptions{Namespace: "geo", TopK: 1})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) == 0 || !strings.Contains(hits[0].Chunk.Content, "alice@example.com") {
		t.Fatalf("expected verbatim content with no redactor, got %+v", hits)
	}
}
