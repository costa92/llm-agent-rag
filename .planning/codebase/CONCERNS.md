# Codebase Concerns

**Analysis Date:** 2026-05-20
**Repo:** `github.com/costa92/llm-agent-rag` @ v1.0.1
**Scope:** This repo is the *fixed point* of the ecosystem — its v1.x additive-only contract is what every other repo pins. Concerns here are weighted accordingly: API-stability risks are first-class incidents, not stylistic notes.

---

## Executive Summary

This is a notably clean repository for its scale (≈26 top-level packages, ≈12k LoC). The most striking finding is what is **absent**: zero `TODO`, zero `FIXME`, zero `XXX`, zero `HACK`, zero `Deprecated:` markers in production code; zero `panic()` outside `examples/`; no goroutine spawns outside tests; properly quarantined pgx deps. The v1 freeze infrastructure (`internal/apisnapshot` + `contract/` + `api/v1.snapshot.txt`) is thoughtfully layered.

Concerns are therefore concentrated in three places:

1. **CHANGELOG drift at v1.0.1** — high severity precisely because the freeze depends on visible release semantics.
2. **Surface-area framing** — `advanced`, `agentic`, `feedback`, `guard` are frozen v1 surface but are documented as "extras" and only thinly tested. If any of them was meant as v2-staging, the v1 freeze just locked them in.
3. **Test coverage cliffs** — a handful of packages (`generate`, `embed`, `tree`, `pack`, `adapter`) have unusually low test:source ratios for a v1 fixed-point repo. The cross-repo `contract` gate pins only ~7 packages out of 22; the `api/v1.snapshot.txt` gate covers the rest but only detects *signature* drift, not behavioral regressions.

---

## 1. API Stability Risks (highest stakes here)

### 1.1 v1.0.1 tag exists with NO CHANGELOG entry — high

**Description:** The git tag `v1.0.1` (2026-05-20, commit `09697ca` "chore: bump llm-agent back-edge to v0.5.0") exists but `CHANGELOG.md` has no `## [v1.0.1]` section. The newest CHANGELOG entry is still `## [v1.0.0] - 2026-05-21`. The umbrella's own CONCERNS.md already flagged this; it is confirmed.

**Evidence:**
- `git tag -l` shows `v1.0.0` (2026-05-19) and `v1.0.1` (2026-05-20).
- `CHANGELOG.md:9` — top entry is `## [v1.0.0] - 2026-05-21` (note: even *this* date does not match the v1.0.0 tag date of 2026-05-19).
- `git show v1.0.1` — annotated tag message: "v1.0.1 — back-edge bump to llm-agent v0.5.0; no public API change (KE-2)". A patch-bump-grade change exists but is undocumented in the public log.

**Severity:** High. Downstream repos read CHANGELOG to decide whether to bump. A silent patch tag undermines the freeze's social contract even when the contents are benign. It also creates a second drift in the same line — the v1.0.0 entry date (2026-05-21) does not match the v1.0.0 tag date (2026-05-19), suggesting the CHANGELOG is being hand-curated post hoc rather than written at tag time.

**Suggested next action:**
1. Add `## [v1.0.1] - 2026-05-20` section noting "back-edge dep bump to llm-agent v0.5.0; no public API change; covered by `internal/apisnapshot` gate at HEAD".
2. Correct the v1.0.0 entry date (2026-05-21 → 2026-05-19) to match the tag, or document the rationale for the delta.
3. Add a release-process item to the freeze policy: "tagging a v1.x release requires a corresponding CHANGELOG entry in the same commit." Enforce via a stdlib `go test` gate in `internal/apisnapshot/` that diffs `git tag -l 'v1.*'` against `## [vX.Y.Z]` headings in `CHANGELOG.md`.

### 1.2 `advanced`, `agentic`, `feedback` are frozen v1 surface but framed as "extras" — high

