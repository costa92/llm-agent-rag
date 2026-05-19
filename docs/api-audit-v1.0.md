# `llm-agent-rag` v1.0 exported-surface audit

**Purpose.** This is the freeze-time exported-surface inventory for the
`v1.0.0` release of `github.com/costa92/llm-agent-rag`. It enumerates every
exported symbol of every importable package plus the build-tagged
`adapter/llmagent`, classifies each symbol **keep / rename / unexport**,
confirms in writing that there are no accidental exports, and records the
ratified pre-freeze naming decisions. After `v1.0.0` every breaking rename
needs a `/v2`; this document is the last-chance review record.

**Audited tag.** `v0.6.0-1-g1d6e206` (`git describe --tags`).
**Date.** 2026-05-19.
**Status.** Point-in-time record. This is *not* the living compatibility
policy — `docs/compatibility.md` (Phase 29) is the living document; this
file is a one-time freeze-time audit and is not updated after v1.0.

**Method.** Package list from `go list ./...` (default build tags) — 22
packages — plus `adapter/llmagent` (behind `-tags llmagent`). Each package's
exported surface captured with `go doc <import-path>`; struct fields and
interface methods expanded where the keep/rename judgement needed them;
`adapter/llmagent` inspected from source (`adapter/llmagent/*.go`) because
`go doc` does not accept a `-tags` flag.

**Disposition legend.**

- **keep** — frozen as-is for v1.0.
- **rename→X** — a ratified breaking rename, *applied in slice 28-02*.
- **unexport** — would be made package-private. (No symbol carries this
  disposition: see "Accidental-export confirmation" below.)

---

## Per-package inventory

### root — package `ragkit` (`github.com/costa92/llm-agent-rag`)

`doc.go` only — a package comment, **no exported symbols**.

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| *(none)* | — | — | `doc.go` declares `package ragkit` with a package comment and zero exported symbols. The `ragkit` ≠ module-path (`llm-agent-rag`) name is a deliberate documentation anchor — the package comment is rewritten in 28-02 (KS-3) to record that, with no symbol change. |

### `advanced`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `ErrModelRequired` | var | keep | Package-prefixed sentinel error. |
| `ExpandQuery` | func | keep | Multi-query expansion helper. |
| `GenerateHypothetical` | func | keep | HyDE helper. |

### `agentic`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `ErrAskerRequired` | var | keep | Package-prefixed sentinel error. |
| `Attempt` | type (struct) | keep | One corrective-loop attempt record. |
| `CorrectiveAsker` | type (struct) | keep | Self-correcting retrieval loop. |
| `LLMReformulator` | type (struct) | keep | LLM-backed `QueryReformulator`. |
| `QueryReformulator` | interface | keep | Deliberate plug-point seam — caller may supply a custom reformulator. |
| `Result` | type (struct) | keep | Corrective-loop result. Package-local; not the `eval.Result` being renamed — `agentic.Result` is unrelated and stays. |

### `contract`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| *(none)* | — | — | `contract/contract_test.go` is a test-only cross-repo compile-pin (`go doc` reports "no source-code package"). No first-class exported API to freeze. See "Contract-gate cross-check" below. |

### `embed`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `CosineSimilarity` | func | keep | Vector similarity helper. |
| `Embedder` | interface | keep | Deliberate plug-point seam — the embedding-backend abstraction. |
| `HashEmbedder` | type (struct) | keep | Deterministic test/default embedder. |
| `NewHashEmbedder` | func | keep | Constructor for `HashEmbedder`. |
| `Vector` | type (`[]float32`) | keep | Embedding vector type. |

