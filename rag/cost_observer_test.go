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

func TestAskGlobalRecordsStageTokens(t *testing.T) {
	st, _ := newGlobalTestStore(t, "kb")
	model := &globalScriptedModel{
		mapText:   "Score: 80\nThis community is highly relevant to the question.",
		reduceTxt: "The synthesized final answer.",
	}
	summarizer := &countingSummarizer{inner: staticSummarizer{}}
	sys := New(Options{
		Model:               model,
		Store:               st,
		CommunitySummarizer: summarizer,
	})
	ans, err := sys.AskGlobal(context.Background(), "what are the themes",
		GlobalOptions{Namespace: "kb"})
	if err != nil {
		t.Fatalf("AskGlobal: %v", err)
	}
	entries := ans.Diagnostics.Metrics.StageTokenUsage
	if len(entries) == 0 {
		t.Fatalf("StageTokenUsage empty; want at least one map/reduce entry")
	}
	// v1.5.1: AskGlobal's inner Generate calls now tag with the sub-stages
	// StageAskGlobalMap (per-community map step) and StageAskGlobalReduce
	// (the synthesis reduce step). The top-level "ask" tag is reserved for
	// System.Ask. See CHANGELOG v1.5.1 compat notes.
	for _, e := range entries {
		if e.Stage != StageAskGlobalMap && e.Stage != StageAskGlobalReduce {
			t.Fatalf("stage = %q, want %q or %q; got entries %+v",
				e.Stage, StageAskGlobalMap, StageAskGlobalReduce, entries)
		}
	}
}

func TestAskDriftRecordsStageTokens(t *testing.T) {
	rec := &observerRecorder{}
	// Reuse the AskDrift test harness via its existing test fixtures —
	// the test below uses the same setup as TestAskDriftPrimerLoopSynthesis.
	// We import the helper via the test package; here we just want a
	// minimal AskDrift call that exercises at least one Generate.
	st, _ := newGlobalTestStore(t, "kb")
	model := &globalScriptedModel{
		mapText:   "Score: 80\nThis community contributes.",
		reduceTxt: "Synthesized answer.",
	}
	summarizer := &countingSummarizer{inner: staticSummarizer{}}
	sys := New(Options{
		Model:               model,
		Store:               st,
		CommunitySummarizer: summarizer,
		Observer: Observer{
			OnGenerateUsage: rec.Hook,
		},
	})
	ans, err := sys.AskDrift(context.Background(), "what are the themes",
		DriftOptions{Namespace: "kb"})
	if err != nil {
		t.Fatalf("AskDrift: %v", err)
	}
	entries := ans.Diagnostics.Metrics.StageTokenUsage
	if len(entries) == 0 {
		t.Fatalf("StageTokenUsage empty; want at least one drift-substage entry")
	}
	// v1.5.1: AskDrift's inner Generate calls now tag with the sub-stages
	// StageAskDriftPrimer (per-community primer map step), StageAskDriftLocal
	// (per local follow-up round), and StageAskDriftSynth (the synthesis
	// step). See CHANGELOG v1.5.1 compat notes.
	for _, e := range entries {
		switch e.Stage {
		case StageAskDriftPrimer, StageAskDriftLocal, StageAskDriftSynth:
			// ok — one of the documented drift sub-stages.
		default:
			t.Fatalf("stage = %q, want one of %q/%q/%q; got entries %+v",
				e.Stage, StageAskDriftPrimer, StageAskDriftLocal, StageAskDriftSynth, entries)
		}
	}
}

