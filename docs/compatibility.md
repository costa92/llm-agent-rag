# Compatibility policy

This document is the Go-module compatibility promise `llm-agent-rag`
commits to at `v1.0.0`. Tagging `v1.0.0` invokes the Go [import
compatibility rule][go-compat] as applied to this module's own public
API. Everything below states the rules so every contributor and user
understands exactly what `v1.x` guarantees — and what it does not.

> **Scope.** This document covers `llm-agent-rag`'s *own* API. For how
> this repo relates to the core `github.com/costa92/llm-agent` repo —
> the two-repo split, which features cross the boundary, and the
> cross-repo CI gates — see [`core-compatibility.md`](./core-compatibility.md).

[go-compat]: https://go.dev/blog/v2-go-modules

## Import compatibility

Within the `v1.x` series the public API is **additive-only**. From
`v1.0.0` onward, no `v1.MINOR.PATCH` release will:

- remove or rename an exported symbol (type, function, method,
  variable, constant);
- change the signature of an exported function or method;
- remove an exported struct field;
- change the value of an exported constant that callers depend on.

What a `v1.x` release **may** do:

- add new exported functions, types, methods, variables, and constants;
- add new exported *fields* to an existing struct (callers using
  positional struct literals should use keyed literals — keyed literals
  are unaffected by added fields);
- add entirely new packages.

The guiding test is the Go import compatibility rule: code that builds
against `v1.N` must continue to build against every later `v1.M`
(`M ≥ N`) with no source change.

### The interface-method gotcha

**Adding a method to an exported interface is a breaking change** — even
though "adding" sounds additive. Every external type that implements
the interface stops satisfying it the moment a new method is required,
and that code fails to compile. So:

- Exported interfaces in `llm-agent-rag` (for example `store.Store`,
  `embed.Embedder`, `generate.Model`, `retrieve.Retriever`,
  `rerank.Reranker`, `ingest.Splitter`, `ingest.Source`,
  `prompt.Template`) are **frozen** within `v1.x`: no method may be
  added to them.
- A new capability that would otherwise require a new interface method
  is instead introduced as a *separate, optional* interface that a type
  may additionally implement (the pattern used by
  `store.LexicalSearcher`). Callers type-assert for the optional
  interface; types that do not implement it are unaffected.
- Growing a core seam interface is a `/v2`-only change (see below).

## Semantic versioning

Releases follow [semantic versioning][semver] as the Go module system
enforces it. Versions are `v1.MINOR.PATCH`:

- **PATCH** (`v1.0.0 → v1.0.1`) — bug fixes only; no API change.
- **MINOR** (`v1.0.0 → v1.1.0`) — backward-compatible additions
  (new functions, types, packages, struct fields).
- **MAJOR** (`v1.x → v2.0.0`) — breaking changes. See `/v2` below.

Go's module tooling and the module proxy enforce the major-version
contract: a `v2+` module must use a distinct import path, so a breaking
change cannot reach existing callers by accident.

[semver]: https://semver.org/

## Breaking changes and `/v2`

There is exactly one mechanism for a breaking change to `llm-agent-rag`:
a new major version published under a new import path,
`github.com/costa92/llm-agent-rag/v2`. There is no other mechanism — no
"breaking patch", no opt-in flag, no build tag that quietly changes the
surface.

Consequences of the `/v2` rule:

- A `v1.x` consumer is never broken by a `v2` release: the import paths
  differ, so `go get` of `v1` keeps resolving `v1`.
- Migrating to `/v2` is an explicit, deliberate act: the consumer edits
  the import path. `v1` and `v2` can even coexist in one build.
- Because `/v2` is the *only* escape hatch, breaking changes are
  expensive and rare by design. Prefer an additive solution within
  `v1.x` whenever one exists.

## The `contract` sub-contract

The `contract` package (`contract/contract_test.go`) is a test-only
package that pins the exact `llm-agent-rag` surface current core
integrations consume. It is a *sub*-set of the full `v1.x` promise: a
narrow, explicitly enumerated surface that the core repo depends on.

