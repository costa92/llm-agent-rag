package rag

import (
	"context"
	"errors"
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

func TestAskReflectionAggregatesMetricsWhenMaxRoundsClampStopsDecision(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1", Usage: generate.Usage{PromptTokens: 11, CompletionTokens: 5, TotalTokens: 16}},
			{Text: "decision=rewrite_and_continue\nreason=need better evidence\nrewrite=zzparis", Usage: generate.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}},
			{Text: "answer round 2", Usage: generate.Usage{PromptTokens: 13, CompletionTokens: 6, TotalTokens: 19}},
			{Text: "decision=rewrite_and_continue\nreason=need one more round\nrewrite=zzparis-again", Usage: generate.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}},
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
			Mode:             ReflectionModeModel,
			MaxRounds:        2,
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
	if got := m.Tokens.PromptTokens; got != 36 {
		t.Fatalf("PromptTokens = %d, want 36", got)
	}
	if got := m.Tokens.CompletionTokens; got != 16 {
		t.Fatalf("CompletionTokens = %d, want 16", got)
	}
	if got := m.Tokens.TotalTokens; got != 52 {
		t.Fatalf("TotalTokens = %d, want 52", got)
	}
	reflectStages := 0
	for _, stage := range m.Stages {
		if stage.Stage == "reflect" {
			reflectStages++
		}
	}
	if reflectStages != 2 {
		t.Fatalf("reflect stage count = %d, want 2", reflectStages)
	}
}

// --- v1.7.0 C2 countingModel budget tests --------------------------------

// fixedUsageModel returns the same generate.Response on every call,
// regardless of request. Used to drive countingModel directly in tests.
type fixedUsageModel struct {
	resp generate.Response
}

func (m fixedUsageModel) Generate(_ context.Context, _ generate.Request) (generate.Response, error) {
	return m.resp, nil
}

// TestCountingModel_BudgetExceeded_ReturnsTypedError asserts that a
// single Generate whose Usage.TotalTokens exceeds the ctx-installed
// budget returns a *BudgetExceededError carrying Stage/Used/Budget.
func TestCountingModel_BudgetExceeded_ReturnsTypedError(t *testing.T) {
	inner := fixedUsageModel{resp: generate.Response{
		Text:  "answer",
		Usage: generate.Usage{PromptTokens: 60, CompletionTokens: 40, TotalTokens: 100},
	}}
	cm := wrapCounting(inner, StageAsk, nil)
	ctx := obs.WithStageUsage(context.Background(), obs.NewStageUsageAccumulator())
	ctx = obs.WithCounter(ctx, obs.NewCounter())
	ctx = obs.WithTokenBudget(ctx, 50)
	_, err := cm.Generate(ctx, generate.Request{})
	if err == nil {
		t.Fatalf("Generate: want budget error")
	}
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("err type = %T, want *BudgetExceededError", err)
	}
	if budgetErr.Stage != StageAsk {
		t.Errorf("Stage = %q, want %q", budgetErr.Stage, StageAsk)
	}
	if budgetErr.Used != 100 {
		t.Errorf("Used = %d, want 100", budgetErr.Used)
	}
	if budgetErr.Budget != 50 {
		t.Errorf("Budget = %d, want 50", budgetErr.Budget)
	}
}

// TestCountingModel_BudgetExceeded_FirstOverageStageWins asserts the
// Stage on the error is the stage of the overage call, not the first
// call. Used is the cumulative TotalTokens through both calls.
func TestCountingModel_BudgetExceeded_FirstOverageStageWins(t *testing.T) {
	inner1 := fixedUsageModel{resp: generate.Response{
		Text:  "r1",
		Usage: generate.Usage{TotalTokens: 30},
	}}
	inner2 := fixedUsageModel{resp: generate.Response{
		Text:  "r2",
		Usage: generate.Usage{TotalTokens: 40},
	}}
	acc := obs.NewStageUsageAccumulator()
	ctx := obs.WithStageUsage(context.Background(), acc)
	ctx = obs.WithCounter(ctx, obs.NewCounter())
	ctx = obs.WithTokenBudget(ctx, 50)
	cm1 := wrapCounting(inner1, StageAsk, nil)
	cm2 := wrapCounting(inner2, StageReflectionDecision, nil)
	if _, err := cm1.Generate(ctx, generate.Request{}); err != nil {
		t.Fatalf("call 1 should not trip (30 <= 50): %v", err)
	}
	_, err := cm2.Generate(ctx, generate.Request{})
	if err == nil {
		t.Fatalf("call 2: want budget error")
	}
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("err type = %T, want *BudgetExceededError", err)
	}
	if budgetErr.Stage != StageReflectionDecision {
		t.Errorf("Stage = %q, want %q (second call's stage wins)", budgetErr.Stage, StageReflectionDecision)
	}
	if budgetErr.Used != 70 {
		t.Errorf("Used = %d, want 70 (30+40)", budgetErr.Used)
	}
}

// TestCountingModel_NoBudget_ZeroIsUnlimited asserts that without a ctx
// budget the countingModel never returns a budget error, even when
// Usage.TotalTokens is huge.
func TestCountingModel_NoBudget_ZeroIsUnlimited(t *testing.T) {
	inner := fixedUsageModel{resp: generate.Response{
		Text:  "answer",
		Usage: generate.Usage{TotalTokens: 1_000_000},
	}}
	cm := wrapCounting(inner, StageAsk, nil)
	ctx := obs.WithStageUsage(context.Background(), obs.NewStageUsageAccumulator())
	ctx = obs.WithCounter(ctx, obs.NewCounter())
	// No WithTokenBudget — TokenBudgetFrom returns 0 (unlimited).
	if _, err := cm.Generate(ctx, generate.Request{}); err != nil {
		t.Fatalf("Generate with no budget: %v", err)
	}
}

// TestCountingModel_BudgetExceeded_AppendStillFiredForOverageCall asserts
// that the StageUsageAccumulator records the overage call BEFORE the
// budget check trips — i.e. the snapshot includes the entry whose
// addition tipped TotalSoFar past Budget.
func TestCountingModel_BudgetExceeded_AppendStillFiredForOverageCall(t *testing.T) {
	inner := fixedUsageModel{resp: generate.Response{
		Text:  "answer",
		Usage: generate.Usage{TotalTokens: 100},
	}}
	cm := wrapCounting(inner, StageAsk, nil)
	acc := obs.NewStageUsageAccumulator()
	ctx := obs.WithStageUsage(context.Background(), acc)
	ctx = obs.WithCounter(ctx, obs.NewCounter())
	ctx = obs.WithTokenBudget(ctx, 50)
	if _, err := cm.Generate(ctx, generate.Request{}); err == nil {
		t.Fatalf("Generate: want budget error")
	}
	snap := acc.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot len = %d, want 1 (overage call should be appended)", len(snap))
	}
	if snap[0].Stage != StageAsk || snap[0].Usage.TotalTokens != 100 {
		t.Errorf("snapshot[0] = %+v, want StageAsk total=100", snap[0])
	}
}
