# External Integrations

**Analysis Date:** 2026-05-20

`llm-agent-rag` is a Go SDK, not a service. It does not call any
external API on its own — every external integration is expressed as
an interface ("seam") the caller fills in. The only sub-package that
talks to an external system directly is `postgres/`, which speaks to
PostgreSQL + pgvector through `pgx/v5`. The build-tagged
`adapter/llmagent/` is the only place that imports the sibling core
`github.com/costa92/llm-agent` agents framework.

Each subpackage entry below lists what the package is, what
interfaces / external backends it ties together, what built-in
implementations it ships, and where to find the entry point.

## Per-Subpackage Audit

### `store/` — vector and metadata storage seam

**Entry point:** `store/store.go`. Defines `store.Store` (the
mandatory storage interface) plus three optional capability
interfaces a backend may additionally satisfy and consumers
type-assert for:

- `store.Store` (`store/store.go:32-48`) — `Upsert`, `Search`,
  `List`, `Get`, `Remove`, `RemoveByFilter`, `Stats`. Search ranks
  by vector similarity.
- `store.LexicalSearcher` (`store/store.go:53-56`) — optional
  BM25/`ts_rank_cd` keyword search; retrieval falls back to an
  in-process scan when a store doesn't implement it.
- `store.GraphStore` (`store/store.go:62-74`) — optional
  entity/relation graph persistence and bounded-depth neighborhood
  traversal.
- `store.CommunityStore` (`store/store.go:83-104`) — optional
  GraphRAG community-hierarchy persistence and lazy
  community-report cache (`PutCommunityReport` / `CommunityReport`).

**Shipped backends:**

| Backend | Where | Implements |
|---|---|---|
| `store.InMemoryStore` | `store/inmemory.go` (and sibling `store/graph.go`, `store/community.go`) | `Store` + all three optional capabilities (`LexicalSearcher`, `GraphStore`, `CommunityStore`). Cosine-similarity vector search, regex-free token-overlap lexical fallback, in-memory adjacency, in-memory community map. Default when `rag.Options.Store` is nil. |
| `postgres.Store` | separate `postgres/` subpackage — see below | `Store`, `LexicalSearcher`, `GraphStore`, `CommunityStore` |

**Conformance harness:** `store/storetest/storetest.go` — a `Factory`
+ `RunConformance` (12 subtests) and `RunGraphConformance` /
`RunCommunityConformance` suites every backend in this repo, sibling
repos, and third-party stores runs against (`store/storetest/storetest.go:1-39`).

**No other stores are shipped.** There is no Pinecone, Weaviate,
Chroma, Qdrant, Milvus, Elasticsearch, OpenSearch, Redis, or sqlite
backend in the repo — these are explicitly callers' responsibility,
held to the `store/storetest` contract.

### `embed/` — embedding-backend seam

**Entry point:** `embed/embedder.go`. Defines `embed.Embedder` —
`Embed(ctx, text) (Vector, error)` plus `Dimension() int`
(`embed/embedder.go:13-18`). `embed.Vector` is `[]float32`
(`embed/vector.go:4`).

**Shipped backend:**

- `embed.HashEmbedder` (`embed/hash.go:13-38`) — a deterministic
  FNV-bucket bag-of-words hasher. Pure stdlib, no model, no network.
  Intended for tests and offline runs. Default when
  `rag.Options.Embedder` is nil. Default dimension 32.

**No external embedding providers are shipped.** There is no OpenAI,
Cohere, Voyage, BGE, sentence-transformers, or any other embedder
client. The SDK is provider-neutral by design — a caller plugs in
their own `Embedder` implementation that wraps whatever HTTP/SDK
client they prefer.

Helper: `embed.CosineSimilarity(a, b)` (`embed/hash.go:42-63`) — the
vector-comparison utility every store uses to rank `Search` hits.

### `retrieve/` — dense / sparse / hybrid / graph retrieval

**Entry point:** `retrieve/retrieve.go`. Defines `retrieve.Retriever`
plus query-shaping seams `QueryDecomposer`, `QueryPreprocessor`,
`QueryEmbedder`, `EntityLinker`, `SectionPlanner`
(`retrieve/retrieve.go:1-6` package doc).

**Shipped concrete retrievers** (every one of them pure stdlib):