- The contract test fails if any symbol a pinned core integration reads is
  removed, renamed, or re-signed — catching cross-repo drift before a
  release.
- The pinned surface changes **only via coordinated PRs** with the
  `llm-agent` repo: the core integration pin and this repo's surface move
  together.
- The full cross-repo story — the two-repo split, which features cross
  the boundary, when to bump what — is documented in
  [`core-compatibility.md`](./core-compatibility.md). This section only
  states that the `contract` package exists, is part of the v1.0
  compatibility guarantee, and is not changed unilaterally.

## External dependencies (`postgres`)

`llm-agent-rag` is **not** stdlib-only — and intentionally so (that is
the whole reason it is a sister repo of the stdlib-only core
`llm-agent`). Its only non-stdlib dependencies are:

- `github.com/jackc/pgx/v5` — the PostgreSQL driver;
- `github.com/pgvector/pgvector-go` — `pgvector` type support.

Policy for these dependencies:

- They are **isolated in the `postgres` package**. Importing any other
  package of `llm-agent-rag` pulls in zero third-party code; only a
  consumer that imports `postgres` takes the dependency.
- `go.mod` declares the **minimum** versions `llm-agent-rag` is tested
  against. A `v1.x` release may raise these minimums within the
  dependency's own `v5.x` / `v0.x` line (a backward-compatible bump).
- A **major** bump of an external dependency (e.g. `pgx/v5 → pgx/v6`) is
  *that dependency's* semver event, not absorbed silently: it surfaces
  in this repo's `go.mod` as a changed import requirement and is
  released as an `llm-agent-rag` minor or major as the API impact
  dictates. External majors are never hidden inside a patch release.

## `adapter/llmagent` coverage

`adapter/llmagent` is a build-tagged package (`//go:build llmagent`)
that adapts the core `llm-agent` chat-model interface into this repo's
`generate.Model` seam. **A build tag does not exempt a package from the
compatibility promise.** The exported surface of `adapter/llmagent` is
covered by the same `v1.x` additive-only rule as every default-built
package: a `v1.x` release will not remove, rename, or re-sign its
exported symbols.

The build tag controls *when the package compiles*, not *whether its API
is stable*. Consumers who build with `-tags llmagent` get the same `v1.x`
guarantee everyone else does.

## `go.sum`

`llm-agent-rag` **commits `go.sum`**, and that is correct for this
module: `go.sum` records the checksums of the `postgres`-island
dependencies (`pgx`, `pgvector-go`) and their transitive closure, so
builds are reproducible and verifiable.

This intentionally **differs from the stdlib-only core `llm-agent`**,
which has no non-stdlib dependencies and therefore no `go.sum` before a
release tag. The difference is by design — see
[`core-compatibility.md`](./core-compatibility.md) for the rationale
behind the two-repo dependency split.

## Minimum Go version

`go 1.26` is the `v1.0` floor — declared as `go 1.26.0` in `go.mod`.
Building `llm-agent-rag v1.x` requires Go 1.26 or newer.

Within the `v1.x` series the minimum Go version **may rise to a newer
1.x Go release** (e.g. `go 1.27`) in a MINOR release; raising the Go
floor is treated as a backward-compatible addition, not a breaking
change, consistent with the Go project's own compatibility policy. A Go
floor bump is always called out in the release notes.

## Deprecation procedure

Because a symbol cannot be removed within `v1.x` (that would break import
compatibility), removal is a two-step, cross-major process:

1. **Mark.** In a `v1.x` MINOR release, the symbol's doc comment gains a
   `// Deprecated: …` line that names the replacement and the reason.
   The symbol keeps working unchanged — `pkg.go.dev` and `go vet` (via
   `staticcheck`-style tooling) surface the deprecation to callers, but
   nothing breaks.
2. **Remove.** The symbol is removed only in a future `/v2`
   (`github.com/costa92/llm-agent-rag/v2`), never within `v1.x`.

So a deprecation in `v1.x` is a *signal*, not a removal. Callers have the
entire remaining `v1.x` lifetime to migrate before the symbol can
disappear, and even then only by explicitly opting into `/v2`.
