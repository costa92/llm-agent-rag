package rag

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/store"
)

// usageGlobalScriptedModel mirrors globalScriptedModel but tags each
// generate.Response with a configurable Usage so the v1.9.0 AskGlobal
// budget tests can size cumulative token usage per sub-stage.
//
//	mapUsage  is the Usage returned for every "global_map" Generate call.
//	reduceUsage is the Usage returned for the single "global_reduce" Generate call.
//
// The text bodies are fixed strings — the budget tests do not inspect the
// reduce output, only the (Answer{}, *BudgetExceededError) shape and the
// PartialDiagnostics.Global counters.
type usageGlobalScriptedModel struct {
	mu          sync.Mutex
	mapText     string
	reduceText  string
	mapUsage    generate.Usage
	reduceUsage generate.Usage
	mapCalls    int
	reduceCalls int
}

func (m *usageGlobalScriptedModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case strings.HasPrefix(req.SystemPrompt, "You are answering a whole-corpus question using ONE community summary"):
		m.mapCalls++
		return generate.Response{Text: m.mapText, Usage: m.mapUsage}, nil
	case strings.HasPrefix(req.SystemPrompt, "You are answering a whole-corpus question.\nYou are given"):
		m.reduceCalls++
		return generate.Response{Text: m.reduceText, Usage: m.reduceUsage}, nil
	default:
		return generate.Response{Text: ""}, nil
	}
}

// usageDriftScriptedModel mirrors driftScriptedModel for the v1.9.0
// AskDrift budget tests: it tags primer/local/synth Generate calls with a
// per-stage Usage so the budget trip can be steered to any sub-stage.
//
//	primerUsage  is the Usage returned for every "drift_primer" call.
//	localUsage   is the Usage returned for every "drift_local" round call.
//	synthUsage   is the Usage returned for the single "drift_synth" call.
//
// zeroScoreMarkers makes selected primer reports score 0 (kept out of the
// local-loop seed set). localResponses is served one entry per round (last
// entry repeats), the same idiom as driftScriptedModel.
type usageDriftScriptedModel struct {
	mu               sync.Mutex
	mapText          string
	zeroScoreMarkers []string
	localResponses   []string
	synthesisText    string

	primerUsage generate.Usage
	localUsage  generate.Usage
	synthUsage  generate.Usage

	primerCalls int
	localCalls  int
	synthCalls  int
}

func (m *usageDriftScriptedModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case strings.HasPrefix(req.SystemPrompt, "You are answering a whole-corpus question using ONE community summary"):
		m.primerCalls++
		content := ""
		if len(req.Messages) > 0 {
			content = req.Messages[0].Content
		}
		for _, marker := range m.zeroScoreMarkers {
			if strings.Contains(content, marker) {
				return generate.Response{Text: "Score: 0\nThis community is irrelevant.", Usage: m.primerUsage}, nil
			}
		}
		return generate.Response{Text: m.mapText, Usage: m.primerUsage}, nil
	case strings.HasPrefix(req.SystemPrompt, "You are answering a question using a slice of a knowledge graph"):
		idx := m.localCalls
		m.localCalls++
		if len(m.localResponses) == 0 {
			return generate.Response{Text: "A local partial answer.\nFollow-up: none", Usage: m.localUsage}, nil
		}
		if idx >= len(m.localResponses) {
			idx = len(m.localResponses) - 1
		}
		return generate.Response{Text: m.localResponses[idx], Usage: m.localUsage}, nil
	case strings.HasPrefix(req.SystemPrompt, "You are answering a question using DRIFT search."):
		m.synthCalls++
		return generate.Response{Text: m.synthesisText, Usage: m.synthUsage}, nil
	default:
		return generate.Response{Text: ""}, nil
	}
}

// TestAsk_BudgetExceeded_ReturnsZeroAnswer_AndTypedError asserts that
// Ask returns (Answer{}, *BudgetExceededError) when MaxTotalTokens is
// exceeded by the answer-stage Generate.
func TestAsk_BudgetExceeded_ReturnsZeroAnswer_AndTypedError(t *testing.T) {
	model := &scriptedUsageModel{
		responses: []generate.Response{
			{Text: "an answer", Usage: generate.Usage{PromptTokens: 80, CompletionTokens: 40, TotalTokens: 120}},
		},
	}
	sys := New(Options{Model: model})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of France", AskOptions{
		Search:         SearchOptions{Namespace: "geo", TopK: 1},
		MaxTotalTokens: 50,
	})
	if err == nil {
		t.Fatalf("Ask: want budget error")
	}
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("err type = %T, want *BudgetExceededError", err)
	}
	if ans.Text != "" {
		t.Errorf("ans.Text = %q, want empty Answer on budget abort", ans.Text)
	}
	if len(ans.Hits) != 0 {
		t.Errorf("ans.Hits len = %d, want 0 on budget abort", len(ans.Hits))
	}
	if !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Errorf("errors.Is to sentinel = false; want true")
	}
}

