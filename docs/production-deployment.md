# Production deployment

This guide walks through deploying `llm-agent-rag` against a real
PostgreSQL + pgvector backend with observability hooks. It assumes
you've already chosen `postgres.Store` as your store — see
[`backend-selection.md`](./backend-selection.md) if you're still
weighing options.

## Prerequisites

- PostgreSQL >= 14 with the `vector` extension available
- A user with `CREATE EXTENSION` permission, or an admin who can
  pre-create the extension on the target database
- The embedder model and its dimension fixed for the deployment
  (the postgres store column is created as `vector(N)`)

## Pool wiring

`postgres.Store` takes a `*pgxpool.Pool` that the caller owns. The
pool's `AfterConnect` hook must register pgvector types on every new
connection so the vector codec is available:

```go
import (
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/costa92/llm-agent-rag/postgres"
)

cfg, err := pgxpool.ParseConfig(os.Getenv("PG_URL"))
if err != nil { /* ... */ }
cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
    return postgres.RegisterTypes(ctx, conn)
}
pool, err := pgxpool.NewWithConfig(ctx, cfg)
if err != nil { /* ... */ }
defer pool.Close()
```

Forgetting `AfterConnect` results in `unknown type vector` errors on
`Upsert` / `Search`. The reference test
`postgres/postgres_test.go::TestPostgresStore_LiveSmoke` shows the
complete shape.

## Construction and migration

```go
s, err := postgres.New(pool, postgres.Config{
    Table:     "rag_chunks",  // optional; defaults to "chunks"
    Dimension: 1536,          // required; must match your embedder
})
if err != nil { /* ... */ }

if err := s.Migrate(ctx); err != nil { /* ... */ }
```

`Migrate(ctx)` is idempotent. It creates the `vector` extension
(no-op if already installed), the chunks table with a `vector(N)`
column, and a namespace index. Run it on every startup, not just
the first one.

The `Table` field is validated as a strict ASCII identifier; the
constructor returns an error before any SQL runs if the name
contains anything outside `[a-zA-Z_][a-zA-Z0-9_]*`.

## Wiring into rag.System

`postgres.Store` satisfies `store.Store`, so the rag facade picks it
up via `Options.Store`:

```go
import "github.com/costa92/llm-agent-rag/rag"

sys := rag.New(rag.Options{
    Store:    s,
    Embedder: yourEmbedder,
    Model:    yourLLM,
})
```

No other rag-facade code changes.

## Security filters

The postgres store implements security filters via JSONB subset
matching (`metadata @> $N`). Caller filters and security filters are
**intersected** (AND), not overridden — the conformance subtest
`Security_filter_intersects_with_caller_filter` documents this
contract.

Example: a multi-tenant deployment with a `tenant` metadata key:

```go
hits, err := sys.Retrieve(ctx, query, rag.SearchOptions{
    Namespace:       "docs",
    Filters:         rag.Filter{"lang": "en"},
    SecurityFilters: rag.Filter{"tenant": userTenantID},
})
```

A request that tries to widen scope by setting `Filters: {"tenant":
otherTenant}` returns zero hits when `SecurityFilters` pins the
allowed tenant — by design.

## Observer wiring

The rag facade emits three observer events on success: `OnImport`,
`OnRetrieve`, `OnAsk`. Pass an `Observer` in `Options` and the
callbacks fire after each top-level operation completes:

```go
sys := rag.New(rag.Options{
    Store: s,
    Model: yourLLM,
    Observer: rag.Observer{
        OnImport: func(ctx context.Context, trace rag.ImportTrace) {
            // record metrics, emit a span, log structured event
        },
        OnRetrieve: func(ctx context.Context, trace retrieve.Trace) {
            // every retrieval — including the one inside each Ask
        },
        OnAsk: func(ctx context.Context, trace rag.Trace) {
            // end-to-end answer trace, fires after OnRetrieve
        },
    },
})
```

OTel adapters in the `llm-agent-otel` sister repo wire to these
hooks. Each callback receives the same trace data the rag facade
returns to callers, so observer code never needs to inspect
internals.

Errors short-circuit before any callback fires. If you need error
spans, wrap the rag method directly in your tracing layer.

## Operational notes

### Pool sizing

`pgxpool.Config.MaxConns` defaults to `max(4, NumCPU)`. For a RAG
workload that mixes embedding-time bursts with steady retrieval,
size to peak concurrent Retrieve calls plus a small import buffer.
A reasonable starting point is `2 * peak QPS / avg query latency in
seconds`, capped at `25` per pool.

### Single table vs table-per-tenant

The default schema uses a single chunks table with a `namespace`
column. This is the right call for most deployments — pgvector's
ivfflat index doesn't get faster from sharding small datasets.

Choose table-per-tenant only when:

- one tenant's hot working set dominates the index
- compliance requires physical isolation
- you need per-tenant ALTER TABLE freedom (different dimensions)

In those cases construct one `postgres.Store` per tenant with a
distinct `Config.Table`.

### Index choice

The default `Migrate` does not create a vector index. For small
datasets (< 10k chunks) sequential scan is fine. For larger
datasets, set `postgres.Config.VectorIndex` and re-run `Migrate`:

```go
s, err := postgres.New(pool, postgres.Config{
    Table:        "rag_chunks",
    Dimension:    1536,
    VectorIndex:  postgres.VectorIndexIVFFlat, // or VectorIndexHNSW
    IVFFlatLists: 100,                          // default 100 when zero
})
// s.Migrate(ctx) now also issues:
//   CREATE INDEX IF NOT EXISTS rag_chunks_embedding_ivfflat
//     ON rag_chunks USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)
```

For HNSW set `VectorIndex: postgres.VectorIndexHNSW` and optionally
`HNSWConstructionM` (default 16). HNSW requires pgvector >= 0.5.

`Migrate` uses `CREATE INDEX IF NOT EXISTS`, so re-running with the
same `VectorIndex` is a no-op. Switching from IVFFlat to HNSW after
the fact requires dropping the prior index out-of-band — `Migrate`
will not drop it for you.

For hot production tables where DDL locks must be avoided, run
`CREATE INDEX CONCURRENTLY` out-of-band instead — `Migrate` does
not currently use `CONCURRENTLY` because it cannot run inside a
transaction:

```sql
-- ivfflat for cosine distance — faster build, lower memory
CREATE INDEX CONCURRENTLY ON rag_chunks USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

-- hnsw for higher recall — slower build, higher memory
CREATE INDEX CONCURRENTLY ON rag_chunks USING hnsw (embedding vector_cosine_ops);
```

`lists = sqrt(rowcount)` is a reasonable starting point for ivfflat.

### Reimport semantics

Use `ImportOptions.ReplaceSource: true` with `Document.SourceID` to
re-import a source cleanly. The store removes any existing chunks
where `metadata.source_id` matches before upserting the new chunks.
The number removed is surfaced in `ImportTrace.RemovedChunks`.

### Cleanup

`Remove` deletes a single chunk by ID. `RemoveByFilter` deletes
every chunk matching the namespace + filter and returns the count.
No soft-delete — deletion is final.
