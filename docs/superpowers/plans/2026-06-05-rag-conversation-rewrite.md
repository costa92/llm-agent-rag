# Conversation-Aware Rewrite (AskConversation) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `System.AskConversation(ctx, history, question, opts)` that resolves multi-turn coreference/ellipsis into a standalone retrieval query (via a new `QueryCondenser` seam) and then delegates to the existing `Ask` pipeline.

**Architecture:** A stateless `advanced.CondenseQuery` helper builds a standalone query from `[]generate.Message` history + the latest question. A `rag.QueryCondenser` seam (default `LLMCondenser`, fallback `passthroughCondenser` when no model) wraps it. `AskConversation` condenses, then calls `Ask` with the standalone query, recording original + condensed query on `Diagnostics`. Empty history → no model call → behaviorally identical to `Ask`.

**Tech Stack:** Go, `generate.Model` / `generate.Message`, existing `advanced` / `rag` packages, `internal/apisnapshot` API gate.

This is **Plan 2 of 3** (Step-back → AskConversation → Compression) from `docs/superpowers/specs/2026-06-05-rag-query-transforms-design.md`. History type reuses `generate.Message` (decided in the spec — no new `rag.Turn`).

---

### Task 1: `advanced.CondenseQuery` helper

**Files:**
- Modify: `advanced/llm.go` (append after `GenerateStepBack`)
- Test: `advanced/llm_test.go` (append after existing tests)

- [ ] **Step 1: Write the failing tests**

Append to `advanced/llm_test.go` (already imports `context`, `errors`, `strings`, `testing`, `generate`, and defines `scriptedModel{resp, err}`):

```go
func TestCondenseQueryEmptyHistoryReturnsQuestionWithoutModel(t *testing.T) {
	// nil model is allowed when history is empty: no rewrite is needed.
	got, err := CondenseQuery(context.Background(), nil, nil, "what is the annual fee?")
	if err != nil {
		t.Fatalf("CondenseQuery(): %v", err)
	}
	if got != "what is the annual fee?" {
		t.Fatalf("got = %q, want question unchanged", got)
	}
}

func TestCondenseQueryRewritesWithHistory(t *testing.T) {
	history := []generate.Message{
		{Role: "user", Content: "What perks does HelloTalk membership have?"},
		{Role: "assistant", Content: "Members get unlimited translation and advanced matching."},
	}
	got, err := CondenseQuery(context.Background(), scriptedModel{
		resp: "  How much is the HelloTalk membership annual fee?  \n",
	}, history, "and the annual fee?")
	if err != nil {
		t.Fatalf("CondenseQuery(): %v", err)
	}
	if got != "How much is the HelloTalk membership annual fee?" {
		t.Fatalf("got = %q, want trimmed standalone query", got)
	}
}

func TestCondenseQueryRequiresModelWhenHistoryPresent(t *testing.T) {
	history := []generate.Message{{Role: "user", Content: "hi"}}
	_, err := CondenseQuery(context.Background(), nil, history, "and then?")
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

func TestCondenseQueryEmptyModelReplyFallsBackToQuestion(t *testing.T) {
	history := []generate.Message{{Role: "user", Content: "hi"}}
	got, err := CondenseQuery(context.Background(), scriptedModel{resp: "   \n"}, history, "and then?")
	if err != nil {
		t.Fatalf("CondenseQuery(): %v", err)
	}
	if got != "and then?" {
		t.Fatalf("got = %q, want fallback to original question", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./advanced/ -run TestCondenseQuery -v`
Expected: FAIL — compile error `undefined: CondenseQuery`.

- [ ] **Step 3: Write the minimal implementation**

Append to `advanced/llm.go` after `GenerateStepBack`'s closing brace:

```go
// CondenseQuery rewrites question into a standalone retrieval query using the
// conversation history, resolving pronouns and omitted context. With empty
// history it returns question unchanged without calling the model. An empty
// model reply also falls back to the original question.
func CondenseQuery(ctx context.Context, model generate.Model, history []generate.Message, question string) (string, error) {
	if len(history) == 0 {
		return question, nil
	}
	if model == nil {
		return "", ErrModelRequired
	}
	var hist strings.Builder
	for _, m := range history {
		hist.WriteString(m.Role)
		hist.WriteString(": ")
		hist.WriteString(m.Content)
		hist.WriteString("\n")
	}
	prompt := fmt.Sprintf(`Given the conversation history, rewrite the latest question into a standalone, complete retrieval query that resolves any pronouns or omitted context. If no rewrite is needed, return the question unchanged. Output only the rewritten question, no commentary.

