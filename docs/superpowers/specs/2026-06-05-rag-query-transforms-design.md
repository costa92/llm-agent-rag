# RAG Query/Document Transform Gaps Design

## Goal

Add three independent query/document transformation capabilities to `llm-agent-rag`, each gated so the zero value / unconfigured state reproduces current behavior byte-for-byte:

- **A. Step-back Prompting** — generate an abstracted, higher-level query variant retrieved alongside the original.
- **B. Conversation-history-aware rewriting (指代消解)** — resolve coreference/ellipsis in multi-turn questions into a standalone query before retrieval.
- **C. Contextual Compression** — compress retrieved chunks to query-relevant content between rerank and pack, via a swappable `Compressor` seam with extractive and abstractive implementations.

The three are independent and SHOULD ship as three separate plans (A → B → C, increasing difficulty). They share one principle: **unconfigured = current behavior unchanged**, matching the existing `EnableMQE` / `Reflection nil` / `MaxTotalTokens<=0` conventions.

## Scope

Included:

- A: `advanced.GenerateStepBack`, `SearchOptions.EnableStepBack`, `retrieve.Request.EnableStepBack`, wiring into `LLMExpansionPreprocessor`.
- B: `System.AskConversation` entry point, `rag.QueryCondenser` seam + `LLMCondenser` default, `advanced.CondenseQuery` prompt helper, `Options.QueryCondenser`.
- C: new `compress` package with `Compressor` interface + `NoopCompressor` / `ExtractiveCompressor` / `AbstractiveCompressor`, `Options.Compressor`, `SearchOptions.EnableCompression`, pipeline insertion in `askRound`.
- Tests (TDD) and API snapshot updates for each component.

Excluded:

- Step-back as a *replacement* for the original query (it is additive only).
- `AskOptions.History` field (rejected in favor of a dedicated `AskConversation` entry point).
- Compression inside reranking or packing seams (it is a distinct stage).
- Multi-turn history flowing into the *generation* stage — history is used only to produce a standalone retrieval query.
- Reflection / active-retrieval changes; AskGlobal / AskDrift changes.

## Existing Constraints

- `rag.System.Ask(ctx, question string, opts AskOptions)` takes a single string; there is no conversation-history input today.
- The query-shaping seam already exists: `retrieve.QueryPreprocessor.Process(ctx, req)` with `LLMExpansionPreprocessor` producing MQE + HyDE variants gated by `SearchOptions.EnableMQE/EnableHyDE`. Downstream `VariantRetriever` already retrieves across multiple variants and merges — no retrieval-layer change is needed to add a variant.
- `generate.Message` is `{Role string, Content string}` (`generate/types.go:4`) and is the universal message type — every `model.Generate` call uses `[]generate.Message`. `rag` already publicly depends on `generate` (`Options.Model generate.Model`).
- `askRound` runs a straight-line `retrieve -> rerank -> pack -> sanitize -> render -> generate`. Rerank finishes at `rag/ask.go:436` (updating `rankedHits`); `packer.Pack` is called at `rag/ask.go:445`. The gap between them is the compression insertion point.
- `advanced` package holds stateless LLM query-transform helpers (`ExpandQuery`, `GenerateHypothetical`) taking a `generate.Model`; new prompt helpers follow the same shape.
- `pack.Trace.TruncatedChunkIDs` is the established style for recording per-chunk content reduction.

## Chosen Architecture

### Component A — Step-back Prompting

Step-back has the same shape as HyDE: generate one extra query variant, retrieve it alongside the original, let `VariantRetriever` merge. Reuse the existing preprocessor seam.

- `advanced/llm.go`: add `GenerateStepBack(ctx context.Context, model generate.Model, query string) (string, error)`. Prompt instructs the model to produce a more general, higher-level question that surfaces background knowledge (e.g. "How many points does Lv5 need?" → "How is the user level system designed?"). Stateless, same pattern as `GenerateHypothetical`.
- `rag/options.go`: add `SearchOptions.EnableStepBack bool`. Add the matching field on `retrieve.Request` and thread it through request construction.
- `retrieve.LLMExpansionPreprocessor.Process`: after the HyDE branch, when `req.EnableStepBack` is set, append the step-back query as an additional variant via `appendUniqueQueries`. Requires `p.Model != nil` (return `advanced.ErrModelRequired` otherwise, matching MQE/HyDE).
- No change to `Retriever` implementations. The original specific query and the abstract query both participate in recall; the abstract query is recorded in `Trace.QueryVariants` like any other variant.

