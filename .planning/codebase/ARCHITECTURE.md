<!-- refreshed: 2026-05-20 -->
# Architecture

**Analysis Date:** 2026-05-20

## System Overview

`llm-agent-rag` is a standalone Go RAG SDK built as a set of small, single-purpose packages composed around a single orchestrator type, `*rag.System`. The module path is `github.com/costa92/llm-agent-rag`; the root package is documentation-only (`ragkit` brand, no exported symbols — see `doc.go`).

The system is deliberately layered so the **core** (everything except `postgres/` and `adapter/llmagent/`) has **no non-stdlib dependencies**. Vector storage on Postgres is opt-in (`pgx/v5`, `pgvector-go`); the core agents framework adapter is build-tag-gated (`llmagent`).

```text
┌──────────────────────────────────────────────────────────────────────────┐
│                  Caller code  (rag.System construction)                  │
│   `rag.New(rag.Options{...})` → `*rag.System`                            │
└──────────────────────────────────────────────────────────────────────────┘
                                   │
                                   ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                       Orchestration layer  (`rag/`)                       │
│   Three answer paths: Ask, AskGlobal, AskDrift                            │
│   Two ingest entry points: Import, ImportFrom                             │
│   Pure retrieval: Retrieve                                                │
│   Plus Stats / Remove / PrewarmCommunityReports / Model() accessor.       │
│   Observer hook: OnImport, OnRetrieve, OnAsk                              │
└──────────────────────────────────────────────────────────────────────────┘
                                   │
        ┌──────────────────────────┼──────────────────────────┐
        ▼                          ▼                          ▼
┌─────────────────┐      ┌──────────────────┐      ┌─────────────────────┐
│   INGEST PATH   │      │ RETRIEVAL PATH   │      │ GENERATION PATH     │
│  `ingest/`      │      │  `retrieve/`     │      │  `prompt/`          │
│  `embed/`       │      │  `embed/`        │      │  `pack/`            │
│  `guard/`(PII)  │      │  `rerank/`       │      │  `generate/` (seam) │
│  `graph/`       │      │  `tree/` (struct)│      │  `guard/`(injection)│
│  `tree/`        │      │  `advanced/`     │      │                     │
└────────┬────────┘      └─────────┬────────┘      └──────────┬──────────┘
         │                         │                          │
         └─────────────┬───────────┘                          │
                       ▼                                      │
        ┌──────────────────────────────┐                      │
        │     Storage abstraction      │                      │
        │     `store/`                 │                      │
        │   Store (vector) + 3 opt-in  │                      │
        │   capabilities:              │                      │
        │   LexicalSearcher, GraphStore│                      │
        │   CommunityStore             │                      │
        └─────────┬────────────┬───────┘                      │
                  ▼            ▼                              │
        ┌────────────────┐ ┌────────────────┐                 │
        │ InMemoryStore  │ │ postgres.Store │                 │
        │ `store/`       │ │ `postgres/`    │                 │
        │ stdlib only    │ │ pgvector + FTS │                 │
        └────────────────┘ └────────────────┘                 │
                                                              ▼
                                              ┌─────────────────────────┐
                                              │ Caller-supplied LLM     │
                                              │ via `generate.Model`    │
                                              │ (or `adapter/llmagent`) │
                                              └─────────────────────────┘

Cross-cutting:  `obs/` (metrics) · `eval/` (CI gate) · `feedback/` (JSONL capture)
Stability:      `api/v1.snapshot.txt` · `contract/` · `internal/apisnapshot/`
```

## Component Responsibilities

### Core types (the v1 frozen surface)

| Component | Responsibility | File |
|-----------|----------------|------|
| `rag.System` | Top-level orchestrator; holds every injected seam | `rag/system.go` |
| `rag.Options` | Construction-time wiring of every dependency | `rag/options.go` |
| `rag.Answer` / `rag.Trace` / `rag.Diagnostics` | Standardized output / observer trace / per-run diagnostics | `rag/system.go` |
| `rag.Observer` | Three optional callbacks (`OnImport`, `OnRetrieve`, `OnAsk`) | `rag/observer.go` |
| `contract` (test package) | Compile-time pin of the symbols `github.com/costa92/llm-agent` consumes | `contract/contract_test.go` |
| `api/v1.snapshot.txt` | Generated, committed whole-module exported-API snapshot — the v1 gate | `api/v1.snapshot.txt` |

### Ingestion pipeline

| Component | Responsibility | File |
|-----------|----------------|------|
| `ingest.Source` / `StreamingSource` | Document-input seam (eager + streaming) | `ingest/source.go` |
| `ingest.Splitter` | Chunking seam; built-ins `CharSplitter`, `MarkdownSplitter` | `ingest/splitter.go` |
| `ingest.Document` / `ingest.Chunk` / `ingest.ImportResult` | Pipeline value types | `ingest/types.go` |
| `ingest.Importer` (standalone) | Source→Splitter pipeline outside `rag.System` | `ingest/import.go` |
| `embed.Embedder` | Embedding-backend seam; built-in `HashEmbedder` (zero-LLM) | `embed/embedder.go`, `embed/hash.go` |
| `embed.Vector`, `embed.CosineSimilarity` | Vector value type + similarity helper | `embed/vector.go`, `embed/hash.go` |
| `guard.Redactor` | PII redaction during ingest (deterministic, regex-based) | `guard/redact.go` |

### Retrieval pipeline