// TestAsk_BudgetExceeded_PartialDiagnostics_CarriesStageTokenUsage
// asserts the PartialDiagnostics carries the StageTokenUsage snapshot
// for the one Generate call that tripped the budget.
func TestAsk_BudgetExceeded_PartialDiagnostics_CarriesStageTokenUsage(t *testing.T) {
	model := &scriptedUsageModel{
		responses: []generate.Response{
			{Text: "an answer", Usage: generate.Usage{TotalTokens: 200}},
		},
	}
	sys := New(Options{Model: model})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	_, err := sys.Ask(context.Background(), "capital of France", AskOptions{
		Search:         SearchOptions{Namespace: "geo", TopK: 1},
		MaxTotalTokens: 50,
	})
	if err == nil {
		t.Fatalf("Ask: want budget error")
	}
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("err type = %T, want *BudgetExceededError", err)
	}
	stages := budgetErr.PartialDiagnostics.Metrics.StageTokenUsage
	if len(stages) != 1 {
		t.Fatalf("PartialDiagnostics.StageTokenUsage len = %d, want 1 (overage call recorded): %+v", len(stages), stages)
	}
	if stages[0].Stage != StageAsk {
		t.Errorf("StageTokenUsage[0].Stage = %q, want %q", stages[0].Stage, StageAsk)
	}
	if stages[0].Usage.TotalTokens != 200 {
		t.Errorf("StageTokenUsage[0].TotalTokens = %d, want 200", stages[0].Usage.TotalTokens)
	}
}

// TestAsk_BudgetExceeded_PartialDiagnostics_CarriesPartialReflectionRounds
// asserts that rule-mode reflection with MaxRounds=3 and a budget that
// allows exactly 2 rounds returns PartialDiagnostics with 2 reflection
// RoundDetails recorded.
func TestAsk_BudgetExceeded_PartialDiagnostics_CarriesPartialReflectionRounds(t *testing.T) {
	model := &scriptedUsageModel{
		responses: []generate.Response{
			// Round 1 answer: 50 tokens
			{Text: "round 1", Usage: generate.Usage{TotalTokens: 50}},
			// Round 2 answer: 50 tokens (cumulative 100 → still <= 150)
			{Text: "round 2", Usage: generate.Usage{TotalTokens: 50}},
			// Round 3 answer: 100 tokens (cumulative 200 → > 150 → trip)
			{Text: "round 3", Usage: generate.Usage{TotalTokens: 100}},
		},
	}
	sys := New(Options{
		Model: model,
		Retriever: orderedResultRetriever{
			results: map[string][]store.Hit{
				"capital of france": {
					orderedHit("docA", "doc1", 0.9, "alpha"),
				},
			},
		},
		Packer: orderedAllPacker{},
	})
	_, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:         SearchOptions{Namespace: "geo", TopK: 1},
		Template:       promptRoutingTemplate{},
		MaxTotalTokens: 150,
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        3,
			MinHits:          99, // never satisfied → loop until MaxRounds
			MinScore:         99,
			MinUniqueDocs:    99,
			RequireCitations: false,
		},
	})
	if err == nil {
		t.Fatalf("Ask: want budget error")
	}
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("err type = %T, want *BudgetExceededError", err)
	}
	got := len(budgetErr.PartialDiagnostics.Reflection.RoundDetails)
	if got != 2 {
		t.Fatalf("PartialDiagnostics.Reflection.RoundDetails len = %d, want 2 (budget allowed 2 of 3 rounds): rounds=%+v", got, budgetErr.PartialDiagnostics.Reflection.RoundDetails)
	}
}

// TestAsk_BudgetExceeded_PartialDiagnostics_HasCallCounts asserts the
// embed/generate counters are populated in the PartialDiagnostics.
func TestAsk_BudgetExceeded_PartialDiagnostics_HasCallCounts(t *testing.T) {
	model := &scriptedUsageModel{
		responses: []generate.Response{
			{Text: "answer", Usage: generate.Usage{TotalTokens: 100}},
		},
	}
	sys := New(Options{Model: model})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	_, err := sys.Ask(context.Background(), "capital", AskOptions{
		Search:         SearchOptions{Namespace: "geo", TopK: 1},
		MaxTotalTokens: 50,
	})
	if err == nil {
		t.Fatalf("Ask: want budget error")
	}
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("err type = %T, want *BudgetExceededError", err)
	}
	calls := budgetErr.PartialDiagnostics.Metrics.Calls
	if calls.Generate < 1 {
		t.Errorf("PartialDiagnostics.Calls.Generate = %d, want >= 1", calls.Generate)
	}
	if calls.Embed < 1 {
		t.Errorf("PartialDiagnostics.Calls.Embed = %d, want >= 1 (query embed)", calls.Embed)
	}
}

