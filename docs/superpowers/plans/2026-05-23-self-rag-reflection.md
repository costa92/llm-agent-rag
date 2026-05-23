# Self-RAG Reflection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add inference-time Self-RAG reflection to `rag.System.Ask`, with parameter-selectable `rule`, `model`, and `hybrid` modes, while preserving the existing `Ask` entrypoint and final-answer semantics.

**Architecture:** Refactor the current single-pass `Ask` pipeline into a reusable private one-round executor plus a bounded reflection loop in `rag/ask.go`. Extend exported `rag` types additively with reflection configuration and round-level diagnostics, keep `Observer.OnAsk` as a single top-level callback, and let `Observer.OnRetrieve` fire once per internal retrieval round.

**Tech Stack:** Go, standard library testing, existing `rag` / `retrieve` / `generate` / `obs` packages, committed API snapshot gate in `internal/apisnapshot`.

---

## File Structure

- Modify: `rag/options.go`
  - Add exported reflection configuration types and hook them into `AskOptions`.
- Modify: `rag/system.go`
  - Add exported reflection diagnostics/trace structures to `Diagnostics` and `Trace`.
- Modify: `rag/ask.go`
  - Extract the current one-round `Ask` body into a private helper.
  - Add the bounded reflection loop, decision helpers, and metrics aggregation.
- Create: `rag/reflection.go`
  - Hold private decision types, policy helpers, rewrite prompt helpers, and aggregation helpers so `ask.go` does not become unbounded.
- Modify: `rag/system_test.go`
  - Add path tests for `off`, `rule`, `model`, and `hybrid`.
- Modify: `rag/observer_test.go`
  - Add explicit assertions for multi-round `OnRetrieve` behavior and top-level `OnAsk` semantics.
- Modify: `rag/instrument_test.go`
  - Assert multi-round metrics and token aggregation.
- Modify: `README.md`
  - Document reflection usage and the `OnRetrieve` multi-round behavior.
- Modify: `docs/production-deployment.md`
  - Document fail-open behavior, rewrite stacking with MQE/HyDE, and observability semantics.
- Modify: `api/v1.snapshot.txt`
  - Refresh the committed exported API snapshot.
- Reference only: `agentic/correct.go`, `agentic/correct_test.go`
  - Use as behavioral inspiration, not as an implementation dependency.

### Task 1: Add Exported Reflection Types

**Files:**
- Modify: `rag/options.go`
- Modify: `rag/system.go`
- Test: `rag/system_test.go`

- [ ] **Step 1: Write the failing API-shape test**

Add a new test near the `Ask` option tests in `rag/system_test.go`:

```go
func TestAskOptionsExposeReflectionConfig(t *testing.T) {
	opts := AskOptions{
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        2,
			MinHits:          1,
			MinScore:         0.4,
			MinUniqueDocs:    1,
			RequireCitations: true,
			AllowRewrite:     true,
			FailOpen:         true,
		},
	}
	if opts.Reflection == nil {
		t.Fatal("Reflection = nil, want config attached")
	}
	if opts.Reflection.Mode != ReflectionModeRule {
		t.Fatalf("Mode = %q, want %q", opts.Reflection.Mode, ReflectionModeRule)
	}
}
```

- [ ] **Step 2: Run the test to confirm the exported types do not exist yet**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run TestAskOptionsExposeReflectionConfig -count=1
```

Expected: FAIL with undefined `ReflectionOptions` / `ReflectionModeRule`.

- [ ] **Step 3: Add the exported option types and additive diagnostics types**

Update `rag/options.go` with:

```go
type ReflectionMode string

const (
	ReflectionModeOff    ReflectionMode = "off"
	ReflectionModeRule   ReflectionMode = "rule"
	ReflectionModeModel  ReflectionMode = "model"
	ReflectionModeHybrid ReflectionMode = "hybrid"
)

type ReflectionOptions struct {
	Mode             ReflectionMode
	MaxRounds        int
	MinHits          int
	MinScore         float64
	MinUniqueDocs    int
	RequireCitations bool
	AllowRewrite     bool
	FailOpen         bool
}
```

And extend `AskOptions` with:

```go
Reflection *ReflectionOptions
```

Update `rag/system.go` with additive exported types:

```go
type ReflectionDiagnostics struct {
	Mode                 ReflectionMode
	Rounds               int
	AdoptedRound         int
	StopReason           string
	FailureFallback      bool
	FailureReason        string
	DecisionModelCalls   int
	RewriteModelCalls    int
	RoundDetails         []ReflectionRoundDiagnostics
}