| Component | Responsibility | File |
|-----------|----------------|------|
| `retrieve.Retriever` | Central retrieval seam | `retrieve/retrieve.go` |
| `retrieve.DenseRetriever` | Vector search via `store.Store.Search` | `retrieve/retrieve.go` |
| `retrieve.LexicalRetriever` | BM25-style keyword search (uses `LexicalSearcher` if available) | `retrieve/retrieve.go` |
| `retrieve.HybridRetriever` | RRF fusion of Dense + Lexical + Structure + Graph signals | `retrieve/retrieve.go` |
| `retrieve.StructureRetriever` | Structure-aware (section/path) retrieval | `retrieve/retrieve.go` |
| `retrieve.GraphRetriever` | Knowledge-graph traversal retrieval (Tier-1 GraphRAG) | `retrieve/graph.go` |
| `retrieve.MultiHopRetriever` | Decomposes compound queries; fans out across sub-queries | `retrieve/multihop.go` |
| `retrieve.VariantRetriever` | Multi-route fan-out / convergence wrapper around any Base | `retrieve/retrieve.go` |
| `retrieve.QueryPreprocessor` | Query-shaping seam (MQE / HyDE expansion) | `retrieve/retrieve.go` |
| `retrieve.SectionPlanner` | Auto-route fan-out vs converge policy; built-in `GapAwareSectionPlanner` | `retrieve/retrieve.go` |
| `retrieve.QueryDecomposer` | Compound-query splitter seam (`HeuristicDecomposer`, `LLMDecomposer`) | `retrieve/multihop.go` |
| `retrieve.EntityLinker` | Query→entity seam (`LexicalEntityLinker` default) | `retrieve/graph.go` |
| `rerank.Reranker` | Re-score seam; `HeuristicReranker`, `ModelReranker`, `NoopReranker` | `rerank/rerank.go` |
| `rerank.ScoringModel` / `HTTPScoringModel` | External rerank model seam + stdlib HTTP impl | `rerank/httpmodel.go` |

### Storage abstraction

| Component | Responsibility | File |
|-----------|----------------|------|
| `store.Store` | Core vector-store interface (Upsert/Search/List/Get/Remove/RemoveByFilter/Stats) | `store/store.go` |
| `store.LexicalSearcher` | Opt-in capability for native keyword search | `store/store.go` |
| `store.GraphStore` | Opt-in capability for entity-graph persistence + traversal | `store/store.go` |
| `store.CommunityStore` | Opt-in capability for community hierarchy + report cache | `store/store.go` |
| `store.InMemoryStore` | Reference backend implementing all four interfaces | `store/inmemory.go`, `store/graph.go`, `store/community.go` |
| `store/storetest` | Shared conformance suite every backend wires up | `store/storetest/storetest.go` |
| `postgres.Store` | PostgreSQL + pgvector + tsvector backend; implements all four interfaces | `postgres/postgres.go`, `postgres/graph.go`, `postgres/community.go` |

### Generation (answer assembly)

| Component | Responsibility | File |
|-----------|----------------|------|
| `prompt.Template` | Prompt-template seam | `prompt/template.go` |
| `prompt.DefaultQATemplate` | Built-in QA template (chunks + question, citation hint) | `prompt/default.go` |
| `pack.Packer` / `GreedyTokenPacker` | Token-budget-aware context packing | `pack/pack.go` |
| `pack.TokenCounter` / `SimpleCounter` | Tokenizer seam; whitespace/CJK fallback | `pack/pack.go` |
| `generate.Model` | LLM seam (single `Generate` method) | `generate/model.go` |
| `generate.Request` / `Response` / `Message` / `Usage` | Generation value types | `generate/types.go` |
| `guard.InjectionScanner` / `PatternScanner` | Prompt-injection screening of retrieved content | `guard/inject.go` |

### Advanced / research features

| Component | Responsibility | File |
|-----------|----------------|------|
| `graph.*` | Knowledge-graph entities, relations, canonicalization, Louvain communities, path ranking, LLM/dictionary extractors, summarizers, embedding resolver | `graph/graph.go`, `graph/canonicalize.go`, `graph/community.go`, `graph/louvain.go`, `graph/path.go`, `graph/extract.go`, `graph/dictionary.go`, `graph/resolve.go`, `graph/summary.go` |
| `agentic.CorrectiveAsker` | Self-correcting ask loop: judge groundedness, reformulate, retry | `agentic/correct.go` |
| `advanced.ExpandQuery` / `GenerateHypothetical` | Stateless LLM-backed query rewrites (MQE / HyDE) | `advanced/llm.go` |
| `tree.DocumentTree` / `Node` | Hierarchical section/heading tree built from chunks | `tree/tree.go` |
| `feedback.Recorder` | JSONL capture of flagged Asks → `eval.Example` round-trip | `feedback/feedback.go` |

### Cross-cutting

| Component | Responsibility | File |
|-----------|----------------|------|
| `obs.Metrics` / `StageTiming` / `CallCounts` / `TokenUsage` | Cost-and-latency record (per Ask/Import/Retrieve) | `obs/obs.go` |
| `obs.Counter` + `WithCounter` / `CounterFrom` | Context-propagated atomic counter; instrumented embed/model decorators | `obs/obs.go`, `rag/instrument.go` |
| `eval.RetrievalEvaluator` | Precision@K / Recall@K / MRR / Grounding@K scoring | `eval/eval.go` |
| `eval.TriadEvaluator` | RAG-Triad: retrieval + judge-scored generation | `eval/triad.go` |
| `eval.GlobalEvaluator` / `DriftEvaluator` | Generation-only evaluators for AskGlobal / AskDrift | `eval/global.go`, `eval/drift.go` |
| `eval.Judge` / `LLMJudge` | Answer-relevance / groundedness judge seam | `eval/judge.go` |
| `eval.LoadJSONL` | JSONL dataset loader (round-trips `feedback.Recorder` output) | `eval/loader.go` |
| `eval.RunGraphAB` | Graph-on vs graph-off retrieval A/B | `eval/graph.go` |
| `adapter/llmagent` (build tag `llmagent`) | `ModelAdapter` (core ChatModel → `generate.Model`) + `AsTool` (System → agent tool) | `adapter/llmagent/model.go`, `adapter/llmagent/tool.go` |
| `internal/apisnapshot` | Stdlib AST walker that regenerates `api/v1.snapshot.txt` as a `go test` gate | `internal/apisnapshot/apisnapshot.go`, `internal/apisnapshot/apisnapshot_test.go` |

