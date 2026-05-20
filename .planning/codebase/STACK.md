# Technology Stack

**Analysis Date:** 2026-05-20

## Languages

**Primary:**
- Go `1.26.0` — declared in `go.mod` line 3 (`go 1.26.0`). Every package
  in the module is Go.

**Secondary:**
- SQL (PostgreSQL dialect) — embedded as string literals in
  `postgres/postgres.go` (`CREATE EXTENSION IF NOT EXISTS vector`,
  generated `tsvector` columns, GIN indexes) and `postgres/graph.go`
  (recursive CTE neighborhood traversal). Not a separately compiled
  language; lives only inside `postgres/*.go`.
- YAML — two GitHub Actions workflow files under `.github/workflows/`.

## Runtime

**Environment:**
- Go toolchain `1.26.0`. CI pins via `go-version-file: go.mod`
  (`.github/workflows/test.yml` line 24).
- No other runtime — this is a library, not a service. There is no
  `main` package, no `cmd/`, and no `examples/` binary (the `examples/`
  directory holds `_test.go` files that double as runnable
  documentation; verified by `ls examples/` returning only
  `*_example_test.go`).

**Package Manager:**
- Go modules.
- Module path: `github.com/costa92/llm-agent-rag` (`go.mod` line 1).
- Lockfile: `go.sum` present (10 KB; covers the closure of every
  direct require). Both `go.mod` and `go.sum` are committed.
- `GOWORK=off` is set in CI (`test.yml` line 14) and in the
  README's "Verification" snippet — the module is verified standalone,
  detached from any umbrella `go.work`.
- `replace` directives are explicitly forbidden in tag-track branches:
  `release-precheck.yml` runs `go mod edit -json` and fails the build
  if `Replace` is non-empty.

## Frameworks

