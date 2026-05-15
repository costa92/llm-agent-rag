// Package contract pins, at compile time, the cross-repo surface this
// repo exports for the core `github.com/costa92/llm-agent` rag/ facade
// to consume.
//
// Any rename or removal of a symbol referenced here breaks `go build`
// on this file, which means `go test ./...` becomes the contract gate
// — no separate workflow needed.
//
// Adding to this file is a deliberate act: it widens the contract
// surface and the core facade may need a coordinated update. Removing
// from this file is a breaking change for the core facade and requires
// a coordinated PR in `github.com/costa92/llm-agent` first.
//
// See docs/core-compatibility.md for the higher-level discussion.
package contract_test

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/prompt"
	"github.com/costa92/llm-agent-rag/rag"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

// TestContract_ConsumedByCoreFacade pins every standalone symbol the
// core `llm-agent/rag` facade currently consumes. Compile success is
// the gate; this test has no runtime assertions.
func TestContract_ConsumedByCoreFacade(t *testing.T) {
	// embed —
	var (
		_ embed.Vector
		_ embed.Embedder = (*embed.HashEmbedder)(nil)
		_                = embed.NewHashEmbedder
		_                = embed.CosineSimilarity
	)

	// generate —
	var (
		_ generate.Message
		_ generate.Request
		_ generate.Response
		_ generate.Model
	)

	// ingest —
	var (
		_ ingest.Document
		_ ingest.Chunk
		_ ingest.ImportOptions
		_ ingest.Splitter = ingest.CharSplitter{}
		_                 = ingest.MetadataSourceIDKey
		_                 = ingest.MetadataSectionPathKey
		_                 = ingest.MetadataHeadingKey
		_                 = ingest.MetadataHeadingLevelKey
	)

	// prompt —
	var (
		_ prompt.Template = prompt.DefaultQATemplate{}
	)

	// rag (standalone) —
	var (
		_ rag.System
		_ rag.Options
		_ rag.SearchOptions
		_ rag.AskOptions
		_ rag.Trace
		_ rag.Diagnostics
		_ rag.Answer
		_ rag.Citation
		_ rag.Observer
		_ rag.ImportTrace
		_ = rag.New
		_ = rag.ErrEmptyQuery
	)

	// store —
	var (
		_ store.Store = (*store.InMemoryStore)(nil)
		_ store.StoredChunk
		_ store.Query
		_ store.Hit
		_ store.Filter
		_ store.Stats
		_ = store.NewInMemoryStore
		_ = store.ErrNotFound
		_ = store.ErrDimensionMismatch
	)

	// retrieve — exposed via rag.Trace.RoutePolicy etc.
	var (
		_ retrieve.Trace
		_ retrieve.RoutePolicyTrace
		_ retrieve.RouteCandidate
		_ retrieve.TrajectoryStep
		_ retrieve.Request
	)

	// keep the test body referenced — context import sanity check
	if ctx := context.Background(); ctx == nil {
		t.Fatal("unreachable")
	}
}
