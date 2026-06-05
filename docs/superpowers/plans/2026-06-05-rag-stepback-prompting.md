# Step-back Prompting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Step-back Prompting — an abstracted higher-level query variant retrieved alongside the original query — gated by `SearchOptions.EnableStepBack`, reusing the existing query-shaping seam.

**Architecture:** Step-back mirrors HyDE: a stateless `advanced.GenerateStepBack` helper produces one extra query variant, `LLMExpansionPreprocessor.Process` appends it when `EnableStepBack` is set, and the existing `VariantRetriever` retrieves + merges all variants. No retrieval-layer change. Unconfigured = current behavior byte-for-byte.

**Tech Stack:** Go, `generate.Model` interface, existing `advanced` / `retrieve` / `rag` packages, `internal/apisnapshot` API gate.

This is **Plan 1 of 3** (Step-back → AskConversation → Compression) from `docs/superpowers/specs/2026-06-05-rag-query-transforms-design.md`.

---

### Task 1: `advanced.GenerateStepBack` helper

**Files:**
- Modify: `advanced/llm.go` (append new function after `GenerateHypothetical`, ends at line 71)
- Test: `advanced/llm_test.go` (append after the existing `GenerateHypothetical` tests)

- [ ] **Step 1: Write the failing tests**

Append to `advanced/llm_test.go` (the file already defines `scriptedModel{resp, err}` and imports `context`, `errors`, `strings`, `testing`, `generate`):

```go
func TestGenerateStepBackTrimsWhitespace(t *testing.T) {
	got, err := GenerateStepBack(context.Background(), scriptedModel{
		resp: "  How is the user level system designed?  \n",
	}, "how many points does Lv5 need?")
	if err != nil {
		t.Fatalf("GenerateStepBack(): %v", err)
	}
	if !strings.Contains(got, "level system") {
		t.Fatalf("got = %q", got)
	}
	if strings.HasPrefix(got, " ") || strings.HasSuffix(got, "\n") {
		t.Fatalf("got not trimmed: %q", got)
	}
}

func TestGenerateStepBackRequiresModel(t *testing.T) {
	_, err := GenerateStepBack(context.Background(), nil, "anything")
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./advanced/ -run TestGenerateStepBack -v`
Expected: FAIL — compile error `undefined: GenerateStepBack`.

- [ ] **Step 3: Write the minimal implementation**

