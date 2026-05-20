# Coding Conventions

**Analysis Date:** 2026-05-20

## Public API Discipline — v1.0.0 Freeze

This SDK is on **v1.0.0** since 2026-05-21 and explicitly governs itself by
the additive-only Go module import-compatibility rule. The policy is
written in `docs/compatibility.md` and is enforced by code.

**The additive-only claim — verified against CHANGELOG.md:**

- CHANGELOG.md uses a strict Keep-a-Changelog/Semver layout with explicit
  `### Added` / `### Changed` / `### Fixed` / `### Notes` sections per
  release.
- The v1.0.0 entry calls out the freeze in the first paragraph and
  declares "**Not a feature release** — no new features, no behavior
  change, and no new dependency."
- The v1.0.0 `### Changed` section contains the **final** breaking
  renames the API will ever take in v1.x — explicitly: `eval.Evaluator →
  eval.RetrievalEvaluator`, `eval.Result → eval.RetrievalResult`, and a
  `doc.go` rewrite that exports nothing. After that moment the rule is
  additive-only.
- v0.1.0 → v0.6.0 are pre-freeze and each carry their own `### Changed`
  blocks (BM25 replacing token-overlap in v0.6, query-expansion
  orchestration moves in v0.1.4, etc.). All renames happened before the
  freeze. Post-freeze releases will only carry `### Added`.

**Deprecation markers:**

- `grep -rn "// Deprecated:" --include="*.go"` returns **zero hits**.
- No symbol in the module is marked deprecated. The v1.0 freeze means
  deprecation is now reserved for the (future) v2 cutover, not for soft
  removal inside v1.x — v1.x cannot remove anything, deprecated or not.

**Unexported→exported renames (recent history):**

- `git log --oneline` shows exactly one post-freeze-relevant commit:
  `a76896d feat: v1.0 API stabilization and the compatibility promise
  (phases 28-30)`. That commit captures the renames already documented
  in CHANGELOG v1.0.0.
- Prior commits (`5d16007`, `fd58ef0`, `12d303f`, `798bf3f`) are the
  pre-freeze GraphRAG milestones — each landed as an additive package
  growth (`graph/`, `eval.GlobalEvaluator`, `eval.DriftEvaluator`,
  `retrieve.GraphRetriever`, `rag.System.AskGlobal`, `rag.System.AskDrift`)
  rather than a rename.
- The v1.0 freeze is gated by **`internal/apisnapshot`** (see below); no
  unintended rename can land without that gate firing.

## Module Structure

**Single root module — no `/v2` yet:**

- One `go.mod` at the repo root declaring `module
  github.com/costa92/llm-agent-rag`, `go 1.26.0`.
- No `v2/` directory anywhere in the tree. A `/v2` would only appear
  when the project takes a breaking change, per `docs/compatibility.md`.
- 26 top-level directories; every importable sub-package lives directly
  under the root (`rag/`, `retrieve/`, `store/`, `embed/`, `ingest/`,
  `generate/`, `eval/`, `graph/`, `pack/`, `prompt/`, `rerank/`, `obs/`,
  `guard/`, `feedback/`, `agentic/`, `advanced/`, `postgres/`, `tree/`,
  `adapter/llmagent/`).

## Internal vs Exported

**Only one `internal/` package:**

- `internal/apisnapshot/` — the v1.0 exported-API-surface snapshot gate.
  Two files:
  - `apisnapshot.go` (343 lines) — deterministic generator that walks
    every importable package via `go/parser` + `go/ast`, emits the
    exported surface in a stable text format.
  - `apisnapshot_test.go` (118 lines) — runs the generator and diffs
    against the committed baseline `api/v1.snapshot.txt` (882 lines).
- `api/v1.snapshot.txt` — the committed baseline. Any rename, removal,
  or signature change to an exported symbol fails the `TestAPISnapshot`
  test inside an ordinary `go test ./...` run. The CI workflow
  (`.github/workflows/test.yml`) re-runs it explicitly for visibility.
- Regenerating after a deliberate additive change is one command:
  `go test ./internal/apisnapshot/ -run TestAPISnapshot -update`.

**The `internal/` package is intentionally narrow** — it holds the API
gate and nothing else. Production logic stays in exported packages so
sister repos can compose with it.

## Module Boundary

**`adapter/llmagent` is the *only* package allowed to import core llm-agent**

- All `adapter/llmagent/*.go` files start with `//go:build llmagent`
  (`tool.go`, `tool_test.go`, `model.go`, `model_test.go`) — they
  compile only when the `llmagent` build tag is set.
