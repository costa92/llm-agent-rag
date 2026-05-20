// Package ragkit is the short brand name for the standalone
// retrieval-augmented generation SDK whose module path is
// github.com/costa92/llm-agent-rag.
//
// The root package is a deliberate documentation anchor only: it exports
// no symbols. Callers import the sub-packages directly. All sub-packages
// listed below are part of the frozen v1.x public surface; the
// build-tagged adapter/llmagent and the non-importable internal/ tree
// are the only carve-outs.
//
// Pipeline core:
//   - rag       — orchestration layer (Import / Retrieve / Ask / Observer)
//   - ingest    — documents, sources, splitters
//   - embed     — embedder seam + default HashEmbedder
//   - store     — vector store seam + InMemoryStore
//   - postgres  — PostgreSQL + pgvector backend (opt-in deps)
//   - retrieve  — hybrid / structure-aware retrieval + search trajectory
//   - rerank    — heuristic and model-scoring rerankers
//   - pack      — token-budget-aware context packing
//   - prompt    — prompt-template seam + default QA template
//   - generate  — text-generation seam (caller-provided model)
//   - tree      — document-tree primitives for structured markdown
//
// GraphRAG:
//   - graph     — Louvain / LabelProp community detection, community
//     summaries, weighted multi-hop path ranking
//
// Answer-path extras (also frozen v1):
//   - advanced  — stateless query-expansion helpers (MQE, HyDE)
//   - agentic   — CorrectiveAsker bounded retry loop
//   - feedback  — JSONL writer for flagged Asks
//   - guard     — PII redaction + prompt-injection screen
//
// Quality / cross-cutting:
//   - eval      — retrieval + RAG-Triad answer evaluation harness
//   - obs       — in-process metrics + Observer hook for OTel sidecar
//   - contract  — cross-repo compile-time pin of the facade subset
//   - api       — committed v1.snapshot.txt baseline (no exported symbols)
//
// The ragkit name diverges from the llm-agent-rag module path on
// purpose, to give the SDK a concise import-free identity; this is a
// recorded decision, not an accidental mismatch.
package ragkit
