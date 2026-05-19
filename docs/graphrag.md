# GraphRAG — relationship-traversal retrieval

`llm-agent-rag` v0.7 adds **Tier-1 lightweight GraphRAG**: extract a
knowledge graph (entities + typed relations) from ingested documents and
retrieve by traversing it, fused as a fourth signal alongside dense,
lexical, and structure retrieval.

GraphRAG is **opt-in and additive** — with none of it wired, the SDK
behaves exactly as before.

## The three pieces

GraphRAG is three composable seams. Wire the ones you need.

### 1. Entity extraction (`graph.EntityExtractor`)

An extractor turns each chunk's text into entities and relations at ingest
time. Set it on `rag.Options.EntityExtractor`:

- `graph.LLMEntityExtractor{Model: m}` — prompts a `generate.Model`; the
  production path.
- `graph.DictionaryEntityExtractor{Terms: gazetteer}` — deterministic,
  zero-LLM; the basis for reproducible tests and a no-LLM default.

`Import` runs the extractor post-split, canonicalizes the graph
(exact-match `(name, type)` merge with source-chunk provenance), and
surfaces it on `ImportResult.Graph`.

### 2. Graph storage (`store.GraphStore`)

`GraphStore` is an **optional capability** a `store.Store` may implement
(the same pattern as `store.LexicalSearcher`):

- the in-memory `store.InMemoryStore` implements it with a stdlib
  adjacency graph;
- `postgres.Store` implements it with `entities`/`relations` tables and
  recursive-CTE traversal — **no graph database**.

When the store is a `GraphStore`, `Import` persists the extracted graph and
reconciles it on a `ReplaceSource` re-ingest (stale contributions removed,
the re-extracted subgraph union-merged). Traversal is hard-bounded: depth
≤ 2, with a per-hop fan-out cap.

### 3. Graph retrieval (`retrieve.GraphRetriever`)

`GraphRetriever` links a query to seed entities (`EntityLinker` —
`LexicalEntityLinker` is the default), expands their bounded neighborhood,
and scores the entities' provenance chunks by graph proximity. Wire it as
the `Graph` field of `HybridRetriever`:

```go
retrieve.HybridRetriever{
    Dense:     retrieve.DenseRetriever{Embedder: emb, Store: st},
    Lexical:   retrieve.LexicalRetriever{Store: st},
    Structure: retrieve.StructureRetriever{Store: st},
    Graph:     retrieve.GraphRetriever{Store: st},
}
```

It fuses as a fourth reciprocal-rank-fusion signal — never replacing dense
or lexical. Enable it per query with `SearchOptions.EnableGraph`. The
graph's contribution is attributed in `FusionAttribution.GraphRank` and in
`Diagnostics.GraphTrace` (seed entities, reached entities, max hop).

See `examples/graphrag_example_test.go` for a complete, deterministic
wiring, and `eval.RunGraphAB` for measuring the graph signal's effect on
retrieval recall.

## Deferred to v0.8

v0.7 is the LightRAG end of the GraphRAG spectrum. Explicitly **not** in
v0.7:

- Microsoft-GraphRAG-style hierarchical **community detection** and
  LLM-generated **community summaries**;
- map-reduce **global search** and **DRIFT search**;
- **fuzzy / embedding-similarity entity resolution** — v0.7
  canonicalization is deterministic exact-match only;
- path-ranking / structured subgraph-as-evidence output.

A dedicated **graph database** (Neo4j, etc.) is intentionally not used:
`GraphStore` is an interface, so a `neo4jgraph`-style subpackage can be
added later in full isolation without touching any v0.7 code. Recursive-CTE
traversal over Postgres covers this SDK's scale.
