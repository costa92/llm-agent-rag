package rag

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/pack"
	"github.com/costa92/llm-agent-rag/prompt"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

type fakeModel struct{}

func (fakeModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	return generate.Response{Text: req.Messages[0].Content}, nil
}

type scriptedUsageModel struct {
	responses []generate.Response
	calls     int
}

func (m *scriptedUsageModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	idx := m.calls
	m.calls++
	if idx < len(m.responses) {
		resp := m.responses[idx]
		if resp.Text == "" {
			resp.Text = req.Messages[0].Content
		}
		return resp, nil
	}
	return generate.Response{Text: req.Messages[0].Content}, nil
}

type promptRoutingTemplate struct{}

func (promptRoutingTemplate) Render(_ context.Context, rc prompt.RenderContext) (generate.Request, error) {
	ids := make([]string, 0, len(rc.Hits))
	for _, hit := range rc.Hits {
		ids = append(ids, hit.Chunk.ID)
	}
	return generate.Request{
		Messages: []generate.Message{{
			Role:    "user",
			Content: fmt.Sprintf("QUESTION=%s\nHITS=%s", rc.Question, strings.Join(ids, ",")),
		}},
	}, nil
}

type scriptedReflectionModel struct {
	responses []generate.Response
	requests  []generate.Request
}

