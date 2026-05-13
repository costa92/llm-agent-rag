# llm-agent-rag

Standalone Go RAG SDK with abstract import, retrieval, custom LLM generation,
and custom prompt-template seams.

## Scope

This SDK is designed around three primary workflows:

- import documents from abstract sources
- retrieve ranked chunks for a query
- generate answers with a caller-provided model and prompt template

The core packages are provider-agnostic and do not depend on
`github.com/costa92/llm-agent`.

## Package layout

- `ingest`: documents, sources, splitters, import helpers
- `embed`: embedder seam and default hash embedder
- `store`: vector store seam and in-memory reference store
- `generate`: text-generation seam
- `prompt`: prompt-template seam and default QA template
- `rag`: orchestration layer for import, retrieve, and ask
- `adapter/llmagent`: optional adapter layer for `llm-agent`

## Status

Current status: scaffold / v0.1 baseline.

Implemented:

- abstract import via `ingest.Source` and `ingest.Importer`
- deterministic default `CharSplitter`
- default `HashEmbedder`
- default `InMemoryStore`
- abstract generation via `generate.Model`
- prompt customization via `prompt.Template`
- `rag.System` with `Import`, `ImportFrom`, `Retrieve`, and `Ask`

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
- `Ask` layers prompt rendering and answer generation on top of retrieval.

## Minimal example workflow

1. Build a `rag.System`
2. Import documents through `Import` or `ImportFrom`
3. Call `Retrieve` for raw ranked chunks
4. Call `Ask` when you want a synthesized answer

Not implemented yet:

- production vector backends
- rerankers
- MQE / HyDE
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

## Verification

```bash
cd /tmp/llm-agent-rag
GOWORK=off GOCACHE=/tmp/go-build go test ./...
```