// TestAsk_NoBudget_UnaffectedByMaxTotalTokensZero asserts MaxTotalTokens
// == 0 leaves Ask byte-for-byte equivalent to the v1.6.0 path — a clean
// (Answer{...}, nil) return regardless of usage size.
func TestAsk_NoBudget_UnaffectedByMaxTotalTokensZero(t *testing.T) {
	model := &scriptedUsageModel{
		responses: []generate.Response{
			{Text: "answer", Usage: generate.Usage{TotalTokens: 999_999}},
		},
	}
	sys := New(Options{Model: model})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital", AskOptions{
		Search:         SearchOptions{Namespace: "geo", TopK: 1},
		MaxTotalTokens: 0,
	})
	if err != nil {
		t.Fatalf("Ask with zero budget: %v", err)
	}
	if ans.Text == "" {
		t.Fatalf("Ask returned empty answer despite no budget")
	}
}

// --- v1.9.0 C-BudgetExpand: AskGlobal ---------------------------------

// TestAskGlobal_BudgetExceeded_ReturnsZeroAnswer_AndTypedError asserts
// that AskGlobal returns (Answer{}, *BudgetExceededError) when
// GlobalOptions.MaxTotalTokens is exceeded by the first map-step
// Generate call. The error wraps ErrTokenBudgetExceeded and carries the
// global_map stage tag.
func TestAskGlobal_BudgetExceeded_ReturnsZeroAnswer_AndTypedError(t *testing.T) {
	st, _ := newGlobalTestStore(t, "kb")
	model := &usageGlobalScriptedModel{
		mapText:    "Score: 80\nrelevant",
		reduceText: "the answer",
		// One map call costs 200 tokens > the 50-token budget → first
		// map Generate trips the budget.
		mapUsage:    generate.Usage{TotalTokens: 200},
		reduceUsage: generate.Usage{TotalTokens: 5},
	}
	sys := New(Options{
		Model:               model,
		Store:               st,
		CommunitySummarizer: staticSummarizer{},
	})

	ans, err := sys.AskGlobal(context.Background(), "themes", GlobalOptions{
		Namespace:      "kb",
		MaxTotalTokens: 50,
	})
	if err == nil {
		t.Fatalf("AskGlobal: want budget error")
	}
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("err type = %T, want *BudgetExceededError", err)
	}
	if ans.Text != "" {
		t.Errorf("ans.Text = %q, want empty Answer on budget abort", ans.Text)
	}
	if !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Errorf("errors.Is to sentinel = false; want true")
	}
	if budgetErr.Stage != StageAskGlobalMap {
		t.Errorf("Stage = %q, want %q", budgetErr.Stage, StageAskGlobalMap)
	}
}

// TestAskGlobal_BudgetExceeded_PartialDiagnostics_HasGlobal asserts the
// PartialDiagnostics.Global is populated with the trace collected up to
// the abort: CommunityIDs/MapScores/MapCalls reflect the map calls that
// completed before the trip, ReduceCalls == 0, and ConsultedReports is
// the full set of reports selected pre-map.
func TestAskGlobal_BudgetExceeded_PartialDiagnostics_HasGlobal(t *testing.T) {
	st, comms := newGlobalTestStore(t, "kb")
	level := coarsestLevel(comms)
	totalCoarse := 0
	for _, c := range comms {
		if c.Level == level {
			totalCoarse++
		}
	}
	if totalCoarse < 3 {
		t.Skipf("graph yielded only %d coarsest communities — need >=3 to assert mid-loop trip", totalCoarse)
	}
	model := &usageGlobalScriptedModel{
		mapText:    "Score: 80\npartial",
		reduceText: "final",
		// Each map call costs 60 tokens. Budget 150 allows exactly 2
		// successful map calls (60 + 60 = 120 <= 150); the 3rd map call
		// pushes cumulative to 180 > 150 → trip in mid-map loop.
		mapUsage:    generate.Usage{TotalTokens: 60},
		reduceUsage: generate.Usage{TotalTokens: 1},
	}
	sys := New(Options{
		Model:               model,
		Store:               st,
		CommunitySummarizer: staticSummarizer{},
	})

	_, err := sys.AskGlobal(context.Background(), "themes", GlobalOptions{
		Namespace:      "kb",
		MaxTotalTokens: 150,
	})
	if err == nil {
		t.Fatalf("AskGlobal: want budget error")
	}
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("err type = %T, want *BudgetExceededError", err)
	}
	g := budgetErr.PartialDiagnostics.Global
	if g.MapCalls != 2 {
		t.Errorf("PartialDiagnostics.Global.MapCalls = %d, want 2 (budget allowed 2 of %d map calls)", g.MapCalls, totalCoarse)
	}
	if len(g.MapScores) != 2 {
		t.Errorf("len(MapScores) = %d, want 2", len(g.MapScores))
	}
	if len(g.CommunityIDs) != 2 {
		t.Errorf("len(CommunityIDs) = %d, want 2 (the two that scored before trip)", len(g.CommunityIDs))
	}
	if g.ReduceCalls != 0 {
		t.Errorf("ReduceCalls = %d, want 0 (trip happened pre-reduce)", g.ReduceCalls)
	}
	if len(g.ConsultedReports) != totalCoarse {
		t.Errorf("len(ConsultedReports) = %d, want %d (full selected set, since selection ran before the trip)", len(g.ConsultedReports), totalCoarse)
	}
	if budgetErr.Stage != StageAskGlobalMap {
		t.Errorf("Stage = %q, want %q", budgetErr.Stage, StageAskGlobalMap)
	}
}