- The `test.yml` CI workflow has an explicit `Verify core packages do
  not import llm-agent` step using `rg -n
  'github.com/costa92/llm-agent([/"[:space:]]|$)'` excluding `adapter/`,
  `.github/`, `README.md`, `CHANGELOG.md`, `go.mod`. A hit fails CI.
- `release-precheck.yml` rejects any `replace` directive in `go.mod`
  before tagging a `release/**` branch.

## Naming Conventions

**Package names:** lowercase, single word, descriptive — `rag`, `retrieve`,
`store`, `embed`, `ingest`, `generate`, `eval`, `graph`, `pack`, `prompt`,
`rerank`, `obs`, `guard`, `feedback`, `agentic`, `advanced`, `postgres`,
`tree`. The root brand-package is `ragkit` (per `doc.go`) — a deliberate
divergence from the module path, documented as an SDK identity choice.

**Interfaces — the `-er` suffix dominates** (33 exported interfaces,
all named after a single verb-noun + `-er`):

- Core seams: `embed.Embedder`, `generate.Model`, `store.Store`,
  `ingest.Source`, `ingest.StreamingSource`, `ingest.Splitter`,
  `prompt.Template`, `retrieve.Retriever`, `rerank.Reranker`,
  `pack.Packer`, `pack.TokenCounter`.
- Optional capability interfaces (the v1.0 alternative to growing core
  interfaces): `store.LexicalSearcher`, `store.GraphStore`,
  `store.CommunityStore`.
- Query-shaping seams: `retrieve.QueryPreprocessor`,
  `retrieve.QueryDecomposer`, `retrieve.QueryEmbedder`,
  `retrieve.EntityLinker`, `retrieve.SectionPlanner`.
- Graph seams: `graph.EntityExtractor`, `graph.EntityResolver`,
  `graph.CommunityDetector`, `graph.CommunitySummarizer`,
  `graph.PathRanker`.
- Guard: `guard.Redactor`, `guard.InjectionScanner`.
- Eval: `eval.Judge`, `eval.Asker`, `eval.GlobalAsker`,
  `eval.DriftAsker`, `eval.Retriever`.
- Agentic: `agentic.QueryReformulator`.
- Rerank: `rerank.ScoringModel`.

No `-able` suffix interfaces anywhere in the tree. Verb-doer convention
is consistent.

**Concrete types — built-ins are prefixed by their behaviour:**

- Defaults / no-ops: `graph.NoopEntityResolver`, `rerank.NoopReranker`,
  `retrieve.NoopPreprocessor`, `prompt.DefaultQATemplate`,
  `pack.GreedyTokenPacker`.
- Strategy names: `ingest.CharSplitter`, `ingest.MarkdownSplitter`,
  `embed.HashEmbedder`, `graph.LouvainDetector`,
  `graph.LabelPropagationDetector`, `graph.LLMEntityExtractor`,
  `graph.DictionaryEntityExtractor`, `graph.LLMCommunitySummarizer`,
  `graph.EmbeddingEntityResolver`, `graph.WeightedPathRanker`,
  `rerank.ModelReranker`, `rerank.HeuristicReranker`,
  `rerank.HTTPScoringModel`, `retrieve.LLMExpansionPreprocessor`,
  `retrieve.LexicalEntityLinker`, `retrieve.GapAwareSectionPlanner`,
  `retrieve.HybridRetriever`, `retrieve.GraphRetriever`,
  `retrieve.MultiHopRetriever`, `retrieve.VariantRetriever`,
  `retrieve.DenseRetriever`, `retrieve.LexicalRetriever`,
  `retrieve.StructureRetriever`, `eval.LLMJudge`,
  `eval.TriadEvaluator`, `eval.GlobalEvaluator`, `eval.DriftEvaluator`,
  `eval.RetrievalEvaluator`, `agentic.CorrectiveAsker`,
  `agentic.LLMReformulator`.

**The implementation name announces both the algorithm/backend and the
interface it satisfies.** This is a hard convention — there are no
exceptions in the v1 snapshot.

**Files:** lowercase, often single-word (`system.go`, `import.go`,
`options.go`, `ask.go`, `drift.go`, `global.go`, `errors.go`), or
snake-with-no-dashes (`inmemory.go`, `httpmodel.go`,
`markdown_splitter_test.go`, `inmemory_conformance_test.go`). Test files
follow the standard Go `*_test.go` rule.

**Doc-comment headers always start with the symbol name** — `// Answer
is the result of…`, `// System is the top-level RAG pipeline…`, `//
Citation links an answer back to…` — even on struct fields, where each
field comment is a complete sentence starting with the field name:

```go
type Answer struct {
    Text        string           // Text is the generated answer.
    Hits        []store.Hit      // Hits are the retrieved chunks the answer was generated from.
    Prompt      generate.Request // Prompt is the generation request sent to the model.
    Citations   []Citation       // Citations link the answer back to its source chunks.
    Diagnostics Diagnostics      // Diagnostics is the per-run diagnostic detail.
    Trace       Trace            // Trace is the per-run trace passed to an Observer.
}
```

## Doc-Comment Coverage

Every importable package opens with a `// Package <name>` comment.
Confirmed by `grep -rn "^// Package " --include="*.go"` — 22 hits, one
per importable package (`doc.go` for `ragkit`, plus 21 sub-packages
including the build-tagged `adapter/llmagent`). The v1.0.0 CHANGELOG
explicitly calls out "complete package- and exported-symbol-level
doc-comment coverage across the module" as a freeze-time deliverable.

Sample exported types — every one documented, every field documented:

- `rag.Answer`, `rag.Citation`, `rag.Diagnostics`, `rag.Trace`,
  `rag.System`, `rag.Options`, `rag.SearchOptions`, `rag.AskOptions`,
  `rag.GlobalOptions`, `rag.DriftOptions` (`rag/system.go`,
  `rag/options.go`) — every field carries an inline doc comment.
- `store.Store`, `store.LexicalSearcher`, `store.GraphStore`,
  `store.CommunityStore`, `store.Query`, `store.Filter`,
  `store.ErrNotFound`, `store.ErrDimensionMismatch`
  (`store/store.go`) — every method has a comment.
- `embed.Embedder`, `embed.Vector`, `embed.HashEmbedder`
  (`embed/embedder.go`, `embed/hash.go`, `embed/vector.go`).
- `graph.Entity`, `graph.Relation`, `graph.Community`,
  `graph.EntityExtractor`, `graph.CommunityDetector`
  (`graph/graph.go`, `graph/community.go`).
- `generate.Model`, `generate.Request`, `generate.Response`,
  `generate.Usage` (`generate/model.go`, `generate/types.go`).
- `obs.Metrics`, `obs.StageTiming`, `obs.CallCounts`, `obs.TokenUsage`
  (`obs/obs.go`).
- `ingest.Document`, `ingest.Chunk`, `ingest.ImportOptions`,
  `ingest.Source`, `ingest.StreamingSource`, `ingest.Splitter`
  (`ingest/import.go`, `ingest/source.go`, `ingest/splitter.go`,
  `ingest/types.go`).

## Error Handling Style

Three patterns, applied consistently:

**1. Sentinel errors — `errors.New` with a package prefix.** Used for
configuration / precondition errors that callers want to test with
`errors.Is`.

```go
// rag/errors.go
var ErrEmptyQuery = errors.New("rag: query is required")
var ErrModelRequired = errors.New("rag: generator required for this operation")
var ErrImporterRequired = errors.New("rag: importer required for this operation")
var ErrRetrieverRequired = errors.New("rag: retriever required for this operation")
var ErrSourceRequired = errors.New("rag: import source is required")
var ErrCommunitySummarizerRequired = errors.New("rag: community summarizer required for global search")

// store/store.go
var ErrNotFound = errors.New("store: chunk not found")
var ErrDimensionMismatch = errors.New("store: vector dimension mismatch")

// graph/extract.go
var ErrEntityExtractorModelRequired = errors.New("graph: entity extractor requires a generate.Model")

// graph/resolve.go
var ErrEntityResolverEmbedderRequired = errors.New("graph: embedding entity resolver requires an embed.Embedder")

// graph/summary.go
var ErrCommunitySummarizerModelRequired = errors.New("graph: community summarizer requires a generate.Model")

// eval/judge.go
var ErrJudgeModelRequired = errors.New("eval: judge model required")

// advanced/llm.go
var ErrModelRequired = errors.New("advanced: generator required")

// rerank/rerank.go
var ErrScoringModelRequired = errors.New("rerank: scoring model required")

// agentic/correct.go
var ErrAskerRequired, ErrJudgeRequired, ErrReformulatorRequired
```

**Pattern:** every sentinel is named `Err<Subject><Condition>` and every
message starts with `pkgname:`. Tests verify them with `errors.Is`.

**2. Wrapped errors — `fmt.Errorf("...: %w", err)`.** Used for
contextual wrapping at every level above the leaf. Every wrap also
prepends a package prefix.