### `eval`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `ErrJudgeModelRequired` | var | keep | Package-prefixed sentinel error. |
| `WriteJSONL` | func | keep | Writes a `TriadResult` as JSONL. |
| `Asker` | interface | keep | Deliberate plug-point seam. |
| `Dataset` | type (struct) | keep | Labeled eval dataset. |
| `LoadJSONL` | func | keep | Loads a `Dataset` from JSONL. |
| `DriftAsker` | interface | keep | Deliberate plug-point seam. |
| `DriftEvalResult` | type (struct) | keep | DRIFT-path evaluator result (already name-prefixed). |
| `DriftEvaluator` | type (struct) | keep | DRIFT-path evaluator (already name-prefixed). |
| `DriftExampleResult` | type (struct) | keep | Per-example DRIFT result. |
| `Evaluator` | type (struct) | **rename→`RetrievalEvaluator`** | The base retrieval evaluator. The only un-prefixed evaluator; renamed for symmetry with `GlobalEvaluator`/`DriftEvaluator`/`TriadEvaluator`. Applied in 28-02. |
| `Example` | type (struct) | keep | One labeled dataset example. |
| `ExampleResult` | type (struct) | keep | Per-example retrieval result. |
| `GenerationMetrics` | type (struct) | keep | Generation-side metrics. |
| `GlobalAsker` | interface | keep | Deliberate plug-point seam. |
| `GlobalEvalResult` | type (struct) | keep | Global-path evaluator result (already prefixed). |
| `GlobalEvaluator` | type (struct) | keep | Global-path evaluator (already prefixed). |
| `GlobalExampleResult` | type (struct) | keep | Per-example global result. |
| `GraphABResult` | type (struct) | keep | A/B comparison result. |
| `RunGraphAB` | func | keep | A/B harness. Constructs `Evaluator{...}` internally — a rename reference site, updated in 28-02. |
| `Judge` | interface | keep | Deliberate plug-point seam. |
| `JudgeRequest` | type (struct) | keep | LLM-judge request. |
| `Judgement` | type (struct) | keep | LLM-judge verdict. |
| `LLMJudge` | type (struct) | keep | LLM-backed `Judge`. |
| `Metrics` | type (struct) | keep | The four headline retrieval metrics. |
| `Result` | type (struct) | **rename→`RetrievalResult`** | The base retrieval-evaluator result. `(Evaluator).Run` returns it; renamed alongside `Evaluator` for symmetry. Applied in 28-02. |
| `Retriever` | interface | keep | Deliberate plug-point seam. |
| `TriadEvaluator` | type (struct) | keep | RAG-triad evaluator (already prefixed). |
| `TriadExampleResult` | type (struct) | keep | Per-example triad result. |
| `TriadResult` | type (struct) | keep | RAG-triad result (already prefixed). |

### `examples`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| *(none)* | — | — | Test-only worked-examples package (`*_test.go` files only; `go doc` reports "no source-code package"). No first-class exported API to freeze. |

### `feedback`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `BuildExample` | func | keep | Converts a `rag.Trace` into an `eval.Example`. |
| `Recorder` | type (struct) | keep | Captures flagged Asks as JSONL. |
| `NewRecorder` | func | keep | Constructor from an `io.Writer`. |
| `OpenFile` | func | keep | Constructor that opens a file. |

### `generate`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `Message` | type (struct) | keep | Chat message. |
| `Model` | interface | keep | The core generation seam — deliberate plug-point. |
| `Request` | type (struct) | keep | Generation request. |
| `Response` | type (struct) | keep | Generation response. |
| `Usage` | type (struct) | keep | Token usage. |

### `graph`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `ErrCommunitySummarizerModelRequired` | var | keep | Package-prefixed sentinel error. |
| `ErrEntityExtractorModelRequired` | var | keep | Package-prefixed sentinel error. |
| `ErrEntityResolverEmbedderRequired` | var | keep | Package-prefixed sentinel error. |
| `CommunityContentHash` | func | keep | Stable content hash for a `Community`. |
| `NormalizeName` | func | keep | Entity-name normalizer. |
| `Community` | type (struct) | keep | A detected community. |
| `CommunityDetector` | interface | keep | Deliberate plug-point seam. |
| `CommunityReport` | type (struct) | keep | A community summary report. |
| `CommunitySummarizer` | interface | keep | Deliberate plug-point seam. |
| `DictionaryEntityExtractor` | type (struct) | keep | Deterministic dictionary-based extractor. |
| `EmbeddingEntityResolver` | type (struct) | keep | Embedding-similarity resolver. |
| `Entity` | type (struct) | keep | A graph entity. |
| `EntityExtractor` | interface | keep | Deliberate plug-point seam. |
| `EntityResolver` | interface | keep | Deliberate plug-point seam. |
| `Graph` | type (struct) | keep | The knowledge graph. |
| `Canonicalize` | func | keep | Builds a canonical `Graph`. |
| `LLMCommunitySummarizer` | type (struct) | keep | LLM-backed summarizer. |
| `LLMEntityExtractor` | type (struct) | keep | LLM-backed extractor. |
| `LabelPropagationDetector` | type (struct) | keep | Label-propagation community detector. |
| `LouvainDetector` | type (struct) | keep | Louvain community detector. |
| `NoopEntityResolver` | type (struct) | keep | No-op resolver. |
| `PathRanker` | interface | keep | Deliberate plug-point seam — path-ranking abstraction. |
| `RankedPath` | type (struct) | keep | A scored graph path. |
| `Relation` | type (struct) | keep | A typed relation. |
| `Subgraph` | type (struct) | keep | A graph neighborhood. |
| `WeightedPathRanker` | type (struct) | keep | Weighted `PathRanker`. |