### Subsystem-specific

| Component | Responsibility | File |
|-----------|----------------|------|
| `postgres.Store` | The only first-party non-stdlib backend; `New(pool, cfg)` + `Migrate()` | `postgres/postgres.go` |
| `postgres.RegisterTypes` | Pool `AfterConnect` hook that registers pgvector types | `postgres/postgres.go` |

## Pattern Overview

**Overall:** **Seam-and-default architecture** — every pipeline stage is an interface, every interface has a stdlib-only default implementation, and `rag.New` fills unset `Options` fields with those defaults. The result is a pipeline that runs end-to-end with zero caller configuration (`rag.New(rag.Options{Model: yourModel})` is sufficient) yet allows replacement of any single stage.

**Key Characteristics:**

- **Interfaces are frozen under v1.** New capability becomes a *sibling* optional interface (e.g. `store.LexicalSearcher` next to `store.Store`), not an added method — see `docs/compatibility.md`.
- **Stdlib-only core.** `go.mod`'s `require` block has three non-stdlib lines (`pgx`, `pgvector-go`, and the build-tagged `llm-agent` core); the default-test path links none of them.
- **Deterministic defaults.** `HashEmbedder`, `CharSplitter`, `LouvainDetector`, `DictionaryEntityExtractor`, `HeuristicReranker`, `GapAwareSectionPlanner`, etc. all produce byte-identical output for the same input — making `go test ./...` a reproducible regression gate.
- **Opt-in capabilities via type assertion.** `rag/import.go` does `graphStore, isGraphStore := s.store.(store.GraphStore)`; missing capability degrades silently rather than erroring.
- **One orchestrator owns the wiring.** `*rag.System` is the only stateful pipeline composition; every other type is either a value, a pure function, or a stateless wrapper.

## Layers

**Layer 1 — Orchestration (`rag/`)**

- Purpose: User-facing API; constructs and runs the pipeline.
- Location: `rag/`
- Contains: `System`, `Options`, `Answer`, `Trace`, `Diagnostics`, `Observer`, the three Ask flows.
- Depends on: every other public package except `agentic`, `feedback`, `eval`, `adapter/*`, `postgres`.
- Used by: caller code; `eval` (via the `Retriever` / `Asker` / `GlobalAsker` / `DriftAsker` subset interfaces); `adapter/llmagent` (`AsTool`); `agentic` (via `eval.Asker`).

**Layer 2 — Pipeline seams**

- Purpose: Define the contract for each pipeline stage.
- Location: `ingest/`, `embed/`, `store/`, `retrieve/`, `rerank/`, `pack/`, `prompt/`, `generate/`, `guard/`.
- Pattern: One leaf interface per package, plus value types and a default impl.
- Depends on: each other in a strict DAG (see `store.Store` ⊂ `retrieve` ⊂ `rag`; `embed` and `generate` are leaves).
- Used by: `rag/` for composition; callers when implementing custom stages.

**Layer 3 — Advanced features (additive)**

- Purpose: Optional capabilities that sit on top of the base pipeline.
- Location: `graph/`, `tree/`, `advanced/`, `agentic/`, `feedback/`.
- Characteristic: Each is wired in through `Options` (a seam) or via stateless helpers; nothing in `rag/` depends on `agentic/`, `feedback/`, or `advanced/`.
- Depends on: `embed`, `generate`, `store`, `rag` (one-way).

**Layer 4 — Cross-cutting**

- Purpose: Observability, evaluation, stability gates.
- Location: `obs/`, `eval/`, `contract/`, `api/`, `internal/apisnapshot/`.
- Characteristic: `obs/` is a leaf (stdlib-only); `eval/` depends on `rag` but the dependency runs only at test time.

**Layer 5 — Backends and adapters**

- Purpose: Concrete implementations behind the seams.
- Location: `postgres/` (real production backend), `adapter/llmagent/` (build-tag-gated cross-repo bridge), `store/` (built-in in-memory backend lives alongside the seam by design).

## Data Flow

### Canonical Query Flow — `(*rag.System).Ask`

Entry point: `rag/ask.go` line 19, `func (s *System) Ask(ctx, question, opts AskOptions) (Answer, error)`.