**Description:** Packages `advanced`, `agentic`, and `feedback` are part of the v1.0 frozen surface (each appears in `api/v1.snapshot.txt`) but are conspicuously absent from `README.md`'s "Package layout" section (`README.md:31-44`) and from `doc.go:7`. They are not used internally by `rag/` except for `retrieve/retrieve.go:240-256` calling `advanced.ExpandQuery` / `GenerateHypothetical`. `agentic` and `feedback` have no intra-module consumers at all. If any of these were a staging ground for v2, the v1 freeze irreversibly locked their exported shape (no rename ever, no removal, no signature change).

**Evidence:**
- `README.md` package layout (lines 31-44) lists ingest, embed, store, postgres, retrieve, pack, rerank, generate, prompt, rag, eval, tree, adapter/llmagent. Missing: advanced, agentic, feedback, guard, graph, obs.
- `doc.go:7` lists "rag, retrieve, store, embed, ingest, generate, pack, prompt, rerank, graph, eval, and the rest". "The rest" silently includes advanced/agentic/feedback/guard/obs.
- `api/v1.snapshot.txt:7-37` — `advanced` and `agentic` symbols frozen.
- `docs/api-audit-v1.0.md:47-60` — every advanced/agentic symbol is marked `keep` (frozen).
- Reverse-import check: `grep -rln '"github.com/costa92/llm-agent-rag/agentic"' . → ./agentic only`; same for `feedback`. No internal consumer.

**Severity:** High. These packages now carry the same compatibility weight as `rag` itself, but their freshness (5月 19), low internal reuse, and absence from the headline README suggest they were not vetted as load-bearing API. Locking provisional shapes is the most common way an "additive-only v1" gets painted into a corner.

**Suggested next action:**
1. Decide explicitly per package: **(a)** promote to documented v1 surface (add to README package layout, write package-level usage example, add to the cross-repo `contract/` gate); or **(b)** acknowledge "stable shape, advisory utility" status in `doc.go` so users know not to build core integrations on them.
2. For `feedback` specifically — it is `Recorder`/`OpenFile`/`Capture`/`BuildExample` (`feedback/feedback.go`) and has no internal users. If a future v2 wants a different writer abstraction (`io.Writer` → batched async writer with a `Flush(ctx)`), v1 can no longer make that change additively without leaving two parallel APIs. Consider whether the current shape is the one to freeze.
3. Same audit for `guard` (inject.go, redact.go) — internally used by `rag/import.go` and `rag/inject.go` so it is more clearly first-class.

### 1.3 `contract/` gate covers only ~7 of 22 importable packages — medium

**Description:** The cross-repo compile-pin `contract/contract_test.go` references symbols from `embed, generate, ingest, prompt, rag, store, retrieve` — that's the "narrow cross-repo subset". The intra-repo whole-surface snapshot (`api/v1.snapshot.txt`, 882 lines, regenerated by `internal/apisnapshot`) covers everything else. This layering is documented (`contract/contract_test.go:1-14`) and is fine in principle, but it means:

- `advanced`, `agentic`, `eval`, `feedback`, `graph`, `guard`, `obs`, `tree`, `adapter/llmagent` are protected only by the *snapshot* gate, not the *compile* gate.
- The snapshot gate detects signature drift but cannot detect cross-repo coordination needs. A breaking-by-name change to `eval.Evaluator` (already shown to be a thing — see `CHANGELOG.md:28-31`) would not break any downstream compilation in `contract/` but might silently break consumers in other repos.

**Evidence:**
- `contract/contract_test.go:34-103` — explicit symbol list.
- `internal/apisnapshot/apisnapshot.go:20-24` — "skips every _test.go file and anything under an internal/ path segment".
- `api/v1.snapshot.txt` is 882 lines spanning all importable packages.

**Severity:** Medium. The asymmetry is intentional, but the *threshold for adding a package to `contract/`* is undocumented. Future PRs are likely to add new exported symbols to `advanced`/`agentic`/`eval` without considering cross-repo impact.