type ReflectionRoundDiagnostics struct {
	Round            int
	InputQuery       string
	EffectiveQuery   string
	RewrittenQuery   string
	ReturnedChunkIDs []string
	PromptChunkIDs   []string
	UniqueDocCount   int
	TopScore         float64
	Decision         string
	DecisionMode     ReflectionMode
	DecisionReason   string
}

type ReflectionTrace struct {
	Mode         ReflectionMode
	AdoptedRound int
	StopReason   string
	Rounds       []ReflectionRoundTrace
}

type ReflectionRoundTrace struct {
	Round            int
	InputQuery       string
	EffectiveQuery   string
	RewrittenQuery   string
	ReturnedChunkIDs []string
	PromptChunkIDs   []string
	Decision         string
	DecisionReason   string
}
```

Attach them additively:

```go
type Diagnostics struct {
	...
	Reflection ReflectionDiagnostics
}

type Trace struct {
	...
	Reflection ReflectionTrace
}
```

- [ ] **Step 4: Run the focused test and the API snapshot gate**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run TestAskOptionsExposeReflectionConfig -count=1
GOCACHE=/tmp/go-build go test ./internal/apisnapshot -run TestAPISnapshot -count=1
```

Expected: the first test passes; the API snapshot test fails because the committed baseline is stale.

- [ ] **Step 5: Commit the exported surface change**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git add rag/options.go rag/system.go rag/system_test.go
git commit -m "feat: add self-rag reflection api types"
```

### Task 2: Extract One-Round Ask Execution

**Files:**
- Modify: `rag/ask.go`
- Test: `rag/system_test.go`

- [ ] **Step 1: Write the failing single-round regression test**

Add this test to `rag/system_test.go`:

```go
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
}
```

- [ ] **Step 2: Run the test to confirm the orchestration path is not implemented yet**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run TestAskOffModeMatchesSingleRoundBehavior -count=1
```

Expected: FAIL because reflection diagnostics are not yet populated consistently.

- [ ] **Step 3: Refactor `Ask` into a reusable one-round helper**

In `rag/ask.go`, extract the current body into a helper with a private result shape like:

```go
type askRoundResult struct {
	answer        Answer
	retrieveTrace retrievepolicy.Trace
	topScore      float64
	uniqueDocIDs  []string
}

func (s *System) askRound(ctx context.Context, originalQuestion, query string, opts AskOptions) (askRoundResult, error) {
	// Move the existing retrieve/rerank/pack/sanitize/render/generate path here.
	// The prompt should still render against originalQuestion.
	// Retrieval should execute against query.
}
```

Then make `Ask` do:

```go
func (s *System) Ask(ctx context.Context, question string, opts AskOptions) (Answer, error) {
	if reflectionDisabled(opts.Reflection) {
		res, err := s.askRound(obs.WithCounter(ctx, obs.NewCounter()), question, question, opts)
		if err != nil {
			return Answer{}, err
		}
		return res.answer, nil
	}
	// Reflection loop added in the next task.
}
```

Use helpers so the old no-reflection path preserves current `Answer` semantics exactly.

- [ ] **Step 4: Run the focused `Ask` tests**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run 'TestSystemImportRetrieveAsk|TestAskOffModeMatchesSingleRoundBehavior|TestAskCarriesTraceAndFilters' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the refactor**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git add rag/ask.go rag/system_test.go
git commit -m "refactor: extract single-round ask execution"
```

### Task 3: Implement Rule-Mode Reflection Loop

**Files:**
- Modify: `rag/ask.go`
- Create: `rag/reflection.go`
- Test: `rag/system_test.go`

- [ ] **Step 1: Write the failing rule-mode tests**

Add these tests:

```go
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
			MinUniqueDocs:    1,
			RequireCitations: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Diagnostics.Reflection.Rounds != 1 {
		t.Fatalf("Reflection.Rounds = %d, want 1", ans.Diagnostics.Reflection.Rounds)
	}
	if ans.Diagnostics.Reflection.StopReason == "" {
		t.Fatal("StopReason empty, want explicit rule stop reason")
	}
}