```go
// eval/triad.go
return TriadResult{}, fmt.Errorf("eval: ask %q: %w", ex.Query, err)
return TriadResult{}, fmt.Errorf("eval: judge %q: %w", ex.Query, err)
return fmt.Errorf("eval: write triad jsonl: %w", err)

// rerank/httpmodel.go
return nil, fmt.Errorf("rerank: marshal request: %w", err)
return nil, fmt.Errorf("rerank: build request: %w", err)
return nil, fmt.Errorf("rerank: decode response: %w", err)

// rag/import.go
return ingest.ImportResult{}, fmt.Errorf("rag: list existing source %s: %w", doc.SourceID, err)

// eval/loader.go
return Dataset{}, fmt.Errorf("eval: open %s: %w", path, err)
```

**No typed (struct) errors anywhere in the exported surface.** No
`type FooError struct{ … }` — the project sticks to sentinels + `%w`
wraps.

**3. `errors.Is` / `errors.As` for inspection.** Tests and callers both
use `errors.Is` to match sentinels; only `postgres/postgres.go` calls
`errors.As` (to peel a `*pgconn.PgError` for constraint inspection).

## Context Propagation

**`ctx context.Context` is always the first parameter** on any function
that needs one. Verified across 156 occurrences of `context.Context` in
non-test code — every signature follows the standard Go convention.

When a method satisfies an interface but does not use the context, it
uses the blank identifier `_ context.Context` rather than naming the
parameter `ctx`:

```go
func (h *HashEmbedder) Embed(_ context.Context, text string) (Vector, error)
func (DictionaryEntityExtractor) Extract(_ context.Context, chunkID, text string) ([]Entity, []Relation, error)
func (NoopEntityResolver) Resolve(_ context.Context, entities []Entity, relations []Relation) ([]Entity, []Relation, error)
func (HeuristicReranker) Rerank(_ context.Context, req Request) ([]store.Hit, Trace, error)
func (s *InMemoryStore) Upsert(_ context.Context, chunks []StoredChunk) error
```

This makes "this implementation is offline / does not call out to a
backend" a one-symbol visual cue — a deliberate signalling convention.

## Construction / Options Pattern

**`Options` struct + `New(opts)` is the dominant pattern.** No
functional-options (`WithFoo(...)`) on user-facing constructors:

```go
// rag/system.go
func New(opts Options) *System {
    emb := opts.Embedder
    if emb == nil {
        emb = embed.NewHashEmbedder(32)
    }
    // …every dependency gets the nil-→-default treatment
}
```

`rag.Options` has 18 fields, each one optional with a documented default
applied inside `New`. The struct itself is a public, additive surface —
new dependencies enter as new fields, never as a new constructor.

**Constructors are `New<Type>(args...)` returning `*<Type>` or `<Type>`:**

- `rag.New(opts) *System`
- `embed.NewHashEmbedder(dim) *HashEmbedder`
- `store.NewInMemoryStore(dim) *InMemoryStore`
- `ingest.NewImporter(src, splitter) *Importer`
- `ingest.NewCharSplitter(maxChars, overlap) CharSplitter`
- `ingest.NewMarkdownSplitter(maxChars, overlap) MarkdownSplitter`
- `postgres.New(pool, cfg) (*Store, error)`
- `feedback.NewRecorder(out) *Recorder`
- `guard.NewPIIRedactor() PIIRedactor`
- `guard.NewPatternScanner() PatternScanner`
- `obs.NewCounter() *Counter`

The *only* `With…` exports in the whole module are:

- `storetest.WithDimensionStrict() Option` — a real functional option,
  used by the conformance suite to opt a backend into the
  dimension-mismatch subtest.
- `obs.WithCounter(ctx, c) context.Context` — a `context.WithValue`
  helper that attaches a counter to a context.

No builder pattern anywhere.

## Logging Style

**The library does not log.** Verified by `grep -rn "log\." --include
="*.go"` and `grep -rn "log/slog\|slog\." --include="*.go"` — **zero
hits** in source files. There is no `log` import in the production
tree.

Observation flows through three mechanisms instead:

1. **`obs.Metrics`** — per-stage timings, embed/generate call counts,
   token usage. Embedded into `rag.Diagnostics`, `retrieve.Trace`,
   `ingest.ImportResult`, `rag.ImportTrace`. The package doc states:
   "It is a leaf package — it imports only the standard library — so any
   package (rag, retrieve, ingest) can embed obs.Metrics without an
   import cycle."
2. **`rag.Observer`** — `OnImport`, `OnRetrieve`, `OnAsk` callbacks the
   caller wires in. The README explicitly mentions OTel adaptation as
   the intended consumer (downstream `llm-agent-otel`).
