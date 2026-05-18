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
	Namespace     string
	Documents     int
	Chunks        int
	ChunkIDs      []string
	EmbedCount    int
	ReplaceSource bool
	RemovedChunks int
	Metrics       obs.Metrics
	Redactions    []guard.Redaction
}

// Observer holds optional callbacks that fire after each top-level rag
// operation completes successfully. Each callback is nil-safe; unset
// callbacks are skipped without allocation.
//
// Errors returned by the underlying operation short-circuit before the
// callback fires — observers see only successful runs. Error
// observability is the consumer's responsibility via the returned error.
type Observer struct {
	OnImport   func(ctx context.Context, trace ImportTrace)
	OnRetrieve func(ctx context.Context, trace retrieve.Trace)
	OnAsk      func(ctx context.Context, trace Trace)
}
