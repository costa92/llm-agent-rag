# Testing Patterns

**Analysis Date:** 2026-05-20

## Test Framework

**Runner:** the standard library — `testing` from Go 1.26.0. No third-party
test framework anywhere (no testify, no ginkgo, no gomega). No
assertion library. Every test is a plain `func TestX(t *testing.T)`.

**Run commands** (from `.github/workflows/test.yml`):

```bash
go vet ./...               # vet all packages
go build ./...             # compile all packages
go test ./...              # run every test
go test ./internal/apisnapshot/...   # explicit gate run (visibility)
go build -tags llmagent ./...
go test  -tags llmagent ./adapter/...
```

Live-backend tests need an env var set; see "Integration Tests" below.

## Totals — by Top-Level Directory

`*.go` files: **125**. `*_test.go` files: **59** (52 inside the core
tree + 5 examples + 1 contract + 1 apisnapshot). Per-directory
src-vs-test breakdown (counting only top-level files in each package):

| Package | Source files | Test files | Source lines | Test lines |
|---|---|---|---|---|
| `rag/` | 11 | 11 | 2191 | 2611 |
| `retrieve/` | 3 | 3 | 2053 | 1748 |
| `graph/` | 9 | 8 | 1494 | 1035 |
| `store/` | 6 | 4 | 1526 | 504 |
| `eval/` | 7 | 7 | 886 | 987 |
| `ingest/` | 4 | 3 | 472 | 178 |
| `postgres/` | 3 | 2 | 950 | 295 |
| `rerank/` | 2 | 2 | 333 | 224 |
| `guard/` | 2 | 2 | 186 | 108 |
| `embed/` | 3 | 1 | 126 | 24 |
| `pack/` | 1 | 1 | 205 | 60 |
| `prompt/` | 3 | 1 | 77 | 35 |
| `obs/` | 1 | 1 | 95 | 40 |
| `feedback/` | 1 | 1 | 99 | 213 |
| `agentic/` | 1 | 1 | 175 | 166 |
| `advanced/` | 2 | 1 | 78 | 59 |
| `tree/` | 1 | 1 | 226 | 57 |
| `generate/` | 2 | 0 | 43 | 0 |
| `examples/` | 0 | 5 | 0 | 448 |
| `internal/apisnapshot/` | 1 | 1 | 343 | 118 |
| `contract/` | 0 | 1 | 0 | 109 |
| `adapter/llmagent/` | 2 | 2 | 239 | 76 |
| `store/storetest/` | 1 | 0 | — (shared suite) | — |

`generate/` has no tests of its own — the package only defines the
interfaces and value types (`Model`, `Request`, `Response`, `Usage`,
`Message`) and they are exercised transitively by every package that
embeds them. `examples/` and `contract/` are test-only packages.

## Test Naming Conventions

**`TestX_CamelCaseScenario` is the dominant form.** No underscores in
the scenario name — the scenario is appended in CamelCase:

```go
// graph/extract_test.go
func TestLLMEntityExtractorCleanOutput(t *testing.T)
func TestLLMEntityExtractorLenientParsing(t *testing.T)
func TestLLMEntityExtractorNilModel(t *testing.T)
func TestLLMEntityExtractorModelError(t *testing.T)

// rag/inject_test.go
func TestAskNeutralizesInjection(t *testing.T)
func TestAskDropsInjection(t *testing.T)
func TestAskNoScannerKeepsContent(t *testing.T)

// adapter/llmagent/tool_test.go
func TestAsToolNamespaceIsolation(t *testing.T)
func TestAsToolSchemaIsValidJSON(t *testing.T)

// tree/tree_test.go
func TestBuildCreatesSectionHierarchy(t *testing.T)
func TestFindLocatesSectionAndChunkNodes(t *testing.T)
```

`grep "^func Test_"` returns **zero hits** — no `Test_X_Subcase` style is
used. The exception is *inside* the shared `storetest` suite, where
subtests are named with snake_case strings passed to `t.Run` (e.g.
`"Upsert_and_Get_round_trip"`, `"Search_returns_nearest_first"`), so the
Go test path reads `TestInMemoryStoreConformance/Upsert_and_Get_round_trip`.

## Test Structure — Not Table-Driven

**Table-driven tests are essentially absent.** `grep -rn "for _, tc :=
range\|for _, tt := range\|for _, c := range" --include="*_test.go"`
returns **zero hits**. Every scenario is its own `TestX` function.

Each test follows the Arrange/Act/Assert shape inline, e.g.:

