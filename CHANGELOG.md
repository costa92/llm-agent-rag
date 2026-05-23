# Changelog

All notable changes to `github.com/costa92/llm-agent-rag` will be documented in
this file.

<!-- Keep a Changelog format: https://keepachangelog.com/en/1.1.0/ -->
<!-- Semver: https://semver.org/ -->

## [1.1.0] - 2026-05-23

Minor release closing Track B of the Self-RAG reflection milestone.
Additive, no breaking changes — covered by the v1.x additive-only
promise.

### Added

- `rag.Grader` interface (per-chunk relevance/support scoring),
  inspired by Self-RAG / RAGLab `[ISREL]` / `[ISSUP]` reflection
  tokens. Two implementations ship in-package:
  - `rag.NoopGrader` — deterministic neutral-0.5 scores, the safe
    default when no real grader is configured.
  - `rag.PromptGrader{Model generate.Model}` — LLM-driven scorer
    that asks the model to emit `score=<float>` lines. Clamps
    out-of-range values to `[0,1]` and fails open (0.5 + raw text as
    reason) on unparseable replies. Propagates model-level transport
    errors so reflection's `FailOpen` logic can distinguish a
    deterministic neutral from a real outage.
- `rag.ChunkScore` (`HitID` / `Relevance` / `Support` / `Reason`) —
  the per-chunk grading record carried on
  `ReflectionRoundDiagnostics.ChunkScores` and
  `ReflectionRoundTrace.ChunkScores`. Populated only when
  `ReflectionOptions.EnableChunkGrading` is true and a `Grader` is
  configured; empty otherwise.
- `rag.Options.Grader` — the System-level wiring slot. A nil Grader
  with grading enabled falls back to `NoopGrader` so the wiring is
  always functional.
- `rag.SelectionMode` enum:
  - `SelectionModeLastRound` (= 0, default) — keeps the v1.0.x
    last-round-wins adoption.
  - `SelectionModeBestByScore` — adopts the round with the highest
    weighted aggregate `ChunkScores`. Loop semantics (when to stop,
    when to rewrite) are unchanged — this flag only affects which
    round's answer/citations are returned at the end. Ties break by
    earliest round index for determinism.
- `ReflectionOptions` gains six additive fields, all zero-defaults
  preserve v1.0.x behavior:
  - `EnableChunkGrading` (default `false`)
  - `GraderRelevanceWeight` (default `0` → `0.5` when active)
  - `GraderSupportWeight` (default `0` → `0.5` when active)
  - `SelectionMode` (default `SelectionModeLastRound`)
  - `AdaptiveRetrieval` (default `false`) — forces one extra round
    when the latest round's max chunk relevance is below
    `AdaptiveRetrievalThreshold`, subject to `MaxRounds`.
  - `AdaptiveRetrievalThreshold` (default `0` → `0.6` when active)

### Changed

- The reflection loop now records per-chunk `ChunkScores` on every
  round when `EnableChunkGrading=true`, and applies the configured
  `SelectionMode` to pick the adopted round at the end. Defaults
  (everything off) leave behavior byte-identical to v1.0.6 — the 22+
  existing reflection tests stay green unchanged.

### Compatibility

- Pure additive: every existing test stays green without
  modification.
- API snapshot diff: 22 lines added, 0 removed, 0 renamed.
- stdlib-only invariant preserved (no new third-party imports).

## [v1.0.6] - 2026-05-23

Additive, no breaking changes — covered by the v1.x additive-only
promise.

### Added

- `ReflectionRoundDiagnostics` and `ReflectionRoundTrace` now capture
  per-round routing intel: `RoutePath`, `AutoRoutePath`,
  `AutoRouteCandidates`, `SearchTrajectory`, `GraphTrace` (on
  Diagnostics); `AutoRoutePath` (on Trace). Previously only the last
  round's routing was visible in `Answer.Trace`; multi-round
  reflections now expose each round's routing decision separately,
  enabling "why did round N pick a different route than round N-1"
  debugging. (D4 closure)
- `ReflectionRoundDiagnostics.RawDecisionText` and `DecisionPrompt`
  preserve the model's raw reflection-decision reply and the prompt
  sent to the model, for post-hoc debugging of decision drift. Empty
  in rule mode and in hybrid rounds where the rule path stopped
  first. (D5 closure)
- `ReflectionRoundTrace.RawDecisionText` mirrors the diagnostic-side
  equivalent for observer-facing trace consumers.

### Changed

