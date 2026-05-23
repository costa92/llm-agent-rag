# Self-RAG Reflection Design

## Goal

Add a bounded Self-RAG path to `rag.System.Ask` so callers can choose `rule`, `model`, or `hybrid` reflection modes through parameters, while keeping the existing single-round `Ask` API shape and preserving current retrieval, rerank, pack, prompt, and generation seams.

## Scope

This v1 design adds inference-time reflection orchestration only.

Included:

- parameterized reflection mode selection on `Ask`
- bounded multi-round retrieval/generation loop
- rule-driven, model-driven, and hybrid decision modes
- round-level diagnostics and top-level aggregated trace/metrics
- tests and API snapshot updates

Excluded:

- token-level Self-RAG control tokens or finetuning-specific behavior
- training pipelines or benchmark dataset imports
- new public `AskSelfRAG` API
- changes to `retrieve.Retriever`, `pack.Packer`, `prompt.Template`, or `generate.Model`
- changes to `eval` package dependencies from inside `rag`

## Existing Constraints

- `rag.System.Ask` is currently a single straight-line orchestration over `retrieve -> rerank -> pack -> sanitize -> render -> generate`.
- `retrieve.Trace` already captures query-shaping and routing details, but `rag.Answer.Diagnostics` only exposes a single-round view.
- `Observer.OnAsk` and `Observer.OnRetrieve` are already consumed externally, so new behavior must stay additive and documented.
- `eval` imports `rag`, so `rag` cannot import `eval` without creating a cycle.

## Chosen Architecture

### Public API

Keep `rag.System.Ask` as the only public answer entrypoint.

Extend `rag.AskOptions` with:

- `Reflection *ReflectionOptions`

Add new public types in `rag`:

- `type ReflectionMode string`
- `type ReflectionOptions struct { ... }`

`ReflectionMode` values:

- `""` or `off`
- `rule`
- `model`
- `hybrid`

`ReflectionOptions` v1 fields:

- `Mode ReflectionMode`
- `MaxRounds int`
- `MinHits int`
- `MinScore float64`
- `MinUniqueDocs int`
- `RequireCitations bool`
- `AllowRewrite bool`
- `FailOpen bool`

V1 keeps the public config intentionally small. More advanced knobs can be added later without changing the orchestration boundary.

### Internal Structure

Refactor `rag/ask.go` so the current one-pass logic moves into a private helper:

- `askRound(ctx, originalQuestion, query string, opts AskOptions) (roundResult, error)`

`roundResult` is a private type that carries:

- final `Answer` data for that round
- raw `retrieve.Trace`
- round-local metrics
- derived rule signals such as hit count, top score, and unique doc count

`System.Ask` becomes a bounded loop:

1. initialize top-level `obs.Counter`
2. if reflection is `off`, run one `askRound` and return the same semantics as today
3. otherwise run up to `MaxRounds`
4. after each round, evaluate the configured reflection policy
5. stop on policy decision or hard cap
6. return the final adopted round as `Answer.Text/Hits/Citations/Prompt`
7. attach full reflection diagnostics and aggregated metrics

### Reflection Decision Model

The decision seam is internal to `rag`; it is not a new cross-package dependency.

Add a private reflection policy abstraction with three built-in implementations:

- rule policy
- model policy
- hybrid policy

The abstraction decides:

- whether to stop
- whether to continue
- whether to rewrite the next retrieval query
- why the decision was made

V1 model-driven reflection uses the existing `generate.Model` with an internal prompt. It does not reuse `eval.Judge`, because that would create an import cycle.

## Decision Semantics

### Rule Mode

Rule mode uses only structured signals from the round:

- retrieved hit count
- top retrieval score
- unique supporting documents
- packed citation count
- whether the current round materially improved over the previous round

If thresholds are satisfied, stop.

If thresholds are not satisfied:

- continue when another round is still allowed
- optionally rewrite if `AllowRewrite` is enabled

### Model Mode

Model mode performs a second model call after the answer round to judge whether:

- evidence is sufficient
- another retrieval round is needed
- the next round should use a rewritten query

The decision is always anchored to the original user question, not the rewritten query. This preserves relevance against the user’s actual task.

### Hybrid Mode

Hybrid mode applies rule checks first.

- if rule checks clearly satisfy stop conditions, stop without a reflection model call
- otherwise call the model policy to decide continue vs rewrite vs stop

This keeps easy cases cheap and lets ambiguous cases use model judgment.

## Query Rewrite Semantics

When rewrite is enabled, the next round may use a rewritten retrieval query, but:

- the original question remains the user-facing question in `Answer.Trace.Question`
- the same `SearchOptions` carry forward
- the normal query preprocessor still runs on the rewritten query

This means rewrite can stack with existing MQE or HyDE behavior. That is acceptable, but must be documented because it changes how users interpret later-round retrieval traces.

## Diagnostics And Trace

`Answer.Text`, `Answer.Hits`, `Answer.Citations`, and `Answer.Prompt` represent only the adopted final round.

Add additive reflection fields to `rag.Diagnostics` and `rag.Trace`.

Suggested public structures:

- `ReflectionDiagnostics`
- `ReflectionRoundDiagnostics`
- `ReflectionTrace`
- `ReflectionRoundTrace`

Each round should record at least:

- round index
- input query
- effective query from retrieval trace
- whether a rewrite was applied
- returned chunk IDs
- packed chunk IDs
- unique doc count
- top score
- decision: `stop`, `continue`, `rewrite_and_continue`
- decision mode: `rule`, `model`, `hybrid`
- decision reason
- round metrics summary

Top-level reflection diagnostics should record:

- configured mode
- total rounds run
- adopted round index
- stop reason
- whether fail-open fallback happened

## Observer And Instrumentation Semantics

`Observer.OnAsk` stays a single top-level success callback. It receives the final `Trace`, which now includes reflection detail.

`Observer.OnRetrieve` continues firing for each internal retrieval call. Under reflection modes this means one top-level `Ask` may trigger multiple `OnRetrieve` callbacks. This is a behavior change and must be documented.

## Metrics And Token Accounting

Current `Ask` token accounting only derives token usage from the final generation request/response. That is not correct once multiple rounds exist.

V1 must aggregate:

- generation calls across all rounds
- embedding calls across all rounds
- total top-level wall-clock time
- summed token usage across answer generations and reflection-model generations

If exact token usage is unavailable from a model response, estimation remains acceptable, but aggregation must include every round rather than only the final one.

## Error Handling

First round failures remain hard failures, as today.

Reflection-stage failures after a usable round should default to fail-open behavior when `FailOpen` is true:

- return the best available round
- mark diagnostics with `reflection_error_fallback`
- preserve the error reason in diagnostics

When `FailOpen` is false, the reflection error should be returned.

This keeps the feature safe for production callers that prefer degraded usefulness over unnecessary hard failure.

## Testing Strategy

### Unit Tests

Add `rag` tests covering:

- `off` mode keeps current one-round behavior
- `rule` mode stops in one round when thresholds are met
- `rule` mode performs an extra round when thresholds fail
- `rule` mode stops at `MaxRounds`
- `model` mode rewrites and continues when scripted model reflection says so
- `model` mode stops immediately when scripted reflection says stop
- `hybrid` mode skips model reflection when rule thresholds clearly stop
- `hybrid` mode invokes model reflection when rule thresholds are inconclusive
- reflection diagnostics record per-round query, IDs, decision, and stop reason
- `OnRetrieve` fires once per round under reflection modes
- aggregated metrics include more than one generation call when multiple rounds run

### Eval Reuse

Reuse `eval.TriadEvaluator` against the final `Ask` implementation. No new eval package integration is required for v1.

Add at least one small dataset-driven regression test where:

- baseline one-round `Ask` underperforms
- reflection-enabled `Ask` reaches a second round
- final groundedness or grounding-at-k does not regress

### API Surface

Update `api/v1.snapshot.txt` because exported `rag` types change.

## Files Expected To Change

- `rag/options.go`
- `rag/ask.go`
- `rag/system.go`
- `rag/observer_test.go`
- `rag/system_test.go`
- `rag/instrument_test.go`
- `api/v1.snapshot.txt`
- `README.md`
- `docs/production-deployment.md`

Potentially add one focused helper file if `ask.go` becomes too large:

- `rag/reflection.go`

## Risks And Guardrails

Primary risks:

- top-level metrics become inconsistent with multi-round execution
- `OnRetrieve` callback count changes may surprise otel consumers
- rewrite plus MQE/HyDE stacking may confuse users without clear docs
- reflection decisions may loop on unchanged evidence unless dedup/early-stop logic is explicit

Guardrails:

- enforce `MaxRounds >= 1`
- stop early when rewritten query is unchanged
- stop early when retrieved chunk set does not materially improve
- keep all new public fields additive
- keep final answer semantics identical to today outside the new reflection diagnostics

## Review Notes

The design is intentionally scoped to a minimal, production-usable inference-time Self-RAG. It avoids new package cycles, avoids widening low-level interfaces, and concentrates change in `rag.Ask`, where the existing orchestration already lives.