```go
// retrieve/retrieve_test.go
func TestNoopPreprocessorPreservesQuery(t *testing.T) {
    res, err := NoopPreprocessor{}.Process(context.Background(), Request{Query: "paris"})
    if err != nil {
        t.Fatalf("Process(): %v", err)
    }
    if len(res.QueryVariants) != 1 || res.QueryVariants[0] != "paris" {
        t.Fatalf("QueryVariants = %+v, want [paris]", res.QueryVariants)
    }
    if res.Trace.EffectiveQuery != "paris" {
        t.Fatalf("EffectiveQuery = %q, want paris", res.Trace.EffectiveQuery)
    }
}
```

**Assertion idiom:** `if got != want { t.Fatalf("got %X, want %Y") }`.
`t.Errorf` is rare; `t.Fatalf` is the default — most tests stop on the
first failed assertion. Error messages are formatted with `got = …,
want …` so the diff reads cleanly in CI logs.

## Subtests — Only Inside Conformance Suites

`t.Run` is used in **only one file**:
`store/storetest/storetest.go`. Counted via `grep -rn "t.Run("
--include="*.go"`: 31 hits, all in `storetest.go`. They name the
conformance subtests of the shared suite:

```go
// store/storetest/storetest.go
func RunConformance(t *testing.T, factory Factory, opts ...Option) {
    t.Helper()
    // …
    t.Run("Upsert_and_Get_round_trip",             func(t *testing.T) { testUpsertGet(t, factory) })
    t.Run("Search_returns_nearest_first",          func(t *testing.T) { testSearchNearest(t, factory) })
    t.Run("Search_respects_namespace",             func(t *testing.T) { testSearchNamespace(t, factory) })
    t.Run("Filter_narrows_results",                func(t *testing.T) { testFilter(t, factory) })
    t.Run("Security_filter_intersects_with_caller_filter", func(t *testing.T) { testSecurityFilter(t, factory) })
    t.Run("List_returns_namespace_chunks",         func(t *testing.T) { testList(t, factory) })
    t.Run("Get_on_missing_returns_ErrNotFound",    func(t *testing.T) { testGetNotFound(t, factory) })
    t.Run("Remove_on_missing_returns_ErrNotFound", func(t *testing.T) { testRemoveNotFound(t, factory) })
    t.Run("Remove_deletes",                         func(t *testing.T) { testRemove(t, factory) })
    t.Run("RemoveByFilter_returns_count_and_removes", func(t *testing.T) { testRemoveByFilter(t, factory) })
    t.Run("Stats_reports_count_and_dim",           func(t *testing.T) { testStats(t, factory) })
    if cfg.dimensionStrict {
        t.Run("Dimension_mismatch_returns_error",  func(t *testing.T) { testDimensionMismatch(t, factory) })
    }
}
```

`RunLexicalConformance`, `RunGraphConformance`, `RunCommunityConformance`
follow the same pattern. They skip the whole suite when the store
doesn't implement the optional capability (`t.Skip("store does not
implement store.LexicalSearcher")`).

## Test Helpers (`t.Helper()`, `t.Cleanup()`)

`t.Helper()` is called in 9 files:

- `internal/apisnapshot/apisnapshot_test.go` — `moduleRoot(t)`
- `postgres/postgres_test.go` and `postgres/postgres_conformance_test.go` —
  `openTestPool(t, ctx, table)`
- `rag/global_test.go`, `rag/drift_test.go`, `rag/tokens_test.go` —
  per-file `newGlobalTestStore`, `newDriftTestSystem`, `seedTokenSystem`
  factories
- `eval/global_test.go`, `eval/drift_test.go` — eval-side factories
- `retrieve/graph_test.go` — graph retriever factory
- `store/storetest/storetest.go` — the conformance dispatcher

`t.Cleanup()` is used in `postgres/postgres_test.go` to drop the test
table and close the pool after each live test. It is the only place
cleanup is wired through.

`t.Parallel()` is not used anywhere — total `t.Parallel` hits across
all `*_test.go` files is **zero**. Tests are sequential by choice; the
shared schema in the live-postgres path makes parallel runs unsafe
without a per-test table name discipline, and the in-memory tests are
fast enough not to need parallelism.

## Fakes / Stubs — Inline, Per-Package

There is **no central fakes package**. Every test file that needs a
seam implementation declares one locally. The naming alternates between
`fakeX` (zero behaviour) and `scriptedX` (deterministic, programmable)
and `stubX` (minimal placeholder):