Conversation history:
%sLatest question: %s`, hist.String(), question)

	resp, err := model.Generate(ctx, generate.Request{
		Messages: []generate.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(resp.Text)
	if out == "" {
		return question, nil
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./advanced/ -run TestCondenseQuery -v`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
git add advanced/llm.go advanced/llm_test.go
git commit -m "feat(advanced): add CondenseQuery for multi-turn query rewriting

CondenseQuery rewrites a follow-up question into a standalone retrieval
query using conversation history; empty history returns the question
unchanged without a model call."
```

---

### Task 2: `QueryCondenser` seam + wiring into `Options`/`System`

**Files:**
- Create: `rag/conversation.go` (the seam, implementations, and `effectiveCondenser`)
- Modify: `rag/options.go` — add `QueryCondenser` field to `Options` (after the `QueryPlanner` field, the last field in `Options`, line 295)
- Modify: `rag/system.go` — add `condenser QueryCondenser` field to `System` (after `queryPlanner QueryPlanner`, line 344) and `condenser: opts.QueryCondenser,` to the `New` struct literal (next to `queryPlanner: opts.QueryPlanner,`, line 426)
- Test: `rag/conversation_test.go`

- [ ] **Step 1: Write the failing test**

Create `rag/conversation_test.go`:

```go
package rag

import (
	"context"
	"errors"
	"testing"

	"github.com/costa92/llm-agent-rag/advanced"
	"github.com/costa92/llm-agent-rag/generate"
)

func TestLLMCondenserEmptyHistoryReturnsQuestion(t *testing.T) {
	// No model needed when history is empty.
	c := LLMCondenser{Model: nil}
	got, err := c.Condense(context.Background(), nil, "what is the fee?")
	if err != nil {
		t.Fatalf("Condense(): %v", err)
	}
	if got != "what is the fee?" {
		t.Fatalf("got = %q, want question unchanged", got)
	}
}

func TestLLMCondenserRequiresModelWhenHistory(t *testing.T) {
	c := LLMCondenser{Model: nil}
	_, err := c.Condense(context.Background(), []generate.Message{{Role: "user", Content: "hi"}}, "and then?")
	if !errors.Is(err, advanced.ErrModelRequired) {
		t.Fatalf("err = %v, want advanced.ErrModelRequired", err)
	}
}

func TestPassthroughCondenserAlwaysReturnsQuestion(t *testing.T) {
	got, err := passthroughCondenser{}.Condense(
		context.Background(),
		[]generate.Message{{Role: "user", Content: "hi"}},
		"unchanged?",
	)
	if err != nil {
		t.Fatalf("Condense(): %v", err)
	}
	if got != "unchanged?" {
		t.Fatalf("got = %q, want unchanged", got)
	}
}

