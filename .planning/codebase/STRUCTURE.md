# Codebase Structure

**Analysis Date:** 2026-05-20

## Directory Layout

```
llm-agent-rag/                 # Module root: github.com/costa92/llm-agent-rag
├── doc.go                     # Root package doc (package `ragkit`, no exports)
├── README.md                  # Top-level docs, quick start, status
├── CHANGELOG.md               # Per-version change log
├── go.mod                     # Module manifest (Go 1.26.0); 3 non-stdlib requires
├── go.sum                     # Dependency checksums
│
├── adapter/                   # Build-tagged cross-repo bridges
│   └── llmagent/              # Adapter to github.com/costa92/llm-agent (tag `llmagent`)
├── advanced/                  # Stateless LLM helpers: ExpandQuery (MQE), GenerateHypothetical (HyDE)
├── agentic/                   # CorrectiveAsker: self-correcting ask + retry loop
├── api/                       # Public-API artifacts
│   └── v1.snapshot.txt        # Frozen v1 exported-API snapshot (the v1 contract gate baseline)
├── contract/                  # Compile-time cross-repo pin (consumed by llm-agent)
├── docs/                      # Long-form documentation (compatibility, GraphRAG, postgres, etc.)
├── embed/                     # Embedding seam + deterministic default HashEmbedder
├── eval/                      # Evaluation framework (retrieval / triad / global / drift / graph A/B)
├── examples/                  # Test-only worked examples (one per primary answer path)
├── feedback/                  # JSONL miss-capture recorder for production→eval round-trip
├── generate/                  # LLM generation seam (caller plugs in their model)
├── graph/                     # Knowledge-graph types, extractors, Louvain, path ranking, summaries
├── guard/                     # PII redaction + prompt-injection screening
├── ingest/                    # Document/Source/Splitter types + char + markdown splitters
├── internal/                  # Non-importable internals (only `apisnapshot` lives here)
│   └── apisnapshot/           # Stdlib AST walker that regenerates api/v1.snapshot.txt
├── obs/                       # Cost/latency metrics + context-propagated atomic Counter
├── pack/                      # Token-budget-aware context packing
├── postgres/                  # PostgreSQL + pgvector backend (opt-in deps)
├── prompt/                    # Prompt-template seam + DefaultQATemplate
├── rag/                       # Orchestration layer: System, Ask, Import, Observer, GraphRAG paths
├── rerank/                    # Re-rank seam + heuristic / model / noop rerankers + HTTP scoring model
├── retrieve/                  # Retrieval seam + Dense/Lexical/Hybrid/Structure/Graph/MultiHop/Variant
├── store/                     # Store interface + 3 optional capabilities + InMemoryStore
│   └── storetest/             # Shared conformance suite every backend runs against
└── tree/                      # DocumentTree (section/heading hierarchy from chunks)
```

Counts: **26 top-level directories**, **125 Go files**, **66 non-test Go files**.

## Directory Purposes

### `adapter/`

- Purpose: Cross-repo bridges that intentionally do not ship in the default build.
- Contains: One subdirectory (`llmagent/`) with build-tagged Go files.
- Key files: `adapter/llmagent/model.go`, `adapter/llmagent/tool.go`.

### `adapter/llmagent/`

- Purpose: Adapter to the core `github.com/costa92/llm-agent` agent framework. Built only under `//go:build llmagent`.
- Contains: A `ModelAdapter` that satisfies `generate.Model` by wrapping `corellm.ChatModel`; `AsTool(*rag.System) agents.Tool` that exposes the System as an LLM tool with `add_text`/`search`/`ask`/`remove`/`stats` actions.
- Key files: `adapter/llmagent/model.go`, `adapter/llmagent/tool.go`.

### `advanced/`

- Purpose: Stateless LLM-backed query rewriting helpers. Not pipeline stages — pure functions.
- Contains: `ExpandQuery` (MQE — N semantically-equivalent rewrites), `GenerateHypothetical` (HyDE — invented short answer for embedding recall).
- Key files: `advanced/llm.go`, `advanced/errors.go`.

### `agentic/`

- Purpose: One agentic pattern on top of the standard `Ask`.
- Contains: `CorrectiveAsker` self-correcting loop; `QueryReformulator` seam + `LLMReformulator` default.
- Key files: `agentic/correct.go`.

### `api/`

- Purpose: Public-API artifacts kept under version control.
- Contains: `v1.snapshot.txt` — the frozen v1 exported-API surface, regenerated and diffed by `internal/apisnapshot/apisnapshot_test.go`.
- Generated: Yes — by `internal/apisnapshot`.
- Committed: Yes — it's the v1 contract gate baseline.

### `contract/`

- Purpose: Compile-time cross-repo pin of the symbols `github.com/costa92/llm-agent` consumes.
- Contains: Only `contract_test.go`. Compilation success is the gate — no runtime assertions.
- Key files: `contract/contract_test.go`.

### `docs/`