### `guard`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `NeutralizeText` | func | keep | Prompt-injection neutralizer. |
| `InjectionPattern` | type (struct) | keep | One injection-detection pattern. |
| `InjectionScanner` | interface | keep | Deliberate plug-point seam. |
| `InjectionVerdict` | type (struct) | keep | Injection-scan verdict. |
| `PIIRedactor` | type (struct) | keep | PII redactor. |
| `NewPIIRedactor` | func | keep | Constructor. |
| `PatternScanner` | type (struct) | keep | Pattern-based injection scanner. |
| `NewPatternScanner` | func | keep | Constructor. |
| `RedactResult` | type (struct) | keep | Redaction result. |
| `Redaction` | type (struct) | keep | One redaction record. |
| `Redactor` | interface | keep | Deliberate plug-point seam. |
| `Rule` | type (struct) | keep | A redaction rule. |
| `SanitizeMode` | type (int) | keep | Sanitization-mode enum. |
| `Neutralize` | const | keep | `SanitizeMode` enum value (and siblings). |

### `ingest`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `MetadataSourceIDKey` | const | keep | Metadata key constant (and siblings). |
| `ErrNilSource` | var | keep | Package-prefixed sentinel error. |
| `ErrNilSplitter` | var | keep | Package-prefixed sentinel error. |
| `ImportFrom` | func | keep | Streaming import entry point. |
| `CharSplitter` | type (struct) | keep | Character-based splitter. |
| `NewCharSplitter` | func | keep | Constructor. |
| `Chunk` | type (struct) | keep | An ingested chunk. |
| `Document` | type (struct) | keep | A source document. |
| `Collect` | func | keep | Drains a `StreamingSource`. |
| `ImportOptions` | type (struct) | keep | Import config. |
| `ImportResult` | type (struct) | keep | Import result. |
| `Importer` | type (struct) | keep | The importer. |
| `NewImporter` | func | keep | Constructor. |
| `MarkdownSplitter` | type (struct) | keep | Markdown-aware splitter. |
| `NewMarkdownSplitter` | func | keep | Constructor. |
| `Source` | interface | keep | Deliberate plug-point seam. |
| `StaticSource` | func | keep | In-memory `Source` constructor. |
| `SourceFunc` | type (func) | keep | Function-adapter for `Source`. |
| `Splitter` | interface | keep | Deliberate plug-point seam. |
| `StreamingSource` | interface | keep | Deliberate plug-point seam. |

### `obs`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `WithCounter` | func | keep | Attaches a `Counter` to a context. |
| `CallCounts` | type (struct) | keep | Per-stage call counts. |
| `Counter` | type (struct) | keep | Cost/latency counter. |
| `CounterFrom` | func | keep | Reads a `Counter` off a context. |
| `NewCounter` | func | keep | Constructor. |
| `Metrics` | type (struct) | keep | Cost-and-latency metrics; embedded across the pipeline. |
| `StageTiming` | type (struct) | keep | Per-stage timing. |
| `TokenUsage` | type (struct) | keep | Token usage record. |

### `pack`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `GreedyTokenPacker` | type (struct) | keep | Greedy context packer. |
| `Packer` | interface | keep | Deliberate plug-point seam. |
| `Request` | type (struct) | keep | Pack request. |
| `Result` | type (struct) | keep | Pack result. Package-local; unrelated to `eval.Result`. |
| `SimpleCounter` | type (struct) | keep | Deliberate seam — default whitespace token counter; `rag/ask.go` uses `SimpleCounter{}` and a caller may swap in a real tokenizer. Not an accidental export. |
| `TokenCounter` | interface | keep | Deliberate plug-point seam — the tokenizer abstraction. |
| `Trace` | type (struct) | keep | Packer trace. |