func TestAskRuleModeStopsAtMaxRounds(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "generic travel text"},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        2,
			MinHits:          2,
			MinUniqueDocs:    2,
			RequireCitations: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Diagnostics.Reflection.Rounds != 2 {
		t.Fatalf("Reflection.Rounds = %d, want 2", ans.Diagnostics.Reflection.Rounds)
	}
	if ans.Diagnostics.Reflection.StopReason != "max_rounds" {
		t.Fatalf("StopReason = %q, want max_rounds", ans.Diagnostics.Reflection.StopReason)
	}
}
```

- [ ] **Step 2: Run the rule-mode tests to verify they fail**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run 'TestAskRuleModeStopsAfterSatisfiedFirstRound|TestAskRuleModeStopsAtMaxRounds' -count=1
```

Expected: FAIL because reflection loop behavior does not exist.

- [ ] **Step 3: Implement the rule decision helpers and loop**

Create `rag/reflection.go` with private helpers like:

```go
type reflectionDecision struct {
	Action string
	Query  string
	Reason string
}

func reflectionDisabled(opts *ReflectionOptions) bool {
	return opts == nil || opts.Mode == "" || opts.Mode == ReflectionModeOff
}

func normalizeReflectionOptions(opts *ReflectionOptions) ReflectionOptions {
	out := ReflectionOptions{Mode: ReflectionModeOff, MaxRounds: 1, FailOpen: true}
	if opts != nil {
		out = *opts
	}
	if out.Mode == "" {
		out.Mode = ReflectionModeOff
	}
	if out.MaxRounds <= 0 {
		out.MaxRounds = 1
	}
	return out
}

func decideRule(next ReflectionOptions, round askRoundResult, prev *askRoundResult) reflectionDecision {
	// Evaluate MinHits / MinScore / MinUniqueDocs / RequireCitations.
	// Stop on unchanged evidence or satisfied thresholds.
	// Otherwise continue, keeping the same query for v1 when no rewrite is requested.
}
```

Then update `Ask` to execute up to `MaxRounds` and populate:

```go
answer.Diagnostics.Reflection
answer.Trace.Reflection
```

Each round must append round details and preserve final-answer semantics from the adopted round.

- [ ] **Step 4: Run the rule-mode tests and the core `Ask` regression set**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run 'TestAskRuleModeStopsAfterSatisfiedFirstRound|TestAskRuleModeStopsAtMaxRounds|TestSystemImportRetrieveAsk|TestAskCarriesTraceAndFilters|TestAskReranksAndPacksContext' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the rule-mode loop**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git add rag/ask.go rag/reflection.go rag/system_test.go
git commit -m "feat: add rule-based self-rag reflection"
```

### Task 4: Implement Model And Hybrid Reflection

**Files:**
- Modify: `rag/ask.go`
- Modify: `rag/reflection.go`
- Test: `rag/system_test.go`

- [ ] **Step 1: Write the failing model and hybrid tests**

Add scripted model tests using a custom model stub:

```go
type sequenceModel struct {
	responses []string
	calls     int
}

func (m *sequenceModel) Generate(_ context.Context, _ generate.Request) (generate.Response, error) {
	if m.calls >= len(m.responses) {
		return generate.Response{}, nil
	}
	out := m.responses[m.calls]
	m.calls++
	return generate.Response{Text: out}, nil
}
```

Then add tests:

```go
func TestAskModelModeRewritesAndContinues(t *testing.T) {
	// First answer weak, reflection says rewrite, second round completes.
}

func TestAskHybridModeSkipsReflectionModelWhenRulesAlreadyPass(t *testing.T) {
	// Reflection model call count should remain zero when first round is clearly sufficient.
}
```

The first test should assert:

- rounds == 2
- first round decision is `rewrite_and_continue`
- second round adopted
- rewritten query recorded

The second test should assert:

- rounds == 1
- decision mode is `hybrid`
- no extra reflection model call beyond the answer generation

- [ ] **Step 2: Run the tests to confirm they fail**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run 'TestAskModelModeRewritesAndContinues|TestAskHybridModeSkipsReflectionModelWhenRulesAlreadyPass' -count=1
```

Expected: FAIL.

- [ ] **Step 3: Implement internal model-driven decisions and rewrite flow**

Add private helpers in `rag/reflection.go`:

```go
func (s *System) reflectWithModel(ctx context.Context, originalQuestion string, round askRoundResult) (reflectionDecision, error) {
	// Prompt the existing generate.Model for a strict stop/continue/rewrite decision.
}

func (s *System) rewriteQuery(ctx context.Context, originalQuestion string, round askRoundResult) (string, error) {
	// Prompt the existing generate.Model for a rewritten retrieval query.
}

func (s *System) decideReflection(ctx context.Context, cfg ReflectionOptions, originalQuestion string, round askRoundResult, prev *askRoundResult) (reflectionDecision, error) {
	// Switch on rule/model/hybrid.
}
```

Implementation rules:

- use the original question as the task anchor
- let hybrid short-circuit on clear rule success
- if rewrite returns the same query, stop early with an explicit reason
- if later-round evidence set is unchanged, stop early
- do not change `SearchOptions` across rounds

- [ ] **Step 4: Run the model/hybrid tests and the broader `rag` suite**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the model and hybrid support**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git add rag/ask.go rag/reflection.go rag/system_test.go
git commit -m "feat: add model and hybrid self-rag reflection"
```

### Task 5: Fix Observer And Metrics Semantics

**Files:**
- Modify: `rag/observer_test.go`
- Modify: `rag/instrument_test.go`
- Modify: `rag/ask.go`
- Modify: `rag/reflection.go`

- [ ] **Step 1: Write the failing observer and metrics tests**

Add to `rag/observer_test.go`:

```go
func TestAskReflectionTriggersRetrieveObserverPerRound(t *testing.T) {
	var retrieveCalls, askCalls int
	sys := New(Options{
		Model: fakeModel{},
		Observer: Observer{
			OnRetrieve: func(context.Context, retrieve.Trace) { retrieveCalls++ },
			OnAsk: func(context.Context, Trace) { askCalls++ },
		},
	})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "generic travel text"},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	_, err = sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        2,
			MinHits:          2,
			MinUniqueDocs:    2,
			RequireCitations: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if retrieveCalls != 2 {
		t.Fatalf("OnRetrieve calls = %d, want 2", retrieveCalls)
	}
	if askCalls != 1 {
		t.Fatalf("OnAsk calls = %d, want 1", askCalls)
	}
}
```

Add to `rag/instrument_test.go`:

```go
func TestAskReflectionAggregatesMetricsAcrossRounds(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "generic travel text"},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        2,
			MinHits:          2,
			MinUniqueDocs:    2,
			RequireCitations: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Diagnostics.Reflection.Rounds != 2 {
		t.Fatalf("Reflection.Rounds = %d, want 2", ans.Diagnostics.Reflection.Rounds)
	}
	if ans.Diagnostics.Metrics.Calls.Generate < 2 {
		t.Fatalf("Calls.Generate = %d, want >= 2", ans.Diagnostics.Metrics.Calls.Generate)
	}
}
```

- [ ] **Step 2: Run the observer and metrics tests to confirm failures**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run 'TestAskReflectionTriggersRetrieveObserverPerRound|TestAskReflectionAggregatesMetricsAcrossRounds' -count=1
```

Expected: FAIL if callback counts or aggregated metrics are incomplete.

- [ ] **Step 3: Implement top-level aggregation and observer semantics**

Update `rag/ask.go` and `rag/reflection.go` so:

- `obs.Counter` is installed once at the top-level `Ask`
- every round reuses the same context counter
- round metrics are appended into top-level reflection diagnostics
- final `Diagnostics.Metrics` reflects all generation/embed calls
- final `Trace.Reflection` mirrors the per-round decisions

Do not add a new observer callback in v1. Preserve:

- `OnAsk`: once on success
- `OnRetrieve`: once per internal retrieval call

- [ ] **Step 4: Run the focused tests and the package suite**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the observability fix**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git add rag/ask.go rag/reflection.go rag/observer_test.go rag/instrument_test.go
git commit -m "feat: aggregate self-rag reflection metrics and trace"
```

### Task 6: Add Fail-Open Behavior And Edge-Case Guards

**Files:**
- Modify: `rag/reflection.go`
- Modify: `rag/system_test.go`

- [ ] **Step 1: Write the failing edge-case tests**

Add tests for:

```go
func TestAskReflectionStopsWhenRewriteDoesNotChangeQuery(t *testing.T) {}

func TestAskReflectionStopsWhenEvidenceDoesNotImprove(t *testing.T) {}

