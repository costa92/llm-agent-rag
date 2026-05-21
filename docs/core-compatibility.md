# Core compatibility

This document explains how `llm-agent-rag` (this repo) relates to
`github.com/costa92/llm-agent` (the core agents repo), what changes
when, and what the cross-repo contract gates enforce.

## The two-repo split

The umbrella project has two RAG-relevant repos:

- **`github.com/costa92/llm-agent`** — the agents framework. Stays
  **stdlib-only**: no non-stdlib deps in `go.mod`, no `go.sum`
  before a release tag. Core packages that need embedding or RAG data
  depend directly on a small, pinned subset of this repo's API.
- **`github.com/costa92/llm-agent-rag`** — this repo. The standalone
  RAG SDK. May take dependencies (and does, since v0.2 — `pgx/v5` +
  `pgvector-go` for the postgres backend).

The split exists because users of `llm-agent` should be able to
`go get` the core and read every line. Pulling in a vector-database
driver would break that read-the-source promise. Putting the
production-grade RAG implementation in a sister repo with its own
dependency budget gives us a deployable system without compromising
the core's auditability.

## Where to look for what

| Looking for                                 | Goes to                                          |
| ------------------------------------------- | ------------------------------------------------ |
| `ChatModel`, `Agent`, tool execution        | `github.com/costa92/llm-agent`                   |
| Core helper integrations (`context`, `memory`) | `github.com/costa92/llm-agent`                |
| RAG orchestration, retrieval, persistence   | `github.com/costa92/llm-agent-rag`               |
| Provider adapters (OpenAI, Anthropic, ...)  | `github.com/costa92/llm-agent-providers`         |
| OTel observability wrappers                 | `github.com/costa92/llm-agent-otel`              |
| Reference customer-support service          | `github.com/costa92/llm-agent-customer-support`  |

## The optional adapter package

This repo ships `adapter/llmagent/` behind a build tag:

```go
//go:build llmagent
```

That package adapts the core's `llm.ChatModel` interface into the
standalone `generate.Model` interface so the rag system can use
core chat models as its answer generator.

Default builds **do not** compile this package. Default `go test
./...` does not run it. That's intentional — without the build tag,
this repo has zero dependency on `github.com/costa92/llm-agent`.

If you want to wire a core chat model into the standalone RAG SDK,
build with the tag:

```bash
go build -tags llmagent ./...
go test  -tags llmagent ./adapter/llmagent
```

For local development you may need a temporary `replace` directive
pointing at your `llm-agent` checkout. Do not commit that replace —
the standalone repo's release artifacts must not depend on a local
core checkout.

## Versioning expectations

The standalone repo evolves independently of the core. The
contract surface between them is small (for example `embed.Embedder`,
`store.Hit`, `store.InMemoryStore`, `ingest.Document`, and
`rag.System`) and is intentionally stable.

- **Standalone minor bumps** add features but preserve the
  subset of the API that current core integrations consume.
- **Standalone major bumps** are the moments where core
  integrations must be updated explicitly. They happen rarely.
- **Core releases** pin a specific standalone version. The pin
  changes only at planned core minor/major bumps.

The contract gates fail when:

- the standalone module exports a new public type or method that
  a pinned core integration reads without an updated pin
- the standalone module changes the signature of a method the core
  integration calls
- compile-time contract tests no longer match the pinned
  standalone version

## What does NOT cross the boundary

The standalone repo's new capabilities are not automatically
exposed through core helper packages. The following landed in
standalone first and are **not wired through the core packages by
default**:

- the `postgres` package (the core stays stdlib-only)
- the `rag.Observer` hook surface
- the `eval` framework
- the `store/storetest` conformance suite
- per-route `SearchTrajectory` data

Consumers who want these features should import
`github.com/costa92/llm-agent-rag` directly. The core repo does not
try to mirror the full standalone surface.

## When to bump what

Rough heuristics:

- **New public method on standalone `rag.System`?** Standalone
  minor bump. Core integrations are unaffected unless they explicitly
  forwards the method.
- **Breaking change to `store.Store`?** Standalone major bump.
  Every backend (in-memory, postgres, third-party) must update.
  Any pinned core integration must update.
- **New backend (Qdrant, SQLite-vec, ...) in standalone?**
  Standalone minor bump. Core packages are unaffected; consumers opt
  in by importing the new package.
- **Change to core's `llm.ChatModel`?** Core repo concern;
  affects the `adapter/llmagent` package here, but only under
  `-tags llmagent`.

When in doubt, prefer standalone minor over major. The contract
surface is small enough that breaking changes should be rare.