- `parseReflectionDecision` now accepts any case for decision values
  (`Stop`, `STOP`, `Continue`, `Rewrite_and_continue`, etc.). The
  protocol documented in `reflectionDecisionPrompt` still asks for
  lowercase, but real-world model output drifts; we normalize the
  value with `strings.ToLower` before the enum match. (D3 closure)

### Compatibility

- Pure additive: existing reflection tests stay green unchanged.
- API snapshot increment is additive only (7 new fields, 0 removals,
  0 renames).
- stdlib-only invariant preserved (no new third-party imports).

## [v1.0.5] - 2026-05-23

Additive, no breaking changes — covered by the v1.x additive-only
promise.

### Added

- `postgres.VectorIndex` enum (`VectorIndexNone` / `VectorIndexIVFFlat`
  / `VectorIndexHNSW`) plus `Config.VectorIndex`, `Config.IVFFlatLists`,
  and `Config.HNSWConstructionM` fields. When set, `Migrate` issues an
  idempotent `CREATE INDEX IF NOT EXISTS` for the embedding column —
  `USING ivfflat (embedding vector_cosine_ops) WITH (lists = N)` or
  `USING hnsw (embedding vector_cosine_ops) WITH (m = M)`. Default
  zero-value preserves v1.0.4 behavior (no vector index — existing
  databases unaffected). On ~100K-chunk tables IVFFlat lists=100 drops
  nearest-neighbor query latency from ~1.5s to ~80ms (~19x speedup, per
  roadmap model — actual gains are environment-dependent). HNSW
  requires pgvector >= 0.5. (P1-1)

## [v1.0.4] - 2026-05-23

### Changed

- `HybridRetriever.Retrieve` now fans out Dense/Lexical/Structure/Graph
  retrievers concurrently. Wall-clock latency for hybrid queries drops
  from sum-of-4 to max-of-4 (typical 2-4× reduction). Behavior is
  byte-identical: fusion (RRF) is deterministic-by-construction, and
  the Dense > Lexical > Structure > Graph error precedence is preserved
  by waiting for all goroutines and selecting by original order. (P1-15)

## [v1.0.3] - 2026-05-23

Additive, no breaking changes — covered by the v1.x additive-only
promise.

### Added

- `embed.BatchEmbedder` — optional sibling-capability interface for
  embedders that natively support multi-text batches. The `rag.System`
  importer (`Import` / `ImportFrom`) type-asserts the configured
  embedder against `BatchEmbedder` and, when satisfied, collapses every
  pending chunk across every document into a single `EmbedBatch` call —
  replacing N sequential per-chunk `Embed` calls with one round-trip.
  Plain `Embedder` callers see no change: the per-chunk loop is
  byte-identical to v1.0.2 behavior. The counting instrumentation
  wrapper (`countingEmbedder`) gains a `countingBatchEmbedder` sibling
  so the capability survives the instrumentation layer and the type
  assertion still succeeds for caller-supplied `BatchEmbedder`s. (P1-16)

## [v1.0.1] - 2026-05-20

Maintenance release. No public-API change — covered by the v1.x
additive-only promise.

### Changed

- bump the build-tagged `adapter/llmagent/` back-edge to
  `github.com/costa92/llm-agent v0.5.0`, picking up the
  ecosystem-aligned core. The default (untagged) build remains
  stdlib-only outside the `postgres` subpackage. (KE-2 — back-edge
  bumps are allowed under v1.x because they are gated behind the
  `llmagent` build tag and reach no exported symbol on the default
  build.)

## [v1.0.0] - 2026-05-21

The v1.0 API freeze. **Not a feature release** — no new features, no
behavior change, and no new dependency anywhere in this release. v1.0.0
freezes the `github.com/costa92/llm-agent-rag` public API and adopts the
Go module import-compatibility promise: within the `v1.x` series the
exported API is **additive-only** — exported symbols are not renamed,
removed, or re-signed, and any breaking change requires a new major
version (`/v2`). The full policy is written in
[`docs/compatibility.md`](docs/compatibility.md).

The `postgres` subpackage remains the only non-stdlib island; everything
else stays stdlib-only.

### Changed

The **final** breaking changes before the freeze — the last renames the
API will ever take in the `v1.x` line:

- `eval.Evaluator` → `eval.RetrievalEvaluator` and `eval.Result` →
  `eval.RetrievalResult`. The retrieval-evaluation type and result are now
  prefixed, for symmetry with the already-prefixed answer-path evaluators
  `eval.GlobalEvaluator`, `eval.DriftEvaluator`, and `eval.TriadEvaluator`.
  Callers update the type names; the method sets and behavior are
  unchanged.