1. **Guard** (`rag/ask.go:20`): return `ErrModelRequired` if `s.model == nil`.
2. **Counter install** (`rag/ask.go:26-27`): allocate fresh `obs.Counter`, attach via `obs.WithCounter(ctx, counter)` — captures nested embed/generate calls.
3. **Retrieve stage** (`rag/ask.go:32` → `rag/retrieve.go:20` `(*System).retrieve`):
   - 3a. **Preprocess** (`rag/retrieve.go:58`, `s.pre.Process`): default `retrieve.LLMExpansionPreprocessor` runs MQE/HyDE expansion when enabled, producing `QueryVariants` (`retrieve/retrieve.go`).
   - 3b. **Retrieve** (`rag/retrieve.go:69`, `s.ret.Retrieve`): default `VariantRetriever{Base: HybridRetriever{Dense, Lexical, Structure}}` (`rag/system.go:200-208`).
     - `HybridRetriever.Retrieve` (`retrieve/retrieve.go`) fans out to each non-nil sub-retriever, computes RRF fusion, records `FusionAttribution` per chunk.
     - `DenseRetriever` calls `s.embedder.Embed` then `store.Search` (`retrieve/retrieve.go`).
     - When `EnableGraph` and the store implements `GraphStore`, `GraphRetriever` links seeds via `EntityLinker`, traverses `Neighborhood` (depth ≤ 2), and adds chunks (`retrieve/graph.go`).
     - When `EnableAutoRoute`, `VariantRetriever` consults `SectionPlanner` to decide fan-out across multiple `RoutePath`s.
   - 3c. Stage timing + counter delta recorded in `retrieve.Trace.Metrics`; `Observer.OnRetrieve` fires (`rag/retrieve.go:85`).
4. **Rerank** (`rag/ask.go:44`): if `opts.Search.EnableRerank` and `s.reranker != nil` (default `HeuristicReranker`), re-score hits (`rerank/rerank.go`).
5. **Pack** (`rag/ask.go:65`): `s.packer.Pack` (default `GreedyTokenPacker` w/ `SimpleCounter`) drops/truncates hits to fit `MaxTokens` (`pack/pack.go`).
6. **Injection screen** (`rag/ask.go:84`, `rag/inject.go`): if `s.injectionScanner != nil`, each packed hit is scanned; suspicious hits are dropped or neutralized per `sanitizeMode`.
7. **Prompt render** (`rag/ask.go:88`): `tpl.Render(ctx, prompt.RenderContext{Question, Namespace, Hits, Metadata})` → `generate.Request` (`prompt/default.go`).
8. **Generate** (`rag/ask.go:98`): `s.model.Generate(ctx, req)` — caller's LLM. The `countingModel` decorator increments the obs.Counter (`rag/instrument.go:36`).
9. **Token usage** (`rag/ask.go:170` `deriveTokenUsage`): use the model's `Usage` if any field > 0, else estimate via `pack.SimpleCounter`.
10. **Assemble** `Answer` with `Hits`, `Prompt`, `Citations`, `Diagnostics`, `Trace`.
11. **Observer** (`rag/ask.go:160`): `s.observer.OnAsk(ctx, answer.Trace)` if set.

The two alternative answer paths (`AskGlobal` in `rag/global.go`, `AskDrift` in `rag/drift.go`) **never call `s.retrieve`, the reranker, or the packer**. They orchestrate community map-reduce / DRIFT primer-then-local-loop directly against `store.CommunityStore` and `store.GraphStore` capabilities.

### Canonical Ingest Flow — `(*rag.System).Import`

Entry point: `rag/import.go` line 18, `func (s *System) Import(ctx, docs, opts) (ImportResult, error)`.

1. **Resolve splitter / maxChars** from `opts` falling back to `s.splitter` / `s.maxChars` (`rag/import.go:19-26`).
2. **Per-document loop** (`rag/import.go:39`):
   - 2a. **Redact** (`rag/import.go:42`): `s.redactor.Redact(doc.Content)` if set — PII removed before splitting so the store never sees raw PII.
   - 2b. **Replace-source reconciliation** (`rag/import.go:49`): if `opts.ReplaceSource && doc.SourceID != ""`, list the source's prior chunks (`s.store.List` filtered by `MetadataSourceIDKey`), record their IDs as `staleGraphChunkIDs`, then `RemoveByFilter`.
   - 2c. **Split** (`rag/import.go:72`): `splitter.Split(doc, maxChars)` → `[]ingest.Chunk` with `Metadata[heading|section_path|...]` populated.
   - 2d. **Embed** (`rag/import.go:76`): per-chunk `s.embedder.Embed`; `countingEmbedder` decorator increments obs.Counter (`rag/instrument.go:20`).
   - 2e. **Materialize** `store.StoredChunk` (`rag/import.go:81-93`) with vector + `SectionID` (built from `Namespace:DocID:Heading` — see `buildSectionID` at line 215).
   - 2f. **Graph extract** (`rag/import.go:97`): `s.entityExtractor.Extract(ctx, chunk.ID, chunk.Content)` → `[]Entity, []Relation`. Accumulated across all chunks.
3. **Upsert** (`rag/import.go:109`): single `s.store.Upsert(ctx, chunks)` call writes the whole batch.
4. **Graph resolve + canonicalize** (`rag/import.go:128-134`): `s.entityResolver.Resolve` (default `NoopEntityResolver` → no-op) merges near-duplicate names, then `graph.Canonicalize` exact-merges by `(name, type)`, returning `res.Graph`.
5. **Graph persist** (`rag/import.go:141`): if `s.store` implements `store.GraphStore`:
   - `RemoveGraphBySource(staleGraphChunkIDs)` reconciles a replace-source re-ingest;
   - `UpsertGraph` union-merges the new subgraph.
6. **Community detection** (`rag/import.go:159`): if `s.store` implements `store.CommunityStore` *and* `s.communityDetector != nil`, read `GraphSnapshot`, call `Detect`, then `UpsertCommunities`. Replace-all — re-detected on every re-ingest.
7. **Observer** (`rag/import.go:175`): `s.observer.OnImport(ctx, ImportTrace{...})` if set; the trace carries chunk IDs, redactions, metrics.

