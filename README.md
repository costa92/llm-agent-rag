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