- the `ragkit` root package comment (`doc.go`) was rewritten to document
  the root as a deliberate documentation anchor — it exports no symbols;
  callers import the sub-packages (`rag`, `retrieve`, `store`, `embed`,
  `ingest`, `generate`, `eval`, and the rest) directly. No symbol changed;
  noted here because the package's documented role is now explicit.

### Added

Additive, non-breaking v1.0 work — documentation and a stability gate, no
runtime change:

- [`docs/compatibility.md`](docs/compatibility.md) — the written Go-module
  compatibility promise: what the `v1.x` additive-only guarantee covers,
  what is explicitly outside it, and how a future `/v2` would be handled.
- `docs/api-audit-v1.0.md` — the freeze-time exported-surface audit: every
  exported symbol of every importable package (plus the build-tagged
  `adapter/llmagent`) inventoried and classified keep / rename / unexport.
- complete package- and exported-symbol-level doc-comment coverage across
  the module — every importable package and every exported symbol now
  carries documentation.
- the `api/v1.snapshot.txt` exported-surface snapshot gate — a committed
  baseline of the frozen v1 API, regenerated and diffed by an ordinary
  stdlib `go test` (`internal/apisnapshot`). It fails any unintended
  exported-API change, complementing the narrower cross-repo `contract`
  compile-pin: `contract` pins the core-facade subset across repos, the
  snapshot diffs the whole intra-repo surface.
- the repository is now `gofmt`-clean.

## [v0.6.0] - 2026-05-20

Minor release closing the v0.9 GraphRAG refinements milestone (Phases
26-27). Builds on v0.7 Tier-1 and v0.8 Tier-3 GraphRAG with path-ranked
subgraph evidence and DRIFT hybrid search. Additive and opt-in — default
behavior is unchanged. No new dependencies and no graph database; the
`postgres` subpackage remains the only non-stdlib island.

### Added

- path-ranking and subgraph-as-evidence (Phase 26):
  - new `graph.RankedPath` type and `graph.PathRanker` seam
  - `graph.WeightedPathRanker` — a deterministic pure-stdlib ranker of
    multi-hop simple paths within a `Subgraph` (bounded-DFS enumeration;
    composite score over path length, `Relation.Weight`, and provenance
    overlap; total entity-ID-sequence tie-break)
  - an opt-in `PathRanker` field on `retrieve.GraphRetriever`;
    `retrieve.GraphTrace` gains additive `Paths` and `EvidenceSubgraph`
    fields, surfaced through `rag.Diagnostics`. With path mode off,
    `GraphRetriever.Retrieve` is byte-identical to v0.7/v0.8.
- DRIFT search (Phase 27):
  - `rag.System.AskDrift` — a hybrid answer path: a global "primer" pass
    for broad orientation, a hard-bounded local follow-up loop
    (round cap 3, terminating on no new follow-up entities), and a
    synthesis step. A separate answer path — it is not a `Retriever` and
    not a mode flag on `Ask`/`AskGlobal`.
  - `rag.DriftOptions` and a `Diagnostics.Drift` block (primer communities,
    rounds run, per-round entity IDs, consulted reports)
  - `eval.DriftEvaluator` — a DRIFT-answer evaluation harness over the
    RAG-Triad / `LLMJudge` path (groundedness, answer-relevance)
  - `docs/graphrag.md` finalized for the full GraphRAG spectrum

### Notes

- Incremental community maintenance remains deferred to v1.0+ — v0.8's
  full re-detection on re-ingest is correct and fast at this SDK's scale;
  revisit only if profiling shows community detection dominating re-ingest.
- Deferred to v1.0+: incremental community maintenance, claim/covariate
  extraction, a dedicated graph database.

## [v0.5.0] - 2026-05-20

Minor release closing the v0.8 GraphRAG Tier-3 milestone (Phases 23-25).
Builds on the v0.7 Tier-1 GraphRAG with hierarchical community detection,
lazy community summaries, a map-reduce global-search answer path, and
embedding-similarity fuzzy entity resolution. Additive and opt-in — default
behavior is unchanged. No new dependencies and no graph database: community
detection is pure stdlib, the `postgres` subpackage remains the only
non-stdlib island.

### Added