| Retriever | File | Strategy |
|---|---|---|
| Dense retriever | `retrieve/retrieve.go` | Vector cosine via `store.Store.Search` |
| Lexical retriever | `retrieve/retrieve.go` | Token-overlap or Okapi BM25; defers to `store.LexicalSearcher` when implemented, else in-process scan |
| Hybrid retriever | `retrieve/retrieve.go` | Reciprocal-rank fusion (RRF) over up to four signals: dense, lexical, structure, and graph. Configurable fusion constant; `FusionAttribution` records per-signal rank contributions |
| Multi-hop retriever | `retrieve/multihop.go` | Decomposes a compound query via `QueryDecomposer` (`HeuristicDecomposer` splits on the conjunction "and"; `LLMDecomposer` uses a `generate.Model`) and merges sub-retrievals |
| Graph retriever | `retrieve/graph.go` | `EntityLinker` (default `LexicalEntityLinker`) → bounded neighborhood expansion (depth ≤ 2) → proximity-decay scoring. Optionally enumerates ranked simple paths via `graph.PathRanker` |
| Structure / route-aware retrieval | `retrieve/retrieve.go` | `RoutePath`, auto-route candidate selection, `GapAwareSectionPlanner` (default `SectionPlanner`), confidence-gap adaptive fanout, per-route `SearchTrajectory` |
| Variant retriever | `retrieve/retrieve.go` | Wraps any base retriever; merges and dedups results across multiple `QueryVariants` produced by MQE / HyDE |

**Query expansion:** `retrieve.LLMExpansionPreprocessor` orchestrates
MQE (multi-query expansion) and HyDE (hypothetical-document
expansion) — both stateless helpers in `advanced/llm.go`.

### `graph/` — GraphRAG construction, communities, paths

**Entry point:** `graph/graph.go`. Defines `Entity`, `Relation`,
`Graph`, `Subgraph`, and the `EntityExtractor` seam
(`graph/graph.go:1-59`).

**Shipped backends — all in-process, pure stdlib (except for two
seams that use the `generate.Model` callers supply):**

| Capability | Built-ins | File |
|---|---|---|
| Entity / relation extraction | `LLMEntityExtractor` (over `generate.Model`, pipe-delimited prompt with lenient parsing); `DictionaryEntityExtractor` (zero-LLM gazetteer + co-occurrence) | `graph/extract.go`, `graph/dictionary.go` |
| Canonicalization | `graph.Canonicalize` — exact-match `(name, type)` entity merge with source-chunk provenance | `graph/canonicalize.go` |
| Entity resolution (fuzzy) | `NoopEntityResolver` (default); `EmbeddingEntityResolver` — opt-in embedding-similarity merge of near-duplicate entities, same-type-only, conservative threshold ≥ 0.92 | `graph/resolve.go` |
| Community detection | `LouvainDetector` — deterministic pure-stdlib Louvain with coarsening passes; `LabelPropagationDetector` — faster single-level alternative | `graph/louvain.go`, `graph/community.go` |
| Community summaries | `LLMCommunitySummarizer` (over `generate.Model`), with `CommunityContentHash` as the cache key | `graph/summary.go` |
| Path ranking | `WeightedPathRanker` — deterministic bounded-DFS enumeration; composite score over path length × `Relation.Weight` × provenance overlap; tie-break on entity-ID sequence | `graph/path.go` |

**No external graph database.** Graph storage rides on top of the
existing `store.GraphStore` capability — either `InMemoryStore`'s
in-memory adjacency or `postgres.Store`'s `_entities` / `_relations`
tables with recursive-CTE traversal. The v0.4 / v0.5 / v0.6
changelog entries explicitly defer "a dedicated graph database" to
post-v1.0+ (`CHANGELOG.md:100`, `CHANGELOG.md:159-161`).

### `ingest/` — document sources, splitters, import pipeline

**Entry point:** `ingest/source.go` and `ingest/import.go`.

**Seams:**

- `ingest.Source` (`ingest/source.go:11-14`) — finite batch source
  (`Documents(ctx) []Document`)
- `ingest.StreamingSource` (`ingest/source.go:32-37`) — open-ended
  source (`Next(ctx) (Document, error)`, returns `io.EOF` when drained)
- `ingest.Splitter` (`ingest/splitter.go:29-32`) — chunking seam

**Shipped sources / helpers:**