- Purpose: Long-form documentation.
- Contains: `api-audit-v1.0.md` (audit notes), `backend-selection.md` (in-memory vs postgres), `compatibility.md` (the v1 promise), `core-compatibility.md` (two-repo split), `graphrag.md` (tiers/usage), `production-deployment.md` (pgvector + pool config).
- Committed: Yes.

### `embed/`

- Purpose: Embedding-backend seam + default deterministic embedder.
- Contains: `Embedder` interface, `Vector` type, `CosineSimilarity` helper, `HashEmbedder` (FNV bag-of-tokens default).
- Key files: `embed/embedder.go`, `embed/vector.go`, `embed/hash.go`.

### `eval/`

- Purpose: Standalone module's CI gate for retrieval and generation quality.
- Contains: Four evaluators (`RetrievalEvaluator`, `TriadEvaluator`, `GlobalEvaluator`, `DriftEvaluator`), `LLMJudge`, `LoadJSONL`/`WriteJSONL`, `RunGraphAB`.
- Key files: `eval/eval.go`, `eval/triad.go`, `eval/global.go`, `eval/drift.go`, `eval/graph.go`, `eval/judge.go`, `eval/loader.go`.

### `examples/`

- Purpose: Test-only worked examples that compile and run as `go test`, doubling as executable docs. One example per primary answer path.
- Contains: `Example_basicImportAndAsk` and four GraphRAG examples (basic, global, path, drift).
- Key files: see "Where examples live" below.

### `feedback/`

- Purpose: Closes the loop between production retrievals and the eval framework.
- Contains: `Recorder` (JSONL append, mutex-protected, line-atomic); `BuildExample(trace, gold, notes)` that maps a `rag.Trace` to an `eval.Example`.
- Key files: `feedback/feedback.go`.

### `generate/`

- Purpose: Text-generation seam — the LLM plug-point.
- Contains: `Model` interface, `Request`/`Response`/`Message`/`Usage` value types. No default implementation — caller supplies the model.
- Key files: `generate/model.go`, `generate/types.go`.

### `graph/`

- Purpose: Knowledge-graph primitives and algorithms for GraphRAG.
- Contains: `Entity`/`Relation`/`Graph`/`Subgraph`/`Community`/`CommunityReport`/`RankedPath` value types; `Canonicalize` (exact-merge); `LouvainDetector`, `LabelPropagationDetector` (community detection); `LLMEntityExtractor`, `DictionaryEntityExtractor`; `EmbeddingEntityResolver`, `NoopEntityResolver`; `LLMCommunitySummarizer`; `WeightedPathRanker`. Imports only stdlib + `generate` + `embed`.
- Key files: see per-file breakdown below.

### `guard/`

- Purpose: Content-safety layer — PII redaction at ingest, prompt-injection screening at retrieve. Stdlib-only leaf.
- Contains: `PIIRedactor` + `Rule`; `PatternScanner` + `InjectionPattern`; `NeutralizeText`; `SanitizeMode` (`Drop`/`Neutralize`).
- Key files: `guard/redact.go`, `guard/inject.go`.

### `ingest/`

- Purpose: Document ingestion pipeline primitives.
- Contains: `Document`/`Chunk`/`ImportResult` types; `Source`/`StreamingSource` seams; `SourceFunc`/`StaticSource` adapters; `Splitter` seam; `CharSplitter`, `MarkdownSplitter`; `Importer` and free-function `ImportFrom`; metadata key constants (`MetadataSourceIDKey`, `MetadataSectionPathKey`, etc.).
- Key files: see per-file breakdown.

### `internal/`

- Purpose: Non-importable internals. Only one subdirectory exists.
- Contains: `apisnapshot/` only.
- Boundary: Anything in `internal/` is excluded from `api/v1.snapshot.txt` and not importable from outside the module.

### `internal/apisnapshot/`

- Purpose: Pure-stdlib generator that walks the module's source via `go/parser`/`go/ast`, renders every exported decl, and compares against `api/v1.snapshot.txt`. The `-update` flag rewrites the baseline.
- Contains: `apisnapshot.go` (generator), `apisnapshot_test.go` (test harness + `-update` flag).
- Key files: `internal/apisnapshot/apisnapshot.go`, `internal/apisnapshot/apisnapshot_test.go`.

### `obs/`

- Purpose: Cost/latency types + context-propagated counter. Stdlib-only leaf.
- Contains: `Metrics`, `StageTiming`, `CallCounts`, `TokenUsage`; `Counter` (atomic) + `NewCounter`/`WithCounter`/`CounterFrom`.
- Key files: `obs/obs.go`.

### `pack/`

- Purpose: Token-budget-aware context packing.
- Contains: `Packer` seam, `GreedyTokenPacker`; `TokenCounter` seam, `SimpleCounter` (whitespace/CJK heuristic).
- Key files: `pack/pack.go`.

### `postgres/`

