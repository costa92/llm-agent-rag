package rag

import (
	"context"

	"github.com/costa92/llm-agent-rag/guard"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/retrieve"
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
}