```
adapter/llmagent/...           — (none, build-tagged adapter tests)
advanced/llm_test.go:12        — type scriptedModel struct { … }
agentic/correct_test.go:144    — type scriptedModel struct{ text string }
eval/eval_test.go:14           — type fakeModel struct{}
eval/judge_test.go:13          — type scriptedModel struct { text string }
feedback/feedback_test.go:21   — type fakeModel struct{}
graph/extract_test.go:12       — type scriptedModel struct { text, err }
graph/resolve_test.go:15       — type scriptedEmbedder struct { dim, err }
rag/system_test.go:15          — type fakeModel struct{}
retrieve/retrieve_test.go:28   — type scriptedModel struct { resps, err, call }
retrieve/retrieve_test.go:95   — type stubEmbedder struct{}
rag/global_test.go             — globalScriptedModel, countingSummarizer, staticSummarizer
```

**Convention:** `fakeModel` returns the prompt verbatim (or a constant);
`scriptedModel` either pre-loads a list of canned responses to walk
through call-by-call, or carries an error to inject. `stubEmbedder`
returns a fixed vector. The same shape repeats across packages —
nothing is shared, every test file is self-contained, the duplication
is intentional (no test-helper package, no `_testdata.go` import).

```go
// retrieve/retrieve_test.go — the canonical scriptedModel shape
type scriptedModel struct {
    resps []string
    err   error
    call  int
}

func (m *scriptedModel) Generate(_ context.Context, _ generate.Request) (generate.Response, error) {
    if m.err != nil {
        return generate.Response{}, m.err
    }
    if len(m.resps) == 0 {
        return generate.Response{}, nil
    }
    idx := m.call
    if idx >= len(m.resps) {
        idx = len(m.resps) - 1
    }
    m.call++
    return generate.Response{Text: m.resps[idx]}, nil
}
```

**In-memory store fake = the production `store.InMemoryStore`.** The
SDK ships its built-in `*InMemoryStore` (in `store/inmemory.go`, 178
lines) which also implements `store.LexicalSearcher`, `store.GraphStore`,
and `store.CommunityStore`. There is no separate in-memory test double
— the production type is the fake, and every rag/retrieve/graph test
that needs a store uses `store.NewInMemoryStore(dim)`.

## Shared Conformance Suite — `store/storetest`

The single piece of shared test infrastructure in the module:

- `store/storetest/storetest.go` is its own importable package, dual-licensed
  to run from inside `store/` and from external backend repos.
- Four entry points, each a dispatcher over a `Factory func(t
  *testing.T) store.Store`: `RunConformance`, `RunLexicalConformance`,
  `RunGraphConformance`, `RunCommunityConformance`.
- The factory is invoked **once per subtest** so each subtest sees a
  fresh isolated store.
- Backends opt in to the dimension-strict subtest with
  `storetest.WithDimensionStrict()` — the in-memory store opts in
  (`store/inmemory_conformance_test.go`); the postgres store also opts
  in (`postgres/postgres_conformance_test.go`).
- Optional-capability suites self-skip with `t.Skip(...)` when the
  factory's store does not implement that capability interface.

In-memory wiring (`store/inmemory_conformance_test.go`, 15 lines):

```go
func TestInMemoryStoreConformance(t *testing.T) {
    storetest.RunConformance(t, func(t *testing.T) store.Store {
        return store.NewInMemoryStore(2)
    }, storetest.WithDimensionStrict())
}
```

`store/community_test.go` runs `RunCommunityConformance` against the
in-memory store and adds one drive-it-end-to-end test
(`TestCommunityDetectedHierarchyRoundTrip`) that uses a real
`LouvainDetector`. `store/graph_test.go` does the analogue for graphs.

## Integration Tests — Live PostgreSQL

`postgres/` is the only package whose tests need a real backend.

**Gate:** the `LLM_AGENT_RAG_PG_URL` environment variable.

```go
// postgres/postgres_test.go
const liveEnvVar = "LLM_AGENT_RAG_PG_URL"

func openTestPool(t *testing.T, ctx context.Context, table string) *pgxpool.Pool {
    t.Helper()
    dsn := os.Getenv(liveEnvVar)
    if dsn == "" {
        t.Skipf("set %s to run this test (e.g. postgres://localhost/llm_agent_rag_test?sslmode=disable)", liveEnvVar)
    }
    cfg, err := pgxpool.ParseConfig(dsn)
    // …
    t.Cleanup(func() {
        if _, err := pool.Exec(context.Background(), fmt.Sprintf(`DROP TABLE IF EXISTS %s`, table)); err != nil {
            t.Logf("cleanup drop table: %v", err)
        }
        pool.Close()
    })
    return pool
}
```

- The env var **opt-out is `t.Skipf`**, not `t.Skip` — the skip message
  shows the env-var name and a sample DSN, so a reader can copy-paste a
  command to run the suite locally.