- Purpose: PostgreSQL + pgvector backend. The only first-party package with non-stdlib dependencies (`pgx/v5`, `pgvector-go`).
- Contains: `Store` (implements `store.Store` + `LexicalSearcher` + `GraphStore` + `CommunityStore`), `Config`, `New(pool, cfg)`, `RegisterTypes(ctx, conn)` AfterConnect hook, `Migrate()`.
- Key files: see per-file breakdown.

### `prompt/`

- Purpose: Prompt-template seam + built-in QA template.
- Contains: `Template` interface, `RenderContext`, `DefaultQATemplate`.
- Key files: `prompt/template.go`, `prompt/default.go`, `prompt/types.go`.

### `rag/`

- Purpose: Orchestration layer — the SDK's front door.
- Contains: `System`, `Options`, `Answer`, `Trace`, `Diagnostics`, `Citation`, `Observer`, the three Ask flows (`Ask`/`AskGlobal`/`AskDrift`), `Import`/`ImportFrom`/`Retrieve`/`Stats`/`Remove`/`Model`/`PrewarmCommunityReports`, package-level sentinel errors, instrumentation decorators.
- Key files: see per-file breakdown.

### `rerank/`

- Purpose: Hit re-scoring before pack/generate.
- Contains: `Reranker` seam; `HeuristicReranker` (default), `ModelReranker`, `NoopReranker`; `ScoringModel` seam; `HTTPScoringModel` (stdlib `net/http` against Cohere/Jina/TEI-style APIs).
- Key files: `rerank/rerank.go`, `rerank/httpmodel.go`.

### `retrieve/`

- Purpose: The retrieval-policy layer — biggest single package (1588 lines in `retrieve.go`).
- Contains: `Retriever` seam; concrete retrievers `DenseRetriever`, `LexicalRetriever`, `HybridRetriever` (RRF fusion), `StructureRetriever`, `GraphRetriever`, `MultiHopRetriever`, `VariantRetriever`; query-shaping seams `QueryPreprocessor` / `QueryDecomposer` / `QueryEmbedder` / `EntityLinker` / `SectionPlanner`; auto-route planner `GapAwareSectionPlanner`; `LLMExpansionPreprocessor`, `LexicalEntityLinker`, `LLMDecomposer`, `HeuristicDecomposer`, `NoopPreprocessor`; trace types `Trace`, `TrajectoryStep`, `RouteCandidate`, `RoutePolicyTrace`, `FusionAttribution`, `HopAttribution`, `GraphTrace`; BM25 params.
- Key files: see per-file breakdown.

### `store/`

- Purpose: Storage abstraction + reference in-memory backend.
- Contains: `Store` interface (the core), `LexicalSearcher`/`GraphStore`/`CommunityStore` (opt-in capabilities), `Query`, `Hit`, `StoredChunk`, `Filter`, `Stats`; `InMemoryStore` reference implementation; `ErrNotFound`, `ErrDimensionMismatch`.
- Key files: see per-file breakdown.

### `store/storetest/`

- Purpose: Shared cross-backend conformance suite. Each backend wires in via `RunConformance(t, factory)` and the optional `RunLexicalConformance` / `RunGraphConformance` / `RunCommunityConformance`.
- Contains: `Factory` type, `Option` toggles (`WithDimensionStrict`), the conformance subtests.
- Key files: `store/storetest/storetest.go` (802 lines — one large test file).

### `tree/`

- Purpose: Section/heading hierarchical tree from a document's chunks. Deterministic data structure, no LLM.
- Contains: `Node`, `DocumentTree`; `Build(doc, chunks)`, `BuildStored(docID, title, chunks)`; `Find`, `Leaves`, `Sections`.
- Key files: `tree/tree.go`.

## Key File Locations

**Entry Points:**

- `doc.go`: Root package documentation (`package ragkit` — brand name; no exports).
- `rag/system.go`: `rag.New(opts) *System` — primary library entry point (line 167).
- `rag/ask.go`: Standard answer path (line 19).
- `rag/global.go`: GraphRAG map-reduce path (line 62).
- `rag/drift.go`: DRIFT hybrid path (line 75).
- `rag/import.go`: Ingestion path (lines 18 and 193).
- `rag/retrieve.go`: Retrieval-only path (line 15).
- `adapter/llmagent/tool.go`: `AsTool(*rag.System)` — exposes System as an `llm-agent` tool (build tag `llmagent`).

**Configuration:**

- `go.mod`: Module manifest. Three non-stdlib `require` lines.
- `go.sum`: Dependency checksums.
- `api/v1.snapshot.txt`: Frozen v1 API surface (882 lines).

**Core Logic:**

- `rag/system.go`: `*System` struct + `New` constructor + nil-default fills.
- `rag/options.go`: `Options`, `SearchOptions`, `AskOptions`, `GlobalOptions`, `DriftOptions`.
- `rag/import.go`: Ingest orchestration (split→embed→redact→upsert→graph→communities).
- `rag/ask.go`: Standard answer orchestration (retrieve→rerank→pack→inject-screen→render→generate).
- `rag/global.go`: AskGlobal flow + community selection + lazy report cache.
- `rag/drift.go`: AskDrift primer+local-loop+synthesis flow.