- community detection (Phase 23):
  - new `graph.Community` type and `graph.CommunityDetector` seam
  - `graph.LouvainDetector` — a deterministic pure-stdlib Louvain detector
    producing a community hierarchy via coarsening passes
  - `graph.LabelPropagationDetector` — a faster single-level alternative
  - `store.CommunityStore` optional-capability interface (sibling of
    `store.GraphStore`) — `GraphSnapshot`, `UpsertCommunities`,
    `Communities` — with a pure-stdlib in-memory implementation and a
    `postgres` implementation (`_communities` table)
  - community detection wired as a post-canonicalization `Import` stage
    (`rag.Options.CommunityDetector`), re-detected on a `ReplaceSource`
    re-ingest
- community summaries (Phase 24):
  - `graph.CommunityReport` type, `graph.CommunitySummarizer` seam, and
    `graph.LLMCommunitySummarizer` over `generate.Model`
  - `graph.CommunityContentHash` — a deterministic community-membership
    hash used as the report cache key
  - lazy report generation (LazyGraphRAG model): reports are generated at
    query time and cached, persisted via `CommunityStore`
    (`PutCommunityReport` / `CommunityReport`; `postgres`
    `_community_reports` table)
- global search (Phase 24):
  - `rag.System.AskGlobal` — a map-reduce global-search answer path over
    community reports (community selection, lazy reports, per-community
    map, score-ranked reduce); a separate path from `Ask` — no retrieve,
    rerank, or pack
  - `rag.System.PrewarmCommunityReports` — opt-in eager report generation
  - `Diagnostics.Global` global-search attribution; `GraphTrace.CommunityIDs`
    local-retrieval community attribution
- fuzzy entity resolution (Phase 25):
  - `graph.EntityResolver` seam, `graph.NoopEntityResolver` (the default),
    and `graph.EmbeddingEntityResolver` — embedding-similarity merge of
    near-duplicate entities, same-type-only, run as an opt-in pre-pass
    before `Canonicalize`
- evaluation (Phase 25):
  - `eval.GlobalEvaluator` — a global-search evaluation harness over the
    RAG-Triad / `LLMJudge` path (groundedness, answer-relevance)
  - `docs/graphrag.md` updated for Tier-3

### Notes

- The `postgres` `_communities` / `_community_reports` paths are env-gated;
  like the v0.5 `tsvector` and v0.7 graph paths they are not yet exercised
  against a live database in CI.
- `EmbeddingEntityResolver` has documented false-positive risk; it ships
  conservative (high threshold, same-type-only) and opt-in.
- Deferred to v0.9: DRIFT search, incremental community maintenance, and
  path-ranking / subgraph-as-evidence.

## [v0.4.0] - 2026-05-19

Minor release closing the v0.7 GraphRAG milestone (Phases 20-22). Tier-1
lightweight GraphRAG: a knowledge graph extracted from ingested documents
and retrieved by traversal, fused as a fourth signal alongside dense,
lexical, and structure retrieval. Additive and opt-in — default behavior
is unchanged. No new dependencies, and no graph database: the `postgres`
subpackage remains the only non-stdlib island.

### Added

- knowledge-graph construction (Phase 20):
  - new `graph` package — `Entity` / `Relation` / `Graph` and an
    `EntityExtractor` seam
  - `graph.LLMEntityExtractor` over `generate.Model` — pipe-delimited
    extraction prompt with lenient parsing of malformed model output
  - `graph.DictionaryEntityExtractor` — a deterministic zero-LLM extractor
    (gazetteer terms + co-occurrence relations)
  - `graph.Canonicalize` — exact-match `(name, type)` entity merge with
    source-chunk provenance and relation-endpoint resolution
  - graph extraction wired as a post-split `Import` stage, surfaced on
    `ingest.ImportResult.Graph`
- graph storage (Phase 21):
  - `store.GraphStore` optional-capability interface (mirroring
    `store.LexicalSearcher`) — `UpsertGraph`, `RemoveGraphBySource`,
    `Neighborhood`, `FindEntities`
  - a pure-stdlib in-memory adjacency implementation and a `postgres`
    implementation over `entities` / `relations` tables with
    recursive-CTE traversal — no graph database
  - hard-bounded traversal: depth cap 2 plus a per-hop fan-out cap,
    enforced in both implementations
  - incremental reconciliation on `ReplaceSource` re-ingest —
    provenance-based removal, union-merge, and garbage-collection
  - `storetest.RunGraphConformance` shared conformance suite