### `postgres`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `RegisterTypes` | func | keep | Registers pgvector types on a connection. |
| `Config` | type (struct) | keep | Postgres store config. |
| `Store` | type (struct) | keep | `store.Store` against PostgreSQL/pgvector. |
| `New` | func | keep | Constructor. |

### `prompt`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `DefaultQATemplate` | type (struct) | keep | Default QA prompt template. |
| `RenderContext` | type (struct) | keep | Template render context. |
| `Template` | interface | keep | Deliberate plug-point seam — the prompt-template abstraction. |

### `rag`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `ErrCommunitySummarizerRequired` | var | keep | Package-prefixed sentinel error. |
| `ErrEmptyQuery` | var | keep | Package-prefixed sentinel error. |
| `ErrImporterRequired` | var | keep | Package-prefixed sentinel error. |
| `ErrModelRequired` | var | keep | Package-prefixed sentinel error. |
| `ErrRetrieverRequired` | var | keep | Package-prefixed sentinel error. |
| `ErrSourceRequired` | var | keep | Package-prefixed sentinel error. |
| `Answer` | type (struct) | keep | An answer-path result. |
| `AskOptions` | type (struct) | keep | Config for `System.Ask`. Naming ratified as-is — see "Ratified naming decisions". |
| `Citation` | type (struct) | keep | An answer citation. |
| `Diagnostics` | type (struct) | keep | Per-run diagnostics. |
| `DriftDiagnostics` | type (struct) | keep | DRIFT-path diagnostics. |
| `DriftOptions` | type (struct) | keep | Config for `System.AskDrift`. Naming ratified as-is. |
| `GlobalDiagnostics` | type (struct) | keep | Global-path diagnostics. |
| `GlobalOptions` | type (struct) | keep | Config for `System.AskGlobal`. Naming ratified as-is. |
| `ImportTrace` | type (struct) | keep | Import-stage trace. |
| `InjectionFinding` | type (struct) | keep | A prompt-injection finding. |
| `Observer` | type (struct) | keep | Pipeline callback hooks. |
| `Options` | type (struct) | keep | `System` construction config. |
| `SearchOptions` | type (struct) | keep | Config for `System.Search`. |
| `System` | type (struct) | keep | The top-level RAG system. |
| `New` | func | keep | Constructor for `System`. |
| `Trace` | type (struct) | keep | Per-run trace passed to `Observer`. |

`System` carries the exported answer-path methods `Ask`, `AskGlobal`,
`AskDrift`, `Search`, `Import` — all keep; their option-struct naming is
addressed in "Ratified naming decisions".

### `rerank`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `ErrScoringModelRequired` | var | keep | Package-prefixed sentinel error. |
| `HTTPScoringModel` | type (struct) | keep | HTTP-backed `ScoringModel`. |
| `HeuristicReranker` | type (struct) | keep | Heuristic reranker. |
| `ModelReranker` | type (struct) | keep | Model-backed reranker. |
| `NoopReranker` | type (struct) | keep | No-op reranker. |
| `Request` | type (struct) | keep | Rerank request. |
| `RerankScore` | type (struct) | keep | A rerank score. |
| `Reranker` | interface | keep | Deliberate plug-point seam. |
| `ScoringModel` | interface | keep | Deliberate plug-point seam. |
| `Trace` | type (struct) | keep | Rerank trace. |