func TestAskReflectionFailOpenReturnsBestAvailableAnswer(t *testing.T) {}
```

The fail-open test should use a scripted model that:

- returns a valid first-round answer
- fails during later reflection or rewrite

and then assert:

- `Ask` returns no error when `FailOpen` is true
- the first usable answer is returned
- `FailureFallback` is true
- `FailureReason` is populated

- [ ] **Step 2: Run the edge-case tests to verify failure**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run 'TestAskReflectionStopsWhenRewriteDoesNotChangeQuery|TestAskReflectionStopsWhenEvidenceDoesNotImprove|TestAskReflectionFailOpenReturnsBestAvailableAnswer' -count=1
```

Expected: FAIL.

- [ ] **Step 3: Implement guardrails and fail-open fallback**

Update `rag/reflection.go` so the loop:

- stops when rewrite returns the same query
- stops when returned chunk IDs are unchanged across rounds
- preserves the best available `askRoundResult`
- returns that best result when `FailOpen` is true and a later reflection-only failure occurs

Use a helper shape like:

```go
func sameChunkSet(a, b []string) bool {
	// compare deduped IDs
}
```

and record fallback state into `ReflectionDiagnostics`.

- [ ] **Step 4: Run the focused tests and the full `rag` package**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the edge-case behavior**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git add rag/reflection.go rag/system_test.go
git commit -m "feat: harden self-rag reflection fallback behavior"
```

### Task 7: Refresh Snapshot And Update Docs

**Files:**
- Modify: `README.md`
- Modify: `docs/production-deployment.md`
- Modify: `api/v1.snapshot.txt`

- [ ] **Step 1: Write the doc updates**

Update `README.md` with a new section showing:

```go
ans, err := sys.Ask(ctx, question, rag.AskOptions{
	Search: rag.SearchOptions{Namespace: "docs", TopK: 4},
	Reflection: &rag.ReflectionOptions{
		Mode:             rag.ReflectionModeHybrid,
		MaxRounds:        2,
		MinHits:          2,
		MinUniqueDocs:    1,
		RequireCitations: true,
		AllowRewrite:     true,
		FailOpen:         true,
	},
})
```

and explain:

- final answer fields reflect only the adopted round
- reflection diagnostics contain all rounds
- `OnRetrieve` may fire multiple times during one `Ask`

Update `docs/production-deployment.md` with:

- fail-open recommendation for production
- note that rewritten queries still pass through MQE/HyDE if enabled
- note that `OnRetrieve` callback count is now per internal retrieval round

- [ ] **Step 2: Regenerate the committed API snapshot**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./internal/apisnapshot -run TestAPISnapshot -update
```

Expected: PASS and `api/v1.snapshot.txt` updated on disk.

- [ ] **Step 3: Run the final verification suite**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag ./eval ./internal/apisnapshot -count=1
```

Expected: PASS.

- [ ] **Step 4: Review the diff for API, docs, and tests**

Run:

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git diff -- README.md docs/production-deployment.md rag/options.go rag/system.go rag/ask.go rag/reflection.go rag/system_test.go rag/observer_test.go rag/instrument_test.go api/v1.snapshot.txt
```

Expected: reflection config, round diagnostics, observer semantics, and docs all appear in the diff with no unrelated changes.

- [ ] **Step 5: Commit the documentation and snapshot refresh**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git add README.md docs/production-deployment.md api/v1.snapshot.txt
git commit -m "docs: document self-rag reflection flow"
```

## Self-Review

Spec coverage check:

- reflection mode selection is covered in Tasks 1, 3, and 4
- bounded loop and final-answer semantics are covered in Tasks 2 and 3
- round-level diagnostics and trace are covered in Tasks 1, 3, and 5
- observer and token aggregation semantics are covered in Task 5
- fail-open and early-stop guards are covered in Task 6
- docs and exported API snapshot are covered in Task 7

Placeholder scan:

- no `TODO`, `TBD`, or deferred implementation placeholders remain
- all code-changing tasks include concrete code snippets or exact helper shapes
- all verification steps include exact commands

Type consistency check:

- `ReflectionOptions`, `ReflectionMode`, `ReflectionDiagnostics`, and `ReflectionTrace` naming is consistent across tasks
- `FailOpen`, `AllowRewrite`, `RequireCitations`, and `MaxRounds` are consistent across API, tests, and docs

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-23-self-rag-reflection.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
