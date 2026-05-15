# Changelog

All notable changes to `github.com/costa92/llm-agent-rag` will be documented in
this file.

<!-- Keep a Changelog format: https://keepachangelog.com/en/1.1.0/ -->
<!-- Semver: https://semver.org/ -->

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
  the core `llm-agent/rag` facade consumes

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