### `retrieve`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `ErrBaseRetrieverRequired` | var | keep | Package-prefixed sentinel error. |
| `BM25Params` | type (struct) | keep | BM25 tuning parameters. |
| `DenseRetriever` | type (struct) | keep | Dense vector retriever — frozen concrete retriever. |
| `EntityLinker` | interface | keep | Deliberate plug-point seam. |
| `FusionAttribution` | type (struct) | keep | Hybrid-fusion attribution. |
| `GapAwareSectionPlanner` | type (struct) | keep | Gap-aware `SectionPlanner`. |
| `GraphRetriever` | type (struct) | keep | Graph retriever — frozen concrete retriever. |
| `GraphTrace` | type (struct) | keep | Graph-retrieval trace. |
| `HeuristicDecomposer` | type (struct) | keep | Heuristic `QueryDecomposer`. |
| `HopAttribution` | type (struct) | keep | Multi-hop attribution. |
| `HybridRetriever` | type (struct) | keep | Hybrid retriever — frozen concrete retriever. |
| `LLMDecomposer` | type (struct) | keep | LLM-backed `QueryDecomposer`. |
| `LLMExpansionPreprocessor` | type (struct) | keep | LLM expansion preprocessor. |
| `LexicalEntityLinker` | type (struct) | keep | Lexical `EntityLinker`. |
| `LexicalRetriever` | type (struct) | keep | Lexical retriever — frozen concrete retriever. |
| `MultiHopRetriever` | type (struct) | keep | Multi-hop retriever — frozen concrete retriever. |
| `NoopPreprocessor` | type (struct) | keep | No-op preprocessor. |
| `PreprocessResult` | type (struct) | keep | Preprocessing result. |
| `QueryDecomposer` | interface | keep | Deliberate plug-point seam. |
| `QueryEmbedder` | interface | keep | Deliberate plug-point seam. |
| `QueryPreprocessor` | interface | keep | Deliberate plug-point seam. |
| `Request` | type (struct) | keep | Retrieval request. |
| `Retriever` | interface | keep | Deliberate plug-point seam — the retriever abstraction. |
| `RouteCandidate` | type (struct) | keep | A route-policy candidate. |
| `RoutePolicyTrace` | type (struct) | keep | Route-policy trace. |
| `SectionPlanner` | interface | keep | Deliberate plug-point seam. |
| `SectionPlannerDecision` | type (struct) | keep | A section-planner decision. |
| `StructureRetriever` | type (struct) | keep | Structure retriever — frozen concrete retriever. |
| `Trace` | type (struct) | keep | Retrieval trace. |
| `TrajectoryStep` | type (struct) | keep | One multi-hop trajectory step. |
| `VariantRetriever` | type (struct) | keep | Variant retriever — frozen concrete retriever. |

The `retrieve` concrete-retriever surface (6+ structs) and its 5+ seam
interfaces are the deliberate, frozen `retrieve` surface — all keep.

### `store`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `ErrDimensionMismatch` | var | keep | Package-prefixed sentinel error. |
| `ErrNotFound` | var | keep | Package-prefixed sentinel error. |
| `CommunityStore` | interface | keep | Deliberate capability-interface seam. |
| `Filter` | type (`map[string]any`) | keep | Metadata filter. |
| `GraphStore` | interface | keep | Deliberate capability-interface seam. |
| `Hit` | type (struct) | keep | A retrieval hit. |
| `InMemoryStore` | type (struct) | keep | In-memory `store.Store`. |
| `NewInMemoryStore` | func | keep | Constructor. |
| `LexicalSearcher` | interface | keep | Deliberate capability-interface seam. |
| `Query` | type (struct) | keep | A store query. |
| `Stats` | type (struct) | keep | Store statistics. |
| `Store` | interface | keep | The core storage seam — deliberate plug-point. |
| `StoredChunk` | type (struct) | keep | A stored chunk. |

The `store` capability interfaces (`CommunityStore`, `GraphStore`,
`LexicalSearcher`) are deliberate capability seams — a backend opts into a
capability by implementing it; callers type-assert. All keep.

### `store/storetest`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `RunCommunityConformance` | func | keep | Community-capability conformance suite. |
| `RunConformance` | func | keep | Core `store.Store` conformance suite. |
| `RunGraphConformance` | func | keep | Graph-capability conformance suite. |
| `RunLexicalConformance` | func | keep | Lexical-capability conformance suite. |
| `Factory` | type (func) | keep | Per-subtest store factory. |
| `Option` | type (func) | keep | Conformance-suite option. |
| `WithDimensionStrict` | func | keep | Option constructor. |

A test-support package, but a deliberate first-class export — backends in
sister repos import it to prove conformance. All keep.

### `tree`

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `DocumentTree` | type (struct) | keep | A document's hierarchical tree. |
| `Build` | func | keep | Builds a tree from a `Document` + chunks. |
| `BuildStored` | func | keep | Builds a tree from stored chunks. |
| `Node` | type (struct) | keep | A tree node. |

### `adapter/llmagent` (build tag: `llmagent`)

Behind `//go:build llmagent`. Inspected from source (`adapter/llmagent/model.go`,
`adapter/llmagent/tool.go`) — `go doc` has no `-tags` flag.

| Symbol | Kind | Disposition | Note |
|--------|------|-------------|------|
| `ModelAdapter` | type (struct) | keep | Adapts a core `llm-agent` `corellm.ChatModel` to the `generate.Model` seam. |
| `ModelAdapter.Inner` | field | keep | The wrapped core `corellm.ChatModel`. Exported so callers construct the adapter directly. |
| `ModelAdapter.Generate` | method | keep | Satisfies `generate.Model`. |
| `AsTool` | func | keep | Exposes a `rag.System` as an `agents.Tool` for the core agent framework. |

