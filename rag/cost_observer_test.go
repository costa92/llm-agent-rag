package rag

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/store"
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

func TestObserverOnGenerateUsageReflectionStage(t *testing.T) {
	rec := &observerRecorder{}
	// Model-mode reflection needs two responses per round (answer + decision).
	// Use ReflectionModeModel with MaxRounds=1 and an immediate stop verdict.
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1", Usage: generate.Usage{PromptTokens: 11, CompletionTokens: 5, TotalTokens: 16}},
			{Text: "decision=stop\nreason=good enough", Usage: generate.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}},
		},
	}
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
	if _, err := sys.Ask(context.Background(), "capital of France", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:      ReflectionModeModel,
			MaxRounds: 1,
		},
	}); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	stages := rec.Stages()
	hasAsk := false
	hasReflectionDecision := false
	for _, s := range stages {
		switch s {
		case "ask":
			hasAsk = true
		case "reflection_decision":
			hasReflectionDecision = true
		}
	}
	if !hasAsk {
		t.Fatalf("missing \"ask\" stage in %v", stages)
	}
	if !hasReflectionDecision {
		t.Fatalf("missing \"reflection_decision\" stage in %v", stages)
	}
}

func TestObserverOnGenerateUsageGraderStage(t *testing.T) {
	rec := &observerRecorder{}
	// askModel just answers; graderModel emits a parseable score line.
	askModel := &recordingModel{resp: generate.Response{Text: "the answer"}}
	graderModel := &recordingModel{resp: generate.Response{Text: "score=0.8"}}
	sys := New(Options{
		Model:  askModel,
		Grader: PromptGrader{Model: graderModel},
		Observer: Observer{
			OnGenerateUsage: rec.Hook,
		},
	})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := sys.Ask(context.Background(), "capital of France", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:               ReflectionModeRule,
			MaxRounds:          1,
			EnableChunkGrading: true,
		},
	}); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	stages := rec.Stages()
	hasGrader := false
	for _, s := range stages {
		if s == "grader" {
			hasGrader = true
			break
		}
	}
	if !hasGrader {
		t.Fatalf("missing \"grader\" stage in %v", stages)
	}
}

func TestObserverOnGenerateUsagePlannerStage(t *testing.T) {
	rec := &observerRecorder{}
	// askModel answers; plannerModel emits a follow-up query line. The
	// active-retrieval driver only consults the planner when seed
	// relevance is below the floor — set the floor high so it fires.
	askModel := &recordingModel{resp: generate.Response{Text: "the answer"}}
	plannerModel := &recordingModel{resp: generate.Response{Text: "follow-up query"}}
	sys := New(Options{
		Model:        askModel,
		QueryPlanner: PromptQueryPlanner{Model: plannerModel},
		Observer: Observer{
			OnGenerateUsage: rec.Hook,
		},
	})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := sys.Ask(context.Background(), "capital of France", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:                          ReflectionModeRule,
			MaxRounds:                     1,
			EnableActiveRetrieval:         true,
			ActiveRetrievalRelevanceFloor: 0.99,
		},
	}); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	stages := rec.Stages()
	hasPlanner := false
	for _, s := range stages {
		if s == "planner" {
			hasPlanner = true
			break
		}
	}
	if !hasPlanner {
		t.Fatalf("missing \"planner\" stage in %v: %+v", stages, rec.Snapshot())
	}
}

// fixedGrader is a custom (non-PromptGrader) Grader that should NOT be
// auto-wrapped — the v1.5.0 contract documents that only shipped types
// are rebuilt during rag.New(opts). This test pins that limitation.
type fixedGrader struct{}

func (fixedGrader) ScoreRelevance(_ context.Context, _ string, _ store.Hit) (float64, string, error) {
	return 0.5, "fixed", nil
}

func (fixedGrader) ScoreSupport(_ context.Context, _ string, _ store.Hit) (float64, string, error) {
	return 0.5, "fixed", nil
}

func TestCustomGraderNotAutoWrapped(t *testing.T) {
	rec := &observerRecorder{}
	sys := New(Options{
		Model:  &recordingModel{resp: generate.Response{Text: "the answer"}},
		Grader: fixedGrader{},
		Observer: Observer{
			OnGenerateUsage: rec.Hook,
		},
	})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := sys.Ask(context.Background(), "capital of France", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:               ReflectionModeRule,
			MaxRounds:          1,
			EnableChunkGrading: true,
		},
	}); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	for _, s := range rec.Stages() {
		if s == "grader" {
			t.Fatalf("custom Grader was auto-wrapped — got \"grader\" stage in %v", rec.Stages())
		}
	}
}