`ImportFrom` (`rag/import.go:193`) is a thin wrapper that calls `src.Documents(ctx)` then `Import`.

### State Management

- `*rag.System` is **immutable after construction**. Every field is set once in `rag.New` and never mutated.
- Per-call state lives in the `context.Context` — the `obs.Counter` is the only piece of mutable shared state and it's attached via `obs.WithCounter` (atomic increments).
- `store.InMemoryStore` is the only first-party stateful type (`sync.RWMutex`-protected maps); `postgres.Store` delegates state to PostgreSQL.

## Key Abstractions

**Seam interface (the dominant pattern).**

- Purpose: A one-method (rarely two) interface in a leaf-ish package, with one or more stdlib-only default implementations alongside.
- Examples: `embed.Embedder` (`embed/embedder.go`), `generate.Model` (`generate/model.go`), `store.Store` (`store/store.go`), `retrieve.Retriever` (`retrieve/retrieve.go`), `rerank.Reranker` (`rerank/rerank.go`), `pack.Packer` (`pack/pack.go`), `prompt.Template` (`prompt/template.go`), `ingest.Splitter` / `Source` (`ingest/splitter.go`, `ingest/source.go`).
- Pattern: Interface + N default implementations + a `New*` helper (or zero-value usable struct).

**Optional capability interface.**

- Purpose: Add a feature without modifying a frozen interface.
- Examples: `store.LexicalSearcher`, `store.GraphStore`, `store.CommunityStore` (all in `store/store.go`).
- Pattern: Define alongside the base interface, document graceful-degradation contract, callers type-assert.

**Trace/Diagnostics value type.**

- Purpose: Every stage emits a structured trace consumed by `Observer.On*` and by `eval`.
- Examples: `retrieve.Trace`, `rerank.Trace`, `pack.Trace`, `rag.Trace`, `rag.Diagnostics`, `rag.ImportTrace`.
- Pattern: Plain `struct` with named slices/maps; deep-copied at boundary crossings (`rag/ask.go:266-298` clone helpers).

**Functional adapter for an interface.**

- Examples: `ingest.SourceFunc` (`ingest/source.go:17`) — adapts `func(ctx) ([]Document, error)` to `Source`.
- Pattern: `type SourceFunc func(...) ...` + `func (f SourceFunc) Documents(...) ...`.

## Entry Points

**Library entry — `rag.New`:**

- Location: `rag/system.go:167`
- Triggers: caller code instantiating the SDK.
- Responsibilities: Fill every nil `Options` field with the SDK default, wrap embedder/model in `countingEmbedder` / `countingModel` instrumentation, return `*System`.

**Three answer paths:**

- `rag/ask.go:19` — `Ask` (standard retrieve-pack-generate).
- `rag/global.go:62` — `AskGlobal` (GraphRAG map-reduce over community summaries).
- `rag/drift.go:75` — `AskDrift` (DRIFT: global primer + bounded local-loop + synthesis).

**Two ingest entry points:**

- `rag/import.go:18` — `Import` (in-memory document slice).
- `rag/import.go:193` — `ImportFrom` (read from an `ingest.Source`).

**Retrieval-only entry — `Retrieve`:**

- Location: `rag/retrieve.go:15`. Used by `eval.RetrievalEvaluator` and `eval.RunGraphAB`.

**Module-doc entry — `doc.go`:**

- Location: `doc.go` (root). Package `ragkit`, no exported symbols. Documentation anchor for the brand name.

**Test gates (act as CI entry points):**

- `internal/apisnapshot/apisnapshot_test.go` — regenerates and diffs `api/v1.snapshot.txt`.
- `contract/contract_test.go` — compile-time pins symbols `github.com/costa92/llm-agent` consumes.

## Public API Surface and v1 Stability Contract

The complete v1 surface is recorded in `api/v1.snapshot.txt` (882 lines). Every entry in that file is frozen under the additive-only `v1.x` rule (`docs/compatibility.md`).

**What's frozen ("import-compatibility rule" — `docs/compatibility.md`):**

- Every symbol in `api/v1.snapshot.txt` — types, functions, methods, vars, consts, fields. No renames, removals, or signature changes within `v1.x`.
- Exported interfaces named in `docs/compatibility.md` ("the interface-method gotcha") are **frozen at method-set level**: `store.Store`, `embed.Embedder`, `generate.Model`, `retrieve.Retriever`, `rerank.Reranker`, `ingest.Splitter`, `ingest.Source`, `prompt.Template`. New capabilities must come as **sibling optional interfaces** (as `store.LexicalSearcher` / `GraphStore` / `CommunityStore` already do).

**What's additive-permitted within `v1.x`:**

- New exported functions, types, methods on new types, vars, consts.
- New struct fields on existing structs (callers using positional literals break; documented hazard).
- Entirely new packages.

**Cross-repo contract (`contract/contract_test.go`):**

- Pins, at compile time, the subset of this module's surface that `github.com/costa92/llm-agent` integrations consume. Removing from this file requires a coordinated PR in the core repo. Adding widens the cross-repo contract.

**Snapshot gate (`internal/apisnapshot/`):**

- Pure stdlib generator walks the module source via `go/parser`+`go/ast`, renders every exported decl, diffs against `api/v1.snapshot.txt`. `go test ./internal/apisnapshot/ -run TestAPISnapshot -update` rewrites the baseline for deliberate additive changes.
- Skips `_test.go` and any `internal/` path segment; **does** parse the build-tagged `adapter/llmagent/` (because `go/parser` ignores build tags).

**Internal — not part of v1:**

- `internal/apisnapshot/` is the only `internal/` package; non-importable externally and excluded from the snapshot.

