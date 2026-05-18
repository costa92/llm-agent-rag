package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/retrieve"
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