**Suggested next action:**
1. Document the criterion in `contract/contract_test.go` package doc: "a symbol joins this file when at least one current llm-agent-ecosystem repo imports it." (That's already the de facto rule; make it explicit.)
2. Add a stdlib `go test` cross-check that grep-scans the sibling repos for `llm-agent-rag/<pkg>` imports and asserts each imported symbol appears in `contract_test.go`. (Listed as a future-work item in the audit but worth tracking explicitly.)

### 1.4 `generate.Model` has zero test files — medium

**Description:** Package `generate` is the most cross-cutting seam in the SDK (consumed by `advanced`, `agentic`, `rag`, `eval`, `feedback`, `adapter`, `prompt`, `retrieve`). It has 43 LoC of source and **0 test files** (`find generate -name '*_test.go' → empty`). Its types are exercised through downstream tests but no unit test pins behavior at the `generate` level — for example, the `Usage` zero-value contract documented in `generate/types.go:17-23` ("the zero value when usage is unknown") has no test.

**Evidence:**
- `find generate -name '*_test.go'` → empty.
- Per-pkg ratio scan: `generate src=43 test=0 ratio=0.00`.
- `generate/types.go:17-23` documents a behavioral contract (zero-value semantics) with no test.

**Severity:** Medium. The package is small and the types are POGOs. The bigger risk is that any future v1-additive change to `Request`/`Response`/`Usage` (e.g., adding a `Stop` field) has no guard against accidentally shifting struct memory layout in ways that would affect callers using struct literals positionally — though Go's rules forbid positional literals on exported structs with mixed-case fields, so this is mostly hypothetical.

**Suggested next action:** Add `generate/generate_test.go` with at least: (a) a compile-time `var _ Model = ...` assertion using a tiny in-package fake; (b) a test for the documented `Usage{}` zero-value contract; (c) a test confirming `Request{Messages: nil}` is a valid input shape.

### 1.5 No `Deprecated:` markers but several "rename →" decisions ratified — low

**Description:** `docs/api-audit-v1.0.md` ratifies several breaking renames that *were applied* in slice 28-02 before the freeze (`eval.Evaluator → RetrievalEvaluator`, etc.). After v1.0.0 no symbol carries a `Deprecated:` marker, which is the correct state for a hard freeze. However, this means the project has no mechanism for soft-deprecation in the v1.x window — any future "we wish this were named X" insight has only the `/v2` exit, even for trivial cases. This is a deliberate policy choice but worth surfacing.

**Evidence:**
- `grep -rn 'Deprecated:' .` → zero matches.
- `docs/compatibility.md:18-26` states the additive-only rule.
- `CHANGELOG.md:23-39` documents the "final breaking changes before the freeze".

**Severity:** Low. The policy is deliberate. Flag only so that a future contributor does not introduce a `Deprecated:` marker thinking it implies removal in some later v1.x — it does not.

**Suggested next action:** Add a one-line note to `docs/compatibility.md`: "Deprecation markers are not used in the v1.x line — any rename is a /v2 event."

---

## 2. Architectural Concerns

### 2.1 `postgres` pgx-pool leak into dependents' go.sum — low (mitigated)

**Description:** `postgres/` legitimately pulls `github.com/jackc/pgx/v5` and `github.com/pgvector/pgvector-go` (`go.mod:7-8`). When a downstream module imports `llm-agent-rag` for *any* package, `go mod tidy` will pull pgx into the downstream's `go.sum` (Go computes module graphs transitively, even for unimported packages). This is the expected Go behavior, not a bug — but it means every consumer of `llm-agent-rag/embed` (an otherwise stdlib-only seam) carries pgx in their checksum file.

**Evidence:**
- `go.mod:5-9` — pgx + pgvector are top-level (not behind a build tag).
- `postgres/postgres.go:9-19` is the only place pgx is imported.
- `grep -rln 'jackc/pgx\|pgvector' .` returns only `postgres/*` files.
- Contrast with `adapter/llmagent` which uses `//go:build llmagent` (`adapter/llmagent/model.go:1`) precisely to avoid this.

**Severity:** Low. The freeze policy already designates pgx as "the only non-stdlib island". The leak is real but documented. The fact that `adapter/llmagent` uses build tags for the same problem shows the team knows the alternative.

**Suggested next action:**
1. Add a note to `docs/backend-selection.md` explaining that pgx appears in transitive `go.sum` for *all* consumers, not just postgres-backend users — this prevents surprised downstream questions.
2. Consider whether `postgres` could be split into its own `llm-agent-rag-postgres` module post-v2 to eliminate the leak entirely. Document the trade-off (extra repo, extra release coordination) in `docs/compatibility.md` so a future /v2 decision has prior art.

### 2.2 `obs/` package vs `llm-agent-otel` sibling — low

**Description:** `obs/` (`obs/obs.go`) defines the cost/latency types (`Metrics`, `StageTiming`, `CallCounts`, `TokenUsage`, `Counter`) and a context-attached `Counter` for call counting. The README (`README.md:64`) says these are "consumed by `llm-agent-otel`". There is no overlap of *names*, but conceptually there is overlap of *role* — both this repo's `obs` and the sibling otel repo are observability surfaces. The boundary is "obs defines the data shape, otel adapts it to OpenTelemetry" — this is the right split, but it isn't stated explicitly in `obs/obs.go`'s package doc.

**Evidence:**
- `obs/obs.go:1-5` — package doc explains it is "a leaf package" but does not name the otel sibling.
- `docs/production-deployment.md:130` — "OTel adapters in the llm-agent-otel sister repo wire to these".

**Severity:** Low. There is no actual cycle or overlap; the documentation just leaves the boundary implicit. Future contributors looking at `obs/` might be tempted to add OTel-specific helpers here, which would either pull in `go.opentelemetry.io/otel` (violating the stdlib-only rule outside `postgres/`) or build an indirection layer that duplicates work in `llm-agent-otel`.

**Suggested next action:** Extend the `obs/obs.go` package doc to name `llm-agent-otel` and state explicitly: "OTel mapping lives in the sibling repo; this package must stay stdlib-only and OTel-vocabulary-free."

### 2.3 No cyclical imports — clean

**Description:** Manual dependency-graph audit (intra-module imports per package) shows no cycles. Notably, `obs/` and `generate/` are true leaf packages with zero intra-module deps. `embed/` and `guard/` are similar. The DAG flows generally as: leaves → graph/store/ingest → retrieve/pack/rerank → rag → eval/agentic/feedback.

**Evidence:** Per-package import scan (this report's exploration step) — every intra-module import is downstream of the importer in the DAG.

**Severity:** None. Logging this as the *absence* of a concern.

### 2.4 `internal/` correctly hides what should be hidden — clean

**Description:** Only `internal/apisnapshot/` exists. It contains the v1-snapshot generator (correctly hidden, since it is a build-time tool, not a runtime API). The `apisnapshot.go:72-89` walker explicitly skips anything under an `internal/` path segment when generating the public surface — so the gate machinery is correctly not part of the frozen surface.

**Severity:** None.

### 2.5 `retrieve/retrieve.go` is 1588 lines — medium

**Description:** A single file holding 1588 LoC is hard to navigate. The package is `retrieve` (the second-largest after `rag/` and `graph/`) and concentrates almost all retrieval logic into one file (`retrieve/retrieve.go`), with `multihop_test.go` and `retrieve_test.go` (1384 LoC) alongside.

**Evidence:**
- `wc -l retrieve/retrieve.go` → 1588.
- `wc -l retrieve/retrieve_test.go` → 1384.
- For comparison, `graph/louvain.go` (the second largest single file) is 295 LoC.

**Severity:** Medium. This is a maintenance / cognitive-load concern, not a correctness concern. Splitting risks the v1 freeze (file-internal renames are fine; but if any unexported type is currently used by a `var _ Interface = (*Type)(nil)` pin in `retrieve_test.go` from a parallel file, splitting can be invisible). Most importantly, large files attract bugs and make `go test ./retrieve/...` diff-review painful.

**Suggested next action:** Post-v1.0.x, split `retrieve/retrieve.go` along the visible seams already present in the package doc: query preprocessing, lexical, hybrid, MQE/HyDE, structure-aware, fanout, trajectory. Each is naturally a separate file. No exported symbol moves — only file boundaries change.

---

## 3. Test Gaps (specific, not generic)

Computed per-package test-to-source line ratios (excluding `examples/` and `docs/`):

| Package | Source LoC | Test LoC | Ratio | Verdict |
|---|---|---|---|---|
| `generate` | 43 | 0 | **0.00** | Critical (cross-cutting seam) |
| `embed` | 126 | 24 | **0.19** | Low (frozen behavioral surface untested) |
| `tree` | 226 | 57 | **0.25** | Low |
| `pack` | 205 | 60 | **0.29** | Low |
| `adapter` | 239 | 76 | **0.32** | Acceptable (build-tagged) |
| `postgres` | 950 | 295 | **0.31** | Acceptable (env-gated integration) |
| `store` | 1526 | 504 | **0.33** | Driven by `storetest/` shared suite |
| `internal` | 343 | 118 | **0.34** | Build-time tool |
| `ingest` | 472 | 178 | 0.38 | Acceptable |
| `obs` | 95 | 40 | 0.42 | Acceptable |
| `prompt` | 77 | 35 | 0.45 | Acceptable |
| `guard` | 186 | 108 | 0.58 | Acceptable |
| `rerank` | 333 | 224 | 0.67 | Good |
| `graph` | 1494 | 1035 | 0.69 | Good |
| `advanced` | 78 | 59 | 0.76 | Good |
| `retrieve` | 2053 | 1748 | 0.85 | Good |
| `agentic` | 175 | 166 | 0.95 | Excellent |
| `eval` | 886 | 987 | 1.11 | Excellent |
| `rag` | 2191 | 2611 | **1.19** | Excellent |
| `feedback` | 99 | 213 | 2.15 | Excellent |

### 3.1 `generate` zero-test — see §1.4 above

### 3.2 `embed` 0.19 ratio — medium

**Description:** `embed/` has 126 LoC across 3 files but only 24 LoC of test. It exports `Vector`, `Embedder` interface, `CosineSimilarity`, and `HashEmbedder` — all frozen-surface v1 symbols. `HashEmbedder` is the deterministic test/default embedder for the whole module; its determinism property is the foundation of every deterministic test downstream. There is no test asserting `HashEmbedder.Embed(ctx, "x")` returns the same vector across calls/processes.

**Evidence:**
- `find embed -name '*_test.go'` → 1 file.
- `embed/` source contents: `embed.go` (interface + Vector), `cosine.go` (similarity), `hash.go` (HashEmbedder).
- `api/v1.snapshot.txt:39-50` — embed symbols frozen.

**Severity:** Medium. A subtle change to hashing constants in `HashEmbedder` would silently invalidate every recorded test fixture in the rest of the repo *and* in downstream repos that pin vectors.

**Suggested next action:** Add at minimum: (a) a determinism test (same input → same output across two calls); (b) a golden-vector test pinning the first 8 elements of `Embed(ctx, "the quick brown fox")` for the default `NewHashEmbedder(32)`; (c) a `CosineSimilarity` symmetry/identity test.

### 3.3 `pack` and `tree` low coverage — low

**Description:** `pack` (token-budget context packer) at 0.29 ratio and `tree` (document-tree primitives) at 0.25 — both are exported v1 surface with thin tests. `tree` in particular has only 57 lines of test against 226 lines of source, and the package is explicitly named in README package layout.

**Suggested next action:** Add a slice of focused tests around the documented contracts in each package (`pack` — token-budget overflow; `tree` — depth/path traversal edge cases).

### 3.4 `postgres` — integration test wiring exists but is silent in default CI — medium

**Description:** `postgres/postgres_test.go:17` and `postgres/postgres_conformance_test.go:21` both gate on `LLM_AGENT_RAG_PG_URL`. `t.Skip` is called when the env var is unset — so `go test ./...` without it returns a green "PASS [no tests to run]" outcome for postgres, masking any regression. The 295 LoC of postgres test only runs against a live database.

**Evidence:**
- `postgres/postgres_test.go:17,23` — `const liveEnvVar = "LLM_AGENT_RAG_PG_URL"` then `t.Skipf(...)`.
- `postgres/postgres_conformance_test.go:21` — same gate.
- `README.md:184-188` verification block has no PG_URL invocation.

**Severity:** Medium. The repo-level CI is presumably running these. But a contributor running `go test ./...` locally sees green and may conclude their postgres change is safe.

**Suggested next action:**
1. Add a build-time hint test that runs without the env var and prints "postgres integration tests skipped — set LLM_AGENT_RAG_PG_URL to run" (some teams do this with `t.Log` at TestMain).
2. Document in the README (or `docs/production-deployment.md`) the exact local docker-compose command to bring up a transient pgvector for `go test ./postgres/...`.
3. Consider whether the snapshot-gate machinery should additionally assert that exported `postgres.*` symbols have at least one test file referencing them.

### 3.5 GraphRAG complexity vs test coverage — acceptable but watch this

**Description:** `graph/` is 1494 LoC across 9 files, second-largest source-LoC package after `rag/`. Its test ratio is 0.69. The components are heavy: `louvain.go` (295 LoC, community detection algorithm), `path.go` (220 LoC, ranked path enumeration), `resolve.go` (207 LoC, entity resolution). `graph/graph.go` itself (the type-defs file) has only a 13-line smoke test, but it's only types.

**Evidence:**
- `wc -l graph/*.go` and pair-matching test files.
- `graph/community_test.go` (248 LoC) vs `graph/community.go` (245 LoC) — well covered.
- `graph/louvain.go` (295 LoC) — paired with `community_test.go` and `louvain_test.go`(?) — let me note that `louvain.go` itself has no eponymous `louvain_test.go`; the algorithm is tested indirectly via community tests.

**Severity:** Low to medium. The community/Louvain algorithm is non-trivial and hard to debug if it breaks. The test-via-public-API approach is sound, but the absence of a dedicated `louvain_test.go` means regression diagnosis points to community-level symptoms first.

**Suggested next action:** Add a focused `graph/louvain_test.go` with deterministic small-graph cases (the canonical Zachary-karate-club test, or a 6-node hand-computed example) that pin the algorithm's behavior independently of the community-detection orchestration.

### 3.6 `eval/` — the eval framework is itself a test, but is also tested — clean

**Description:** Concern from the prompt: "Eval framework — is it tested, or is it itself a test?" Answer: both. `eval/` has 886 LoC of source and 987 LoC of test (ratio 1.11). Files like `eval/eval_test.go`, `eval/triad_test.go`, `eval/global_test.go`, `eval/drift_test.go`, `eval/judge_test.go`, `eval/loader_test.go`, `eval/graph_test.go` cover every evaluator type. The eval package is both the CI gate for retrieval quality (see `eval/eval.go:1-17` package doc) and a normal package with unit tests of its own metrics.

**Severity:** None.

---

## 4. Documentation / CHANGELOG drift

### 4.1 CHANGELOG missing v1.0.1 entry — see §1.1

### 4.2 CHANGELOG date for v1.0.0 disagrees with git tag date — medium

**Description:** `CHANGELOG.md:9` claims `## [v1.0.0] - 2026-05-21` but `git tag -l --format='%(refname:short) %(creatordate:short)'` shows the v1.0.0 tag created on 2026-05-19. A two-day delta.

**Severity:** Medium. This is the kind of thing that downstream consumers spot when they correlate "when did this release happen" against their own dep-bump dates and find a discrepancy.

**Suggested next action:** Align the CHANGELOG date with the tag date. Going forward, make CHANGELOG date == tag date via release process.

### 4.3 `README.md` "Package layout" undercounts public packages — high (overlap with §1.2)

**Description:** README package-layout section (`README.md:31-44`) lists 14 packages. The repo has 22 importable, snapshot-tracked packages. Missing from README: `advanced`, `agentic`, `feedback`, `graph`, `guard`, `obs`, `contract` (test-only), plus `internal/apisnapshot` (correctly hidden).

**Severity:** High (because cross-references with the v1 freeze — see §1.2). Users reading the README will not even discover the existence of `advanced.ExpandQuery` or `agentic.CorrectiveAsker`.

**Suggested next action:** Update `README.md:31-44` to include all packages in the frozen surface, with one-line summaries (`graph` — knowledge-graph entities and relations for GraphRAG; `guard` — prompt-injection redaction; `obs` — cost/latency metrics; etc.).

### 4.4 `doc.go` undercounts public packages — medium

**Description:** `doc.go:7` says "the sub-packages directly — rag, retrieve, store, embed, ingest, generate, pack, prompt, rerank, graph, eval, and the rest". "And the rest" is doing a lot of work here — it elides `advanced`, `agentic`, `feedback`, `guard`, `obs`, `tree`, `adapter/llmagent`.

**Severity:** Medium. `go doc github.com/costa92/llm-agent-rag` is one of the first commands a user runs — that output should name every importable package.

**Suggested next action:** Replace "and the rest" with an enumerated list.

### 4.5 Zero TODO/FIXME/XXX/HACK comments — clean

**Description:** Exhaustive grep across all `.go` files (excluding `_test.go`):

```
TODO   → 0 matches
FIXME  → 0 matches
XXX    → 0 matches
HACK   → 0 matches
NOTE:  → 0 matches
```

This is extraordinary discipline at this scale. Logged as positive observation.

**Severity:** None.

---

## 5. Cross-Repo Coupling Concerns

### 5.1 `contract/` is the only compile-pinned cross-repo surface — medium

**Description:** As covered in §1.3, only 7 of 22 packages are in the cross-repo `contract/` gate. If a downstream repo (`llm-agent-otel`, `llm-agent`, etc.) imports outside that subset, a future v1.x-additive change can still indirectly break it via behavioral drift, which the snapshot gate cannot catch.

**Severity:** Medium.

**Suggested next action:** Maintain a *manifest* file (e.g., `contract/CROSS_REPO_IMPORTS.md`) listing which sibling repos import which `llm-agent-rag` packages, updated when a sibling repo adds an import. The freeze policy could be amended: "adding a sibling-repo import of a non-`contract`-pinned package requires adding the package to the contract first."

### 5.2 `rag.Options` field surface is the highest blast-radius v1 symbol — medium

**Description:** `rag/options.go` (120 LoC) imports 11 of the module's own packages (`embed, generate, graph, guard, ingest, pack, prompt, rerank, retrieve, store` + the implicit obs via observer). Every field of `rag.Options` is part of the frozen surface — adding a field is additive (safe), but removing one or renaming is `/v2`. Any downstream that uses `rag.New(rag.Options{...})` with named-field literals is fine; any that uses positional construction (impossible for a struct with mixed-case fields, so theoretical) would be locked.

**Severity:** Medium. The risk is not technical but social — `rag.Options` is the spinal cord of every integration. If a future contributor decides "this field name was wrong", v1 cannot fix it.

**Suggested next action:** Add an `rag/options_test.go` golden-naming test that simply lists every field name in `Options`, comparing against a committed string slice. This makes any accidental rename (which would otherwise be caught by the API snapshot gate, but with a less obvious diff) into a single-symbol obvious diff.

### 5.3 Over-exposed types — low

**Description:** Most types in the API are deliberately exposed. One mild candidate for re-evaluation: `retrieve.TrajectoryStep` (`api/v1.snapshot.txt` shows it pinned by `contract/contract_test.go:101`). Trajectory information is fundamentally diagnostic; whether downstream consumers need the *struct shape* or just a printed representation is unclear. Now frozen either way.

**Severity:** Low (locked in; flag for v2 audit).

**Suggested next action:** Maintain a forward-looking `docs/v2-wishlist.md` listing types whose shape we would re-examine on a /v2 cut. Start with `retrieve.TrajectoryStep`, the `rag.Trace` family, and any `agentic.Result`/`Attempt` that ended up frozen without being load-bearing.

---

## 6. Operational Risks

### 6.1 No `panic()` in production paths — clean

**Description:** `grep -rn 'panic(' --include='*.go' .` matched only `examples/*_test.go` (12 hits, all in examples). The runtime packages never panic.

**Severity:** None.

### 6.2 No goroutine spawns outside tests — clean

**Description:** `grep -rn 'go func()' --include='*.go' .` excluding tests → empty. No background goroutines, no fire-and-forget patterns, no leaked goroutine risk. The `feedback.Recorder.Capture` uses a `sync.Mutex` and synchronous bufio.Writer — no async path. The `postgres` package uses a caller-supplied `*pgxpool.Pool` and never spawns goroutines itself.

**Severity:** None.

### 6.3 Resource handling — clean with one stylistic note

**Description:** Every `os.Open` and `pgx.Rows` is closed via `defer` except `postgres/graph.go:150-155` where a scan-error path calls `rows.Close()` directly then falls through to a `defer`-free post-loop `rows.Close()` followed by `rows.Err()` check. This is correct (pgx-v5 allows `Err()` on closed rows) but reads awkwardly — the standard idiom is `defer rows.Close()` and a single `rows.Err()` check after the loop.

**Evidence:** `postgres/graph.go:141-158`.

**Severity:** Low (stylistic only). The loop is correct.

**Suggested next action:** Refactor to `defer rows.Close()` at line 142 for symmetry with the rest of the file (lines 210, 233, 244, 292, 320 all use the deferred pattern).

### 6.4 Error swallowing — none detected

**Description:** Grep for typical error-swallow patterns (`_ = err`, `_ =` assignments of error-returning calls, `// ignore error` comments) returned nothing across production code.

**Severity:** None.

### 6.5 `feedback.OpenFile` permission 0o600 — clean

**Description:** `feedback/feedback.go:58` opens the JSONL file with mode `0o600` (owner-only). Correct for files that may carry user-flagged production queries (potentially sensitive).

**Severity:** None (positive observation).

---

## 7. Go Version Pin — medium

**Description:** `go.mod:3` requires `go 1.26.0`. This is the current/latest Go (per `go version` on the local machine: `go1.26.0`). Pinning to the exact patch version means any sibling repo that has not upgraded to 1.26 cannot build this. The ecosystem-wide concern is whether every other repo also requires `go 1.26.0`.

**Evidence:** `go.mod:3` — `go 1.26.0`.

**Severity:** Medium for cross-repo coordination; informational for this repo in isolation. A `go 1.26` (minor-version) directive is more typical than `go 1.26.0` (patch-version). The patch-version pin is unusual and forces downstream repos onto the same patch.

**Suggested next action:** Verify ecosystem alignment. If sibling repos use `go 1.26`, soften this pin to `go 1.26` to give consumers patch-level wiggle room. If they all pin patch-level deliberately, document the rationale.

---

## Summary Counts

| Marker | Count |
|---|---|
| `TODO` in production code | 0 |
| `FIXME` in production code | 0 |
| `XXX` in production code | 0 |
| `HACK` in production code | 0 |
| `Deprecated:` markers | 0 |
| `panic(` in production code | 0 |
| Goroutines spawned outside tests | 0 |
| Build-tagged files (excluding examples) | 4 (all `//go:build llmagent` in `adapter/llmagent/`) |
| Packages with 0 test files | 1 (`generate`) |
| Packages with test:src ratio < 0.30 | 4 (`generate`, `embed`, `tree`, `pack`) |
| v1.x git tags | 2 (`v1.0.0`, `v1.0.1`) |
| CHANGELOG `## [v1.x.y]` entries | 1 (only `v1.0.0`) |
| Largest source file | `retrieve/retrieve.go` (1588 LoC) |

---

## Recommended Priority Order

1. **Fix CHANGELOG drift at v1.0.1** (§1.1) — closes the umbrella's flagged issue and the v1.0.0 date discrepancy in one PR.
2. **Update README package layout to enumerate all 22 packages** (§4.3) — visibility is a freeze-survival mechanism.
3. **Decide explicitly on `advanced` / `agentic` / `feedback` status in v1** (§1.2) — once decided, document it in `doc.go` and the audit doc.
4. **Add tests for `generate` and `embed` determinism guarantees** (§1.4, §3.2) — these are the cross-cutting seams; fixing now is cheap.
5. **Document the threshold for adding to `contract/`** (§1.3) — prevents future ambiguity.
6. **Stylistic refactor of `postgres/graph.go:141-158`** (§6.3) — small, safe, improves file consistency.
7. **Split `retrieve/retrieve.go`** (§2.5) — file-internal, no API impact, large quality-of-life win.

---

*Concerns audit: 2026-05-20*