- graph-traversal retrieval (Phase 22):
  - `retrieve.EntityLinker` seam and `LexicalEntityLinker`
  - `retrieve.GraphRetriever` — query entity linking, bounded neighborhood
    expansion, and proximity-decay scoring
  - graph fused into `HybridRetriever` as a fourth RRF signal: a `Graph`
    field, `FusionAttribution.GraphRank`, an `EnableGraph` toggle, and
    graph attribution in `retrieve.Trace` / `rag.Diagnostics.GraphTrace`
  - `eval.RunGraphAB` — a graph-on/off A/B over the evaluation harness
  - a deterministic GraphRAG worked example and `docs/graphrag.md`

### Notes

- The `postgres` graph path is env-gated; like the v0.5 `tsvector` path it
  is not yet exercised against a live database in CI.
- Deferred to v0.8: MS-GraphRAG community detection / summaries,
  global/DRIFT search, and fuzzy/embedding entity resolution.

## [v0.3.0] - 2026-05-18

Minor release closing the v0.6 production-grade-retrieval milestone
(Phases 14-19). No new dependencies — entirely standard library plus the
existing seams; the `postgres` subpackage remains the only non-stdlib
island.

### Added

- BM25 lexical retrieval (Phase 14):
  - Okapi BM25 ranking in the in-memory lexical path, replacing
    token-overlap scoring; configurable `retrieve.BM25Params`
  - optional `store.LexicalSearcher` capability interface, implemented by
    the `postgres` store via a `tsvector`/`ts_rank_cd` path
  - configurable RRF fusion constant plus per-signal `FusionAttribution`
    in the retrieval trace
- model-based reranking (Phase 15):
  - `rerank.ScoringModel` seam, `ModelReranker`, and `HTTPScoringModel`
    (a `net/http` rerank-API client)
  - rerank explainability: `rerank.RerankScore` / `Trace.Scores` surfaced
    through `rag.Diagnostics.RerankScores`
- generation-side evaluation — the RAG Triad (Phase 16):
  - `eval.Judge` seam and `LLMJudge` (LLM-as-judge for groundedness and
    answer-relevance)
  - `eval.TriadEvaluator` assembling retrieval + generation metrics into a
    `TriadResult`, with a JSONL report and a RAG-Triad CI gate
- cost and latency observability (Phase 17):
  - `obs` package — `Metrics` with per-stage durations, embed/generate
    call counts, and token usage, recorded on `rag.Diagnostics`,
    `retrieve.Trace`, `ingest.ImportResult`, and `rag.ImportTrace`
  - `generate.Usage` token-accounting field on `generate.Response`
- content safety (Phase 18):
  - `guard` package — `PIIRedactor` redacts PII from ingested content
    before chunking, with a configurable entity rule set
  - `guard.PatternScanner` prompt-injection filter with a `SanitizeMode`
    (neutralize/drop), applied to retrieved chunks before prompt assembly
- agentic retrieval (Phase 19):
  - `retrieve.MultiHopRetriever` decomposes a compound query into
    sub-queries and merges the sub-retrievals (`QueryDecomposer` seam)
  - `agentic` package — `CorrectiveAsker` self-correcting retrieval loop
    that detects low grounding and retries with reformulated queries
    under a bounded cap

### Changed

- lexical retrieval now uses Okapi BM25 instead of token-overlap scoring
- `generate.Response` gains an additive `Usage` field
- `rag.Diagnostics`, `retrieve.Trace`, `ingest.ImportResult`, and
  `rag.ImportTrace` carry additional observability and safety fields
  (`Metrics`, `Redactions`, `InjectionFindings`, `Hops`) — all additive

## [v0.2.0] - 2026-05-15

Minor release closing the v0.5 RAG-productionization milestone
(Phases 11-13). First release with non-stdlib dependencies — confined
to the `postgres` subpackage.

### Added

- structure-aware retrieval policy (Phase 11):
  - subtree route-path constraints and automatic section route selection
  - multi-candidate auto-route planning with confidence/evidence metadata
  - executable route policy: confidence threshold + top-N fanout
  - route-policy rationale and selected-route trace markers
  - confidence-gap adaptive fanout — converge on a strong top-1, fan out
    when the top two routes are close
  - per-route `SearchTrajectory` output attributing hits and sections to
    each executed route
  - pluggable `retrieve.SectionPlanner` interface with
    `GapAwareSectionPlanner` as the default
- `postgres` package — PostgreSQL + pgvector implementation of
  `store.Store`, behind the first non-stdlib deps in this module
  (`pgx/v5`, `pgvector-go`)