The build-tagged adapter is the deliberate, opt-in core-repo integration
seam; its three exported symbols (`ModelAdapter`, `ModelAdapter.Generate`,
`AsTool`) are all keep. (`ragToolArgs`, `ragToolSchema`, `ragToolHandler`,
`search`, `ask`, `modelFromSystem`, `max` in `tool.go` are correctly
lower-case package-private — no accidental export here.)

---

## Accidental-export confirmation

The exported surface above was reviewed **symbol by symbol**. There are
**no accidental exports** — every exported symbol of every importable
package (and `adapter/llmagent`) is a deliberate part of the v1.0 API and
carries the disposition **keep**, except the two ratified `eval` renames.
No symbol carries the **unexport** disposition.

In particular, the many small seam interfaces are confirmed as deliberate
plug-points, not accidents:

- `retrieve.EntityLinker`, `retrieve.QueryDecomposer`,
  `retrieve.SectionPlanner`, `retrieve.QueryEmbedder`,
  `retrieve.QueryPreprocessor`, `retrieve.Retriever` — retrieval-pipeline
  seams; callers and sister repos supply custom implementations.
- `graph.PathRanker`, `graph.EntityExtractor`, `graph.EntityResolver`,
  `graph.CommunityDetector`, `graph.CommunitySummarizer` — GraphRAG seams.
- `pack.TokenCounter` / `pack.SimpleCounter` — a legitimate seam:
  `rag/ask.go` uses `SimpleCounter{}` as the default whitespace counter and
  a caller may supply a real tokenizer. Both are deliberate — keep.
- `store.Store` plus the `store.CommunityStore` / `store.GraphStore` /
  `store.LexicalSearcher` capability interfaces — the storage-backend
  contract and its opt-in capability seams; sister-repo backends implement
  them and prove conformance via `store/storetest`.
- The `retrieve` concrete retrievers (`DenseRetriever`, `HybridRetriever`,
  `LexicalRetriever`, `GraphRetriever`, `MultiHopRetriever`,
  `StructureRetriever`, `VariantRetriever`) — the frozen concrete-retriever
  surface, intentionally exported for direct construction — keep.
- `embed.Embedder`, `generate.Model`, `prompt.Template`, `ingest.Source` /
  `Splitter` / `StreamingSource`, `rerank.Reranker` / `ScoringModel`,
  `guard.Redactor` / `InjectionScanner`, `agentic.QueryReformulator`,
  `eval.Asker` / `GlobalAsker` / `DriftAsker` / `Judge` / `Retriever` —
  the per-package abstraction seams; all deliberate, all keep.

Per KS-2, v1.0 is a freeze — this audit *records keep decisions*; it does
not trim or redesign the surface.

---

## Ratified naming decisions

### 1. `Ask` / `AskGlobal` / `AskDrift` vs `AskOptions` / `GlobalOptions` / `DriftOptions` — ratified as-is

The three answer-path methods are `System.Ask`, `System.AskGlobal`,
`System.AskDrift`; their option structs are `AskOptions`, `GlobalOptions`,
`DriftOptions`. The asymmetry (`AskGlobal`/`AskDrift` carry the `Ask`
prefix, `GlobalOptions`/`DriftOptions` do not) is **ratified as-is**, not
renamed, for v1.0.

Rationale:

- The option structs name the *answer mode* (`Global`, `Drift`), not the
  method. `GlobalOptions` reads as "options for the global answer mode" —
  which is exactly what it is.
- The set is internally consistent: every answer mode `X` has an
  `XOptions` struct (`Ask`→`AskOptions`, `Global`→`GlobalOptions`,
  `Drift`→`DriftOptions`).
- Renaming to `AskGlobalOptions` / `AskDriftOptions` would be pure churn —
  a breaking change for no clarity gain, contrary to KS-2's freeze intent.

Decision: **keep** all six symbols. No 28-02 action.

### 2. `eval.Evaluator` → `eval.RetrievalEvaluator`, `eval.Result` → `eval.RetrievalResult` (KS-4) — ratified, applied in 28-02