Append to `advanced/llm.go` after line 71 (after `GenerateHypothetical`'s closing brace):

```go
// GenerateStepBack asks the model to abstract query into a more general,
// higher-level question that surfaces background knowledge. The result is
// retrieved alongside the original query (step-back prompting).
func GenerateStepBack(ctx context.Context, model generate.Model, query string) (string, error) {
	if model == nil {
		return "", ErrModelRequired
	}
	prompt := fmt.Sprintf(`Generate a more general, higher-level version of the question below that retrieves useful background knowledge. Keep the original intent. Output only the rewritten question, no numbering, no commentary.

Question: %s`, query)

	resp, err := model.Generate(ctx, generate.Request{
		Messages: []generate.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Text), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./advanced/ -run TestGenerateStepBack -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add advanced/llm.go advanced/llm_test.go
git commit -m "feat(advanced): add GenerateStepBack query-transform helper

Step-back prompting abstracts a specific query into a higher-level
background question, mirroring the GenerateHypothetical helper."
```

---

### Task 2: Wire step-back into `LLMExpansionPreprocessor`

**Files:**
- Modify: `retrieve/retrieve.go` — add `EnableStepBack` field to `Request` (struct ends at line 49, after `MQECount` at line 41) and append a step-back branch in `Process` (after the HyDE block, lines 253-262)
- Test: `retrieve/retrieve_test.go` (append after `TestLLMExpansionPreprocessorRequiresModelWhenEnabled`, line 93)

- [ ] **Step 1: Write the failing tests**

Append to `retrieve/retrieve_test.go` (the file already defines `*scriptedModel{resps, err, call}` and imports `context`, `errors`, `strings`, `testing`, `advanced`, `generate`):

```go
func TestLLMExpansionPreprocessorAppendsStepBack(t *testing.T) {
	model := &scriptedModel{resps: []string{"how is the level system designed"}}
	pre := LLMExpansionPreprocessor{Model: model}
	res, err := pre.Process(context.Background(), Request{
		Query:          "how many points does lv5 need",
		EnableStepBack: true,
	})
	if err != nil {
		t.Fatalf("Process(): %v", err)
	}
	if len(res.QueryVariants) != 2 {
		t.Fatalf("QueryVariants = %#v, want 2", res.QueryVariants)
	}
	if res.QueryVariants[0] != "how many points does lv5 need" {
		t.Fatalf("first variant = %q, want original query", res.QueryVariants[0])
	}
	if res.QueryVariants[1] != "how is the level system designed" {
		t.Fatalf("second variant = %q, want step-back query", res.QueryVariants[1])
	}
}

func TestLLMExpansionPreprocessorStepBackRequiresModel(t *testing.T) {
	_, err := LLMExpansionPreprocessor{}.Process(context.Background(), Request{
		Query:          "anything",
		EnableStepBack: true,
	})
	if !errors.Is(err, advanced.ErrModelRequired) {
		t.Fatalf("err = %v, want advanced.ErrModelRequired", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./retrieve/ -run TestLLMExpansionPreprocessor -v`
Expected: FAIL — `Request` has no field `EnableStepBack` (compile error).

- [ ] **Step 3: Add the `EnableStepBack` field to `Request`**

In `retrieve/retrieve.go`, in the `Request` struct, add the field immediately after the `MQECount` line:

```go
	MQECount                     int            // MQECount is the number of expansion queries to generate.
	EnableStepBack               bool           // EnableStepBack turns on step-back (higher-level) query expansion.
```

- [ ] **Step 4: Append the step-back branch in `Process`**

In `retrieve/retrieve.go`, in `LLMExpansionPreprocessor.Process`, insert this block immediately after the HyDE `if req.EnableHyDE { ... }` block (which closes at line 262) and before the `if len(variants) == 0 {` check (line 263):

```go
	if req.EnableStepBack {
		if p.Model == nil {
			return PreprocessResult{}, advanced.ErrModelRequired
		}
		stepback, err := advanced.GenerateStepBack(ctx, p.Model, req.Query)
		if err != nil {
			return PreprocessResult{}, err
		}
		variants = appendUniqueQueries(variants, stepback)
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./retrieve/ -run TestLLMExpansionPreprocessor -v`
Expected: PASS (all `TestLLMExpansionPreprocessor*` tests, including the two new ones).

- [ ] **Step 6: Commit**

```bash
git add retrieve/retrieve.go retrieve/retrieve_test.go
git commit -m "feat(retrieve): append step-back query variant when EnableStepBack set

LLMExpansionPreprocessor now adds a higher-level step-back query as an
extra variant, retrieved alongside the original via VariantRetriever."
```

---

### Task 3: Expose `EnableStepBack` on `SearchOptions` and map it through

**Files:**
- Modify: `rag/options.go` — add `EnableStepBack` field to `SearchOptions` (after `MQECount`, line 32)
- Modify: `rag/retrieve.go` — add the mapping line into the `retrievepolicy.Request{...}` literal (after `MQECount: opts.MQECount,`, line 51)
- Update: `api/v1.snapshot.txt` (regenerated, not hand-edited)

- [ ] **Step 1: Add the `EnableStepBack` field to `SearchOptions`**

In `rag/options.go`, in the `SearchOptions` struct, add the field immediately after the `MQECount` line:

```go
	MQECount                     int            // MQECount is the number of expansion queries to generate.
	EnableStepBack               bool           // EnableStepBack turns on step-back (higher-level) query expansion.
```

- [ ] **Step 2: Map the field into the retrieve request**

In `rag/retrieve.go`, in the `req := retrievepolicy.Request{...}` literal, add the mapping immediately after the `MQECount` line:

```go
		MQECount:                     opts.MQECount,
		EnableStepBack:               opts.EnableStepBack,
```

- [ ] **Step 3: Verify it builds**

Run: `go build ./...`
Expected: no output, exit 0.

- [ ] **Step 4: Run the API snapshot test to confirm the gate fires**

Run: `go test ./internal/apisnapshot/ -run TestAPISnapshot -v`
Expected: FAIL — the exported surface changed (new `field EnableStepBack bool` on `SearchOptions`). This is the deliberate additive change the gate is meant to flag.

- [ ] **Step 5: Regenerate the API snapshot baseline**

Run: `go test ./internal/apisnapshot/ -run TestAPISnapshot -update`
Expected: PASS, logs `updated baseline`. This adds the `advanced.GenerateStepBack` function entry and the two `SearchOptions`/`retrieve.Request` `EnableStepBack` fields to `api/v1.snapshot.txt`.

- [ ] **Step 6: Run the full test suite**

Run: `go test ./...`
Expected: all packages PASS (`ok` / `no test files`), no `FAIL`.

- [ ] **Step 7: Commit**

```bash
git add rag/options.go rag/retrieve.go api/v1.snapshot.txt
git commit -m "feat(rag): expose EnableStepBack on SearchOptions

Maps the search-level flag through to retrieve.Request so Ask/Search
callers can turn on step-back prompting. Regenerates the v1 API snapshot
for the additive surface change."
```

---

## Self-Review

**Spec coverage (Component A section of the spec):**
- `advanced.GenerateStepBack` → Task 1. ✓
- `SearchOptions.EnableStepBack` → Task 3 Step 1. ✓
- `retrieve.Request.EnableStepBack` → Task 2 Step 3. ✓
- Wiring into `LLMExpansionPreprocessor` after HyDE, additive variant via `appendUniqueQueries` → Task 2 Step 4. ✓
- `ErrModelRequired` when model nil and flag set → Task 1 Step 3 + Task 2 Step 4 (+ tests in Task 1 Step 1, Task 2 Step 1). ✓
- No `Retriever` change; abstract query recorded in `Trace.QueryVariants` (built by existing `Process` trace code). ✓
- Unconfigured = byte-for-byte unchanged: `EnableStepBack` defaults false; both new branches are guarded. ✓
- API snapshot update → Task 3 Steps 4-5. ✓

**Placeholder scan:** No TBD/TODO; every code step shows complete code; every command shows expected output. ✓

**Type consistency:** `GenerateStepBack(ctx, model generate.Model, query string) (string, error)` is defined identically in Task 1 (impl) and called with the same signature in Task 2 Step 4. Field name `EnableStepBack` is identical across `retrieve.Request` (Task 2), `SearchOptions` (Task 3), and the mapping (Task 3 Step 2). ✓