**Build-tagged — part of v1 but opt-in:**

- `adapter/llmagent/` (build tag `llmagent`). Exports `ModelAdapter` and `AsTool`. Listed in the snapshot.

## Extension Points

Every pipeline stage is replaceable. The complete list of extension seams, with the `Options` field that injects each:

| Seam | Interface | `Options` field | Default |
|------|-----------|-----------------|---------|
| Document source | `ingest.Source`, `ingest.StreamingSource` | (passed to `ImportFrom` / `Importer`) | n/a — caller-supplied |
| Chunking | `ingest.Splitter` | `Options.Splitter` | `ingest.CharSplitter{Overlap: 50}` |
| Embedding | `embed.Embedder` | `Options.Embedder` | `embed.NewHashEmbedder(32)` |
| PII redaction | `guard.Redactor` | `Options.Redactor` | nil (no redaction) |
| Storage | `store.Store` (+ optional `LexicalSearcher` / `GraphStore` / `CommunityStore`) | `Options.Store` | `store.NewInMemoryStore(dim)` |
| Query preprocessing | `retrieve.QueryPreprocessor` | `Options.Preprocessor` | `retrieve.LLMExpansionPreprocessor{Model: model}` |
| Retrieval | `retrieve.Retriever` | `Options.Retriever` | `VariantRetriever{HybridRetriever{Dense, Lexical, Structure}}` |
| Reranking | `rerank.Reranker` | `Options.Reranker` | `rerank.HeuristicReranker{}` |
| Context packing | `pack.Packer` | `Options.Packer` | `pack.GreedyTokenPacker{}` |
| Prompt template | `prompt.Template` | `Options.Template` | `prompt.DefaultQATemplate{}` |
| Generation | `generate.Model` | `Options.Model` | nil — Ask returns `ErrModelRequired` until set |
| Injection screening | `guard.InjectionScanner` | `Options.InjectionScanner` | nil (no screening) |
| Entity extraction | `graph.EntityExtractor` | `Options.EntityExtractor` | nil (no graph build) |
| Entity resolution | `graph.EntityResolver` | `Options.EntityResolver` | `graph.NoopEntityResolver{}` |
| Community detection | `graph.CommunityDetector` | `Options.CommunityDetector` | nil (no detection) |
| Community summarization | `graph.CommunitySummarizer` | `Options.CommunitySummarizer` | nil — `AskGlobal` cache miss returns `ErrCommunitySummarizerRequired` |
| Trace observers | `rag.Observer{OnImport, OnRetrieve, OnAsk}` | `Options.Observer` | zero (no-op) |

Sub-seams inside `retrieve/`:

- `retrieve.QueryEmbedder` (`DenseRetriever.Embedder`) — accepts anything implementing `Embed(ctx, text) (Vector, error)`.
- `retrieve.QueryDecomposer` (`MultiHopRetriever.Decomposer`) — `HeuristicDecomposer` (deterministic, splits on " and ") vs `LLMDecomposer`.
- `retrieve.EntityLinker` (`GraphRetriever.Linker`) — `LexicalEntityLinker` default.
- `graph.PathRanker` (`GraphRetriever.PathRanker`) — opt-in path-mode evidence; default nil.
- `retrieve.SectionPlanner` (passed via `VariantRetriever.Planner`) — `GapAwareSectionPlanner` default.
- `pack.TokenCounter` (`GreedyTokenPacker.Counter`) — `SimpleCounter` default; swap for a real tokenizer.
- `rerank.ScoringModel` (`ModelReranker.Model`) — `HTTPScoringModel` adapts Cohere/Jina/TEI-style HTTP rerank endpoints.

## GraphRAG Architecture

GraphRAG is **not co-equal** with vector RAG and is **not a parallel path** — it is a set of **fused signals and parallel answer paths**, all opt-in and additive. With nothing wired, the SDK is byte-identical to a pure vector RAG (see `docs/graphrag.md`).

### Tier-1 — Fourth retrieval signal

`retrieve.GraphRetriever` (`retrieve/graph.go`) plugs into `retrieve.HybridRetriever` as its `Graph` field. Hybrid retrieval then fuses **Dense + Lexical + Structure + Graph** via RRF (Reciprocal Rank Fusion). Enable per query with `SearchOptions.EnableGraph`. The graph signal is attributed in `FusionAttribution.GraphRank` and `Diagnostics.GraphTrace`. **The graph signal never replaces dense or lexical** — it is purely additive.

### Tier-3 — Two new answer paths

These run *alongside* `Ask`, never *through* it:

- **`System.AskGlobal`** (`rag/global.go`) — Map-reduce over community summaries. Flow: select coarsest-level communities (query-token overlap with member names if too many) → lazy-load community reports via `store.CommunityStore` (generate on cache miss using `Options.CommunitySummarizer`) → **map**: one LLM call per report for a scored partial answer → **reduce**: drop score-0, rank, one LLM call to synthesize. Does not call `retrieve`, `rerank`, or `pack`.
- **`System.AskDrift`** (`rag/drift.go`) — DRIFT hybrid search. Flow: primer (reuses `AskGlobal`'s map step) → seed entities from highest-scoring communities → **local follow-up loop** (hard-bounded by `driftMaxRounds=3`): for each round traverse 1-hop `Neighborhood`, pack provenance chunks, LLM call for partial + named follow-up entities, resolve to next round's seeds → terminate on no new entities → **synthesis**: one LLM call folds primer + every round into final answer.

### Pipeline integration

- **Ingest** persists the graph when the store implements `store.GraphStore` (`rag/import.go:141`); detects communities when it also implements `store.CommunityStore` and a detector is set (`rag/import.go:159`). Graceful degradation: a non-graph store silently leaves graph features off.
- **Storage** — `store.InMemoryStore` (stdlib adjacency map) and `postgres.Store` (recursive-CTE traversal on `entities`/`relations` tables — no separate graph database) both implement the full stack.
- **Traversal bounds** are hard-coded constants: `store.maxGraphDepth = 2`, `store.maxGraphFanout = 64` (`store/graph.go:13-16`), `graph.maxPathLen = 2` (`graph/path.go:29`), `driftMaxRounds = 3` (`rag/drift.go:25`). The system cannot accidentally fan out without bound.

## "Agentic" vs "Advanced" vs "Tree"

These three packages have similar research-flavored names but **distinct concrete roles**:

- **`advanced/`** — Just two **stateless helper functions** for LLM-backed query rewrites, used both standalone and inside `retrieve.LLMExpansionPreprocessor`:
  - `ExpandQuery(ctx, model, query, n)` — multi-query expansion (MQE): prompt the model for `n` semantically-equivalent rewrites.
  - `GenerateHypothetical(ctx, model, query)` — HyDE: synthesize a short hypothetical answer to embed and retrieve against.
  - Imported by `retrieve/retrieve.go` (the default `LLMExpansionPreprocessor` uses both when `Request.EnableMQE` / `EnableHyDE` is true).

- **`agentic/`** — One self-correcting wrapper, `CorrectiveAsker` (`agentic/correct.go`):
  - Holds an `Asker` (typically `*rag.System`), a `Judge` (typically `eval.LLMJudge`), and a `QueryReformulator` (the built-in `LLMReformulator` uses a `generate.Model`).
  - `AskWithCorrection` loop: ask → judge groundedness → if below `MinGrounding` and retries left, reformulate the query → re-ask → return the best `Attempt` by groundedness.
  - **Wraps** the standard `Ask` path; doesn't replace it.

- **`tree/`** — Structural primitive: `DocumentTree` is a section/heading **tree** built from a `Document` and its chunks. Pure data structure — no LLM.
  - `Build(doc, chunks)` (`tree/tree.go:37`) constructs from `ingest.Chunk`; `BuildStored(...)` from `store.StoredChunk`.
  - Consumed by `retrieve.StructureRetriever` (via the section metadata splitters write onto chunks) and by structure-aware route auto-routing in `retrieve.VariantRetriever`.
  - Used by `SearchOptions.EnableTreeExpansion` / `ExpansionDepth` to walk neighbor sections from a hit.

In short: `advanced/` is query-side LLM helpers; `agentic/` is a retry loop; `tree/` is a deterministic data structure for hierarchical markdown corpora.

## Architectural Constraints

- **Threading:** Single-process Go runtime. `store.InMemoryStore` is `sync.RWMutex`-protected (`store/inmemory.go:17`). `feedback.Recorder` is mutex-protected (`feedback/feedback.go:44`). `obs.Counter` uses `atomic.Int64` (`obs/obs.go:48-51`). `*rag.System` is read-only after `New` and safe for concurrent calls.
- **Global state:** None. The closest thing is the `obs.Counter` attached to a `context.Context` via `obs.WithCounter` — but it is per-call, not module-global.
- **Circular imports:** None. Strict DAG: `embed` and `generate` are leaves; `store` depends on `embed` and `graph`; `retrieve` depends on `store`, `embed`, `graph`, `advanced`, `tree`, `generate`, `obs`; `rag` depends on every public package except `agentic`/`feedback`/`eval`/`adapter`/`postgres`. `graph` imports only `generate` and `embed`. `obs` and `guard` are stdlib-only leaves.
- **Dependency budget:** `go.mod` has exactly **three** non-stdlib `require` lines: `pgx/v5`, `pgvector-go` (both for `postgres/`), and `costa92/llm-agent v0.5.0` (only linked under the `llmagent` build tag in `adapter/llmagent/`). The default-build dependency closure is **stdlib only**.
- **Determinism:** Every default implementation is deterministic by construction (sorted iteration, no `map` range without sort, no `time.Now`-seeded randomness, no goroutine fan-out). `graph.LouvainDetector`'s docstring (`graph/louvain.go:8-19`) explicitly states "no randomness and no random restarts — the same graph always yields the same hierarchy."
- **Traversal bounds:** Hard-coded constants prevent unbounded graph work — see the GraphRAG section above.
- **Build tags:** `adapter/llmagent/*.go` is gated behind `//go:build llmagent` (`adapter/llmagent/model.go:1`, `adapter/llmagent/tool.go:1`). Default `go test ./...` does not exercise it.

## Anti-Patterns

### Adding a method to an exported interface

**What happens:** A contributor wants to add a new operation to `store.Store` (e.g. a batched `GetMany`) and considers extending the interface.

**Why it's wrong:** It breaks every external implementation, violating the v1 import-compatibility rule (`docs/compatibility.md:39-56`). This module's contract forbids it for `v1.x`.

**Do this instead:** Add a **sibling optional interface** (the `LexicalSearcher` / `GraphStore` / `CommunityStore` pattern in `store/store.go:53-104`); have consumers type-assert and degrade gracefully when absent.

### Adding a non-stdlib dependency to a core package

**What happens:** A new feature in `retrieve/` or `graph/` is tempting to implement using a third-party library.

**Why it's wrong:** It pollutes the default dependency closure, which is intentionally stdlib-only (see `README.md:25-27`, `docs/core-compatibility.md`). The two-repo split with `llm-agent` exists precisely to keep this promise.

**Do this instead:** Either implement with stdlib (the path taken by `graph.LouvainDetector`, `postgres` recursive-CTE traversal vs a graph DB, `rerank.HTTPScoringModel` using only `net/http`), or move the feature behind a build tag (the `adapter/llmagent` pattern), or scope it into the `postgres/` subpackage if it is storage-specific.

### Routing GraphRAG flows through `Ask`

**What happens:** Treating `AskGlobal` or `AskDrift` as just another retrieval mode and trying to call `(*System).retrieve` from inside them.

**Why it's wrong:** They are deliberately *separate* answer paths (`rag/global.go:42-44`, `rag/drift.go:50-52`). Their flows do not match retrieve-pack-generate — they have their own select/map/reduce or primer/loop/synthesize structure. Forcing them through `retrieve` would require contorting the retrieval pipeline.

**Do this instead:** Keep the three answer paths independent. Each owns its own metrics/observability and produces its own diagnostics (`GlobalDiagnostics`, `DriftDiagnostics`). The shared surface is the `rag.Answer` return type and the `obs.Counter` instrumentation.

### Mutating `rag.Options` after `rag.New`

**What happens:** Caller code reaches into the `*System` returned by `rag.New` and tries to swap a field.

**Why it's wrong:** `*System` is read-only after construction. Every field is unexported; you'd have to introduce a setter and risk concurrent-modification bugs.

**Do this instead:** Build a new `*System` with the desired `Options`. The construction cost is one allocation and a handful of nil-default fills (`rag/system.go:167-247`); it is cheap and intentional.

## Error Handling

**Strategy:** Sentinel errors at package boundaries, wrapped with `fmt.Errorf("rag: ...: %w", err)` when crossing layers. No panics in the public API.

**Patterns:**

- **Package-level sentinels** (`rag/errors.go`, `ingest/source.go:54-58`, `eval/judge.go:14`, `graph/*` `ErrXxxRequired`, `retrieve/retrieve.go` `ErrBaseRetrieverRequired`, `rerank/rerank.go` `ErrScoringModelRequired`, `store/store.go:106-111`): each package owns its `ErrXxx` vars.
- **Wrapping with context** at orchestration boundaries: `rag/import.go:58, 68, 78, 100, 110, 131, 144, 149, 162` consistently wrap downstream errors with `fmt.Errorf("rag: <action>: %w", err)`.
- **Graceful capability degradation:** Type-assertion `cs, ok := s.store.(store.CommunityStore)` followed by an early return of an empty (non-error) `Answer` — see `rag/global.go:74-79`. Missing capability is not an error.
- **Caller-required errors:** `ErrModelRequired`, `ErrCommunitySummarizerRequired`, `ErrEmptyQuery`, `ErrImporterRequired`, `ErrRetrieverRequired`, `ErrSourceRequired` (`rag/errors.go`). Returned when the caller wired the System incompletely for the path they invoked.

## Cross-Cutting Concerns

**Observability (`obs/`)**

- Synchronous instrumentation: `obs.Counter` rides the context (`obs.WithCounter` / `CounterFrom`), wraps embedder/model in `countingEmbedder` / `countingModel` (`rag/instrument.go`) — every embed and generate call is counted, even when nested deep in a retriever or preprocessor.
- Stage timings: each Ask/Import path records `obs.StageTiming` for `preprocess`, `retrieve`, `rerank`, `pack`, `generate`, `embed`, `upsert`, `select`, `report`, `map`, `reduce` (names vary by path).
- `obs.Metrics` lands on `Answer.Diagnostics.Metrics`, `ImportResult.Metrics`, `retrieve.Trace.Metrics`.

**Observer hooks (`rag.Observer`)**

- Three optional callbacks: `OnImport`, `OnRetrieve`, `OnAsk` (`rag/observer.go:33-37`). Fire only on success. Designed to be consumed by `github.com/costa92/llm-agent-otel` for OpenTelemetry export, and by `feedback.Recorder` for miss capture.

**Evaluation (`eval/`)**

- Four evaluators: `RetrievalEvaluator` (P/R/MRR/Grounding@K), `TriadEvaluator` (retrieval + judged generation), `GlobalEvaluator`, `DriftEvaluator`. All take a `Dataset` (in-memory `Dataset{}` literal or `LoadJSONL`).
- `RunGraphAB(retriever, base, dataset)` A/B-tests retrieval with/without `EnableGraph` (`eval/graph.go:23`).
- `Judge` seam with `LLMJudge` (`eval/judge.go`) — prompts a `generate.Model` for groundedness/relevance scores in `[0,1]`.
- `Asker` / `GlobalAsker` / `DriftAsker` are narrow subset interfaces of `*rag.System` — `eval` does not depend on the full `rag.System` shape.

**Stability gates**

- `internal/apisnapshot/` regenerates and diffs `api/v1.snapshot.txt` on every `go test`.
- `contract/contract_test.go` pins the cross-repo surface that `github.com/costa92/llm-agent` consumes.
- `store/storetest/RunConformance` is the cross-backend regression suite — every backend (in-memory, postgres) runs the same subtests.

**Logging**

- The SDK does **no logging** of its own — observation flows through `Observer` callbacks and `obs.Metrics`. Callers attach a logger via their Observer implementation.

**Validation**

- Per-stage value validation lives in each package (`store/inmemory.go` rejects dimension-mismatched vectors with `ErrDimensionMismatch`; `rag/retrieve.go:21` rejects empty queries with `ErrEmptyQuery`). No central validator.

---

*Architecture analysis: 2026-05-20*