- `ingest.StaticSource(docs...)` (`ingest/source.go:25-30`) — wraps
  a `[]Document` literal
- `ingest.SourceFunc` — function-typed adapter
- `ingest.Collect(ctx, streamingSrc)` — drains a `StreamingSource`
  into a slice

**Shipped splitters:**

- `CharSplitter` (`ingest/splitter.go:34-49`) — fixed-size
  character window with overlap. Default chunk size 500 chars
  when `rag.Options.MaxChars` is unset.
- `MarkdownSplitter` (`ingest/splitter.go:41-49`) — heading-aware,
  emits `heading`, `heading_level`, `section_path` metadata for
  downstream structure-aware retrieval.

**No file / URL / HTML / PDF source** is shipped. The package
deliberately ships zero "what to ingest" connectors — callers wire
filesystem readers, S3 walkers, HTTP fetchers, or PDF/HTML parsers
behind the `Source` or `StreamingSource` seam themselves. Lineage
fields (`SourceID`, `Version`, `Checksum`, `EmbeddingVersion`,
`ReplaceSource`) on `ingest.Document` and `ingest.ImportOptions`
support replace-by-source ingestion when callers do.

### `rerank/` — reranking layer

**Entry point:** `rerank/rerank.go`. `Reranker` is the seam;
`ScoringModel` is the model-scoring seam
(`rerank/rerank.go:1-5` package doc).

**Shipped rerankers:**

| Reranker | File | Strategy |
|---|---|---|
| `NoopReranker` | `rerank/rerank.go:84-94` | Identity — used to keep the rerank stage observable when disabled |
| `HeuristicReranker` | `rerank/rerank.go:96-120+` | Network-free: retrieval score plus a lexical query-overlap boost |
| `ModelReranker` | `rerank/rerank.go` | Wraps a `ScoringModel`; surfaces full `RerankScore` explainability (`InputRank`, `OutputRank`, `RankDelta`) |

**Shipped scoring model:**

- `HTTPScoringModel` (`rerank/httpmodel.go:14-23`) — `net/http`
  client that POSTs `{model, query, documents[]}` JSON and parses a
  Cohere / Jina / TEI-style `{results: [{index, relevance_score}]}`
  response. **The only place in the entire module that calls an
  external HTTP service directly** — and even this is opt-in (the
  caller constructs and supplies a `HTTPScoringModel{Endpoint,
  Model, Token}`). It uses only the standard library; there is no
  vendor-specific SDK.

### `generate/` — text-generation seam

**Entry point:** `generate/model.go`. Defines a minimal
`generate.Model` interface — one method, `Generate(ctx, req) (resp,
error)` — and the value types `Message`, `Request`, `Response`,
`Usage` (`generate/model.go:11-14`, `generate/types.go`).

**Generate ships no concrete model.** The package is intentionally a
pure seam: no OpenAI, no Anthropic, no Bedrock, no Ollama, no
provider adapter at all. `generate/` is pure interface +
value-types and does not import any other package outside
`context` (verified — `generate/types.go` imports nothing,
`generate/model.go` imports only `context`).

**How does `generate/` relate to the core `llm-agent` `ChatModel`?**
`generate/` is **self-contained**. The mapping to the core's
`corellm.ChatModel` interface is the responsibility of the
build-tagged `adapter/llmagent/` (see below) — it lives entirely in
the adapter so the core SDK does not have to import
`github.com/costa92/llm-agent`. Anything in the standalone SDK that
needs an LLM (graph extraction, community summarization, query
decomposition, MQE / HyDE, reranking via `HTTPScoringModel` is the
exception) talks to a `generate.Model` the caller plugs in.

### `postgres/` — PostgreSQL + pgvector backend

**Entry point:** `postgres/postgres.go`. The only subpackage in the
module that imports non-stdlib code:

- `github.com/jackc/pgx/v5`, `pgx/v5/pgconn`, `pgx/v5/pgxpool`
- `github.com/pgvector/pgvector-go`, `pgvector-go/pgx`

**Connection model:** caller-owned `*pgxpool.Pool`. `postgres.New(pool,
postgres.Config{Table, Dimension, TextSearchConfig})`
(`postgres/postgres.go:62-79`) constructs the store; the caller is
expected to wire `postgres.RegisterTypes` (`postgres/postgres.go:84-86`)
into the pool's `pgxpool.Config.AfterConnect` hook so each pooled
connection registers the pgvector codec. The canonical wiring is
documented in `docs/production-deployment.md:17-44`. Without this,
upserts and searches error with `unknown type vector`.