- **No `//go:build integration` build tag** — the tests are *always
  compiled* and skip cleanly when the env var is absent. CI does not
  set it, so they always skip in CI; the v0.5/v0.7/v0.8 GraphRAG release
  notes explicitly say the postgres path "is env-gated; like the v0.5
  `tsvector` path it is not yet exercised against a live database in CI."
- `t.Cleanup` drops the per-test table with `DROP TABLE IF EXISTS`,
  isolating tests that share a database.
- **`testing.Short()` is not used anywhere** — `grep -rn "testing.Short"`
  returns zero hits. The skip switch is the env var, not `-short`.

`postgres/postgres_conformance_test.go` wires the live pool into
`storetest.RunConformance`, so a live postgres run gets the same 12
subtests as the in-memory backend.

## Build Tags

The only build-tagged code in the module is the optional `adapter/llmagent`
bridge. All four files start with `//go:build llmagent`:

- `adapter/llmagent/tool.go`
- `adapter/llmagent/model.go`
- `adapter/llmagent/tool_test.go`
- `adapter/llmagent/model_test.go`

CI runs them with `go test -tags llmagent ./adapter/...` — the only CI
step that pulls in `github.com/costa92/llm-agent`. This is the *only*
build tag in the entire repository.

## Examples as Tests

`examples/` is **test-only** — `find examples -name '*.go' -not -name
'*_test.go'` returns zero, every file is `*_test.go`. There are **5
`Example*` functions**, one per primary answer path:

| File | Function | Output check |
|---|---|---|
| `examples/basic_import_and_ask_test.go` | `Example_basicImportAndAsk` | `// Output:` present |
| `examples/graphrag_example_test.go` | `Example_graphRAG` | `// Output:` present |
| `examples/graphrag_global_example_test.go` | `Example_graphRAGGlobal` | `// Output:` present |
| `examples/graphrag_drift_example_test.go` | `Example_graphRAGDrift` | `// Output:` present |
| `examples/graphrag_path_example_test.go` | `Example_graphRAGPaths` | `// Output:` present |

Every example carries a `// Output:` block, so `go test` verifies the
example output exactly. Per the package doc: "They are test-only and
compile and run as `go test`, doubling as executable documentation."

These also exercise full end-to-end pipelines — `Example_basicImportAndAsk`
runs Import → Retrieve, `Example_graphRAGDrift` constructs a
deterministic `driftExampleModel` that handles all four kinds of DRIFT
generation. They are real integration tests as well as documentation.

## Eval as Test — Both Modes

`eval/` is a **runtime API** that is *also* exercised at test time:

- The package exports `eval.RetrievalEvaluator`, `eval.TriadEvaluator`,
  `eval.GlobalEvaluator`, `eval.DriftEvaluator`, `eval.LLMJudge`,
  `eval.RunGraphAB` (an A/B harness) as production types — production
  consumers build datasets and run them through these.
- Every one of those types has its own test file: `eval/eval_test.go`,
  `eval/triad_test.go`, `eval/judge_test.go`, `eval/global_test.go`,
  `eval/drift_test.go`, `eval/graph_test.go`, `eval/loader_test.go`.
- Tests pair a deterministic `fakeModel` / `scriptedModel` with a tiny
  in-memory dataset and assert the metrics: e.g. `TestRunGraphAB`,
  `TestLLMJudgeParsesCleanJSON`, `TestLLMJudgeExtractsJSONWrappedInProse`,
  `TestLLMJudgeClampsOutOfRangeScores`.
- The package doc on `eval/eval.go` calls eval "the standalone module's
  CI gate: a regression in route policy, fanout, converge, namespace
  isolation, or grounding shows up as a drop in one of the four headline
  metrics" — eval is both the production API and the gate-test path.

## GraphRAG Testing

`graph/` (9 source files, 1494 lines / 8 test files, 1035 lines) has
its own test scaffolding pattern but **no separate fixture package**.
The `scriptedModel` / `scriptedEmbedder` types are declared per-test-file
(`graph/extract_test.go` line 12, `graph/resolve_test.go` line 15).
Tests are golden-style: every detector / extractor / resolver /
summarizer is required to be **deterministic** (per the type's doc
comment), and the tests assert byte-identical output:

```go
// graph/community.go (excerpt)
// Community is one cluster in a detected community hierarchy. … IDs are
// a deterministic function of the level and the cluster's members, so a
// given graph always yields the same hierarchy — community detection is
// reproducible, golden-testable output.
```