- `store/storetest.RunConformance` — shared 12-subtest conformance suite
  every `store.Store` implementation runs against
- `rag.Observer{OnImport, OnRetrieve, OnAsk}` hook surface plus
  `rag.ImportTrace`, for external tracing without touching internals
- `eval` package — retrieval/grounding evaluation framework with
  precision@k / recall@k / MRR / grounding@k metrics and a JSONL loader
- `feedback` package — concurrent-safe writer that captures flagged Asks
  as JSONL eval examples (online-to-offline regression feedback loop)
- `contract` package — compile-time gate pinning the cross-repo surface
  the core `llm-agent/rag` facade consumed at the time. Historical note:
  that facade has since been removed from the current core tree.

### Fixed

- `adapter/llmagent` rag tool: `add_text` now generates a unique base
  document ID per call when the caller omits one, preventing silent
  chunk-ID collision across namespaces

## [v0.1.4] - 2026-05-14

### Added

- retrieval-layer query expansion orchestration via `SearchOptions`:
  - `EnableMQE`
  - `EnableHyDE`
  - `MQECount`
- `retrieve.LLMExpansionPreprocessor` for policy-layer MQE/HyDE query rewriting
- `retrieve.VariantRetriever` for multi-query merge/dedup over any base retriever
- rerank and context-packing seams:
  - `rerank.Reranker`
  - `pack.Packer`
- default heuristic reranking and greedy token-budget-aware context packing

### Changed

- default `rag.System` retrieval now routes through policy-aware preprocessor
  plus variant-merging retrieval instead of requiring adapter-side query loops
- optional `adapter/llmagent` search/ask now delegates MQE/HyDE handling to the
  standalone retrieval layer
- default `rag.Ask(...)` now supports rerank plus prompt-evidence packing with
  traceable chunk selection

### Fixed

- removed duplicate MQE/HyDE orchestration logic between adapter and core
  retrieval paths

## [v0.1.3] - 2026-05-14

Patch release for Phase 9 source-aware ingestion groundwork.

### Added

- additive source-lineage fields on `ingest.Document`:
  - `SourceID`
  - `Version`
  - `Checksum`
  - `EmbeddingVersion`
- automatic lineage metadata propagation into chunk and stored-chunk metadata
- `MarkdownSplitter` with section-aware metadata:
  - `heading`
  - `heading_level`
  - `section_path`
- `ImportOptions.ReplaceSource` for replace-by-source ingestion behavior
- `Store.RemoveByFilter(...)` plus default `InMemoryStore` support

### Changed

- standalone import can now remove existing chunks for the same `source_id`
  before upserting new content when `ReplaceSource` is enabled

## [v0.1.2] - 2026-05-14

Patch release for Phase 8 RAG contract hardening.

### Added

- real metadata filtering in the default `InMemoryStore`
- explicit `SecurityFilters` plumbing in standalone retrieval queries
- machine-readable answer citations, diagnostics, and retrieval trace fields

### Changed

- standalone retrieval now distinguishes normal caller filters from mandatory
  security trimming inputs

## [v0.1.1] - 2026-05-14

Patch release for CI stability.

### Fixed

- replaced `go mod tidy` drift enforcement with a module-boundary check so
  `adapter/llmagent` build-tagged imports do not force a hard dependency on
  `github.com/costa92/llm-agent`
- kept standalone core packages publishable without modifying `go.mod`

## [v0.1.0] - 2026-05-14

Initial standalone RAG SDK release.

### Added

- standalone Go module: `github.com/costa92/llm-agent-rag`
- abstract import via `ingest.Source`, `Import`, and `ImportFrom`
- deterministic default `ingest.CharSplitter`
- default `embed.HashEmbedder`
- default `store.InMemoryStore`
- abstract generation seam via `generate.Model`
- prompt customization via `prompt.Template`
- `rag.System` orchestration for import, retrieve, ask, remove, and stats
- `advanced` package for:
  - multi-query expansion (`MQE`)
  - HyDE-style hypothetical answer generation
- optional `adapter/llmagent` bridge behind build tag `llmagent`

### Notes

- Core module is intentionally publishable without a hard dependency on
  `github.com/costa92/llm-agent`.
- `adapter/llmagent` is a development bridge and requires a temporary local
  `require` / `replace` when tested in isolation.
- This release is intended as a reusable `v0.1` baseline, not a stability
  guarantee.