**Migration:** `(*Store).Migrate(ctx)` (`postgres/postgres.go:93-161`)
is idempotent and called on every startup. It creates the
following objects, parameterized on `Config.Table` (validated against
`isSafeIdent` to block injection through table names):

| Object | Backing table / column | Purpose |
|---|---|---|
| `vector` extension | n/a | pgvector |
| `<Table>` table | `id TEXT PK, namespace TEXT, doc_id TEXT, title TEXT, section_id TEXT, section_path TEXT[], heading TEXT, heading_level INT, content TEXT, metadata JSONB, embedding vector(<Dimension>)` | Chunks. Default `Table` is `"chunks"`. |
| `<Table>_namespace_idx` (btree) | `<Table>(namespace)` | Namespace scoping |
| `<Table>.content_tsv` (generated `tsvector`) | `GENERATED ALWAYS AS to_tsvector('<TextSearchConfig>', content) STORED` (default `english`) | BM25-style lexical search via `ts_rank_cd` for `LexicalSearcher` |
| `<Table>_content_tsv_idx` (GIN) | `<Table>(content_tsv)` | Lexical search index |
| `<Table>_entities` | `(namespace TEXT, id TEXT, name TEXT, type TEXT, description TEXT, source_chunk_ids TEXT[], metadata JSONB)` PK `(namespace, id)` | `GraphStore` entities |
| `<Table>_relations` | `(namespace TEXT, id TEXT, source TEXT, target TEXT, relation TEXT, description TEXT, source_chunk_ids TEXT[], weight DOUBLE PRECISION)` PK `(namespace, id)` | `GraphStore` relations |
| `<Table>_rel_source_idx`, `<Table>_rel_target_idx`, `<Table>_ent_name_idx` | endpoint / lower(name) | Traversal and name lookup |
| `<Table>_communities` | `(namespace, community_id, level, parent_id, entity_ids TEXT[], relation_ids TEXT[])` PK `(namespace, community_id)` | `CommunityStore` hierarchy |
| `<Table>_community_reports` | `(namespace, community_id, title, summary, content_hash)` PK `(namespace, community_id)` | `CommunityStore` lazy report cache |

**Capability satisfaction:** `*postgres.Store` declares compile-time
that it implements `store.Store`, `store.LexicalSearcher` (in
`postgres/postgres.go:45-48`), `store.GraphStore`
(`postgres/graph.go:17`), and `store.CommunityStore`
(`postgres/community.go:16`).

**Traversal bounds:** `Neighborhood` is hard-capped at depth 2
(`postgres/graph.go:23` `maxGraphDepth = 2`) and at 4096 rows
(`maxNeighborhoodRows`), enforced inside the recursive CTE — the
same invariant `InMemoryStore` enforces in process.

**Live-database tests:** `postgres/postgres_test.go` and
`postgres/postgres_conformance_test.go` skip when the
`LLM_AGENT_RAG_PG_URL` env var is unset (`postgres/postgres_test.go:17`
defines the constant; tests `t.Skipf` at lines 22-24). CI does not
run these — the `tsvector`, graph, and community paths are
env-gated and not exercised against a live DB in CI (noted in the
v0.5.0 changelog entry, `CHANGELOG.md:155-157`).

### `obs/` — observability hooks (in-process metrics)

**Entry point:** `obs/obs.go`. Pure stdlib (`context`, `sync/atomic`,
`time` — no external observability library).

**What it provides:**

- `obs.Metrics` (`obs/obs.go:38-43`) — per-run wall-clock
  `TotalDuration`, ordered `[]StageTiming` (e.g.
  `"retrieve"`, `"rerank"`, `"pack"`, `"generate"`, `"embed"`),
  `CallCounts{Embed, Generate}`, and `TokenUsage` with an
  `Estimated` flag distinguishing model-reported from
  token-counter-estimated counts.