func TestEffectiveCondenserDefaults(t *testing.T) {
	// No model configured → passthrough.
	noModel := New(Options{})
	if _, ok := noModel.effectiveCondenser().(passthroughCondenser); !ok {
		t.Fatalf("effectiveCondenser() = %T, want passthroughCondenser", noModel.effectiveCondenser())
	}
	// Model configured → LLMCondenser.
	withModel := New(Options{Model: fakeModel{}})
	if _, ok := withModel.effectiveCondenser().(LLMCondenser); !ok {
		t.Fatalf("effectiveCondenser() = %T, want LLMCondenser", withModel.effectiveCondenser())
	}
	// Explicit override wins.
	override := New(Options{Model: fakeModel{}, QueryCondenser: passthroughCondenser{}})
	if _, ok := override.effectiveCondenser().(passthroughCondenser); !ok {
		t.Fatalf("effectiveCondenser() = %T, want overridden passthroughCondenser", override.effectiveCondenser())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./rag/ -run 'TestLLMCondenser|TestPassthroughCondenser|TestEffectiveCondenser' -v`
Expected: FAIL — compile errors (`undefined: LLMCondenser`, `passthroughCondenser`, `QueryCondenser`, `effectiveCondenser`).

- [ ] **Step 3: Create the seam file**

Create `rag/conversation.go`:

```go
package rag

import (
	"context"

	"github.com/costa92/llm-agent-rag/advanced"
	"github.com/costa92/llm-agent-rag/generate"
)

// QueryCondenser rewrites a follow-up question into a standalone retrieval
// query using conversation history. It is the multi-turn query-shaping seam
// AskConversation depends on. With empty history a condenser MUST return the
// question unchanged.
type QueryCondenser interface {
	Condense(ctx context.Context, history []generate.Message, question string) (string, error)
}

// LLMCondenser is the default QueryCondenser. It delegates to
// advanced.CondenseQuery: empty history returns the question unchanged with
// no model call; otherwise the model rewrites it into a standalone query.
type LLMCondenser struct {
	Model generate.Model // Model performs the rewrite.
}

// Condense rewrites question into a standalone query using history.
func (c LLMCondenser) Condense(ctx context.Context, history []generate.Message, question string) (string, error) {
	return advanced.CondenseQuery(ctx, c.Model, history, question)
}

// passthroughCondenser returns the question unchanged. It is the default when
// no model is configured, so AskConversation degrades to plain Ask.
type passthroughCondenser struct{}

// Condense returns question unchanged.
func (passthroughCondenser) Condense(_ context.Context, _ []generate.Message, question string) (string, error) {
	return question, nil
}

// effectiveCondenser returns the configured QueryCondenser, or a default: an
// LLMCondenser over the System's model when a model is set, otherwise a
// passthroughCondenser. Mirrors effectiveGrader / effectiveQueryPlanner.
func (s *System) effectiveCondenser() QueryCondenser {
	if s.condenser != nil {
		return s.condenser
	}
	if s.model == nil {
		return passthroughCondenser{}
	}
	return LLMCondenser{Model: s.model}
}
```

- [ ] **Step 4: Add the `QueryCondenser` field to `Options`**

In `rag/options.go`, in the `Options` struct, add immediately after the `QueryPlanner QueryPlanner` field (the closing field, line 295):

```go
	// QueryCondenser, when set, rewrites multi-turn follow-up questions
	// into standalone retrieval queries for System.AskConversation. A nil
	// condenser defaults to an LLMCondenser over the System's model, or a
	// passthrough when no model is configured.
	QueryCondenser QueryCondenser
```

- [ ] **Step 5: Add the `condenser` field to `System` and wire it in `New`**

In `rag/system.go`, in the `System` struct, add immediately after the `queryPlanner QueryPlanner` field (line 344):

```go
	condenser QueryCondenser
```

Then, in `New`, in the struct literal that builds `s`, add immediately after the `queryPlanner: opts.QueryPlanner,` line (line 426):

```go
		condenser: opts.QueryCondenser,
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./rag/ -run 'TestLLMCondenser|TestPassthroughCondenser|TestEffectiveCondenser' -v`
Expected: PASS (all four).

- [ ] **Step 7: Commit**

```bash
git add rag/conversation.go rag/options.go rag/system.go rag/conversation_test.go
git commit -m "feat(rag): add QueryCondenser seam wired into Options and System

LLMCondenser (default) delegates to advanced.CondenseQuery; a passthrough
condenser is used when no model is configured. effectiveCondenser mirrors
effectiveGrader/effectiveQueryPlanner."
```

---

### Task 3: `Diagnostics` provenance fields + `System.AskConversation`

**Files:**
- Modify: `rag/system.go` — add two fields to the `Diagnostics` struct (after `Reflection ReflectionDiagnostics`, line 76)
- Modify: `rag/conversation.go` — add the `AskConversation` method
- Update: `api/v1.snapshot.txt` (regenerated)
- Test: `rag/conversation_test.go` (append integration tests)

- [ ] **Step 1: Write the failing integration tests**

Append to `rag/conversation_test.go` (add `"github.com/costa92/llm-agent-rag/ingest"` to its imports):

```go
func TestAskConversationCondensesAndRecordsProvenance(t *testing.T) {
	// Call 1 is the condense (returns the standalone query); call 2 is the
	// answer generation.
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "HelloTalk membership annual fee"},
			{Text: "the annual fee is $99"},
		},
	}
	sys := New(Options{Model: model})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "HelloTalk membership annual fee is $99 per year."},
	}, ingest.ImportOptions{Namespace: "kb"}); err != nil {
		t.Fatalf("Import(): %v", err)
	}

	history := []generate.Message{
		{Role: "user", Content: "What perks does HelloTalk membership have?"},
		{Role: "assistant", Content: "Unlimited translation and advanced matching."},
	}
	ans, err := sys.AskConversation(context.Background(), history, "and the annual fee?", AskOptions{
		Search: SearchOptions{Namespace: "kb"},
	})
	if err != nil {
		t.Fatalf("AskConversation(): %v", err)
	}
	if ans.Diagnostics.OriginalQuestion != "and the annual fee?" {
		t.Fatalf("OriginalQuestion = %q, want original", ans.Diagnostics.OriginalQuestion)
	}
	if ans.Diagnostics.CondensedQuery != "HelloTalk membership annual fee" {
		t.Fatalf("CondensedQuery = %q, want standalone query", ans.Diagnostics.CondensedQuery)
	}
	// The condense prompt (call 1) must contain the history and the question.
	if len(model.requests) < 2 {
		t.Fatalf("model calls = %d, want >= 2 (condense + answer)", len(model.requests))
	}
	condensePrompt := model.requests[0].Messages[0].Content
	if !strings.Contains(condensePrompt, "and the annual fee?") {
		t.Fatalf("condense prompt missing latest question: %q", condensePrompt)
	}
}

