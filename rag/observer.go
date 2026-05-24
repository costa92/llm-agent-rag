package rag

import (
	"context"

	"github.com/costa92/llm-agent-rag/guard"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

// ImportTrace captures observable data from a successful Import call.
// Consumers (typically OTel adapters in llm-agent-otel) receive this via
// Observer.OnImport.
type ImportTrace struct {
	Namespace     string            // Namespace is the namespace the documents were imported into.
	Documents     int               // Documents is the number of documents imported.
	Chunks        int               // Chunks is the number of chunks produced.
	ChunkIDs      []string          // ChunkIDs are the IDs of every produced chunk.
	EmbedCount    int               // EmbedCount is the number of embedding calls made.
	ReplaceSource bool              // ReplaceSource is true when the import replaced an existing source.
	RemovedChunks int               // RemovedChunks is the number of chunks removed by a replace.
	Metrics       obs.Metrics       // Metrics is the cost-and-latency record for the import.
	Redactions    []guard.Redaction // Redactions records PII redactions applied during ingest.
}

// Observer holds optional callbacks that fire after each top-level rag
// operation completes successfully. Each callback is nil-safe; unset
// callbacks are skipped without allocation.
//
// Errors returned by the underlying operation short-circuit before the
// callback fires — observers see only successful runs. Error
// observability is the consumer's responsibility via the returned error.
type Observer struct {
	OnImport   func(ctx context.Context, trace ImportTrace)    // OnImport fires after a successful Import.
	OnRetrieve func(ctx context.Context, trace retrieve.Trace) // OnRetrieve fires after a successful Search.
	OnAsk      func(ctx context.Context, trace Trace)          // OnAsk fires after a successful Ask.
	// OnPlanFollowups fires once per reflection round after the active-
	// retrieval QueryPlanner emits a non-empty plan. It runs BEFORE any
	// follow-up retrieval dispatches, so observers can record the
	// pre-grade signal and the planner's intent in one place.
	//
	// scores carries the pre-graded relevance scores on the seed hit
	// set (also stashed back into ReflectionRoundDiagnostics.ChunkScores).
	// planned is the planner's emitted queries in planner output order,
	// already truncated by the per-round and per-Ask caps.
	//
	// Nil-safe: an unset callback skips the dispatch without allocation.
	// Thread safety: this callback fires from a single goroutine even
	// under ParallelFollowups, but observers should still be safe to
	// invoke from any goroutine because some host adapters re-dispatch.
	OnPlanFollowups func(ctx context.Context, question string, scores []ChunkScore, planned []string)
	// OnFollowupRetrieve fires once per executed follow-up retrieval —
	// regardless of dispatch mode (sequential or parallel). hits is the
	// retrieved hit slice (nil on error); err is the retrieval error
	// (nil on success). Active retrieval is fail-open: an error here is
	// observed but does not abort the round.
	//
	// Nil-safe: an unset callback skips the dispatch without allocation.
	// Thread safety: under ReflectionOptions.ParallelFollowups=true the
	// callback MAY fire concurrently from multiple goroutines. Caller
	// implementations must be thread-safe (e.g., guard shared state
	// with a sync.Mutex or use sync/atomic).
	OnFollowupRetrieve func(ctx context.Context, query string, hits []store.Hit, err error)
	// OnGenerateUsage fires once per successful generate.Model.Generate
	// call made inside a top-level Ask/AskGlobal/AskDrift. The stage
	// argument identifies which leg of the pipeline issued the call.
	// Standard stages: "ask" (answer leg, including the LLMExpansion
	// query preprocessor), "reflection_decision" (the reflection-mode
	// decision call), "grader" (PromptGrader chunk scoring),
	// "planner" (PromptQueryPlanner active-retrieval follow-ups).
	//
	// Hook fires ONLY on success — partial usage on error is unreliable
	// and is suppressed. Custom user-supplied Grader / QueryPlanner
	// implementations are NOT auto-wrapped; only the shipped
	// PromptGrader / PromptQueryPlanner have their Model rebuilt during
	// New(opts). Custom implementations must wrap their own model to
	// fire this hook.
	//
	// Nil-safe: an unset callback skips the dispatch without allocation.
	// Thread safety: under v1.2.1 ParallelFollowups and v1.4.0
	// AnswerBenchmark.Parallelism, the hook MAY fire concurrently from
	// multiple goroutines. Caller implementations must be thread-safe
	// (e.g., guard shared state with a sync.Mutex).
	OnGenerateUsage func(ctx context.Context, stage string, usage obs.TokenUsage)
}