- `obs.Counter` (`obs/obs.go:48-95`) — atomic embed/generate
  call counter that rides on `context.Context` via
  `obs.WithCounter` / `obs.CounterFrom`. The rag `System.New`
  wraps the caller's `Embedder` and `Model` in `countingEmbedder` /
  `countingModel` (`rag/instrument.go:16-39`) so MQE / HyDE
  expansions and inner-retriever embeddings are all counted
  transparently.

**Does it emit OpenTelemetry spans natively?** **No.** `obs/`
imports zero OTel packages. OTel is the sibling repo
`llm-agent-otel`'s job — it consumes the `rag.Observer` callbacks
(`OnImport`, `OnRetrieve`, `OnAsk`) and the `obs.Metrics` /
`retrieve.Trace` / `rag.Diagnostics` payloads through them.
`rag/observer.go:33-37` defines the seam; `rag/system.go:1-9`
package doc says the same; the README "Implemented" list
explicitly attributes the OTel consumption to `llm-agent-otel`. The
standalone SDK is OTel-free at runtime.

### `eval/` — evaluation harness

**Entry point:** `eval/eval.go`. The CI-gate evaluation framework.

**Headline metrics** (`eval/eval.go:44-52`):

- Retrieval — `PrecisionAtK`, `RecallAtK`, `MRR`, `GroundingAtK`
- Generation (RAG Triad legs 2 and 3) — `MeanGroundedness`,
  `MeanAnswerRelevance`

**Evaluators:**

| Evaluator | File | Scope |
|---|---|---|
| `RetrievalEvaluator` | `eval/eval.go:73-80` | Pure retrieval — wraps a `rag.System` (via the narrow `Retriever` interface) and scores against gold doc / chunk IDs |
| `TriadEvaluator` | `eval/triad.go:11-40` | Retrieval plus generation through a `Judge`; produces a `TriadResult` |
| `GlobalEvaluator` | `eval/global.go:13-28` | `rag.System.AskGlobal` map-reduce path |
| `DriftEvaluator` | `eval/drift.go:18-22` | `rag.System.AskDrift` hybrid global-then-local path |
| `RunGraphAB` | `eval/graph.go:18-30` | A/B over `EnableGraph` off vs on, reporting `RecallDelta` / `MRRDelta` |

**`Judge` seam** (`eval/judge.go:35-38`) — `Groundedness` and
`AnswerRelevance` in [0,1]. Built-in: `LLMJudge`
(`eval/judge.go:42-50`) prompts a `generate.Model` for a strict
JSON judgment.

**Datasets:** inline `eval.Dataset` literals or JSONL via
`eval.LoadJSONL(path)` (`eval/loader.go:29-39`). The JSONL schema
mirrors `eval.Example` (`eval/eval.go:29-35`):
`{query, namespace?, gold_doc_ids[], gold_chunk_ids[], notes?}`,
with a `top_k` field on the first line that defines `Dataset.TopK`.

### `guard/` — content safety

**Entry point:** `guard/redact.go` and `guard/inject.go`. Pure
stdlib (`regexp`, `strings`).

**Two passes, both regex-based:**

- `guard.PIIRedactor` (`guard/redact.go:38-60`) — ordered
  `Rule[]` of `{Kind, *regexp.Regexp, Placeholder}`. Applied to
  ingested content before chunking; emits `[]Redaction` tallies.
  Caller-configurable rule set; `NewPIIRedactor` provides the
  built-in default rules.
- `guard.PatternScanner` (`guard/inject.go:31-43`) — prompt-injection
  signature scanner over retrieved content. Driven by a
  `SanitizeMode` (`neutralize` / `drop`) at the `rag.System` level.

### `feedback/` — production-to-eval feedback loop

**Entry point:** `feedback/feedback.go:1-40` package doc.
**Purpose:** capture flagged `rag.Ask` traces as JSONL lines that
`eval.LoadJSONL` reads back, so production misses become regression
cases on the next eval pass.

**What it integrates with:** `rag.Observer.OnAsk` is the recommended
attachment point (the doc-comment example wires a
`feedback.Recorder` straight into the observer). `feedback.OpenFile`
returns a concurrent-safe writer; `feedback.BuildExample` projects
a `rag.Trace` into an `eval.Example`. Pure stdlib (`bufio`,
`encoding/json`, `os`, `sync`).

### `agentic/` — self-correcting retrieval loop

**Entry point:** `agentic/correct.go:1-38` package doc.
**Purpose:** `CorrectiveAsker` — an answer / judge / reformulate /
retry loop bounded by a max-attempts cap.