Tests: `EnableStepBack=true` → `Process` returns `[original, step-back]`; `false` → byte-identical to current output; nil model with the flag set → `ErrModelRequired`.

### Component B — Conversation-history-aware rewriting (`AskConversation`)

Dedicated entry point + "condense then delegate to `Ask`" — reuses the entire existing pipeline with zero duplication.

- History type: **reuse `generate.Message`** (no new `rag.Turn`). Rationale: identical shape would make `rag.Turn` a speculative structural clone; `rag` already publicly depends on `generate`; the condenser builds `generate.Request{Messages}` directly so history passes through with zero conversion; consistency with the universal message idiom.
- New seam `rag.QueryCondenser`:
  ```go
  type QueryCondenser interface {
      // Condense rewrites question into a standalone retrieval query using
      // history. With empty history it MUST return question unchanged.
      Condense(ctx context.Context, history []generate.Message, question string) (string, error)
  }
  ```
- Default `LLMCondenser{Model generate.Model}` uses `advanced.CondenseQuery(ctx, model, history, question)` — prompt: "Given the conversation history, rewrite the latest question into a standalone, complete retrieval query; if no rewrite is needed, return it unchanged." Empty history short-circuits to `question` with no model call.
- `Options.QueryCondenser`: nil + Model set → default `LLMCondenser`; nil + no Model → passthrough condenser returning `question` unchanged.
- New entry `System.AskConversation(ctx context.Context, history []generate.Message, question string, opts AskOptions) (Answer, error)` in `rag/conversation.go`: condense → call `Ask(ctx, standaloneQuery, opts)`. **Empty history → condense returns `question` unchanged → byte-equivalent to `Ask`.**
- Observability: record original question + condensed query on `Answer` diagnostics, following the existing diagnostics style, so a mis-rewrite is debuggable.

Tests: coreference multi-turn case rewrites to a standalone query; empty history passthrough; no-Model condenser does not error; `AskConversation` with empty history == `Ask`.

### Component C — Contextual Compression

New `compress` package, inserted **after rerank, before pack** (hits are sorted and converged — best ROI).

- Interface:
  ```go
  type Compressor interface {
      // Compress shortens each hit's Content to query-relevant material.
      // ID, Score, and Metadata are preserved so citation provenance holds.
      Compress(ctx context.Context, query string, hits []store.Hit) ([]store.Hit, error)
  }
  ```
- Implementations:
  - `NoopCompressor` (default) — passthrough; preserves current behavior.
  - `ExtractiveCompressor` — sentence-split each chunk, score sentences against the query with the embedder, keep top-relevant sentences. No extra LLM cost, no hallucination, citations remain verbatim source. Matches the "keep only relevant fragments" intent.
  - `AbstractiveCompressor{Model generate.Model}` — one LLM call per chunk producing a query-focused summary. Higher compression ratio; hallucination risk; higher cost.
- Wiring: `Options.Compressor` (nil → `NoopCompressor`); `SearchOptions.EnableCompression bool` (mirrors `EnableRerank`). In `askRound`, insert a compress stage between rerank (`ask.go:436`) and `packer.Pack` (`ask.go:445`), operating on `rankedHits`, timed as an `obs.StageTiming{Stage: "compress"}`.
- Trace: record pre/post character counts and compressed chunk IDs, following `pack.Trace.TruncatedChunkIDs` style.

Tests: extractive keeps relevant sentences and drops irrelevant ones; abstractive output is non-empty and shorter than input; `EnableCompression=false` → byte-identical pipeline; Noop default unchanged.

## Component Summary

| Component | Primary files | Surface | Risk |
|-----------|---------------|---------|------|
| A. Step-back | `advanced/llm.go`, `rag/options.go`, `retrieve/retrieve.go` | smallest (reuses preprocessor seam) | low |
| B. AskConversation | `rag/conversation.go`, `advanced/llm.go`, `rag/options.go` | new entry + condense seam | medium |
| C. Compression | new `compress/`, `rag/ask.go`, `rag/options.go` | new package + pipeline insertion | medium |

## Rejected Alternatives

- **B via `AskOptions.History` field** — smaller surface, but a dedicated `AskConversation` entry point makes the multi-turn responsibility explicit. Chosen by the user.
- **New `rag.Turn` type for history** — rejected as a speculative clone of `generate.Message`; see Component B rationale.
- **C extractive-only** — rejected in favor of a `Compressor` interface with both extractive and abstractive implementations so callers choose the cost/fidelity trade-off.