**Testing:**

- `internal/apisnapshot/apisnapshot_test.go`: Whole-module API snapshot diff (the v1 gate).
- `contract/contract_test.go`: Cross-repo compile-time pin (consumed by `llm-agent`).
- `store/storetest/storetest.go`: Shared backend conformance suite.
- `store/inmemory_conformance_test.go`: In-memory backend conformance entry.
- `postgres/postgres_conformance_test.go`: Postgres backend conformance entry.

## Per-Package File Listings (non-test only)

### `adapter/llmagent/`

| File | Purpose |
|------|---------|
| `model.go` | `ModelAdapter` adapts `corellm.ChatModel` → `generate.Model` (build tag `llmagent`). |
| `tool.go` | `AsTool(*rag.System) agents.Tool` exposes the System as an LLM tool with five actions (build tag `llmagent`). |

### `advanced/`

| File | Purpose |
|------|---------|
| `llm.go` | `ExpandQuery` (MQE) and `GenerateHypothetical` (HyDE) — stateless `generate.Model`-backed helpers. |
| `errors.go` | `ErrModelRequired` sentinel. |

### `agentic/`

| File | Purpose |
|------|---------|
| `correct.go` | `CorrectiveAsker` self-correcting loop; `QueryReformulator` seam; `LLMReformulator` default; `Attempt`, `Result` types. |

### `api/`

| File | Purpose |
|------|---------|
| `v1.snapshot.txt` | Frozen v1 exported-API surface (text, not Go). The contract baseline. |

### `contract/`

| File | Purpose |
|------|---------|
| `contract_test.go` | Compile-time pin of cross-repo symbols. (No production .go file — the test file *is* the contract.) |

### `embed/`

| File | Purpose |
|------|---------|
| `embedder.go` | `Embedder` interface + package doc. |
| `vector.go` | `Vector []float32` type. |
| `hash.go` | `HashEmbedder` deterministic FNV bag-of-tokens default + `NewHashEmbedder` + `CosineSimilarity`. |

### `eval/`

| File | Purpose |
|------|---------|
| `eval.go` | `Example`, `Dataset`, `Metrics`, `ExampleResult`, `RetrievalResult`; `RetrievalEvaluator`; `Asker`, `Retriever` interfaces; `Run`. |
| `triad.go` | `TriadEvaluator` (retrieval + judged generation); `TriadResult`, `TriadExampleResult`, `GenerationMetrics`; `WriteJSONL`. |
| `global.go` | `GlobalEvaluator` for `AskGlobal`; `GlobalAsker`, `GlobalEvalResult`, `GlobalExampleResult`. |
| `drift.go` | `DriftEvaluator` for `AskDrift`; `DriftAsker`, `DriftEvalResult`, `DriftExampleResult`. |
| `graph.go` | `RunGraphAB` — graph-on/off retrieval A/B; `GraphABResult`. |
| `judge.go` | `Judge` seam, `JudgeRequest`, `Judgement`; `LLMJudge` implementation; `ErrJudgeModelRequired`. |
| `loader.go` | `LoadJSONL(path) (Dataset, error)` — reads `feedback`-format JSONL. |

### `feedback/`

| File | Purpose |
|------|---------|
| `feedback.go` | `Recorder` (mutex-protected JSONL writer); `NewRecorder`, `OpenFile`; `Capture`; `BuildExample(trace, gold, notes)`. |

### `generate/`

| File | Purpose |
|------|---------|
| `model.go` | `Model` interface + package doc. |
| `types.go` | `Message`, `Request`, `Response`, `Usage` value types. |

### `graph/`

| File | Purpose |
|------|---------|
| `graph.go` | `Entity`, `Relation`, `Graph`, `Subgraph` types + `EntityExtractor` seam + package doc. |
| `canonicalize.go` | `Canonicalize(entities, relations) Graph` — exact-merge by `(name, type)`, drops orphan relations. |
| `community.go` | `Community`, `CommunityDetector`, `LabelPropagationDetector`. |
| `louvain.go` | `LouvainDetector` — deterministic two-phase modularity-gain hierarchy. |
| `dictionary.go` | `DictionaryEntityExtractor` — zero-LLM gazetteer extractor (reproducible tests). |
| `extract.go` | `LLMEntityExtractor` — prompts a `generate.Model` for pipe-delimited extractions. |
| `resolve.go` | `EntityResolver` seam; `NoopEntityResolver`; `EmbeddingEntityResolver` (cosine threshold 0.92 default). |
| `path.go` | `RankedPath`, `PathRanker` seam, `WeightedPathRanker`; `maxPathLen = 2`. |
| `summary.go` | `CommunityReport`, `CommunitySummarizer` seam, `LLMCommunitySummarizer`; `CommunityContentHash`; `NormalizeName`. |

### `guard/`

