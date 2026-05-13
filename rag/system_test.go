package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
)

type fakeModel struct{}

func (fakeModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	return generate.Response{Text: req.Messages[0].Content}, nil
}

func TestSystemImportRetrieveAsk(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is in France."},
		{ID: "doc2", Content: "Berlin is in Germany."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	hits, err := sys.Retrieve(context.Background(), "Paris France", SearchOptions{Namespace: "geo", TopK: 1})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("len(hits) = %d, want 1", len(hits))
	}
	ans, err := sys.Ask(context.Background(), "Where is Paris?", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Text == "" || len(ans.Hits) != 1 || len(ans.Prompt.Messages) != 1 {
		t.Fatalf("Answer = %+v", ans)
	}
}

func TestSystemImportFrom(t *testing.T) {
	sys := New(Options{})
	_, err := sys.ImportFrom(context.Background(), ingest.StaticSource(
		ingest.Document{ID: "doc1", Content: "hello world"},
	), ingest.ImportOptions{})
	if err != nil {
		t.Fatalf("ImportFrom(): %v", err)
	}
}

func TestAskRequiresModel(t *testing.T) {
	sys := New(Options{})
	_, err := sys.Ask(context.Background(), "q", AskOptions{})
	if err != ErrModelRequired {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}