// TestAskMetricsStageTokenUsagePopulated pins that the v1.5.0 stage-token
// accumulator surfaces into Metrics.StageTokenUsage with entries in call
// order.
func TestAskMetricsStageTokenUsagePopulated(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1", Usage: generate.Usage{PromptTokens: 11, CompletionTokens: 5, TotalTokens: 16}},
			{Text: "decision=stop\nreason=ok", Usage: generate.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}},
		},
	}
	sys := New(Options{Model: model})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:      ReflectionModeModel,
			MaxRounds: 1,
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	entries := ans.Diagnostics.Metrics.StageTokenUsage
	if len(entries) != 2 {
		t.Fatalf("StageTokenUsage len = %d, want 2; entries = %+v", len(entries), entries)
	}
	if entries[0].Stage != "ask" {
		t.Fatalf("entries[0].Stage = %q, want %q", entries[0].Stage, "ask")
	}
	if entries[1].Stage != "reflection_decision" {
		t.Fatalf("entries[1].Stage = %q, want %q", entries[1].Stage, "reflection_decision")
	}
	if entries[0].Usage.TotalTokens != 16 {
		t.Fatalf("entries[0].Usage.TotalTokens = %d, want 16", entries[0].Usage.TotalTokens)
	}
	if entries[1].Usage.TotalTokens != 10 {
		t.Fatalf("entries[1].Usage.TotalTokens = %d, want 10", entries[1].Usage.TotalTokens)
	}
}

// TestAskMetricsTokensUnchanged pins that Metrics.Tokens stays byte-for-byte
// equivalent to v1.4.0 for a reflection run — StageTokenUsage is additive
// and does NOT alter how the answer-leg TokenUsage is computed.
func TestAskMetricsTokensUnchanged(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1", Usage: generate.Usage{PromptTokens: 11, CompletionTokens: 5, TotalTokens: 16}},
			{Text: "decision=stop\nreason=ok", Usage: generate.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}},
		},
	}
	sys := New(Options{Model: model})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:      ReflectionModeModel,
			MaxRounds: 1,
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	// Existing v1.4.0 contract: per reflection.go aggregateReflectionMetrics
	// + combineMetricsSnapshots, Tokens.{PromptTokens,CompletionTokens,
	// TotalTokens} sums the answer-leg AND the decision-leg deriveTokenUsage
	// for each round. One round here: answer 11/5/16 + decision 7/3/10 =
	// 18/8/26.
	want := obs.TokenUsage{PromptTokens: 18, CompletionTokens: 8, TotalTokens: 26, Estimated: false}
	got := ans.Diagnostics.Metrics.Tokens
	if got != want {
		t.Fatalf("Metrics.Tokens = %+v, want %+v (must stay byte-identical to v1.4.0)", got, want)
	}
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