| File | Purpose |
|------|---------|
| `redact.go` | `PIIRedactor`, `Rule`, `RedactResult`, `Redaction`; `Redactor` seam; `NewPIIRedactor` with built-in regex rule set. |
| `inject.go` | `PatternScanner`, `InjectionPattern`, `InjectionVerdict`; `InjectionScanner` seam; `NewPatternScanner`; `NeutralizeText`; `SanitizeMode` constants. |

### `ingest/`

| File | Purpose |
|------|---------|
| `types.go` | `Document`, `Chunk`, `ImportResult` value types. |
| `source.go` | `Source`, `StreamingSource` seams; `SourceFunc`, `StaticSource`, `Collect`; `ErrNilSource`/`ErrNilSplitter`. |
| `splitter.go` | `Splitter` seam; `CharSplitter` + `NewCharSplitter`; metadata key constants. |
| `import.go` | `ImportOptions`; `Importer` struct + `Import` method; free-function `ImportFrom`. (The markdown splitter implementation lives across this and tests; see also `markdown_splitter_test.go`.) |

### `internal/apisnapshot/`

| File | Purpose |
|------|---------|
| `apisnapshot.go` | `Generate(modulePath)` — walks every `*.go` (skipping `_test.go` and `internal/`), uses `go/parser`+`go/ast`+`go/printer` to render exported decls deterministically. |

### `obs/`

| File | Purpose |
|------|---------|
| `obs.go` | `StageTiming`, `CallCounts`, `TokenUsage`, `Metrics`; `Counter` (atomic); `NewCounter`, `WithCounter`, `CounterFrom`, `AddEmbed`, `AddGenerate`, `Counts`. |

### `pack/`

| File | Purpose |
|------|---------|
| `pack.go` | `Packer` seam; `Request`, `Result`, `Trace`; `TokenCounter` seam; `SimpleCounter`; `GreedyTokenPacker`. |

### `postgres/`

| File | Purpose |
|------|---------|
| `postgres.go` | `Config`, `Store`; `New(pool, cfg)`; `RegisterTypes`; `Migrate`; `Upsert`, `Search`, `LexicalSearch`, `List`, `Get`, `Remove`, `RemoveByFilter`, `Stats`. |
| `graph.go` | Compile-time assertion `*Store` implements `store.GraphStore`; `UpsertGraph`, `Neighborhood`, `FindEntities`, `RemoveGraphBySource` via entity/relation tables + recursive CTE. |
| `community.go` | Compile-time assertion `*Store` implements `store.CommunityStore`; `GraphSnapshot`, `UpsertCommunities`, `Communities`, `PutCommunityReport`, `CommunityReport`. |

### `prompt/`

| File | Purpose |
|------|---------|
| `template.go` | `Template` interface + package doc. |
| `types.go` | `RenderContext` value type. |
| `default.go` | `DefaultQATemplate{SystemPrompt, Instructions}` + `Render`. |

### `rag/`

| File | Purpose |
|------|---------|
| `system.go` | `System` struct; `New(Options) *System` constructor with all defaults; `Answer`, `Citation`, `Diagnostics`, `DriftDiagnostics`, `GlobalDiagnostics`, `Trace` types; `Remove`, `Stats`, `Model` methods. |
| `options.go` | `Options`, `SearchOptions`, `AskOptions`, `GlobalOptions`, `DriftOptions` configuration types. |
| `ask.go` | `Ask` standard answer flow; `deriveTokenUsage`; clone helpers for trace deep-copy. |
| `retrieve.go` | `Retrieve` public method and `retrieve` private helper (preprocess + retrieve + counter delta). |
| `import.go` | `Import` and `ImportFrom` ingest flow; section ID builder; redaction summary; metadata helpers. |
| `global.go` | `AskGlobal` map-reduce flow; community selection; lazy report cache helpers. |
| `drift.go` | `AskDrift` primer + bounded local loop + synthesis flow. |
| `observer.go` | `Observer` struct; `ImportTrace` type. |
| `errors.go` | Six package sentinel errors. |
| `inject.go` | `sanitizeHits` method + `InjectionFinding` type. |
| `instrument.go` | `countingEmbedder`, `countingModel` decorators that increment the obs.Counter on the call context. |

### `rerank/`

| File | Purpose |
|------|---------|
| `rerank.go` | `Reranker` seam; `Request`, `Trace`, `RerankScore`; `HeuristicReranker`, `ModelReranker`, `NoopReranker`; `ScoringModel` seam; `ErrScoringModelRequired`. |
| `httpmodel.go` | `HTTPScoringModel` — stdlib `net/http` POST to Cohere/Jina/TEI-style rerank endpoints. |

### `retrieve/`