3. **Diagnostics on the return value** — every `Ask`, `AskGlobal`,
   `AskDrift` call returns the trace as part of `Answer.Diagnostics` /
   `Answer.Trace`, so a caller can record outcomes without injecting a
   logger.

This is a deliberate library-vs-application choice: the SDK emits
structured data, the application decides whether/how to log.

## License + Headers

- **No `LICENSE*` file** exists in the repository root.
- **No per-file header** — no copyright banner, no SPDX identifier, no
  authorship line. Files open directly with the `// Package …` doc
  comment (or the `//go:build llmagent` tag on adapter files).

## Imports

**Standard 3-group ordering with blank lines:**

```go
// rag/system.go
import (
    "context"

    "github.com/costa92/llm-agent-rag/embed"
    "github.com/costa92/llm-agent-rag/generate"
    "github.com/costa92/llm-agent-rag/graph"
    // …
)

// postgres/postgres.go
import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "strings"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgconn"
    "github.com/jackc/pgx/v5/pgxpool"
    pgvector "github.com/pgvector/pgvector-go"
    pgvector_pgx "github.com/pgvector/pgvector-go/pgx"

    "github.com/costa92/llm-agent-rag/embed"
    "github.com/costa92/llm-agent-rag/store"
)
```

Group 1 stdlib → blank line → Group 2 third-party (if any) → blank line
→ Group 3 in-module imports. Aliasing is used only where the package
basename collides (`pgvector_pgx` to disambiguate from the type alias).

**No path aliases** (no `module/internal/x`-style aliases — the project
has no separate alias config; `internal/apisnapshot` is reached by its
real path).

## Compile-Time Interface Checks

Implementations advertise their contracts with `var _ Iface =
(*Impl)(nil)` lines at the top of the file:

```go
// postgres/postgres.go
var (
    _ store.Store           = (*Store)(nil)
    _ store.LexicalSearcher = (*Store)(nil)
)
```

This makes `go build` the contract gate — an unintended drift between
implementation and seam is a compile failure, not a runtime surprise.
Used in `postgres/`, in `contract/contract_test.go` (the cross-repo
gate), and in several built-in implementations.

## Cross-Repo Contract Surface

`contract/contract_test.go` (109 lines, build tag-free) is a
**compile-only** test that imports `embed`, `generate`, `ingest`,
`prompt`, `rag`, `retrieve`, `store` and pins every symbol the core
`github.com/costa92/llm-agent` integrations consume:

```go
var (
    _ embed.Vector
    _ embed.Embedder = (*embed.HashEmbedder)(nil)
    _                = embed.NewHashEmbedder
    _                = embed.CosineSimilarity
)
```

It has no runtime assertions. Removing or renaming any pinned symbol
breaks `go build` on this file, which means `go test ./...` fails. The
package comment explicitly says: "Adding to this file is a deliberate
act … Removing from this file is a breaking change for those
integrations and requires a coordinated PR in `github.com/costa92/
llm-agent` first."

This is a narrower gate than `internal/apisnapshot`: snapshot covers the
whole intra-repo surface, contract pins the subset consumed across the
two-repo boundary.

## Formatting / Style Tooling

- **`gofmt`-clean** — the v1.0.0 CHANGELOG declares "the repository is
  now `gofmt`-clean" as a freeze-time deliverable.
- **`go vet ./...`** runs in CI (`.github/workflows/test.yml`).
- **No `golangci-lint`, no `staticcheck`, no `revive` config files** — the
  project relies on `gofmt` + `go vet` only.
- **No `.editorconfig`, no `.gitattributes`** controlling style.
- **No `go:generate` directives** anywhere — the API snapshot baseline
  is regenerated through a flagged test (`-update`), not through `go
  generate`.

## File Sizes (Convention Signal)

Source files lean small:

- Largest source files are in `rag/`: `drift.go` (507), `global.go` (491),
  `ask.go` (299), `import.go` (273), `system.go` (262).
- Most package leaf files sit under 250 lines (`embed/embedder.go` is
  19, `generate/model.go` is 15, `generate/types.go` is 30,
  `guard/redact.go` ≈ 90).
- Seam interfaces and their types live in a file named after the package
  (`store/store.go`, `embed/embedder.go`, `generate/model.go`,
  `graph/graph.go`, `retrieve/retrieve.go`); concrete implementations
  go in their own file (`store/inmemory.go`, `embed/hash.go`,
  `graph/louvain.go`, `retrieve/graph.go`).

---

*Convention analysis: 2026-05-20*
