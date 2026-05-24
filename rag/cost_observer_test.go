package rag

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/obs"
)

// recordingModel returns a canned generate.Response and records every
// request it was asked to handle. It mirrors the v1.4.0 scriptedAsker
// pattern but at the generate.Model seam, used to exercise per-stage
// CostObserver wiring.
type recordingModel struct {
	mu       sync.Mutex
	requests []generate.Request
	resp     generate.Response
	err      error
}

func (m *recordingModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	if m.err != nil {
		return generate.Response{}, m.err
	}
	resp := m.resp
	if resp.Text == "" {
		// Fall back to echoing the user content so default fixtures keep
		// producing plausible answers.
		if len(req.Messages) > 0 {
			resp.Text = req.Messages[0].Content
		}
	}
	return resp, nil
}

// observerRecorder is a thread-safe ledger of OnGenerateUsage events used by
// the v1.5.0 CostObserver tests. The Hook method value can be assigned to
// Observer.OnGenerateUsage directly.
type observerRecorder struct {
	mu      sync.Mutex
	entries []struct {
		Stage string
		Usage obs.TokenUsage
	}
}

func (r *observerRecorder) Hook(_ context.Context, stage string, usage obs.TokenUsage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, struct {
		Stage string
		Usage obs.TokenUsage
	}{Stage: stage, Usage: usage})
}

func (r *observerRecorder) Snapshot() []struct {
	Stage string
	Usage obs.TokenUsage
} {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]struct {
		Stage string
		Usage obs.TokenUsage
	}, len(r.entries))
	copy(out, r.entries)
	return out
}

func (r *observerRecorder) Stages() []string {
	snap := r.Snapshot()
	out := make([]string, len(snap))
	for i, e := range snap {
		out[i] = e.Stage
	}
	return out
}

func TestObserverOnGenerateUsageFiresWithStageAsk(t *testing.T) {
	rec := &observerRecorder{}
	model := &recordingModel{resp: generate.Response{Text: "answer"}}
	sys := New(Options{
		Model: model,
		Observer: Observer{
			OnGenerateUsage: rec.Hook,
		},
	})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := sys.Ask(context.Background(), "capital of France",
		AskOptions{Search: SearchOptions{Namespace: "geo", TopK: 1}}); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	stages := rec.Stages()
	if len(stages) == 0 {
		t.Fatalf("OnGenerateUsage never fired")
	}
	for _, s := range stages {
		if s != "ask" {
			t.Fatalf("stage = %q, want all entries to be \"ask\"; got %v", s, stages)
		}
	}
	for i, e := range rec.Snapshot() {
		if e.Usage.TotalTokens == 0 && e.Usage.PromptTokens == 0 && e.Usage.CompletionTokens == 0 {
			t.Fatalf("entry[%d] usage is all zero: %+v", i, e.Usage)
		}
	}
}

func TestObserverOnGenerateUsageNilSafe(t *testing.T) {
	// No OnGenerateUsage hook — must not panic.
	sys := New(Options{Model: fakeModel{}})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := sys.Ask(context.Background(), "capital of France",
		AskOptions{Search: SearchOptions{Namespace: "geo", TopK: 1}}); err != nil {
		t.Fatalf("Ask with nil OnGenerateUsage hook: %v", err)
	}
}

func TestCountingModelTagsStage(t *testing.T) {
	rec := &observerRecorder{}
	inner := &recordingModel{resp: generate.Response{Text: "ok"}}
	obsRef := &Observer{OnGenerateUsage: rec.Hook}
	cm := wrapCounting(inner, "custom", obsRef)
	req := generate.Request{
		Messages: []generate.Message{{Role: "user", Content: "hello"}},
	}
	if _, err := cm.Generate(context.Background(), req); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	snap := rec.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Snapshot len = %d, want 1: %+v", len(snap), snap)
	}
	if snap[0].Stage != "custom" {
		t.Fatalf("stage = %q, want %q", snap[0].Stage, "custom")
	}
}

func TestCountingModelSkipsOnError(t *testing.T) {
	rec := &observerRecorder{}
	inner := &recordingModel{err: errors.New("boom")}
	obsRef := &Observer{OnGenerateUsage: rec.Hook}
	cm := wrapCounting(inner, "ask", obsRef)
	req := generate.Request{
		Messages: []generate.Message{{Role: "user", Content: "hi"}},
	}
	if _, err := cm.Generate(context.Background(), req); err == nil {
		t.Fatalf("Generate err = nil, want non-nil")
	}
	if snap := rec.Snapshot(); len(snap) != 0 {
		t.Fatalf("hook fired on error path: %+v", snap)
	}
}