| File | Purpose |
|------|---------|
| `retrieve.go` | The 1588-line core: `Request`, `Trace`, `PreprocessResult`, `RouteCandidate`, `RoutePolicyTrace`, `TrajectoryStep`, `FusionAttribution`, `SectionPlannerDecision`, `BM25Params`; `Retriever` seam; `DenseRetriever`, `LexicalRetriever`, `StructureRetriever`, `HybridRetriever`, `VariantRetriever`; `QueryPreprocessor` seam, `NoopPreprocessor`, `LLMExpansionPreprocessor`; `QueryEmbedder` seam; `SectionPlanner` seam, `GapAwareSectionPlanner`. |
| `graph.go` | `GraphTrace`; `EntityLinker` seam, `LexicalEntityLinker`; `GraphRetriever` with bounded `Neighborhood` traversal. |
| `multihop.go` | `HopAttribution`; `QueryDecomposer` seam; `HeuristicDecomposer` (splits on " and "), `LLMDecomposer`; `MultiHopRetriever`. |

### `store/`

| File | Purpose |
|------|---------|
| `store.go` | `Filter`, `Query`, `Store` interface, `LexicalSearcher`, `GraphStore`, `CommunityStore` capability interfaces; `ErrNotFound`, `ErrDimensionMismatch`. |
| `types.go` | `StoredChunk`, `Hit`, `Stats` value types. |
| `inmemory.go` | `InMemoryStore` struct + `NewInMemoryStore` + base `Store` methods (Upsert/Search/List/Get/Remove/RemoveByFilter/Stats). |
| `graph.go` | `InMemoryStore` `GraphStore` implementation: `UpsertGraph`, `RemoveGraphBySource`, `Neighborhood`, `FindEntities`; `maxGraphDepth = 2`, `maxGraphFanout = 64`. |
| `community.go` | `InMemoryStore` `CommunityStore` implementation: `GraphSnapshot`, `UpsertCommunities`, `Communities`, `PutCommunityReport`, `CommunityReport`. |

### `store/storetest/`

| File | Purpose |
|------|---------|
| `storetest.go` | The whole conformance suite (Factory, Option, RunConformance, RunLexicalConformance, RunGraphConformance, RunCommunityConformance). 802 lines — one large file. |

### `tree/`

| File | Purpose |
|------|---------|
| `tree.go` | `Node`, `DocumentTree`; `Build(doc, chunks)`, `BuildStored(docID, title, chunks)`; `Find`, `Leaves`, `Sections`; internal heading-path metadata helpers. |

## Where Examples Live

All examples are **test-only** files in `examples/` (`package examples`, `_test.go` suffix) — they compile and run as `go test ./examples/...` and double as `godoc` examples.

| File | Demonstrates |
|------|--------------|
| `examples/basic_import_and_ask_test.go` | Minimal `rag.New` → `Import` → `Retrieve` → `Ask` flow with `echoModel`. |
| `examples/graphrag_example_test.go` | Tier-1 GraphRAG: wire `EntityExtractor`, `HybridRetriever{Graph: ...}`, query with `EnableGraph: true`. |
| `examples/graphrag_global_example_test.go` | Tier-3 `AskGlobal` map-reduce over communities. |
| `examples/graphrag_path_example_test.go` | Path-mode `GraphRetriever` with `WeightedPathRanker`. |
| `examples/graphrag_drift_example_test.go` | `AskDrift` primer + local-loop + synthesis. |

## Where Docs Live

| File | Topic |
|------|-------|
| `README.md` | Quick start, scope, status, package layout, optional adapter. |
| `CHANGELOG.md` | Per-version change log. |
| `doc.go` | Root `package ragkit` docstring (brand name + module-path divergence rationale). |
| `docs/compatibility.md` | The v1 import-compatibility promise (additive-only `v1.x`, `/v2` for breaking changes, interface-method gotcha). |
| `docs/core-compatibility.md` | Two-repo split between `llm-agent` and `llm-agent-rag`; what's where; the build-tagged adapter. |
| `docs/backend-selection.md` | In-memory vs postgres; conformance contract; adding a new backend. |
| `docs/production-deployment.md` | pgvector setup, pool config, observer wiring, operational notes. |
| `docs/graphrag.md` | GraphRAG tier-by-tier usage (Tier-1 fusion, Tier-3 communities + AskGlobal + AskDrift). |
| `docs/api-audit-v1.0.md` | v1.0 audit notes (30188 bytes — the largest doc). |

## Test Layout

Tests are **co-located** with the production code (Go standard pattern). Every package has at least one `_test.go` file; the in-memory store also has a conformance-wiring file.