**Core:**
- No external framework. The module is implemented against the Go
  standard library only, with one explicit exception (see "Stdlib-only
  outside `postgres` — verified" below).

**Testing:**
- Standard library `testing` package. There is no `testify`,
  `gomega`, `ginkgo`, `gocheck`, or any other test framework imported
  by source code. (`go.sum` lists `stretchr/testify` as a transitive
  artifact of `pgx/v5`'s own test graph, but no file under
  `llm-agent-rag` imports it — verified by
  `grep -r '"github.com/stretchr/testify"' --include='*.go'` returning
  no hits.)
- 59 `*_test.go` files across 125 total `.go` files.
- One internal test-only package: `store/storetest` — a shared
  conformance harness every `store.Store` implementation runs.
- One internal CI gate: `internal/apisnapshot` — a pure-`go/parser` +
  `go/ast` + `go/printer` exported-API snapshot generator, with the
  baseline committed at `api/v1.snapshot.txt`.

**Build/Dev:**
- `go build`, `go vet`, `go test` are the only build/dev tools — both
  CI jobs invoke them directly (`test.yml` lines 40-49).
- No `Makefile`, no `Taskfile`, no `Justfile`, no `Dockerfile`, no
  `.golangci.yml`, no `.editorconfig` at the repo root (verified by
  `ls /...llm-agent-rag/Makefile ...` returning no matches and
  `find -maxdepth 1 -name '.*' -type f` returning nothing).
- Formatting gate: `gofmt`. The v1.0.0 changelog entry says "the
  repository is now `gofmt`-clean" (`CHANGELOG.md:60`); no linter
  config is configured, so `gofmt` and `go vet` are the only
  enforced style gates.

## Key Dependencies

**Direct (declared in `go.mod`):**

| Module | Version | Used by | Purpose |
|---|---|---|---|
| `github.com/costa92/llm-agent` | `v0.5.0` | **`adapter/llmagent/` only**, behind build tag `//go:build llmagent` | Provides `corellm.ChatModel` and `agents.Tool` so the standalone SDK can be wrapped as a tool for, and consume chat models from, the core agent framework |
| `github.com/jackc/pgx/v5` | `v5.9.2` | **`postgres/` only** | PostgreSQL driver and connection pool (`pgx`, `pgxpool`, `pgconn`) |
| `github.com/pgvector/pgvector-go` | `v0.3.0` | **`postgres/` only** | pgvector type codec — both the base package (used as `pgvector.NewVector`) and the `pgx`-codec sub-package (`pgvector-go/pgx` for `RegisterTypes`) |

**Indirect (closure of the above, all in `go.mod`'s second
`require` block):**

- `github.com/jackc/pgpassfile v1.0.0`
- `github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761`
- `github.com/jackc/puddle/v2 v2.2.2`
- `github.com/x448/float16 v0.8.4`
- `golang.org/x/sync v0.17.0`
- `golang.org/x/text v0.29.0`

> `go.sum` also lists `entgo.io/ent`, `gorm.io/{gorm,driver/postgres}`,
> `github.com/uptrace/bun*`, `github.com/lib/pq`, `github.com/go-pg/pg/v10`,
> `github.com/jmoiron/sqlx`, and `mellium.im/sasl`. These come from
> `pgvector-go`'s own module graph closure. **No file under
> `llm-agent-rag` imports any of them** (verified by
> `grep -rE '"(entgo|gorm|github\.com/uptrace|mellium|lib/pq|go-pg)' --include='*.go'`
> returning zero matches). They are present in `go.sum` only for the
> Go module checksum integrity of `pgvector-go`'s declared graph; they
> are not compiled into any binary produced from this module.

## Stdlib-Only Outside `postgres` — Verified

The README, root `doc.go`, and `CHANGELOG.md` all claim that every
package other than `postgres` is stdlib-only. **This claim was verified
by direct import inspection.** Per-package external-import audit:

| Package | Go files | Non-stdlib imports (excluding the module itself) |
|---|---|---|
| `adapter/llmagent` | 4 | `github.com/costa92/llm-agent`, `github.com/costa92/llm-agent/llm` (build tag `llmagent` only) |
| `advanced` | 3 | none |
| `agentic` | 2 | none |
| `api` | 0 | — (holds only `v1.snapshot.txt`) |
| `contract` | 1 | none (`contract_test.go` — compile-time pin only) |
| `embed` | 4 | none |
| `eval` | 14 | none |
| `examples` | 5 | none |
| `feedback` | 2 | none |
| `generate` | 2 | none |
| `graph` | 17 | none |
| `guard` | 4 | none |
| `ingest` | 7 | none |
| `internal/apisnapshot` | 2 | none |
| `obs` | 2 | none |
| `pack` | 2 | none |
| **`postgres`** | **5** | **`github.com/jackc/pgx/v5`, `github.com/jackc/pgx/v5/pgconn`, `github.com/jackc/pgx/v5/pgxpool`, `github.com/pgvector/pgvector-go`, `github.com/pgvector/pgvector-go/pgx`** |
| `prompt` | 4 | none |
| `rag` | 22 | none |
| `rerank` | 4 | none (the `HTTPScoringModel` uses `net/http` from stdlib) |
| `retrieve` | 6 | none |
| `store` (incl. `store/storetest`) | 9+1 | none |
| `tree` | 2 | none |

Total: 125 `.go` files, 59 of them `*_test.go`.

The CI `test.yml` codifies this boundary with an explicit ripgrep
gate ("Verify core packages do not import llm-agent",
`test.yml:26-39`) — every file outside `adapter/**` that imports
`github.com/costa92/llm-agent` fails the build. There is no parallel
gate that forbids `pgx` or `pgvector-go` imports outside `postgres/`,
but the contract is documented in the README, the v0.2.0 changelog
entry, and `docs/core-compatibility.md`, and is held by convention.

## Configuration

**Environment:**
- One environment variable, consumed only by tests:
  `LLM_AGENT_RAG_PG_URL` — when unset, `postgres/postgres_test.go` and
  `postgres/postgres_conformance_test.go` `t.Skip` the live-database
  cases. (`postgres/postgres_test.go:17` defines the constant.)
- The library itself reads no environment variables at runtime. The
  postgres backend takes its DSN-derived `*pgxpool.Pool` from the
  caller via `postgres.New(pool, cfg)` — see `postgres/postgres.go:62`
  and `docs/production-deployment.md:17-44` for the canonical wiring.
- No `.env*` files exist in the repo.

**Build:**
- `go.mod`, `go.sum` are the only build config files at the module
  root.
- No `tsconfig`, no `vite`, no language-specific config beyond Go's own.
- Build tag `llmagent` selectively compiles `adapter/llmagent/` — used
  only when callers want the build-tagged adapter to the core
  `llm-agent` repo.

## Platform Requirements

**Development:**
- Go `1.26.0` (`go.mod:3`).
- Standard `go` toolchain — no additional CLI dependencies.
- Optionally, a live PostgreSQL `>= 14` with the `vector` extension
  for `postgres/` live tests (`docs/production-deployment.md:11`).

**Production:**
- The SDK is a Go library, not a deployable binary. "Production" is
  the caller's host process; the only production runtime
  requirement contributed by this module is that the `postgres`
  backend, if used, needs PostgreSQL `>= 14` with the `vector`
  extension. The default `InMemoryStore` has no external requirements.

## Build Tooling

**CI workflows** (`.github/workflows/`):

| File | Trigger | Purpose |
|---|---|---|
| `test.yml` | push and PR to `master`/`main` | Cross-repo module-boundary check (ripgrep gate forbidding `github.com/costa92/llm-agent` imports outside `adapter/`), `go vet`, `go build`, `go test`, the `internal/apisnapshot` API-surface gate (run explicitly for visibility, also covered by `go test ./...`), and a tagged `go build -tags llmagent ./...` + `go test -tags llmagent ./adapter/...` build to keep the adapter green |
| `release-precheck.yml` | push and PR to `release/**` branches | Fails the build if `go mod edit -json` reports any `replace` directives, so a release tag can never accidentally ship pinned to a local-replace dependency |

**Setup:** both workflows use `actions/setup-go@v5` with `go-version-file: go.mod` — the Go version is single-sourced from `go.mod`.

**No local Makefile / Taskfile / Justfile**, no `.golangci.yml`, no
`.editorconfig`. The `Verification` section of the README simply
invokes `GOWORK=off GOCACHE=/tmp/go-build go test ./...`.

## Tagging Discipline

- Branch: `master` (verified by `git branch --show-current`). CI
  triggers on both `master` and `main` for forward compatibility.
- Tags so far (`git tag --list`):
  `v0.1.0 v0.1.1 v0.1.2 v0.1.3 v0.1.4 v0.2.0 v0.3.0 v0.4.0 v0.5.0
  v0.6.0 v1.0.0 v1.0.1`
- `CHANGELOG.md` follows
  [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) with
  semver — markers explicit at `CHANGELOG.md:6-7`. Every tag has its
  own dated section with `### Added`, `### Changed`, `### Fixed`,
  `### Notes` subsections.
- v1.0.0 entry (`CHANGELOG.md:9`) marks the API freeze: additive-only
  promise for the entire `v1.x` series, breaking changes go to a `/v2`
  module path. The freeze is enforced by two complementary gates:
  - `contract/contract_test.go` — narrow, cross-repo compile-pin for
    the surface the core `llm-agent` integrations consume
  - `internal/apisnapshot/apisnapshot_test.go` — whole-module
    exported-surface diff against `api/v1.snapshot.txt`
- Release-precheck branches (`release/**`) reject any `replace`
  directive (`release-precheck.yml:21-33`), so v1.x tags are always
  cut from a `go.mod` that resolves entirely against the public module
  proxy.

---

*Stack analysis: 2026-05-20*