// TestAskGlobal_BudgetExceeded_AtReduce asserts that when the budget is
// large enough for every map call but the reduce Generate pushes
// cumulative usage past the cap, AskGlobal trips at global_reduce. The
// PartialDiagnostics.Global then carries the full map output (MapCalls
// == len(reports)) and ReduceCalls == 0 (the reduce call did not
// complete from the caller's perspective — it triggered the abort).
func TestAskGlobal_BudgetExceeded_AtReduce(t *testing.T) {
	st, comms := newGlobalTestStore(t, "kb")
	level := coarsestLevel(comms)
	totalCoarse := 0
	for _, c := range comms {
		if c.Level == level {
			totalCoarse++
		}
	}
	model := &usageGlobalScriptedModel{
		mapText:    "Score: 80\npartial",
		reduceText: "final",
		// Every map call costs 10 tokens. The reduce call costs 1000.
		// Budget 500 allows every map call (10 * totalCoarse) and trips
		// on the reduce call.
		mapUsage:    generate.Usage{TotalTokens: 10},
		reduceUsage: generate.Usage{TotalTokens: 1000},
	}
	sys := New(Options{
		Model:               model,
		Store:               st,
		CommunitySummarizer: staticSummarizer{},
	})

	_, err := sys.AskGlobal(context.Background(), "themes", GlobalOptions{
		Namespace:      "kb",
		MaxTotalTokens: 500,
	})
	if err == nil {
		t.Fatalf("AskGlobal: want budget error")
	}
	var budgetErr *BudgetExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("err type = %T, want *BudgetExceededError", err)
	}
	if budgetErr.Stage != StageAskGlobalReduce {
		t.Errorf("Stage = %q, want %q", budgetErr.Stage, StageAskGlobalReduce)
	}
	g := budgetErr.PartialDiagnostics.Global
	if g.MapCalls != totalCoarse {
		t.Errorf("MapCalls = %d, want %d (every map call ran before reduce tripped)", g.MapCalls, totalCoarse)
	}
	if g.ReduceCalls != 0 {
		t.Errorf("ReduceCalls = %d, want 0 (the reduce that tripped does not count as completed)", g.ReduceCalls)
	}
	if len(g.ConsultedReports) != totalCoarse {
		t.Errorf("len(ConsultedReports) = %d, want %d", len(g.ConsultedReports), totalCoarse)
	}
}

// TestAskGlobal_NoBudget_UnaffectedByMaxTotalTokensZero asserts that
// MaxTotalTokens == 0 leaves AskGlobal byte-for-byte equivalent to v1.8.0
// — a clean (Answer{...}, nil) return regardless of usage size.
func TestAskGlobal_NoBudget_UnaffectedByMaxTotalTokensZero(t *testing.T) {
	st, _ := newGlobalTestStore(t, "kb")
	model := &usageGlobalScriptedModel{
		mapText:     "Score: 70\nrelevant",
		reduceText:  "the synthesis",
		mapUsage:    generate.Usage{TotalTokens: 999_999},
		reduceUsage: generate.Usage{TotalTokens: 999_999},
	}
	sys := New(Options{
		Model:               model,
		Store:               st,
		CommunitySummarizer: staticSummarizer{},
	})

	ans, err := sys.AskGlobal(context.Background(), "themes", GlobalOptions{
		Namespace:      "kb",
		MaxTotalTokens: 0,
	})
	if err != nil {
		t.Fatalf("AskGlobal with zero budget: %v", err)
	}
	if ans.Text != "the synthesis" {
		t.Errorf("ans.Text = %q, want %q", ans.Text, "the synthesis")
	}
}