The base retrieval evaluator and its result are the only un-prefixed pair
in the `eval` package; the three later evaluators are already name-prefixed
(`GlobalEvaluator`/`GlobalEvalResult`, `DriftEvaluator`/`DriftEvalResult`,
`TriadEvaluator`/`TriadResult`). For symmetry, the base pair is renamed
`Evaluator`→`RetrievalEvaluator` and `Result`→`RetrievalResult`. The method
`(Evaluator).Run` keeps its name `Run`.

Reference scope (grep-confirmed, repo-wide — `eval` package only):

- `eval/eval.go` — the declarations (`type Result` line 65, `type Evaluator`
  line 81) and all internal returns (`(Evaluator).Run` line 88).
- `eval/graph.go` — `RunGraphAB` constructs `Evaluator{...}`.
- `eval/eval_test.go` — `eval.Evaluator{...}` literals and test names.
- `eval/drift.go`, `eval/global.go` — two doc-comment mentions.
- **No references in `examples/`, `contract/`, or any other package** —
  confirmed by `grep -rn "eval\." contract/*.go examples/*.go` (no matches).

The rename is entirely contained to the `eval` package. **Applied in slice
28-02.**

### 3. `ragkit` `doc.go` package-comment rewrite (KS-3) — ratified, applied in 28-02

The root package is `package ragkit` while the module path is
`github.com/costa92/llm-agent-rag`. The name is **kept** — `ragkit` is the
SDK's short brand name and a deliberate documentation anchor. The `doc.go`
package comment is rewritten to *state* that intent: `ragkit` is a
documentation anchor, and callers import the sub-packages, not the root.
This converts the name mismatch from an undocumented accident into a
recorded decision. No symbol change — `doc.go` stays exported-symbol-free.
**Applied in slice 28-02.**

---

## Contract-gate cross-check

`contract/contract_test.go` is the cross-repo compile-pin: it pins, at
compile time, the surface this repo exports for the core
`github.com/costa92/llm-agent` `rag/` facade to consume.

Confirmed by `grep -rn "eval\." contract/*.go`: the contract test
references **no `eval.` symbols at all** — in particular it does not
reference `eval.Evaluator` or `eval.Result`. Therefore the two ratified
renames in decision 2 touch **zero contract-pinned symbols**, and **no
coordinated core-repo PR is required** to land them. (Slice 28-02 still
runs the core-facade smoke as a safety net.)

---

## Release-readiness

Re-verified at the v1.0 freeze (slice 28-03). The codebase carries no
deferred-work markers, no local-dev dependency escape hatches, and no
dead-code stubs — the audited surface is the shipped surface.

### Zero deferred-work markers in non-test code

No `TODO` / `FIXME` / `XXX` / `HACK` / `Deprecated` marker exists in any
non-test Go file. Evidence:

```
$ grep -rn 'TODO\|FIXME\|XXX\|HACK\|Deprecated' --include='*.go' . | grep -v _test.go
(no output — exit status 1)
```

There is no carried-forward "finish later" work hiding behind a comment
marker; every exported symbol in this audit is fully implemented.

### No `replace` directives

`go.mod` contains no `replace` directive — the module resolves entirely
through tagged dependencies, with no local-filesystem escape hatch that
would break a fresh `go get`. Evidence:

```
$ grep -n '^replace\|	replace' go.mod
(no output — exit status 1)
```

### No dead code / no HTTP server, no CLI

The two genuine non-goals on the README "Not implemented yet" list were
re-verified against the codebase:

- **HTTP service layer** — there is no HTTP server. `grep` for
  `http.ListenAndServe` / `http.Server` / `http.Handle` / `ServeMux` /
  `http.HandlerFunc` across non-test code returns nothing. The single
  `net/http` importer, `rerank/httpmodel.go`, is an HTTP *client*
  (`HTTPScoringModel` POSTs to an external rerank API) — a `ScoringModel`
  seam, not a service the SDK exposes.
- **CLI** — there is no `cmd/` directory and no `package main` anywhere in
  the repo (`grep -rln '^package main'` over non-test code returns
  nothing); no `os.Args` consumer exists.

### README "Not implemented yet" list — now factually accurate

As of slice 28-03 the README "Not implemented yet" list names only the two
genuine deferred non-goals above (HTTP service layer, CLI). The two stale
entries — the `online-to-offline production-feedback workflow` and the
`cross-repo contract-drift CI gates` — were removed: both **shipped** (the
`feedback` package and `contract/contract_test.go` exist in this repo).
The list no longer claims a shipped feature is unimplemented.