func TestAskConversationEmptyHistoryMatchesAsk(t *testing.T) {
	build := func() *System {
		sys := New(Options{Model: fakeModel{}})
		if _, err := sys.Import(context.Background(), []ingest.Document{
			{ID: "doc1", Content: "Paris is the capital of France."},
		}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
			t.Fatalf("Import(): %v", err)
		}
		return sys
	}
	opts := AskOptions{Search: SearchOptions{Namespace: "geo"}}

	askAns, err := build().Ask(context.Background(), "capital of France", opts)
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	convAns, err := build().AskConversation(context.Background(), nil, "capital of France", opts)
	if err != nil {
		t.Fatalf("AskConversation(): %v", err)
	}
	if convAns.Text != askAns.Text {
		t.Fatalf("AskConversation Text = %q, want Ask Text %q", convAns.Text, askAns.Text)
	}
	if len(convAns.Hits) != len(askAns.Hits) {
		t.Fatalf("AskConversation Hits = %d, want %d", len(convAns.Hits), len(askAns.Hits))
	}
}

func TestAskConversationRequiresModel(t *testing.T) {
	sys := New(Options{}) // no model
	_, err := sys.AskConversation(context.Background(), nil, "anything", AskOptions{})
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./rag/ -run TestAskConversation -v`
Expected: FAIL — `ans.Diagnostics.OriginalQuestion` undefined and `AskConversation` undefined (compile errors).

- [ ] **Step 3: Add provenance fields to `Diagnostics`**

In `rag/system.go`, in the `Diagnostics` struct, add immediately after the `Reflection ReflectionDiagnostics` field (line 76):

```go
	// OriginalQuestion is the caller's pre-condense question on a
	// System.AskConversation run; empty for an ordinary Ask. Additive.
	OriginalQuestion string
	// CondensedQuery is the standalone query AskConversation derived from
	// OriginalQuestion plus conversation history; empty for an ordinary
	// Ask. Additive.
	CondensedQuery string
```

- [ ] **Step 4: Add `AskConversation` to `rag/conversation.go`**

Append to `rag/conversation.go` (the `context` and `generate` imports are already present):

```go
// AskConversation answers a follow-up question in a multi-turn conversation.
// It condenses history + question into a standalone retrieval query
// (resolving coreference and ellipsis) via the effective QueryCondenser, then
// delegates to Ask. With empty history the condense step returns the question
// unchanged with no extra model call, so the answer is identical to
// Ask(ctx, question, opts); AskConversation additionally records
// OriginalQuestion and CondensedQuery on the result's Diagnostics.
func (s *System) AskConversation(ctx context.Context, history []generate.Message, question string, opts AskOptions) (Answer, error) {
	if s.model == nil {
		return Answer{}, ErrModelRequired
	}
	standalone, err := s.effectiveCondenser().Condense(ctx, history, question)
	if err != nil {
		return Answer{}, err
	}
	ans, err := s.Ask(ctx, standalone, opts)
	if err != nil {
		return ans, err
	}
	ans.Diagnostics.OriginalQuestion = question
	ans.Diagnostics.CondensedQuery = standalone
	return ans, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./rag/ -run TestAskConversation -v`
Expected: PASS (all three).

- [ ] **Step 6: Regenerate the API snapshot**

Run: `go test ./internal/apisnapshot/ -run TestAPISnapshot -update`
Expected: PASS, logs `updated baseline`. Adds `func CondenseQuery`, `func (s *System) AskConversation`, `type QueryCondenser interface`, `type LLMCondenser struct` (+ method), `Options` field `QueryCondenser`, and `Diagnostics` fields `OriginalQuestion` / `CondensedQuery`.

- [ ] **Step 7: Run the full suite**

Run: `go test ./...`
Expected: all packages PASS, no `FAIL`.

- [ ] **Step 8: Commit**

```bash
git add rag/system.go rag/conversation.go rag/conversation_test.go api/v1.snapshot.txt
git commit -m "feat(rag): add System.AskConversation for multi-turn retrieval

AskConversation condenses conversation history + follow-up question into a
standalone query, delegates to Ask, and records the original and condensed
queries on Diagnostics. Empty history degrades to plain Ask. Regenerates
the v1 API snapshot for the additive surface."
```

---

## Self-Review

**Spec coverage (Component B section of the spec):**
- History type reuses `generate.Message` (no `rag.Turn`) → all tasks use `[]generate.Message`. ✓
- `rag.QueryCondenser` seam + `Condense` signature → Task 2 Step 3. ✓
- Default `LLMCondenser{Model}` delegating to `advanced.CondenseQuery` → Task 2 Step 3 + Task 1. ✓
- `advanced.CondenseQuery` prompt ("standalone, complete retrieval query; if no rewrite needed, return unchanged") → Task 1 Step 3. ✓
- Empty history short-circuits to question with no model call → Task 1 Step 3 (`len(history)==0` guard) + tests. ✓
- `Options.QueryCondenser`: nil + Model → LLMCondenser; nil + no Model → passthrough → Task 2 Step 3 `effectiveCondenser` + Task 2 Step 1 test. ✓
- `System.AskConversation(ctx, history []generate.Message, question, opts)` in a conversation file: condense → delegate to `Ask` → Task 3 Step 4. ✓
- Empty history → behaviorally equivalent to `Ask` → Task 3 Step 1 `TestAskConversationEmptyHistoryMatchesAsk`. ✓
- Record original + condensed query on `Answer` diagnostics → Task 3 Step 3 (fields) + Step 4 (population) + Step 1 (assertion). ✓
- API snapshot update → Task 3 Step 6. ✓

**Placeholder scan:** No TBD/TODO; every code step shows complete code; every command states expected output. ✓

**Type consistency:** `CondenseQuery(ctx, model generate.Model, history []generate.Message, question string) (string, error)` defined in Task 1, called by `LLMCondenser.Condense` in Task 2 with the same signature. `QueryCondenser.Condense(ctx, history []generate.Message, question string) (string, error)` is identical across the interface (Task 2), `LLMCondenser` (Task 2), `passthroughCondenser` (Task 2), and the `effectiveCondenser`/`AskConversation` call sites (Task 2/3). `Diagnostics.OriginalQuestion` / `CondensedQuery` field names match between definition (Task 3 Step 3) and use (Task 3 Step 4 + tests). ✓

**Note (deliberate, documented):** the condense model call runs before `Ask` installs its counters, so its token cost is not included in `Diagnostics.Metrics`. This is an accepted v1 limitation (a dedicated `StageCondense` tag is future-additive); it keeps the change minimal and does not affect correctness.