| Test cluster | What it covers | Files |
|--------------|----------------|-------|
| API snapshot gate | Whole-module exported-symbol diff against `api/v1.snapshot.txt` (`-update` rewrites) | `internal/apisnapshot/apisnapshot_test.go` |
| Cross-repo contract | Compile-time symbol pin consumed by `github.com/costa92/llm-agent` | `contract/contract_test.go` |
| Backend conformance | Shared `Store`/`LexicalSearcher`/`GraphStore`/`CommunityStore` subtests | `store/storetest/storetest.go` (the suite), `store/inmemory_conformance_test.go`, `store/inmemory_test.go`, `store/community_test.go`, `store/graph_test.go`, `postgres/postgres_conformance_test.go`, `postgres/postgres_test.go` |
| Orchestration flows | Full Ask/Import/Retrieve/AskGlobal/AskDrift behavior with stubs | `rag/system_test.go` (765 lines), `rag/drift_test.go`, `rag/global_test.go`, `rag/community_test.go`, `rag/graph_test.go`, `rag/resolve_test.go`, `rag/inject_test.go`, `rag/redact_test.go`, `rag/tokens_test.go`, `rag/observer_test.go`, `rag/instrument_test.go` |
| Retrieval policies | Hybrid fusion, auto-route fan-out/converge, graph traversal, multi-hop, lexical, dense | `retrieve/retrieve_test.go` (1384 lines), `retrieve/graph_test.go`, `retrieve/multihop_test.go` |
| Graph algorithms | Canonicalize, Louvain, path ranking, entity resolution, community summary, dictionary/LLM extractors | `graph/*_test.go` (canonicalize, community, dictionary, extract, graph, path, resolve, summary) |
| Ingest splitters | Char + Markdown splitting, importer pipeline | `ingest/import_test.go`, `ingest/splitter_test.go`, `ingest/markdown_splitter_test.go` |
| Embedding | Hash embedder determinism + cosine sim | `embed/hash_test.go` |
| Reranker | Heuristic + HTTP scoring model | `rerank/rerank_test.go`, `rerank/httpmodel_test.go` |
| Packer | Token-budget greedy packing | `pack/pack_test.go` |
| Prompt | Default QA template rendering | `prompt/default_test.go` |
| Guard | PII redaction + injection scanning | `guard/redact_test.go`, `guard/inject_test.go` |
| Tree | DocumentTree build + section/leaf traversal | `tree/tree_test.go` |
| Obs | Counter atomicity + context propagation | `obs/obs_test.go` |
| Eval | Per-evaluator scoring against fixed datasets | `eval/eval_test.go`, `eval/triad_test.go`, `eval/global_test.go`, `eval/drift_test.go`, `eval/graph_test.go`, `eval/judge_test.go`, `eval/loader_test.go` |
| Agentic | Self-correcting loop convergence | `agentic/correct_test.go` |
| Advanced | MQE + HyDE prompts (stubbed model) | `advanced/llm_test.go` |
| Feedback | JSONL recorder round-trip with eval loader | `feedback/feedback_test.go` |
| Adapter (build-tag) | Model adapter + tool actions (only with `-tags llmagent`) | `adapter/llmagent/model_test.go`, `adapter/llmagent/tool_test.go` |
| Examples | Runnable godoc examples for the five answer paths | `examples/*_test.go` |

Run commands:

```bash
go test ./...                                                    # full suite (stdlib-only)
go test ./internal/apisnapshot/ -run TestAPISnapshot -update     # regenerate v1 snapshot baseline
go test -tags llmagent ./adapter/llmagent                        # adapter (needs local llm-agent require/replace)
```

## Internal vs Public Boundaries

**Public — every directory listed above EXCEPT `internal/`.**

The Go `internal` rule makes anything under any path containing `internal/` non-importable from outside the module. In this repo that means:

- `internal/apisnapshot/` — the only `internal/` package. Excluded from `api/v1.snapshot.txt` by design (the snapshot generator explicitly skips `internal/` path segments — see `internal/apisnapshot/apisnapshot.go:20-21` comment block).

**Test-only packages — also effectively boundary-controlled:**

- `examples/` — `package examples`, all files are `_test.go`. Not part of the v1 surface; not importable for production use.
- `contract/` — `package contract_test`. Compile-time gate only.
- `store/storetest/` — `package storetest`. Importable (used by `postgres/postgres_conformance_test.go`), but its purpose is test-time only.

**Build-tag-gated — opt-in extension of the public surface:**

- `adapter/llmagent/` — `//go:build llmagent`. Default builds exclude it. The API snapshot generator does include it (because `go/parser` ignores build tags), so its `ModelAdapter` and `AsTool` are part of the v1 contract.

## Naming Conventions

**Files:**

- `<concept>.go` for the main type or interface (`embedder.go`, `system.go`, `model.go`, `splitter.go`).
- `<concept>_test.go` co-located with production code (`embedder.go` → `hash_test.go`, `system.go` → `system_test.go`).
- `errors.go` for sentinel-error files (`advanced/errors.go`, `rag/errors.go`).
- `types.go` for value-type collections (`ingest/types.go`, `generate/types.go`, `prompt/types.go`, `store/types.go`).
- `doc.go` only at the module root; package-level docstrings live atop the file that defines the package's central type instead.

**Directories:**

