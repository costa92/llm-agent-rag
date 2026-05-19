// Package ragkit is the short brand name for the standalone
// retrieval-augmented generation SDK whose module path is
// github.com/costa92/llm-agent-rag.
//
// The root package is a deliberate documentation anchor only: it exports
// no symbols. Callers import the sub-packages directly — rag, retrieve,
// store, embed, ingest, generate, pack, prompt, rerank, graph, eval, and
// the rest — each of which carries its own package documentation. The
// ragkit name diverges from the llm-agent-rag module path on purpose, to
// give the SDK a concise import-free identity; this is a recorded
// decision, not an accidental mismatch.
package ragkit
