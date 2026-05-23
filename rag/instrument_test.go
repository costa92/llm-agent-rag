package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

func hasStage(stages []obs.StageTiming, name string) bool {
	for _, s := range stages {
		if s.Stage == name {
			return true
		}
	}
	return false
}

func TestAskRecordsMetrics(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of France",
		AskOptions{Search: SearchOptions{Namespace: "geo", TopK: 1}})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	m := ans.Diagnostics.Metrics
	if m.TotalDuration <= 0 {
		t.Fatalf("Diagnostics.Metrics.TotalDuration = %v, want > 0", m.TotalDuration)
	}
	if !hasStage(m.Stages, "retrieve") || !hasStage(m.Stages, "generate") {
		t.Fatalf("Stages missing retrieve/generate: %+v", m.Stages)
	}
	if m.Calls.Generate < 1 {
		t.Fatalf("Calls.Generate = %d, want >= 1 (the answer generation)", m.Calls.Generate)
	}
}

func TestImportRecordsMetrics(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	res, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is in France."},
		{ID: "doc2", Content: "Berlin is in Germany."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Metrics.Calls.Embed != res.Chunks {
		t.Fatalf("Metrics.Calls.Embed = %d, want %d (one embed per chunk)",
			res.Metrics.Calls.Embed, res.Chunks)
	}
	if !hasStage(res.Metrics.Stages, "embed") || !hasStage(res.Metrics.Stages, "upsert") {
		t.Fatalf("import Stages missing embed/upsert: %+v", res.Metrics.Stages)
	}
}

func TestRetrieveRecordsMetricsViaObserver(t *testing.T) {
	var captured retrieve.Trace
	sys := New(Options{
		Model: fakeModel{},
		Observer: Observer{
			OnRetrieve: func(_ context.Context, tr retrieve.Trace) { captured = tr },
		},
	})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := sys.Retrieve(context.Background(), "capital of France",
		SearchOptions{Namespace: "geo", TopK: 1}); err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if !hasStage(captured.Metrics.Stages, "preprocess") || !hasStage(captured.Metrics.Stages, "retrieve") {
		t.Fatalf("retrieve Trace.Metrics missing preprocess/retrieve stages: %+v", captured.Metrics.Stages)
	}
}

func TestAskReflectionAggregatesMetricsAcrossRounds(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1", Usage: generate.Usage{PromptTokens: 11, CompletionTokens: 5, TotalTokens: 16}},
			{Text: "decision=rewrite_and_continue\nreason=need better evidence\nrewrite=zzparis", Usage: generate.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}},
			{Text: "answer round 2", Usage: generate.Usage{PromptTokens: 13, CompletionTokens: 6, TotalTokens: 19}},
			{Text: "decision=stop\nreason=sufficient evidence", Usage: generate.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}},
		},
	}
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

	m := ans.Diagnostics.Metrics
	if m.Calls.Generate != 4 {
		t.Fatalf("Calls.Generate = %d, want 4 across answer+decision model calls", m.Calls.Generate)
	}
	if m.TotalDuration <= 0 {
		t.Fatalf("TotalDuration = %v, want > 0", m.TotalDuration)
	}
	if got := m.Tokens.PromptTokens; got != 36 {
		t.Fatalf("PromptTokens = %d, want 36", got)
	}
	if got := m.Tokens.CompletionTokens; got != 16 {
		t.Fatalf("CompletionTokens = %d, want 16", got)
	}
	if got := m.Tokens.TotalTokens; got != 52 {
		t.Fatalf("TotalTokens = %d, want 52", got)
	}
	retrieveStages := 0
	generateStages := 0
	for _, stage := range m.Stages {
		switch stage.Stage {
		case "retrieve":
			retrieveStages++
		case "generate":
			generateStages++
		}
	}
	if retrieveStages != 2 {
		t.Fatalf("retrieve stage count = %d, want 2", retrieveStages)
	}
	if generateStages != 2 {
		t.Fatalf("generate stage count = %d, want 2 answer-generation stages", generateStages)
	}
}
