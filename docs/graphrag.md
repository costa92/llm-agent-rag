# GraphRAG — relationship-traversal retrieval

`llm-agent-rag` v0.7 added **Tier-1 lightweight GraphRAG**: extract a
knowledge graph (entities + typed relations) from ingested documents and
retrieve by traversing it, fused as a fourth signal alongside dense,
lexical, and structure retrieval.

v0.8 adds **Tier-3 GraphRAG** on top of that foundation: hierarchical
**community detection**, lazy LLM-written **community summaries**,
map-reduce **global search** for whole-corpus "sense-making" questions, and
opt-in **fuzzy entity resolution**.

Every piece of GraphRAG is **opt-in and additive** — with none of it wired,
the SDK behaves exactly as before. Tier-3 sits behind seams that default to
no-ops; an SDK that wires only Tier-1 (or nothing) is byte-identical to v0.7.

---

## Tier-1 — the three pieces

Tier-1 GraphRAG is three composable seams. Wire the ones you need.

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
`Diagnostics.GraphTrace` (seed entities, reached entities, max hop). When
the store also carries detected communities (Tier-3 below), the trace adds
`GraphTrace.CommunityIDs` — the communities the reached entities belong to.

See `examples/graphrag_example_test.go` for a complete, deterministic
wiring, and `eval.RunGraphAB` for measuring the graph signal's effect on
retrieval recall.

---

## Tier-3 — communities and global search

Tier-1 answers **local** questions: "what is entity X and what is it
connected to?" — anchored to a query, retrieved by neighborhood traversal.
It cannot answer **global**, whole-corpus questions — "what are the main
themes across everything ingested?" — because there is no query-anchored
entity to traverse from.

Tier-3 adds that path. It groups the knowledge graph into a **community
hierarchy**, has the model write a **summary report** per community, and
answers global questions by a **map-reduce over those reports**. It is a
separate answer path (`System.AskGlobal`) from the Tier-1 `Ask` — it never
runs retrieve, rerank, or pack.

### 1. Community detection (`graph.CommunityDetector`)

A `CommunityDetector` partitions a `graph.Graph` into a hierarchy of
`graph.Community` clusters. Two implementations ship, both **deterministic
and pure stdlib** — the same graph always yields the same `[]Community`,
byte-for-byte (keystone KG3-6):

- `graph.LouvainDetector{Resolution: r}` — the hierarchical default. Runs
  the standard two-phase Louvain method (local modularity-gain greedy
  moves, then coarsening into super-nodes) and repeats. Each coarsening
  pass yields one hierarchy level: `Level` 0 is the finest partition; each
  higher level groups the level below it, linked by `ParentID`. The
  optional `Resolution` knob scales the modularity null-model term —
  values `> 1` favor smaller communities, `< 1` larger ones; `<= 0` is
  treated as classic modularity (`1.0`).
- `graph.LabelPropagationDetector{}` — the simpler single-level
  alternative. Each entity starts in its own community, then adopts the
  label carried by the greatest incident edge weight among its neighbors,
  sweeping until convergence. It produces one level (`Level` 0) — no
  hierarchy.

A `graph.Community` carries its `ID`, `Level`, `ParentID` (`""` at the top
level), and the sorted `EntityIDs` / `RelationIDs` of its members. Community
IDs are a deterministic function of the level and the cluster's members, so
the hierarchy is golden-testable output.

Wire a detector on `rag.Options.CommunityDetector`. When it is set **and**
the store implements `store.CommunityStore`, `Import` detects the community
hierarchy over the namespace graph after the graph is persisted, and
`UpsertCommunities` stores it. Re-detection is **full per-namespace**:
every re-ingest re-detects the whole namespace and replaces the stored set
(`UpsertCommunities` is replace-all). A nil detector, or a store that is
not a `CommunityStore`, leaves communities undetected — `Import` behaves
exactly as before.

`store.CommunityStore` is an optional capability, a sibling of
`GraphStore`: consumers type-assert for it and degrade gracefully when a
store does not implement it. `store.InMemoryStore` implements it; so does
`postgres.Store`. The community set is per-namespace.

### 2. Community summaries (`graph.CommunitySummarizer`)

A community on its own is just a set of entity IDs. To map-reduce over it,
the model writes a short **`graph.CommunityReport`** — a title and a
paragraph summary — for each community. The seam is
`graph.CommunitySummarizer`:

- `graph.LLMCommunitySummarizer{Model: m}` — prompts a `generate.Model`
  with the community's member entities and relations, parses a title and a
  paragraph leniently (a malformed response is never fatal, mirroring
  `LLMEntityExtractor`). A nil `Model` returns
  `graph.ErrCommunitySummarizerModelRequired`.

Wire it on `rag.Options.CommunitySummarizer`.

#### Lazy by default, eager by opt-in

Generating a report costs one model call per community. v0.8's design
choice (keystone KG3-2) is to make reports **lazy by default**:

