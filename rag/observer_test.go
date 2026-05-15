package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/retrieve"
)

func TestObserverFiresOnImport(t *testing.T) {
	var got ImportTrace
	calls := 0
	sys := New(Options{
		Model:    fakeModel{},
		Splitter: ingest.NewMarkdownSplitter(500, 50),
		Observer: Observer{
			OnImport: func(ctx context.Context, trace ImportTrace) {
				calls++
				got = trace
			},
		},
	})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "d1", Content: "# Title\nHello world."},
		{ID: "d2", Content: "# Other\nMore content here."},
	}, ingest.ImportOptions{Namespace: "obs"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnImport fired %d times, want 1", calls)
	}
	if got.Namespace != "obs" {
		t.Fatalf("trace namespace = %q, want obs", got.Namespace)
	}
	if got.Documents != 2 {
		t.Fatalf("trace documents = %d, want 2", got.Documents)
	}
	if got.Chunks == 0 || got.EmbedCount == 0 {
		t.Fatalf("trace chunks/embeds zero: %+v", got)
	}
	if got.Chunks != got.EmbedCount {
		t.Fatalf("chunks (%d) != embed count (%d)", got.Chunks, got.EmbedCount)
	}
	if len(got.ChunkIDs) != got.Chunks {
		t.Fatalf("chunk ids len = %d, want %d", len(got.ChunkIDs), got.Chunks)
	}
}

func TestObserverFiresOnRetrieve(t *testing.T) {
	var got retrieve.Trace
	calls := 0
	sys := New(Options{
		Model: fakeModel{},
		Observer: Observer{
			OnRetrieve: func(ctx context.Context, trace retrieve.Trace) {
				calls++
				got = trace
			},
		},
	})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "d1", Content: "hello world about paris"},
	}, ingest.ImportOptions{Namespace: "obs"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	_, err = sys.Retrieve(context.Background(), "paris", SearchOptions{Namespace: "obs", TopK: 1})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnRetrieve fired %d times, want 1", calls)
	}
	if got.OriginalQuery != "paris" {
		t.Fatalf("trace original query = %q, want paris", got.OriginalQuery)
	}
}

func TestObserverFiresOnAsk(t *testing.T) {
	var askCalls, retrieveCalls int
	var askTrace Trace
	sys := New(Options{
		Model: fakeModel{},
		Observer: Observer{
			OnRetrieve: func(ctx context.Context, trace retrieve.Trace) { retrieveCalls++ },
			OnAsk: func(ctx context.Context, trace Trace) {
				askCalls++
				askTrace = trace
			},
		},
	})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "d1", Content: "paris capital of france"},
	}, ingest.ImportOptions{Namespace: "obs"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "what is the capital of france", AskOptions{
		Search: SearchOptions{Namespace: "obs", TopK: 1},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if askCalls != 1 {
		t.Fatalf("OnAsk fired %d times, want 1", askCalls)
	}
	if retrieveCalls != 1 {
		t.Fatalf("OnRetrieve fired %d times during Ask, want 1 (transitive)", retrieveCalls)
	}
	if askTrace.Question != "what is the capital of france" {
		t.Fatalf("OnAsk trace question = %q, want full prompt", askTrace.Question)
	}
	if askTrace.Question != ans.Trace.Question {
		t.Fatalf("OnAsk trace differs from answer trace")
	}
}

func TestObserverNilCallbacksAreSafe(t *testing.T) {
	sys := New(Options{
		Model:    fakeModel{},
		Splitter: ingest.NewMarkdownSplitter(500, 50),
		Observer: Observer{},
	})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "d1", Content: "# Title\nbody"},
	}, ingest.ImportOptions{Namespace: "obs"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := sys.Retrieve(context.Background(), "title", SearchOptions{Namespace: "obs", TopK: 1}); err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if _, err := sys.Ask(context.Background(), "title", AskOptions{Search: SearchOptions{Namespace: "obs", TopK: 1}}); err != nil {
		t.Fatalf("Ask: %v", err)
	}
}
