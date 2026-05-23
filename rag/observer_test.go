package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
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

func TestAskReflectionTriggersRetrieveObserverPerRound(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1"},
			{Text: "decision=rewrite_and_continue\nreason=need better evidence\nrewrite=zzparis"},
			{Text: "answer round 2"},
			{Text: "decision=stop\nreason=sufficient evidence"},
		},
	}
	var (
		askCalls       int
		retrieveTraces []retrieve.Trace
		askTrace       Trace
	)
	sys := New(Options{
		Model: model,
		Retriever: orderedResultRetriever{
			results: map[string][]store.Hit{
				"capital of france": {
					orderedHit("docA", "doc1", 0.9, "berlin"),
				},
				"zzparis": {
					orderedHit("docB", "doc2", 0.95, "paris"),
				},
			},
		},
		Packer: orderedAllPacker{},
		Observer: Observer{
			OnRetrieve: func(_ context.Context, trace retrieve.Trace) {
				retrieveTraces = append(retrieveTraces, trace)
			},
			OnAsk: func(_ context.Context, trace Trace) {
				askCalls++
				askTrace = trace
			},
		},
	})

	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeHybrid,
			MaxRounds:        3,
			MinHits:          2,
			MinScore:         2,
			MinUniqueDocs:    2,
			RequireCitations: true,
			AllowRewrite:     true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if askCalls != 1 {
		t.Fatalf("OnAsk fired %d times, want 1", askCalls)
	}
	if len(retrieveTraces) != 2 {
		t.Fatalf("OnRetrieve fired %d times, want 2 for two internal rounds", len(retrieveTraces))
	}
	if retrieveTraces[0].OriginalQuery != "capital of france" {
		t.Fatalf("round 1 original query = %q, want %q", retrieveTraces[0].OriginalQuery, "capital of france")
	}
	if retrieveTraces[1].OriginalQuery != "zzparis" {
		t.Fatalf("round 2 original query = %q, want %q", retrieveTraces[1].OriginalQuery, "zzparis")
	}
	if askTrace.Question != ans.Trace.Question {
		t.Fatal("OnAsk trace differs from answer trace")
	}
	if askTrace.Reflection.AdoptedRound != 2 {
		t.Fatalf("OnAsk trace adopted round = %d, want 2", askTrace.Reflection.AdoptedRound)
	}
	if len(askTrace.Reflection.Rounds) != 2 {
		t.Fatalf("OnAsk reflection rounds = %d, want 2", len(askTrace.Reflection.Rounds))
	}
}