- **Lazy** — `System.AskGlobal` generates a report only for the communities
  a given query actually selects, only on a **cache miss**. Each report is
  cached on the `CommunityStore` keyed by `graph.CommunityContentHash` — a
  deterministic SHA-256 over the community's sorted membership. A
  re-detected community with unchanged membership reuses its cached report;
  any membership change flips the hash and forces a fresh summary. The cost
  of summarization is paid incrementally, only for communities that are
  queried, and never paid twice for a stable community.

- **Eager** — `System.PrewarmCommunityReports(ctx, namespace)` walks every
  community in a namespace and generates+persists any report that is
  missing or stale, returning the count generated. It uses the *same*
  summarizer and the *same* `CommunityStore`-backed cache as the lazy path
  — it just pays the cost ahead of time so the first global query runs
  all-cache-hits. A report that is already fresh is left untouched.

**The tradeoff:** lazy spreads summarization cost across queries and never
summarizes a community no one asks about, but the first query touching a
cold community pays its summarization latency inline. Eager front-loads
*every* community's cost — including communities that may never be queried
— in exchange for uniformly fast global queries afterward. Prewarm when
global-query latency must be predictable (and the corpus is stable enough
that the up-front cost amortizes); stay lazy otherwise. Both paths share
one cache, so a prewarm followed by lazy queries, or the reverse, compose
without redundant generation.

A cache miss with no configured summarizer returns
`rag.ErrCommunitySummarizerRequired` — from `AskGlobal` on the lazy path,
from `PrewarmCommunityReports` on the eager path.

### 3. Global search (`System.AskGlobal`)

`System.AskGlobal(ctx, question, GlobalOptions)` answers a whole-corpus
question by **map-reduce over community reports**. It is a **separate
answer path** from `Ask`: it never calls retrieve, the reranker, or the
packer. The flow is *select → lazy report → map → reduce*:

1. **Select** — pick the coarsest community level (the broadest themes). If
   that level has more than `GlobalOptions.MaxCommunities` communities,
   rank them by query-token overlap with member entity names and cap to
   the top N (ties break by community ID — fully deterministic).
2. **Report** — resolve a `CommunityReport` per selected community: a cache
   hit on the `CommunityStore` is reused iff its `ContentHash` still
   matches the live community, otherwise the report is summarized lazily
   and cached.
3. **Map** — one model call per report: judge how much that community
   contributes to the answer and write a partial answer plus a self-rated
   helpfulness score.
4. **Reduce** — drop the score-0 partials, rank the survivors by score, and
   make one model call to synthesize the final answer.

`GlobalOptions` is intentionally small — v0.8 fixes global search on the
coarsest level, so the only knob is `Namespace` and `MaxCommunities` (a
value `<= 0` selects a sane default).

`Answer.Diagnostics.Global` (a `rag.GlobalDiagnostics`) attributes the run:
`CommunityIDs` consulted, the per-community `MapScores`, the `MapCalls` /
`ReduceCalls` counts, and `ConsultedReports` — the reports the map step
actually ran over (the grounding context an evaluator reads off the
`Answer`).

Graceful degradation matches the Tier-1 graph signal: a store that does not
implement `store.CommunityStore`, or a namespace with no detected
communities, yields an empty `Answer` and no error. A nil model returns
`rag.ErrModelRequired`.

#### Wiring global search

```go
sys := rag.New(rag.Options{
    Store:               st, // an InMemoryStore / postgres.Store — a CommunityStore
    Model:               model,
    EntityExtractor:     graph.DictionaryEntityExtractor{Terms: gazetteer},
    CommunityDetector:   graph.LouvainDetector{},          // detect at Import
    CommunitySummarizer: graph.LLMCommunitySummarizer{Model: model}, // write reports
})

// Import builds the graph and detects the community hierarchy.
if _, err := sys.Import(ctx, docs, ingest.ImportOptions{Namespace: "kb"}); err != nil {
    return err
}

// Optional: pay summarization cost up front so the first query is all-cache-hits.
if _, err := sys.PrewarmCommunityReports(ctx, "kb"); err != nil {
    return err
}

// Global search — a whole-corpus "sense-making" question.
answer, err := sys.AskGlobal(ctx, "What are the main themes across the corpus?",
    rag.GlobalOptions{Namespace: "kb", MaxCommunities: 8})
if err != nil {
    return err
}
fmt.Println(answer.Text)
```

See `examples/graphrag_global_example_test.go` for a complete, fully
deterministic end-to-end wiring (a `DictionaryEntityExtractor` gazetteer, a
`LouvainDetector`, and a single scripted `generate.Model` serving the
summarize / map / reduce steps).

### 4. Fuzzy entity resolution (`graph.EntityResolver`)

Tier-1 canonicalization is **exact-match only**: `graph.Canonicalize`
merges entities sharing a `(NormalizeName, type)` key, so "Acme" and
"Acme Corp" stay two separate nodes. Fuzzy resolution closes that gap.

`graph.EntityResolver` is an **opt-in pre-pass before `Canonicalize`**
(keystone KG3-8) — `Canonicalize` and its tests are untouched:

- `graph.NoopEntityResolver{}` — the **default**. Returns its input
  unchanged; with it, `Import` is byte-identical to pre-fuzzy-resolution
  behavior.