// TestObserverOnGenerateUsageDriftPrimerStage pins that AskDrift's three
// inner Generate legs — primer, local-loop, and synthesis — emit
// Observer.OnGenerateUsage with the v1.5.1 sub-stage tags
// StageAskDriftPrimer, StageAskDriftLocal, and StageAskDriftSynth in
// the canonical "primer first, then locals, then synth" order. The
// top-level "ask" tag must never appear for an AskDrift run.
func TestObserverOnGenerateUsageDriftPrimerStage(t *testing.T) {
	rec := &observerRecorder{}
	st, _ := newDriftTestStore(t, "kb")
	model := &driftScriptedModel{
		mapText: "Score: 80\nThis community contributes to the answer.",
		// Community L1-b1 scores 0 so its members aren't primer seeds —
		// matches TestAskDriftPrimerLoopSynthesis.
		zeroScoreMarkers: []string{"L1-b1"},
		localResponses: []string{
			"Round 0 partial answer.\nFollow-up: bravo",
			"Round 1 partial answer.\nFollow-up: none",
		},
		synthesisText: "The synthesized DRIFT final answer.",
	}
	sys := New(Options{
		Model:               model,
		Store:               st,
		CommunitySummarizer: staticSummarizer{},
		Observer: Observer{
			OnGenerateUsage: rec.Hook,
		},
	})
	if _, err := sys.AskDrift(context.Background(), "what are the themes",
		DriftOptions{Namespace: "kb"}); err != nil {
		t.Fatalf("AskDrift: %v", err)
	}
	stages := rec.Stages()
	if len(stages) == 0 {
		t.Fatalf("OnGenerateUsage never fired for AskDrift")
	}
	var hasPrimer, hasLocal, hasSynth bool
	var firstLocalIdx, lastPrimerIdx, synthIdx = -1, -1, -1
	for i, s := range stages {
		switch s {
		case StageAskDriftPrimer:
			hasPrimer = true
			lastPrimerIdx = i
		case StageAskDriftLocal:
			hasLocal = true
			if firstLocalIdx == -1 {
				firstLocalIdx = i
			}
		case StageAskDriftSynth:
			hasSynth = true
			synthIdx = i
		case StageAsk:
			t.Fatalf("AskDrift emitted %q stage — v1.5.1 routes inner calls through drift sub-stages (got %v)",
				StageAsk, stages)
		}
	}
	if !hasPrimer {
		t.Fatalf("missing %q stage in %v", StageAskDriftPrimer, stages)
	}
	if !hasLocal {
		t.Fatalf("missing %q stage in %v", StageAskDriftLocal, stages)
	}
	if !hasSynth {
		t.Fatalf("missing %q stage in %v", StageAskDriftSynth, stages)
	}
	// Canonical order: every primer entry precedes every local entry, and
	// the synth entry trails everything.
	if firstLocalIdx < lastPrimerIdx {
		t.Fatalf("primer and local stages interleaved (first local at %d, last primer at %d) in %v",
			firstLocalIdx, lastPrimerIdx, stages)
	}
	if synthIdx != len(stages)-1 {
		t.Fatalf("%q is not the last entry (synthIdx=%d, len=%d) in %v",
			StageAskDriftSynth, synthIdx, len(stages), stages)
	}
}

// TestObserverOnGenerateUsageGlobalMapStage pins that AskGlobal's inner map
// and reduce Generate calls fire Observer.OnGenerateUsage with the v1.5.1
// sub-stage tags StageAskGlobalMap and StageAskGlobalReduce — and never with
// the top-level "ask" tag. The constraint matches the compatibility note in
// CHANGELOG v1.5.1: AskGlobal/AskDrift now use sub-stage tags.
func TestObserverOnGenerateUsageGlobalMapStage(t *testing.T) {
	rec := &observerRecorder{}
	st, _ := newGlobalTestStore(t, "kb")
	model := &globalScriptedModel{
		mapText:   "Score: 80\nThis community is highly relevant to the question.",
		reduceTxt: "Synthesized answer.",
	}
	summarizer := &countingSummarizer{inner: staticSummarizer{}}
	sys := New(Options{
		Model:               model,
		Store:               st,
		CommunitySummarizer: summarizer,
		Observer: Observer{
			OnGenerateUsage: rec.Hook,
		},
	})
	if _, err := sys.AskGlobal(context.Background(), "what are the themes",
		GlobalOptions{Namespace: "kb"}); err != nil {
		t.Fatalf("AskGlobal: %v", err)
	}
	stages := rec.Stages()
	if len(stages) == 0 {
		t.Fatalf("OnGenerateUsage never fired for AskGlobal")
	}
	var hasMap, hasReduce bool
	for _, s := range stages {
		switch s {
		case StageAskGlobalMap:
			hasMap = true
		case StageAskGlobalReduce:
			hasReduce = true
		case StageAsk:
			t.Fatalf("AskGlobal emitted %q stage — v1.5.1 routes inner calls through %q/%q (got %v)",
				StageAsk, StageAskGlobalMap, StageAskGlobalReduce, stages)
		}
	}
	if !hasMap {
		t.Fatalf("missing %q stage in %v", StageAskGlobalMap, stages)
	}
	if !hasReduce {
		t.Fatalf("missing %q stage in %v", StageAskGlobalReduce, stages)
	}
}