- Single-word, lowercase, snake-case-free (`retrieve`, `rerank`, `embed`, `pack`, `prompt`, `ingest`, `guard`, `agentic`, `advanced`, `feedback`, `obs`, `eval`, `tree`).
- Two-word names use no separator (`storetest` — note the absence of `store_test`, which would conflict with Go's `_test.go` convention).
- The build-tagged subdirectory `llmagent` matches its build tag exactly.

**Packages:**

- Package name matches directory name in every case except the root: root directory is `llm-agent-rag/` but root package is `ragkit` (a deliberate brand-name divergence documented in `doc.go`).

## Where to Add New Code

**New retrieval signal:**

- Implementation: `retrieve/<signal>.go` (e.g. new file `retrieve/sparse.go`).
- Wire-up: extend `retrieve.HybridRetriever` *only* if you can do so as a new optional field — but be aware that adding a struct field changes positional literals. Prefer composition: wrap `HybridRetriever` with a new outer retriever.
- Test: `retrieve/<signal>_test.go` co-located.

**New store backend:**

- Implementation: `<backend>/` at the module root (sister to `postgres/`), e.g. `qdrant/`.
- Required: implement `store.Store`; optional: also implement `LexicalSearcher`, `GraphStore`, `CommunityStore`.
- Conformance: a `*_conformance_test.go` that calls `storetest.RunConformance(t, factory)` and the relevant capability suites — see `postgres/postgres_conformance_test.go` as the template.
- Dependencies: keep all backend-specific deps inside the new package; do **not** add them to any existing package's import list.

**New seam (advanced feature):**

- Create a new top-level package (`<feature>/`); follow the pattern of `agentic/`, `advanced/`, `feedback/`.
- Take dependencies on `rag`, `eval`, `generate`, `embed` as needed — but `rag` itself must NOT depend on your new package.
- Examples + a doc page in `docs/<feature>.md`.

**New evaluator:**

- Add to `eval/` as a new file: `eval/<name>.go` + `eval/<name>_test.go`.
- Follow the existing pattern: define a sibling subset interface of `rag.System` (`Asker` / `GlobalAsker` / `DriftAsker`) so the evaluator doesn't depend on the full `System` shape.

**New `rag.Options` field:**

- Add to `rag/options.go` (`Options` struct).
- Add a nil-default fill in `rag.New` (`rag/system.go:167`).
- Update `api/v1.snapshot.txt` via `go test ./internal/apisnapshot/ -run TestAPISnapshot -update`.
- Document in `README.md` and the relevant `docs/` page.

**New tests for an existing package:**

- Co-locate as `*_test.go` in the same directory.
- Use existing test helpers (`store/storetest.RunConformance` for stores; the `echoModel{}` pattern in `examples/` for stub LLMs).

**Configuration changes:**

- `go.mod` changes (new non-stdlib dep): only acceptable inside `postgres/` or behind a build tag (`adapter/llmagent/`). The default-build closure stays stdlib-only.
- New constants for tuning bounds: add to the package that uses them (e.g. graph depth in `store/graph.go`, drift rounds in `rag/drift.go`).

## Special Directories

**`api/`**

- Purpose: Hosts the frozen v1 API snapshot text file.
- Generated: The contents are generated by `internal/apisnapshot/`.
- Committed: Yes — `v1.snapshot.txt` is the contract baseline and **must** be in version control.

**`internal/`**

- Purpose: Non-importable internals per Go's `internal` rule.
- Contains: Only `apisnapshot/`.
- Generated: No (hand-written).
- Committed: Yes.

**`examples/`**

- Purpose: Runnable godoc examples; one per answer path.
- All files are `*_test.go` — they don't ship as importable production code.
- Committed: Yes.

**`adapter/`**

- Purpose: Cross-repo bridges, build-tag-gated.
- Generated: No.
- Committed: Yes.
- Default build excludes its contents; `go test -tags llmagent ./adapter/llmagent` exercises it.

**`store/storetest/`**

- Purpose: Shared test machinery, intentionally exported (not under `internal/`) so out-of-tree backend repos could use it if the project ever sprouts one.
- Generated: No.
- Committed: Yes.

## `doc.go` Content (the canonical package doc)

`doc.go` at the module root contains the **only** package-level documentation for the root `ragkit` package. Its contents (verbatim):

> Package ragkit is the short brand name for the standalone retrieval-augmented generation SDK whose module path is github.com/costa92/llm-agent-rag.
>
> The root package is a deliberate documentation anchor only: it exports no symbols. Callers import the sub-packages directly — rag, retrieve, store, embed, ingest, generate, pack, prompt, rerank, graph, eval, and the rest — each of which carries its own package documentation. The ragkit name diverges from the llm-agent-rag module path on purpose, to give the SDK a concise import-free identity; this is a recorded decision, not an accidental mismatch.

Key points:

- The root package is intentionally **empty of symbols** — `doc.go` only declares `package ragkit`.
- The brand-name/module-path mismatch (`ragkit` vs `llm-agent-rag`) is deliberate and recorded.
- Every other package carries its own package-level docstring on the file that defines its central type (e.g. `embed/embedder.go`'s leading comment block, `rag/system.go`'s leading comment block).

---

*Structure analysis: 2026-05-20*