func (m *scriptedReflectionModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	m.requests = append(m.requests, req)
	idx := len(m.requests) - 1
	if idx < len(m.responses) {
		return m.responses[idx], nil
	}
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
	if len(ans.Citations) != 1 {
		t.Fatalf("len(ans.Citations) = %d, want 1", len(ans.Citations))
	}
	if ans.Diagnostics.HitCount != 1 {
		t.Fatalf("ans.Diagnostics.HitCount = %d, want 1", ans.Diagnostics.HitCount)
	}
	if ans.Trace.Question != "Where is Paris?" {
		t.Fatalf("ans.Trace.Question = %q, want original question", ans.Trace.Question)
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

func TestSystemRetrieveSecurityFilters(t *testing.T) {
	sys := New(Options{})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "Paris is in France.",
			Metadata: map[string]any{
				"tenant": "a",
			},
		},
		{
			ID:      "doc2",
			Content: "Paris travel guide.",
			Metadata: map[string]any{
				"tenant": "b",
			},
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	hits, err := sys.Retrieve(context.Background(), "Paris", SearchOptions{
		Namespace: "geo",
		TopK:      5,
		SecurityFilters: map[string]any{
			"tenant": "a",
		},
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("len(hits) = %d, want 1", len(hits))
	}
	if hits[0].Chunk.Metadata["tenant"] != "a" {
		t.Fatalf("hit tenant = %v, want a", hits[0].Chunk.Metadata["tenant"])
	}
}

func TestAskCarriesTraceAndFilters(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "Paris is in France.",
			Metadata: map[string]any{
				"tenant": "a",
				"lang":   "en",
			},
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "Where is Paris?", AskOptions{
		Search: SearchOptions{
			Namespace: "geo",
			TopK:      3,
			Filters: map[string]any{
				"lang": "en",
			},
			SecurityFilters: map[string]any{
				"tenant": "a",
			},
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Trace.Namespace != "geo" {
		t.Fatalf("ans.Trace.Namespace = %q, want geo", ans.Trace.Namespace)
	}
	if ans.Trace.TopK != 3 {
		t.Fatalf("ans.Trace.TopK = %d, want 3", ans.Trace.TopK)
	}
	if ans.Trace.Filters["lang"] != "en" {
		t.Fatalf("ans.Trace.Filters = %+v, want lang=en", ans.Trace.Filters)
	}
	if ans.Trace.SecurityFilters["tenant"] != "a" {
		t.Fatalf("ans.Trace.SecurityFilters = %+v, want tenant=a", ans.Trace.SecurityFilters)
	}
	if len(ans.Trace.SelectedChunkIDs) != 1 {
		t.Fatalf("len(ans.Trace.SelectedChunkIDs) = %d, want 1", len(ans.Trace.SelectedChunkIDs))
	}
}

func TestAskOffModeMatchesSingleRoundBehavior(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
		Reflection: &ReflectionOptions{
			Mode:      ReflectionModeOff,
			MaxRounds: 3,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Diagnostics.Reflection.Rounds != 0 {
		t.Fatalf("Reflection.Rounds = %d, want 0 for off mode", ans.Diagnostics.Reflection.Rounds)
	}
	if len(ans.Hits) != 1 {
		t.Fatalf("len(ans.Hits) = %d, want 1", len(ans.Hits))
	}
	if ans.Trace.Question != "capital of france" {
		t.Fatalf("ans.Trace.Question = %q, want original question", ans.Trace.Question)
	}
}

func TestAskRuleModeStopsAfterSatisfiedFirstRound(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        2,
			MinHits:          1,
			MinScore:         0,
			MinUniqueDocs:    1,
			RequireCitations: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Diagnostics.Reflection.Mode != ReflectionModeRule {
		t.Fatalf("Reflection.Mode = %q, want %q", ans.Diagnostics.Reflection.Mode, ReflectionModeRule)
	}
	if ans.Diagnostics.Reflection.Rounds != 1 {
		t.Fatalf("Reflection.Rounds = %d, want 1", ans.Diagnostics.Reflection.Rounds)
	}
	if ans.Diagnostics.Reflection.AdoptedRound != 1 {
		t.Fatalf("Reflection.AdoptedRound = %d, want 1", ans.Diagnostics.Reflection.AdoptedRound)
	}
	if ans.Diagnostics.Reflection.StopReason == "" {
		t.Fatal("Reflection.StopReason empty, want stop reason")
	}
	if len(ans.Diagnostics.Reflection.RoundDetails) != 1 {
		t.Fatalf("len(Reflection.RoundDetails) = %d, want 1", len(ans.Diagnostics.Reflection.RoundDetails))
	}
	round := ans.Diagnostics.Reflection.RoundDetails[0]
	if round.Decision != ReflectionDecisionStop {
		t.Fatalf("round.Decision = %q, want %q", round.Decision, ReflectionDecisionStop)
	}
	if round.DecisionMode != ReflectionModeRule {
		t.Fatalf("round.DecisionMode = %q, want %q", round.DecisionMode, ReflectionModeRule)
	}
	if round.DecisionReason == "" {
		t.Fatal("round.DecisionReason empty, want rule decision reason")
	}
	if len(ans.Trace.Reflection.Rounds) != 1 {
		t.Fatalf("len(Trace.Reflection.Rounds) = %d, want 1", len(ans.Trace.Reflection.Rounds))
	}
	if len(ans.Hits) != 1 || len(ans.Citations) != 1 {
		t.Fatalf("final adopted round semantics changed: hits=%d citations=%d", len(ans.Hits), len(ans.Citations))
	}
}

func TestAskRuleModeStopsAtMaxRounds(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Berlin is in Germany."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        2,
			MinHits:          1,
			MinScore:         1.1,
			MinUniqueDocs:    2,
			RequireCitations: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Diagnostics.Reflection.Mode != ReflectionModeRule {
		t.Fatalf("Reflection.Mode = %q, want %q", ans.Diagnostics.Reflection.Mode, ReflectionModeRule)
	}
	if ans.Diagnostics.Reflection.Rounds != 2 {
		t.Fatalf("Reflection.Rounds = %d, want 2", ans.Diagnostics.Reflection.Rounds)
	}
	if ans.Diagnostics.Reflection.AdoptedRound != 2 {
		t.Fatalf("Reflection.AdoptedRound = %d, want 2", ans.Diagnostics.Reflection.AdoptedRound)
	}
	if ans.Diagnostics.Reflection.StopReason == "" {
		t.Fatal("Reflection.StopReason empty, want max-round stop reason")
	}
	if len(ans.Diagnostics.Reflection.RoundDetails) != 2 {
		t.Fatalf("len(Reflection.RoundDetails) = %d, want 2", len(ans.Diagnostics.Reflection.RoundDetails))
	}
	if ans.Diagnostics.Reflection.RoundDetails[0].Decision != ReflectionDecisionContinue {
		t.Fatalf(
			"round 1 decision = %q, want %q",
			ans.Diagnostics.Reflection.RoundDetails[0].Decision,
			ReflectionDecisionContinue,
		)
	}
	if ans.Diagnostics.Reflection.RoundDetails[1].Decision != ReflectionDecisionStop {
		t.Fatalf(
			"round 2 decision = %q, want %q",
			ans.Diagnostics.Reflection.RoundDetails[1].Decision,
			ReflectionDecisionStop,
		)
	}
	if len(ans.Trace.Reflection.Rounds) != 2 {
		t.Fatalf("len(Trace.Reflection.Rounds) = %d, want 2", len(ans.Trace.Reflection.Rounds))
	}
	if len(ans.Hits) != 1 || len(ans.Citations) != 1 {
		t.Fatalf("final adopted round semantics changed: hits=%d citations=%d", len(ans.Hits), len(ans.Citations))
	}
}

func TestAskRuleModeContinuesWithoutImplicitRewrite(t *testing.T) {
	sys := New(Options{
		Model:        fakeModel{},
		Preprocessor: rewritePreprocessor{},
	})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Berlin is in Germany."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	question := "capital of france"
	ans, err := sys.Ask(context.Background(), question, AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        2,
			MinHits:          1,
			MinScore:         1.1,
			MinUniqueDocs:    2,
			RequireCitations: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if got := ans.Diagnostics.Reflection.Rounds; got != 2 {
		t.Fatalf("Reflection.Rounds = %d, want 2", got)
	}
	for i, round := range ans.Diagnostics.Reflection.RoundDetails {
		if round.InputQuery != question {
			t.Fatalf("round %d InputQuery = %q, want original question %q", i+1, round.InputQuery, question)
		}
		if round.EffectiveQuery != "france capital" {
			t.Fatalf("round %d EffectiveQuery = %q, want rewritten retrieval query", i+1, round.EffectiveQuery)
		}
	}
	for i, round := range ans.Trace.Reflection.Rounds {
		if round.InputQuery != question {
			t.Fatalf("trace round %d InputQuery = %q, want original question %q", i+1, round.InputQuery, question)
		}
		if round.EffectiveQuery != "france capital" {
			t.Fatalf("trace round %d EffectiveQuery = %q, want rewritten retrieval query", i+1, round.EffectiveQuery)
		}
	}
}

func TestAskRuleModeAggregatesMetricsAcrossRounds(t *testing.T) {
	model := &scriptedUsageModel{
		responses: []generate.Response{
			{
				Text:  "round 1",
				Usage: generate.Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18},
			},
			{
				Text:  "round 2",
				Usage: generate.Usage{PromptTokens: 13, CompletionTokens: 5, TotalTokens: 18},
			},
		},
	}
	sys := New(Options{Model: model})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Berlin is in Germany."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        2,
			MinHits:          1,
			MinScore:         1.1,
			MinUniqueDocs:    2,
			RequireCitations: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if got := ans.Diagnostics.Reflection.Rounds; got != 2 {
		t.Fatalf("Reflection.Rounds = %d, want 2", got)
	}
	if got := ans.Diagnostics.Metrics.Calls.Generate; got != 2 {
		t.Fatalf("Metrics.Calls.Generate = %d, want 2", got)
	}
	if got := len(ans.Diagnostics.Metrics.Stages); got != 6 {
		t.Fatalf("len(Metrics.Stages) = %d, want 6 for two retrieve/pack/generate rounds", got)
	}
	wantStages := []string{"retrieve", "pack", "generate", "retrieve", "pack", "generate"}
	for i, stage := range ans.Diagnostics.Metrics.Stages {
		if stage.Stage != wantStages[i] {
			t.Fatalf("Metrics.Stages[%d].Stage = %q, want %q", i, stage.Stage, wantStages[i])
		}
	}
	tok := ans.Diagnostics.Metrics.Tokens
	if tok.PromptTokens != 24 || tok.CompletionTokens != 12 || tok.TotalTokens != 36 {
		t.Fatalf("Metrics.Tokens = %+v, want summed usage across rounds", tok)
	}
	if tok.Estimated {
		t.Fatalf("Metrics.Tokens.Estimated = true, want false for reported usage")
	}
	if ans.Text != "round 2" {
		t.Fatalf("final adopted answer text = %q, want round 2 output", ans.Text)
	}
}

func TestAskModelModeRewritesAndContinues(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1"},
			{Text: "decision=rewrite_and_continue\nreason=need better evidence\nrewrite=paris capital france"},
			{Text: "answer round 2"},
			{Text: "decision=stop\nreason=sufficient evidence"},
		},
	}
	sys := New(Options{Model: model})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Berlin is in Germany."},
		{ID: "doc2", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:         ReflectionModeModel,
			MaxRounds:    2,
			AllowRewrite: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if got := len(model.requests); got != 4 {
		t.Fatalf("model call count = %d, want 4 (answer, reflect, answer, reflect)", got)
	}
	if ans.Diagnostics.Reflection.Mode != ReflectionModeModel {
		t.Fatalf("Reflection.Mode = %q, want %q", ans.Diagnostics.Reflection.Mode, ReflectionModeModel)
	}
	if ans.Diagnostics.Reflection.Rounds != 2 {
		t.Fatalf("Reflection.Rounds = %d, want 2", ans.Diagnostics.Reflection.Rounds)
	}
	if ans.Diagnostics.Reflection.AdoptedRound != 2 {
		t.Fatalf("Reflection.AdoptedRound = %d, want 2", ans.Diagnostics.Reflection.AdoptedRound)
	}
	if ans.Diagnostics.Reflection.StopReason == "" {
		t.Fatal("Reflection.StopReason empty, want model stop reason")
	}
	if ans.Diagnostics.Reflection.DecisionModelCalls != 2 {
		t.Fatalf("Reflection.DecisionModelCalls = %d, want 2", ans.Diagnostics.Reflection.DecisionModelCalls)
	}
	if ans.Diagnostics.Reflection.RewriteModelCalls != 0 {
		t.Fatalf("Reflection.RewriteModelCalls = %d, want 0 for single-call structured decision", ans.Diagnostics.Reflection.RewriteModelCalls)
	}
	if ans.Diagnostics.Reflection.RoundDetails[0].Decision != ReflectionDecisionRewriteAndContinue {
		t.Fatalf(
			"round 1 decision = %q, want %q",
			ans.Diagnostics.Reflection.RoundDetails[0].Decision,
			ReflectionDecisionRewriteAndContinue,
		)
	}
	if ans.Diagnostics.Reflection.RoundDetails[0].DecisionMode != ReflectionModeModel {
		t.Fatalf("round 1 decision mode = %q, want %q", ans.Diagnostics.Reflection.RoundDetails[0].DecisionMode, ReflectionModeModel)
	}
	if ans.Diagnostics.Reflection.RoundDetails[0].RewrittenQuery != "paris capital france" {
		t.Fatalf(
			"round 1 rewritten query = %q, want %q",
			ans.Diagnostics.Reflection.RoundDetails[0].RewrittenQuery,
			"paris capital france",
		)
	}
	if ans.Trace.Reflection.Rounds[0].RewrittenQuery != "paris capital france" {
		t.Fatalf(
			"trace round 1 rewritten query = %q, want %q",
			ans.Trace.Reflection.Rounds[0].RewrittenQuery,
			"paris capital france",
		)
	}
	if ans.Trace.Reflection.Rounds[1].InputQuery != "paris capital france" {
		t.Fatalf(
			"trace round 2 input query = %q, want rewritten query",
			ans.Trace.Reflection.Rounds[1].InputQuery,
		)
	}
	if ans.Trace.Question != "capital of france" {
		t.Fatalf("Trace.Question = %q, want original question anchor", ans.Trace.Question)
	}
	if ans.Text != "answer round 2" {
		t.Fatalf("final answer text = %q, want adopted round 2 text", ans.Text)
	}
	if len(ans.Hits) != 1 || ans.Hits[0].Chunk.ID != "doc2:0" {
		t.Fatalf("final adopted hits = %+v, want rewritten round hit doc2:0", ans.Hits)
	}
	if len(ans.Citations) != 1 || ans.Citations[0].ChunkID != "doc2:0" {
		t.Fatalf("final adopted citations = %+v, want doc2:0 only", ans.Citations)
	}
	if !strings.Contains(ans.Prompt.Messages[0].Content, "HITS=doc2:0") {
		t.Fatalf("final adopted prompt = %q, want round 2 prompt", ans.Prompt.Messages[0].Content)
	}
}

func TestAskHybridModeSkipsReflectionModelWhenRulesAlreadyPass(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1"},
		},
	}
	sys := New(Options{Model: model})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeHybrid,
			MaxRounds:        2,
			MinHits:          1,
			MinScore:         0,
			MinUniqueDocs:    1,
			RequireCitations: true,
			AllowRewrite:     true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if got := len(model.requests); got != 1 {
		t.Fatalf("model call count = %d, want 1 answer-only call when rules pass", got)
	}
	if ans.Diagnostics.Reflection.Mode != ReflectionModeHybrid {
		t.Fatalf("Reflection.Mode = %q, want %q", ans.Diagnostics.Reflection.Mode, ReflectionModeHybrid)
	}
	if ans.Diagnostics.Reflection.Rounds != 1 {
		t.Fatalf("Reflection.Rounds = %d, want 1", ans.Diagnostics.Reflection.Rounds)
	}
	if ans.Diagnostics.Reflection.DecisionModelCalls != 0 {
		t.Fatalf("Reflection.DecisionModelCalls = %d, want 0 when hybrid short-circuits", ans.Diagnostics.Reflection.DecisionModelCalls)
	}
	if ans.Diagnostics.Reflection.RoundDetails[0].Decision != ReflectionDecisionStop {
		t.Fatalf("round 1 decision = %q, want %q", ans.Diagnostics.Reflection.RoundDetails[0].Decision, ReflectionDecisionStop)
	}
	if ans.Diagnostics.Reflection.RoundDetails[0].DecisionMode != ReflectionModeHybrid {
		t.Fatalf("round 1 decision mode = %q, want %q", ans.Diagnostics.Reflection.RoundDetails[0].DecisionMode, ReflectionModeHybrid)
	}
	if ans.Diagnostics.Reflection.RoundDetails[0].RewrittenQuery != "" {
		t.Fatalf("round 1 rewritten query = %q, want empty", ans.Diagnostics.Reflection.RoundDetails[0].RewrittenQuery)
	}
	if ans.Text != "answer round 1" {
		t.Fatalf("final answer text = %q, want first-round answer", ans.Text)
	}
}

func TestReflectionModeConstantsStable(t *testing.T) {
	if ReflectionModeOff != "off" {
		t.Fatalf("ReflectionModeOff = %q, want %q", ReflectionModeOff, "off")
	}
	if ReflectionModeRule != "rule" {
		t.Fatalf("ReflectionModeRule = %q, want %q", ReflectionModeRule, "rule")
	}
	if ReflectionModeModel != "model" {
		t.Fatalf("ReflectionModeModel = %q, want %q", ReflectionModeModel, "model")
	}
	if ReflectionModeHybrid != "hybrid" {
		t.Fatalf("ReflectionModeHybrid = %q, want %q", ReflectionModeHybrid, "hybrid")
	}
}

func TestReflectionDecisionConstantsStable(t *testing.T) {
	if ReflectionDecisionStop != "stop" {
		t.Fatalf("ReflectionDecisionStop = %q, want %q", ReflectionDecisionStop, "stop")
	}
	if ReflectionDecisionContinue != "continue" {
		t.Fatalf("ReflectionDecisionContinue = %q, want %q", ReflectionDecisionContinue, "continue")
	}
	if ReflectionDecisionRewriteAndContinue != "rewrite_and_continue" {
		t.Fatalf(
			"ReflectionDecisionRewriteAndContinue = %q, want %q",
			ReflectionDecisionRewriteAndContinue,
			"rewrite_and_continue",
		)
	}
}

func TestReflectionZeroValueConfigIsSafe(t *testing.T) {
	var opts AskOptions
	if opts.Reflection != nil {
		t.Fatalf("zero-value AskOptions.Reflection = %#v, want nil", opts.Reflection)
	}

	var cfg ReflectionOptions
	if cfg.Mode != "" {
		t.Fatalf("zero-value ReflectionOptions.Mode = %q, want empty disabled mode", cfg.Mode)
	}
}

func TestImportPreservesLineageMetadataIntoStore(t *testing.T) {
	mem := store.NewInMemoryStore(32)
	sys := New(Options{Store: mem})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:               "doc1",
			Content:          "Paris is in France.",
			SourceID:         "knowledge-base",
			Version:          "2026-05-14",
			Checksum:         "sha256:abc",
			EmbeddingVersion: "hash-32-v1",
			Metadata: map[string]any{
				"lang": "en",
			},
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	chunk, err := mem.Get(context.Background(), "doc1:0")
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if chunk.Metadata[ingest.MetadataSourceIDKey] != "knowledge-base" {
		t.Fatalf("source_id = %v, want knowledge-base", chunk.Metadata[ingest.MetadataSourceIDKey])
	}
	if chunk.Metadata[ingest.MetadataVersionKey] != "2026-05-14" {
		t.Fatalf("version = %v, want 2026-05-14", chunk.Metadata[ingest.MetadataVersionKey])
	}
	if chunk.Metadata[ingest.MetadataChecksumKey] != "sha256:abc" {
		t.Fatalf("checksum = %v, want sha256:abc", chunk.Metadata[ingest.MetadataChecksumKey])
	}
	if chunk.Metadata[ingest.MetadataEmbeddingVersionKey] != "hash-32-v1" {
		t.Fatalf("embedding_version = %v, want hash-32-v1", chunk.Metadata[ingest.MetadataEmbeddingVersionKey])
	}
	if chunk.Metadata["lang"] != "en" {
		t.Fatalf("lang = %v, want en", chunk.Metadata["lang"])
	}
}

func TestImportReplaceSourceRemovesPreviousChunks(t *testing.T) {
	mem := store.NewInMemoryStore(32)
	sys := New(Options{Store: mem})

	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:       "doc1",
			Content:  "Old alpha content",
			SourceID: "source-alpha",
		},
	}, ingest.ImportOptions{Namespace: "docs"})
	if err != nil {
		t.Fatalf("first Import(): %v", err)
	}

	_, err = sys.Import(context.Background(), []ingest.Document{
		{
			ID:       "doc2",
			Content:  "New alpha content",
			SourceID: "source-alpha",
		},
	}, ingest.ImportOptions{Namespace: "docs", ReplaceSource: true})
	if err != nil {
		t.Fatalf("second Import(): %v", err)
	}

	if _, err := mem.Get(context.Background(), "doc1:0"); err == nil {
		t.Fatalf("old chunk still exists after ReplaceSource import")
	}
	chunk, err := mem.Get(context.Background(), "doc2:0")
	if err != nil {
		t.Fatalf("Get(new chunk): %v", err)
	}
	if chunk.Metadata[ingest.MetadataSourceIDKey] != "source-alpha" {
		t.Fatalf("source_id = %v, want source-alpha", chunk.Metadata[ingest.MetadataSourceIDKey])
	}
}

