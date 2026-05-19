# llm-agent-rag

Standalone Go RAG SDK with abstract import, retrieval, custom LLM generation,
and custom prompt-template seams. Production-ready with a PostgreSQL +
pgvector backend, an observation hook for OTel, and an evaluation
framework as a `go test` regression gate.

## Documentation

- [Production deployment](./docs/production-deployment.md) — pgvector
  setup, pool config, observer wiring, operational notes
- [Backend selection](./docs/backend-selection.md) — in-memory vs
  postgres, conformance contract, adding a new backend
- [Core compatibility](./docs/core-compatibility.md) — relationship
  to `github.com/costa92/llm-agent` and the optional adapter

## Scope

This SDK is designed around three primary workflows:

- import documents from abstract sources
- retrieve ranked chunks for a query
- generate answers with a caller-provided model and prompt template

The default packages have **no non-stdlib dependencies**. The
`postgres` subpackage and the `adapter/llmagent` build-tagged
package are the only places that pull external deps in.

## Package layout

- `ingest`: documents, sources, splitters, import helpers
- `embed`: embedder seam and default hash embedder
- `store`: vector store seam and in-memory reference store
- `store/storetest`: shared conformance suite every backend wires against
- `postgres`: PostgreSQL + pgvector backend (opt-in deps: `pgx/v5`, `pgvector-go`)
- `retrieve`: hybrid retrieval, structure-aware route policy, search trajectory
- `pack`: token-budget-aware context packing
- `rerank`: heuristic reranker
- `generate`: text-generation seam
- `prompt`: prompt-template seam and default QA template
- `rag`: orchestration layer for import, retrieve, ask + observer hook
- `eval`: dataset format + metrics + JSONL loader for the CI eval gate
- `tree`: document-tree primitives for structured markdown corpora
- `adapter/llmagent`: optional adapter layer for `llm-agent` (build tag `llmagent`)

## Status

Current status: stable — v1.0. The public API is frozen under an
additive-only compatibility promise for the `v1.x` series; see
[docs/compatibility.md](docs/compatibility.md) for the import-compatibility
rule, semver policy, and the `/v2` procedure for breaking changes.

Implemented:

- abstract import via `ingest.Source` and `ingest.Importer`
- deterministic default `CharSplitter` and markdown splitter
- default `HashEmbedder`
- default `InMemoryStore` + `postgres.Store` (pgvector)
- shared `store/storetest.RunConformance` contract suite
- abstract generation via `generate.Model`
- prompt customization via `prompt.Template`
- `rag.System` with `Import`, `ImportFrom`, `Retrieve`, and `Ask`
- `rag.Observer{OnImport, OnRetrieve, OnAsk}` hook for external
  tracing (consumed by `llm-agent-otel`)
- retrieval policy seams for:
  - query preprocessing
  - lexical retrieval
  - hybrid retrieval
  - MQE / HyDE query expansion
  - heuristic reranking
  - token-budget-aware context packing
  - structure-aware section/path retrieval
  - subtree-constrained route-path retrieval
  - automatic section route selection for hierarchical corpora
  - confidence-gap adaptive fanout (converge on strong top-1,
    fan out when top two routes are close)
  - per-route `SearchTrajectory` attribution
  - pluggable `SectionPlanner` interface (default
    `GapAwareSectionPlanner`)
- document-tree primitives for structured markdown corpora
- evaluation framework (`eval`) with precision / recall / MRR /
  grounding@k metrics, a JSONL loader, and a seed regression
  test that gates retrieval quality at `go test` time

## Quick start

```go
package main

import (
	"context"
	"fmt"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/rag"
)

type echoModel struct{}

func (echoModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	return generate.Response{Text: req.Messages[0].Content}, nil
}

func main() {
	sys := rag.New(rag.Options{Model: echoModel{}})

	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "paris", Content: "Paris is the capital of France."},
		{ID: "berlin", Content: "Berlin is the capital of Germany."},
	}, ingest.ImportOptions{Namespace: "cities"})
	if err != nil {
		panic(err)
	}

	hits, err := sys.Retrieve(context.Background(), "France capital", rag.SearchOptions{
		Namespace: "cities",
		TopK:      1,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(hits[0].Chunk.ID)

	ans, err := sys.Ask(context.Background(), "What is the capital of France?", rag.AskOptions{
		Search: rag.SearchOptions{Namespace: "cities", TopK: 1},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(ans.Text)
}
```

## Usage notes

- `Import` is for explicit in-memory document batches.
- `ImportFrom` is for document sources that already implement the source seam.
- `Retrieve` is LLM-free and only depends on the embedder and store.
- `Ask` layers retrieval, optional rerank, context packing, prompt rendering,
  and answer generation.

## Minimal example workflow

1. Build a `rag.System`
2. Import documents through `Import` or `ImportFrom`
3. Call `Retrieve` for raw ranked chunks
4. Call `Ask` when you want a synthesized answer

Not implemented yet:

- HTTP service layer
- CLI

## Optional adapter

The `adapter/llmagent` package is intentionally behind a build tag:

- build tag: `llmagent`

That keeps the core SDK publishable and testable without requiring
`github.com/costa92/llm-agent`.

Core verification:

```bash
cd /tmp/llm-agent-rag
GOWORK=off GOCACHE=/tmp/go-build go test ./...
```

If you want to develop the `llm-agent` adapter locally, add a temporary
development dependency and run:

```bash
GOWORK=off GOCACHE=/tmp/go-build go test -tags llmagent ./adapter/llmagent
```

Because the adapter imports `github.com/costa92/llm-agent`, the standalone
module does not keep that dependency in its publishable core `go.mod`.
For local adapter development, add a temporary `require` and `replace`
pointing at your local `llm-agent` checkout, then run the tagged test above.

## Verification

```bash
cd /tmp/llm-agent-rag
GOWORK=off GOCACHE=/tmp/go-build go test ./...
```