**What it integrates with:** an `Asker` (the standalone
`rag.System` satisfies it), a `Judge` from `eval` (typically
`eval.LLMJudge`), and a `QueryReformulator` (`agentic/correct.go:29-33`
defines the seam; `LLMReformulator` is the built-in over
`generate.Model`).

### `advanced/` — query-transformation helpers

**Entry point:** `advanced/llm.go:1-14` package doc.
**Purpose:** the two stateless LLM-backed query-transformation
helpers used by `retrieve.LLMExpansionPreprocessor`:

- `ExpandQuery(ctx, model, query, n)` — multi-query expansion (MQE).
  Asks a `generate.Model` for `n` semantically-equivalent
  alternatives, dedup'd and dropped-on-empty.
- `GenerateHypothetical(ctx, model, query)` — HyDE-style
  hypothetical-answer synthesis for embedding-driven recall.

Both are pure functions over `generate.Model` — no state, no
network of their own.

### `api/` — exported-API snapshot baseline

**Entry point:** `api/v1.snapshot.txt`. **Not a Go package** — it
contains no `.go` files. The text file is the committed baseline of
every exported symbol of every importable package (plus the
build-tagged `adapter/llmagent`), generated by
`internal/apisnapshot` and diffed at `go test` time. Any rename,
removal, or signature change of an exported symbol fails the test
until either the change is reverted or the baseline is regenerated
with the `-update` flag (deliberate, v1-additive changes only).

### `contract/` — cross-repo compile-time pin

**Entry point:** `contract/contract_test.go`. **Test-only package
(`contract_test`)**, no shipped Go binary. It declares
`var _ = …` references to every standalone symbol the current
core `github.com/costa92/llm-agent` integrations consume — a
compile failure here means the cross-repo surface has drifted and a
coordinated PR in the core repo is required (the same pattern the
package doc comment spells out at `contract/contract_test.go:1-14`).
It is narrower than `internal/apisnapshot`: that gate covers the
whole intra-repo surface; `contract` covers only the cross-repo
subset.

### `pack/` — token-budgeted context packing

**Entry point:** `pack/pack.go:1-5` package doc.
**Purpose:** assemble retrieved chunks into a prompt context under
a token budget. `Packer` is the central seam; `GreedyTokenPacker`
is the built-in greedy under-budget packer.

**Tokenizer seam:** `TokenCounter` (`pack/pack.go:17-20`). The
built-in `SimpleCounter` (`pack/pack.go:24-50`) is a whitespace +
CJK heuristic — a caller swaps it for a real tokenizer (`tiktoken`,
sentencepiece, etc.) by implementing `TokenCounter`. The package
itself does not import any tokenizer.

### `prompt/` — prompt-template seam

**Entry point:** `prompt/template.go:1-18`.
**Purpose:** `Template.Render(ctx, RenderContext) (generate.Request,
error)` — the prompt-template seam. Built-in: `DefaultQATemplate`
(`prompt/default.go`), the SDK's default QA template a caller can
replace by setting `rag.Options.Template` or
`rag.AskOptions.Template`. No template engine dependency
(`text/template` is not used in the default template's body
construction).

### `tree/` — structured-markdown document trees

**Entry point:** `tree/tree.go:1-12` package doc.
**Purpose:** build a section/heading tree from a `Document` and
its chunks (`Build`) or from chunks already in a store
(`BuildStored`). Drives the
`retrieve.EnableTreeExpansion` /
`retrieve.ExpansionDepth` knobs — the retriever expands a hit
into its neighboring tree nodes when enabled.

**What it integrates with:** the `ingest` chunk metadata
(`heading`, `heading_level`, `section_path`) written by
`MarkdownSplitter`, and `store.StoredChunk.SectionPath` /
`Heading` / `HeadingLevel` for the post-import path.

### `adapter/llmagent/` — optional bridge to core `llm-agent`

**Build tag:** `//go:build llmagent` (`adapter/llmagent/model.go:1`,
`adapter/llmagent/tool.go:1`). **Not compiled by default**; explicit
`go test -tags llmagent ./adapter/...` is the only path that
exercises it (and the CI workflow runs this in its final step,
`test.yml:50-57`).