type rewritePreprocessor struct{}

func (rewritePreprocessor) Process(_ context.Context, req retrieve.Request) (retrieve.PreprocessResult, error) {
	return retrieve.PreprocessResult{
		QueryVariants: []string{"france capital"},
		Trace: retrieve.Trace{
			OriginalQuery:  req.Query,
			EffectiveQuery: "france capital",
			QueryVariants:  []string{"france capital"},
		},
	}, nil
}

func TestRetrieveUsesConfiguredPreprocessor(t *testing.T) {
	sys := New(Options{Preprocessor: rewritePreprocessor{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
		{ID: "doc2", Content: "Berlin is the capital of Germany."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	hits, err := sys.Retrieve(context.Background(), "what is the capital of france", SearchOptions{
		Namespace: "geo",
		TopK:      1,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("len(hits) = %d, want 1", len(hits))
	}
	if hits[0].Chunk.DocID != "doc1" {
		t.Fatalf("top hit doc = %s, want doc1", hits[0].Chunk.DocID)
	}
}

func TestAskReranksAndPacksContext(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "general travel guide"},
		{ID: "doc2", Content: "Paris is the capital of France and has museums cafes boulevards"},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{
			Namespace:    "geo",
			TopK:         2,
			EnableRerank: true,
		},
		MaxTokens: 10,
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Trace.RerankedChunkIDs) == 0 || ans.Trace.RerankedChunkIDs[0] != "doc2:0" {
		t.Fatalf("reranked ids = %#v, want doc2 first", ans.Trace.RerankedChunkIDs)
	}
	if len(ans.Diagnostics.PromptChunkIDs) == 0 || ans.Diagnostics.PromptChunkIDs[0] != "doc2:0" {
		t.Fatalf("prompt chunk ids = %#v, want doc2 included", ans.Diagnostics.PromptChunkIDs)
	}
	if !strings.Contains(ans.Prompt.Messages[0].Content, "doc2:0") {
		t.Fatalf("prompt missing packed chunk doc2: %q", ans.Prompt.Messages[0].Content)
	}
}

func TestAskPopulatesRerankScores(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "general travel guide"},
		{ID: "doc2", Content: "Paris is the capital of France and has museums cafes"},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	withRerank, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:    SearchOptions{Namespace: "geo", TopK: 2, EnableRerank: true},
		MaxTokens: 10,
	})
	if err != nil {
		t.Fatalf("Ask(rerank on): %v", err)
	}
	if len(withRerank.Diagnostics.RerankScores) == 0 {
		t.Fatalf("RerankScores empty, want per-hit rerank detail when EnableRerank is true")
	}
	for _, s := range withRerank.Diagnostics.RerankScores {
		if s.ChunkID == "" || s.OutputRank == 0 {
			t.Fatalf("rerank score %+v incomplete", s)
		}
	}
	noRerank, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 2},
	})
	if err != nil {
		t.Fatalf("Ask(rerank off): %v", err)
	}
	if noRerank.Diagnostics.RerankScores != nil {
		t.Fatalf("RerankScores = %+v, want nil when EnableRerank is false", noRerank.Diagnostics.RerankScores)
	}
}

