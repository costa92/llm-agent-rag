# Backend selection

`llm-agent-rag` ships two `store.Store` implementations today and is
designed for more. This guide explains when to pick each one and
how to add a new backend.

## The two shipped backends

### `store.InMemoryStore`

Pure-Go map-backed store. No external dependencies.

**Use it for:**

- unit tests and integration tests
- demos, prototypes, notebooks
- single-process apps with small (~thousands) corpora

**Don't use it for:**

- multi-process deployments (state per process)
- corpora that don't fit in memory
- anything that must survive a restart

### `postgres.Store`

PostgreSQL + pgvector backend. Lives in the `postgres/` subpackage.
Pulls `github.com/jackc/pgx/v5` and `github.com/pgvector/pgvector-go`
as the first non-stdlib deps in the SDK.

**Use it for:**

- production deployments
- multi-process workloads
- corpora from thousands to millions of chunks
- environments where you already operate PostgreSQL

**Trade-offs:**

- requires the `vector` extension on the target database
- vector dimension is fixed at table-creation time
  (`postgres.Config.Dimension`)
- live-Postgres integration tests are env-gated behind
  `LLM_AGENT_RAG_PG_URL` and skip cleanly when unset

See [`production-deployment.md`](./production-deployment.md) for
the full setup walkthrough.

## The conformance contract

Every backend must pass `store/storetest.RunConformance`. The
helper produces 12 named subtests that cover the contract:

- `Upsert_and_Get_round_trip` — every `StoredChunk` field round-trips
- `Search_returns_nearest_first` — cosine ranking
- `Search_respects_namespace` — no cross-namespace bleed
- `Filter_narrows_results` — metadata equality
- `Security_filter_intersects_with_caller_filter` — AND semantics
- `List_returns_namespace_chunks` — with and without filters
- `Get_on_missing_returns_ErrNotFound`
- `Remove_on_missing_returns_ErrNotFound`
- `Remove_deletes`
- `RemoveByFilter_returns_count_and_removes`
- `Stats_reports_count_and_dim`
- `Dimension_mismatch_returns_error` (opt-in via
  `WithDimensionStrict()` — for backends that enforce a fixed
  embedding dimension)

Wire your backend into the suite with one call:

```go
import "github.com/costa92/llm-agent-rag/store/storetest"

func TestMyBackendConformance(t *testing.T) {
    storetest.RunConformance(t, func(t *testing.T) store.Store {
        // build a fresh, isolated store for this subtest
        return newMyBackend(t)
    }, storetest.WithDimensionStrict())
}
```

The factory is called once per subtest. For backends with shared
schema (like postgres), the factory creates a fresh
namespace/table/collection per call and registers `t.Cleanup` to
drop it.

## Adding a new backend

Five-step checklist:

1. **Implement `store.Store`.** All seven methods. Map missing
   rows to `store.ErrNotFound`; map vector-dimension mismatches to
   `store.ErrDimensionMismatch`. Compile-time check:
   `var _ store.Store = (*MyStore)(nil)`.
2. **Run conformance.** Add a `*_conformance_test.go` that calls
   `storetest.RunConformance`. Gate any external-service tests
   behind an env var so default `go test` still passes.
3. **Document operational guidance.** Add a section to
   [`production-deployment.md`](./production-deployment.md) covering
   pool/connection setup, index choice, and dimension semantics.
4. **Update the matrix below** with your backend's capability
   profile.
5. **Send a PR.** Conformance pass is the merge gate.

## Capability matrix (current)

| Backend             | Persistence | Vector index            | Metadata filter | Security filter (AND) | Live test gate        |
| ------------------- | ----------- | ----------------------- | --------------- | --------------------- | --------------------- |
| `InMemoryStore`     | no          | linear scan             | yes             | yes                   | (always on)           |
| `postgres.Store`    | yes         | optional ivfflat / hnsw | JSONB `@>`      | yes                   | `LLM_AGENT_RAG_PG_URL` |

## Forward-looking backends

These are plausible future contributions. None are shipped today —
each would slot into the matrix above after passing the conformance
suite:

- **Qdrant** — purpose-built vector DB, gRPC + HTTP, has a maintained
  Go client. Strongest hybrid-retrieval feature set today.
- **SQLite + sqlite-vec** — embeddable single-file store; gives the
  SDK a zero-server demo path. cgo today, pure-Go variants exist.
- **DuckDB** — in-process analytical DB with growing vector support.
  Strong for offline eval pipelines.
- **pgvector with HNSW** — same backend, different index. Could be
  exposed as a `postgres.Config.Index` knob rather than a new
  package.

Pick based on operational cost, not feature checklist. The SDK
contract is small enough that backend choice is reversible — start
where ops is cheapest, swap when scale demands it.