`graph/community_test.go` and `rag/community_test.go` walk hierarchies
and assert membership; `graph/path_test.go` asserts deterministic
path-ranking output; `graph/canonicalize_test.go` asserts entity merges.
`rag/global_test.go` and `rag/drift_test.go` exercise the GraphRAG
answer paths end-to-end using the in-memory store + scripted summarizers
(`countingSummarizer`, `staticSummarizer`) declared inline.

`store/community_test.go` includes `TestCommunityDetectedHierarchyRoundTrip`:
it drives a real `LouvainDetector` over a constructed graph and then
round-trips the detected hierarchy through `store.CommunityStore`,
catching any persistence drift. This is the only test that wires the
real detector + the conformance store + the round-trip in one place.

## Coverage

**CI does not collect or gate coverage.** The `.github/workflows/test.yml`
file invokes `go test ./...` without `-cover`, `-coverprofile`, or any
coverage upload step. There is no Codecov / Coveralls badge in the
README. The single mention of "coverage" in `.github/` is a comment
about *CI coverage of the adapter package*, not test coverage.

A developer can produce coverage locally with the standard tools:

```bash
go test -cover ./...
go test -coverprofile=cover.out ./...
go tool cover -html=cover.out
```

— but no gate enforces a coverage floor.

## Benchmarks

**Zero `Benchmark*` functions in the module.** Verified by
`grep -rn "^func Benchmark" --include="*.go"` — no hits. There are no
`testing.B`-driven benchmarks, no `-benchmem` runs, no committed
benchmark baselines. Performance is monitored indirectly through the
`obs.Metrics` runtime accounting on every `Ask` / `AskGlobal` / `AskDrift`
call, not via the Go benchmark framework.

## CI Gates — The Full Set

`test.yml` runs, in order:

1. **Module-boundary check** — fail if any non-`adapter/` file imports
   `github.com/costa92/llm-agent`.
2. **`go vet ./...`**
3. **`go build ./...`**
4. **`go test ./...`** — this catches every test below, including:
   - The `internal/apisnapshot` snapshot gate (additive-only enforcement
     for v1.x)
   - The `contract` cross-repo compile pin
   - The `storetest` conformance suite against `InMemoryStore`
   - All eval framework tests (which double as quality gates)
   - All 5 `Example*` `// Output:` checks
5. **`go test ./internal/apisnapshot/...`** — explicit re-run for log
   visibility (redundant with step 4).
6. **`go build -tags llmagent ./...`** and **`go test -tags llmagent
   ./adapter/...`** — the only `llm-agent` integration path.

`release-precheck.yml` runs only on `release/**` branches and rejects
any `replace` directive in `go.mod` before a tag.

## What Is *Not* Tested — Concern Areas

Packages where the test:source line ratio is well below 1:1:

| Package | Source lines | Test lines | Ratio | Note |
|---|---|---|---|---|
| `embed/` | 126 | 24 | 0.19 | `HashEmbedder` only; `Vector` and `CosineSimilarity` exercised transitively |
| `generate/` | 43 | 0 | 0.00 | No tests at all — pure interfaces + value types |
| `tree/` | 226 | 57 | 0.25 | Two tests cover `Build` and `Find`; node mutation paths under-tested |
| `prompt/` | 77 | 35 | 0.45 | One test file; `DefaultQATemplate` only |
| `pack/` | 205 | 60 | 0.29 | `GreedyTokenPacker` happy path; token-budget edge cases thinly covered |
| `store/` | 1526 | 504 | 0.33 | Most coverage lives inside `store/storetest/` conformance |
| `ingest/` | 472 | 178 | 0.38 | `MarkdownSplitter` and `CharSplitter`; source-lineage fields lightly tested |
| `postgres/` | 950 | 295 | 0.31 | Tests skip in CI without `LLM_AGENT_RAG_PG_URL`; the actual `tsvector`/graph paths are not exercised against a live DB in CI per CHANGELOG |
| `obs/` | 95 | 40 | 0.42 | `Counter`/`WithCounter` round-trip only |

Packages where test:source ratio is healthy (≥ 1:1 or close): `rag/`
(1.19), `eval/` (1.11), `agentic/` (0.95), `feedback/` (2.15 — feedback
has heavy serialization tests), `contract/` (test-only),
`examples/` (test-only), `adapter/llmagent/` (0.32 but build-tagged and
exercised in its own CI job).

**The postgres concern is the dominant one.** The package has a
working live-DB suite, but the env-var gate means CI never runs it.
v0.5/v0.7/v0.8 release notes explicitly acknowledge this: "the
`postgres` graph path is env-gated; like the v0.5 `tsvector` path it is
not yet exercised against a live database in CI." A future phase that
wires a postgres service into CI would close this gap without code
change — the conformance suite is already in place.

---

*Testing analysis: 2026-05-20*