func TestAskCanUseCustomPacker(t *testing.T) {
	sys := New(Options{
		Model:  fakeModel{},
		Packer: fixedPacker{},
	})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
		{ID: "doc2", Content: "Berlin is the capital of Germany."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "Where is Paris?", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 2},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Hits) != 1 || ans.Hits[0].Chunk.ID != "doc1:0" {
		t.Fatalf("hits = %+v, want only doc1:0", ans.Hits)
	}
	if len(ans.Trace.DroppedChunkIDs) == 0 || ans.Trace.DroppedChunkIDs[0] != "doc2:0" {
		t.Fatalf("dropped ids = %#v, want doc2 dropped", ans.Trace.DroppedChunkIDs)
	}
}

func TestStructureAwareRetrieveAndAskTrace(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Splitter: ingest.NewMarkdownSplitter(500, 50)})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "# Cities\nParis is in France.\n## Travel\nMuseums and cafes.\n## History\nAncient capital notes.",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	hits, err := sys.Retrieve(context.Background(), "travel paris", SearchOptions{
		Namespace:       "geo",
		TopK:            2,
		EnableStructure: true,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected structure-aware hits")
	}
	if len(hits[0].Chunk.SectionPath) == 0 {
		t.Fatalf("top hit missing section path: %+v", hits[0].Chunk)
	}
	foundTravel := false
	for _, hit := range hits {
		if strings.Join(hit.Chunk.SectionPath, " > ") == "Cities > Travel" {
			foundTravel = true
			break
		}
	}
	if !foundTravel {
		t.Fatalf("structure hits missing travel section: %+v", hits)
	}

	ans, err := sys.Ask(context.Background(), "Where should I travel in Paris?", AskOptions{
		Search: SearchOptions{
			Namespace:           "geo",
			TopK:                3,
			EnableStructure:     true,
			EnableTreeExpansion: true,
			ExpansionDepth:      3,
			EnableRerank:        true,
		},
		MaxTokens: 50,
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Trace.MatchedSections) == 0 {
		t.Fatalf("matched sections = %#v, want non-empty", ans.Trace.MatchedSections)
	}
	if len(ans.Trace.SearchPath) == 0 {
		t.Fatalf("search path = %#v, want non-empty", ans.Trace.SearchPath)
	}
	if len(ans.Trace.ExpandedSections) == 0 {
		t.Fatalf("expanded sections = %#v, want non-empty", ans.Trace.ExpandedSections)
	}
	if len(ans.Trace.ExpandedChunkIDs) == 0 {
		t.Fatalf("expanded chunk ids = %#v, want non-empty", ans.Trace.ExpandedChunkIDs)
	}
	if len(ans.Diagnostics.ExpandedChunkIDs) == 0 {
		t.Fatalf("diagnostics expanded chunk ids = %#v, want non-empty", ans.Diagnostics.ExpandedChunkIDs)
	}
	if len(ans.Citations) == 0 || len(ans.Citations[0].SectionPath) == 0 {
		t.Fatalf("citations = %+v, want section path", ans.Citations)
	}
	if !strings.Contains(ans.Prompt.Messages[0].Content, "Cities >") {
		t.Fatalf("prompt missing structured path prefix: %q", ans.Prompt.Messages[0].Content)
	}
}

func TestAskCarriesRoutePathAndConstrainsSubtree(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Splitter: ingest.NewMarkdownSplitter(500, 50)})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "# Cities\nParis overview.\n## Travel\nMuseums and cafes.\n## History\nAncient capital notes.",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "Paris notes", AskOptions{
		Search: SearchOptions{
			Namespace:           "geo",
			TopK:                3,
			RoutePath:           []string{"Cities", "Travel"},
			EnableStructure:     true,
			EnableTreeExpansion: true,
			ExpansionDepth:      3,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if !pathEquals(ans.Trace.RoutePath, []string{"Cities", "Travel"}) {
		t.Fatalf("trace route path = %#v, want travel route", ans.Trace.RoutePath)
	}
	for _, hit := range ans.Hits {
		if strings.Join(hit.Chunk.SectionPath, " > ") != "Cities > Travel" {
			t.Fatalf("hit path = %q, want only travel subtree", strings.Join(hit.Chunk.SectionPath, " > "))
		}
	}
}

func TestAskCarriesAutoRoutePathAndConstrainsSubtree(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Splitter: ingest.NewMarkdownSplitter(500, 50)})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "# Cities\nParis overview.\n## Travel\nMuseums and cafes.\n## History\nAncient capital notes.",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "travel museums", AskOptions{
		Search: SearchOptions{
			Namespace:           "geo",
			TopK:                3,
			EnableAutoRoute:     true,
			AutoRouteMinScore:   2,
			EnableStructure:     true,
			EnableTreeExpansion: true,
			ExpansionDepth:      3,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if !pathEquals(ans.Trace.RoutePath, []string{"Cities", "Travel"}) {
		t.Fatalf("trace route path = %#v, want travel route", ans.Trace.RoutePath)
	}
	if !pathEquals(ans.Trace.AutoRoutePath, []string{"Cities", "Travel"}) {
		t.Fatalf("trace auto route path = %#v, want travel route", ans.Trace.AutoRoutePath)
	}
	if len(ans.Trace.AutoRouteCandidates) == 0 {
		t.Fatalf("trace auto route candidates = %#v, want non-empty", ans.Trace.AutoRouteCandidates)
	}
	if len(ans.Diagnostics.AutoRouteCandidates) == 0 {
		t.Fatalf("diagnostics auto route candidates = %#v, want non-empty", ans.Diagnostics.AutoRouteCandidates)
	}
	for _, hit := range ans.Hits {
		if strings.Join(hit.Chunk.SectionPath, " > ") != "Cities > Travel" {
			t.Fatalf("hit path = %q, want only auto-routed travel subtree", strings.Join(hit.Chunk.SectionPath, " > "))
		}
	}
}

func TestAskCarriesMergedAutoRouteCandidatesAcrossVariants(t *testing.T) {
	sys := New(Options{
		Model:        fakeModel{},
		Splitter:     ingest.NewMarkdownSplitter(500, 50),
		Preprocessor: rewriteVariantPreprocessor{},
	})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "# Cities\nParis overview.\n## Travel\nMuseums and cafes.\n## History\nAncient capital notes.",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "route planner", AskOptions{
		Search: SearchOptions{
			Namespace:              "geo",
			TopK:                   3,
			EnableAutoRoute:        true,
			AutoRouteMinScore:      2,
			AutoRouteMaxCandidates: 2,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Trace.AutoRouteCandidates) < 2 {
		t.Fatalf("trace auto route candidates = %#v, want merged candidates", ans.Trace.AutoRouteCandidates)
	}
}

func TestAskCanFanoutAcrossTopRouteCandidates(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Splitter: ingest.NewMarkdownSplitter(500, 50)})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "# Cities\nParis overview.\n## Travel\nMuseums and cafes.\n## History\nHistory museums and capital notes.",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "travel history museums", AskOptions{
		Search: SearchOptions{
			Namespace:                    "geo",
			TopK:                         5,
			EnableAutoRoute:              true,
			AutoRouteMinScore:            2,
			AutoRouteMaxCandidates:       2,
			AutoRouteConfidenceThreshold: 0.5,
			AutoRouteFanout:              2,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	foundTravel := false
	foundHistory := false
	for _, hit := range ans.Hits {
		if pathEquals(hit.Chunk.SectionPath, []string{"Cities", "Travel"}) {
			foundTravel = true
		}
		if pathEquals(hit.Chunk.SectionPath, []string{"Cities", "History"}) {
			foundHistory = true
		}
	}
	if !foundTravel || !foundHistory {
		t.Fatalf("hits = %+v, want travel and history fanout hits", ans.Hits)
	}
	if ans.Trace.RoutePolicy.Mode != "fanout" {
		t.Fatalf("trace route policy mode = %q, want fanout", ans.Trace.RoutePolicy.Mode)
	}
	if ans.Trace.RoutePolicy.SelectedCount != 2 {
		t.Fatalf("trace route policy selected count = %d, want 2", ans.Trace.RoutePolicy.SelectedCount)
	}
	if len(ans.Trace.RoutePolicy.Rationale) == 0 {
		t.Fatalf("trace route policy rationale = %#v, want non-empty", ans.Trace.RoutePolicy.Rationale)
	}
	if ans.Diagnostics.RoutePolicy.Mode != "fanout" {
		t.Fatalf("diagnostics route policy mode = %q, want fanout", ans.Diagnostics.RoutePolicy.Mode)
	}
}

func TestAskConvergesWhenConfidenceGapDominates(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Splitter: ingest.NewMarkdownSplitter(500, 50)})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "# Cities\nParis overview.\n## Travel museums\nWalking tour notes.\n## History museums\nLegacy notes.",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "travel museums", AskOptions{
		Search: SearchOptions{
			Namespace:                    "geo",
			TopK:                         5,
			EnableAutoRoute:              true,
			AutoRouteMinScore:            2,
			AutoRouteMaxCandidates:       2,
			AutoRouteConfidenceThreshold: 0.4,
			AutoRouteFanout:              2,
			AutoRouteConfidenceGap:       0.3,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Trace.RoutePolicy.Mode != "converged" {
		t.Fatalf("trace route policy mode = %q, want converged", ans.Trace.RoutePolicy.Mode)
	}
	if ans.Trace.RoutePolicy.SelectedCount != 1 {
		t.Fatalf("trace route policy selected count = %d, want 1", ans.Trace.RoutePolicy.SelectedCount)
	}
	if ans.Trace.RoutePolicy.ConfidenceGap != 0.3 {
		t.Fatalf("trace route policy confidence gap = %v, want 0.3", ans.Trace.RoutePolicy.ConfidenceGap)
	}
	if ans.Trace.RoutePolicy.Gap <= 0 {
		t.Fatalf("trace route policy gap = %v, want > 0", ans.Trace.RoutePolicy.Gap)
	}
	hasConverged := false
	for _, line := range ans.Trace.RoutePolicy.Rationale {
		if strings.HasPrefix(line, "converged:") {
			hasConverged = true
			break
		}
	}
	if !hasConverged {
		t.Fatalf("trace route policy rationale = %#v, want converged: entry", ans.Trace.RoutePolicy.Rationale)
	}
	if ans.Diagnostics.RoutePolicy.Mode != "converged" {
		t.Fatalf("diagnostics route policy mode = %q, want converged", ans.Diagnostics.RoutePolicy.Mode)
	}
	if ans.Diagnostics.RoutePolicy.Gap != ans.Trace.RoutePolicy.Gap {
		t.Fatalf("diagnostics gap = %v, want trace gap %v", ans.Diagnostics.RoutePolicy.Gap, ans.Trace.RoutePolicy.Gap)
	}
	for _, hit := range ans.Hits {
		if pathEquals(hit.Chunk.SectionPath, []string{"Cities", "History museums"}) {
			t.Fatalf("hits = %+v, history museums route should not be queried when converged", ans.Hits)
		}
	}
}

func TestAskExposesPerRouteSearchTrajectory(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Splitter: ingest.NewMarkdownSplitter(500, 50)})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID:      "doc1",
			Content: "# Cities\nParis overview.\n## Travel\nMuseums and cafes.\n## History\nHistory museums and capital notes.",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}

	ans, err := sys.Ask(context.Background(), "travel history museums", AskOptions{
		Search: SearchOptions{
			Namespace:                    "geo",
			TopK:                         5,
			EnableAutoRoute:              true,
			AutoRouteMinScore:            2,
			AutoRouteMaxCandidates:       2,
			AutoRouteConfidenceThreshold: 0.5,
			AutoRouteFanout:              2,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Trace.SearchTrajectory) != 2 {
		t.Fatalf("trace search trajectory len = %d, want 2 fanout steps", len(ans.Trace.SearchTrajectory))
	}
	if len(ans.Diagnostics.SearchTrajectory) != len(ans.Trace.SearchTrajectory) {
		t.Fatalf("diagnostics trajectory len = %d, want %d", len(ans.Diagnostics.SearchTrajectory), len(ans.Trace.SearchTrajectory))
	}
	for i, step := range ans.Trace.SearchTrajectory {
		if step.HitCount == 0 {
			t.Fatalf("trajectory step %d route=%v has zero hits", i, step.Route)
		}
		if len(step.HitIDs) != step.HitCount {
			t.Fatalf("trajectory step %d hit count = %d but ids = %#v", i, step.HitCount, step.HitIDs)
		}
		if step.Mode != "fanout" {
			t.Fatalf("trajectory step %d mode = %q, want fanout", i, step.Mode)
		}
		mirror := ans.Diagnostics.SearchTrajectory[i]
		if mirror.HitCount != step.HitCount {
			t.Fatalf("diagnostics trajectory step %d hit count = %d, trace had %d", i, mirror.HitCount, step.HitCount)
		}
		if !pathEquals(mirror.Route, step.Route) {
			t.Fatalf("diagnostics trajectory step %d route = %#v, trace had %#v", i, mirror.Route, step.Route)
		}
	}
}

type fixedPacker struct{}

type rewriteVariantPreprocessor struct{}

func (rewriteVariantPreprocessor) Process(_ context.Context, req retrieve.Request) (retrieve.PreprocessResult, error) {
	return retrieve.PreprocessResult{
		QueryVariants: []string{"travel museums", "history notes"},
		Trace: retrieve.Trace{
			OriginalQuery:  req.Query,
			EffectiveQuery: "travel museums",
			QueryVariants:  []string{"travel museums", "history notes"},
		},
	}, nil
}

func (fixedPacker) Pack(_ context.Context, req pack.Request) (pack.Result, error) {
	selected := make([]store.Hit, 0, 1)
	if len(req.Hits) > 0 {
		selected = append(selected, req.Hits[0])
	}
	dropped := make([]string, 0, len(req.Hits))
	for _, hit := range req.Hits[1:] {
		dropped = append(dropped, hit.Chunk.ID)
	}
	return pack.Result{
		Hits: selected,
		Trace: pack.Trace{
			SelectedChunkIDs: chunkIDs(selected),
			DroppedChunkIDs:  dropped,
		},
	}, nil
}

func pathEquals(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