**What it does:** two adapters between this SDK and
`github.com/costa92/llm-agent v0.5.0`:

- `ModelAdapter` (`adapter/llmagent/model.go:19-21`) wraps a core
  `corellm.ChatModel` so it satisfies `generate.Model` — making a
  core chat model usable as the `rag.System` answer generator.
- `AsTool(r *rag.System) agents.Tool`
  (`adapter/llmagent/tool.go:21-28`) exposes the `rag.System` as an
  `agents.NewFuncTool` for the core agent loop, with actions
  `add_text`, `search`, `ask`, `remove`, `stats`. JSON schema lives
  in `ragToolSchema()` (`adapter/llmagent/tool.go:31-48`).

This is the **only** place in the entire module that imports
`github.com/costa92/llm-agent`. The default core build does not
depend on it; the README, `docs/core-compatibility.md`, and the
v0.1.1 changelog entry all spell out the module-boundary
discipline.

## APIs and External Services

**The SDK calls no external API on its own at runtime.** The two
seams that involve a network call when used are:

- `rerank.HTTPScoringModel` — caller-supplied endpoint URL +
  optional bearer token; stdlib `net/http` only
- `postgres.Store` — caller-supplied `*pgxpool.Pool` (PostgreSQL
  `>= 14` with the `vector` extension)

Everything else that touches an LLM does so by calling a
`generate.Model` the **caller** plugged in.

## Data Storage

**Vector store:** `store.InMemoryStore` (process-local, no
persistence) by default; `postgres.Store` (PostgreSQL `>= 14` with
pgvector) when wired.

**Metadata store:** **No separate metadata service.** Chunk metadata
travels on `store.StoredChunk.Metadata map[string]any`; in the
postgres backend that field maps to the `metadata JSONB` column on
the same chunks table.

**File / object storage:** **None.** The SDK does not read or write
files except through the caller's `Source` implementation, the
`feedback` writer (a caller-supplied filesystem path), and
`eval.LoadJSONL` (also a caller-supplied path).

**Caching:** **None at the SDK level.** GraphRAG community reports
are cached on the `store.CommunityStore` capability — i.e. in
the same backing store as the chunks (in-memory map for
`InMemoryStore`, `_community_reports` table for `postgres.Store`).
The cache key is the deterministic `graph.CommunityContentHash`.

## Authentication & Identity

**None at the SDK level.** Authentication for outbound HTTP calls
is the caller's concern: `HTTPScoringModel{Token: "..."}` sets a
`Authorization: Bearer <token>` header on rerank requests
(`rerank/httpmodel.go`), but the SDK itself never reads tokens from
the environment or any credential store.

## Monitoring & Observability

**Error tracking:** none built in. The SDK returns errors; callers
decide.

**Logs:** none built in. There is no log call anywhere in the
`store/`, `embed/`, `retrieve/`, `rag/`, `generate/`, `eval/`,
`obs/`, or `postgres/` packages. Observability is delivered through
`rag.Observer` callbacks plus the `obs.Metrics` and `retrieve.Trace`
payloads — the consumer chooses where to send them.

**Metrics / tracing:** see `obs/` above. Native OTel emission is
deferred to the sibling `llm-agent-otel` repo via the
`rag.Observer` seam.

## CI / CD & Deployment

**Hosting:** N/A — this is a library, not a deployable service.

**CI pipeline:** GitHub Actions — `.github/workflows/test.yml`
(every push and PR to `master` / `main`) and
`.github/workflows/release-precheck.yml` (every push and PR to
`release/**` branches). See STACK.md for the per-step contents.

## Environment Configuration

**Required env vars at runtime:** none.

**Optional env vars consumed only by tests:**

- `LLM_AGENT_RAG_PG_URL` — DSN of a live PostgreSQL `>= 14` with
  pgvector; the `postgres/*_test.go` live cases skip when unset.

**Secrets location:** none. No `.env*` files exist in the repo.

## Webhooks & Callbacks

**Outgoing webhooks:** none built in.

**Inbound webhooks:** none built in.

**In-process callbacks:** `rag.Observer.{OnImport, OnRetrieve,
OnAsk}` (`rag/observer.go:33-37`) — synchronous, nil-safe,
fire-after-success-only. These are the SDK's seam for OTel /
metrics / feedback consumers.

---

*Integration audit: 2026-05-20*