- `graph.EmbeddingEntityResolver{Embedder: e, Threshold: t}` — embeds each
  entity's `Name` via an `embed.Embedder`, clusters entities of the **same
  `Type`** whose cosine similarity is at least `Threshold`, and rewrites
  every member entity name — **and every matching relation endpoint** — to
  one canonical surface form per cluster (the longest member name, ties
  broken lexically lowest).

Rewriting relation endpoints is not optional: `Canonicalize` resolves
relation endpoints by name and **drops a relation whose endpoint matches no
entity**. A resolver that rewrote "Acme" → "Acme Corp" on the entity but
left a relation still naming "Acme" would silently orphan that relation —
so the `EntityResolver` seam rewrites both consistently.

The resolver is **deterministic by construction** (keystone KG3-6):
entities are processed in sorted-by-name order, clustering is single-link
over a fixed sorted pair scan, the canonical name is chosen by a fixed
rule, and there is no randomness — the same input always yields the same
output. It is unit-tested against a scripted embedder returning fixed
vectors.

Wire it on `rag.Options.EntityResolver`. `graph` gains an `embed` import to
support `EmbeddingEntityResolver` — `embed` is a stdlib-only leaf package,
so no dependency cycle and no new module dependency.

#### The false-positive caveat — ship conservative

Embedding-similarity merging can be **wrong**: "Apple" the company and
"Apple" the fruit embed close together but are different entities; two
unrelated people with similar name embeddings should not collapse into one
node. A false merge is **destructive** — it conflates two real entities and
cannot be undone downstream.

v0.8 therefore ships `EmbeddingEntityResolver` deliberately conservative:

- **Opt-in.** The default `NoopEntityResolver` does nothing; fuzzy
  resolution is never on unless explicitly wired.
- **High default threshold.** When `Threshold <= 0` the resolver uses
  `0.92` — a near miss stays two nodes rather than risking a false merge.
- **Same-type-only.** Entities of different `Type` never merge — a person
  is never folded into an org.

The honest framing: fuzzy resolution trades recall (catching "Acme" /
"Acme Corp" as one entity) against the precision risk of a false merge.
v0.8 picks precision. Raise `Threshold` if false positives appear; lower it
cautiously, and only with a representative corpus to check against.
Resolution-quality improvements are a v0.9 item (below).

### 5. Evaluating global search (`eval.GlobalEvaluator`)

`eval.RunGraphAB` measures **chunk recall@k** — the right metric for
Tier-1 local retrieval, meaningless for global search, which synthesizes an
answer with **no gold chunk set**. v0.8 adds a separate harness for the
global path:

- `eval.GlobalAsker` — the seam `*rag.System` satisfies via `AskGlobal`;
  the global-search counterpart of `eval.Asker`.
- `eval.GlobalEvaluator{Asker, Judge, MaxCommunities}` — runs a `Dataset`
  of whole-corpus questions through `AskGlobal` and scores each answer with
  the RAG-Triad `Judge`. The judge's grounding context is the community
  reports the answer actually consulted
  (`Answer.Diagnostics.Global.ConsultedReports`), so global-search
  groundedness reads as "is the answer grounded in the community reports it
  read"; answer relevance is question-vs-answer.
- `eval.GlobalEvalResult` — carries `MeanGroundedness`,
  `MeanAnswerRelevance`, and per-example detail. It deliberately carries
  **no** chunk recall@k / precision@k: global search has no chunk-recall
  notion. `RunGraphAB` / `Evaluator` remain the chunk-recall path for the
  local signal.

The harness is exercised by a scripted-model + scripted-judge CI gate, the
project's standard deterministic-evaluation discipline.

---

## Deferred to v0.9

v0.8 delivers the first half of v0.7's "Deferred to v0.8" list — community
detection, community summaries, map-reduce global search, fuzzy entity
resolution. The remainder is explicitly **not** in v0.8:

- **DRIFT search** — the query-adaptive blend of local entity traversal and
  global community context. v0.8 ships `Ask` (local) and `AskGlobal`
  (global) as two distinct paths; DRIFT's dynamic routing between them is a
  v0.9 item.
- **Incremental community maintenance** — v0.8 does **full per-namespace
  re-detection** on every re-ingest (`UpsertCommunities` is replace-all).
  Incrementally updating only the communities a re-ingest actually touched
  — and selectively invalidating only their reports — is deferred.
- **Path-ranking / subgraph-as-evidence** — ranking and returning a
  structured subgraph (a chain of relations) as an evidence artifact,
  rather than scoring provenance chunks. Still a v0.9 item, unchanged from
  the v0.7 deferral.
- **Fuzzy-resolution quality improvements** — v0.8's
  `EmbeddingEntityResolver` is deliberately conservative (high threshold,
  same-type-only, single-link clustering). Better clustering, type-aware
  thresholds, description-aware embedding, and an audit trail for merges
  are deferred.

A dedicated **graph database** (Neo4j, etc.) is still intentionally not
used: `GraphStore` and `CommunityStore` are interfaces, so a
`neo4jgraph`-style subpackage can be added later in full isolation without
touching any existing code. Recursive-CTE traversal over Postgres covers
this SDK's scale.
