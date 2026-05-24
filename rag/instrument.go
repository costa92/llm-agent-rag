package rag

import (
	"context"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/obs"
)

// countingEmbedder wraps an embed.Embedder and increments the obs.Counter
// on the call context once per Embed call. Counting is transparent: when no
// Counter rides the context the increment is a nil-safe no-op. New wraps the
// system embedder in this so embedding calls nested inside the default
// retriever wiring (the DenseRetriever query embedding) are still counted.
type countingEmbedder struct {
	inner embed.Embedder
}

func (e countingEmbedder) Embed(ctx context.Context, text string) (embed.Vector, error) {
	obs.CounterFrom(ctx).AddEmbed(1)
	return e.inner.Embed(ctx, text)
}

func (e countingEmbedder) Dimension() int { return e.inner.Dimension() }

// countingBatchEmbedder is the BatchEmbedder-aware variant of
// countingEmbedder, used when the wrapped embedder also implements
// embed.BatchEmbedder. It counts the batch under one obs.Counter
// AddEmbed bump per text — preserving per-chunk accounting parity with
// the per-chunk path — and delegates to the inner BatchEmbedder.
//
// Both wrappers share the same Embed/Dimension methods; the only
// difference is that countingBatchEmbedder additionally satisfies
// embed.BatchEmbedder so the rag.Importer type-assertion succeeds and
// the batch fast path engages.
type countingBatchEmbedder struct {
	inner      embed.Embedder      // for Embed/Dimension (always the same value)
	innerBatch embed.BatchEmbedder // for EmbedBatch
}

func (e countingBatchEmbedder) Embed(ctx context.Context, text string) (embed.Vector, error) {
	obs.CounterFrom(ctx).AddEmbed(1)
	return e.inner.Embed(ctx, text)
}

func (e countingBatchEmbedder) Dimension() int { return e.inner.Dimension() }

func (e countingBatchEmbedder) EmbedBatch(ctx context.Context, texts []string) ([]embed.Vector, error) {
	obs.CounterFrom(ctx).AddEmbed(len(texts))
	return e.innerBatch.EmbedBatch(ctx, texts)
}

// countingModel wraps a generate.Model and instruments each Generate call
// with three independent side-effects:
//
//  1. Increment the obs.Counter on the call context (legacy v1.0.0 path —
//     CallCounts.Generate).
//  2. Append a StageTokenUsage entry to the obs.StageUsageAccumulator on
//     the call context, tagged with stage.
//  3. Fire Observer.OnGenerateUsage(ctx, stage, usage) when the wrapped
//     Observer pointer is non-nil and exposes a non-nil hook.
//
// New wraps the system model with four per-stage instances ("ask",
// "reflection_decision", "grader", "planner") so generation calls nested
// inside the default preprocessor wiring (LLMExpansionPreprocessor MQE/HyDE)
// and the v1.5.0 CostObserver consumers can attribute spend.
//
// Side effects (2) and (3) fire ONLY on success (err == nil). Partial usage
// from a failed Generate is unreliable, so this wrapper suppresses it.
type countingModel struct {
	inner    generate.Model
	stage    string
	observer *Observer
}

// wrapCounting returns a countingModel wrapping inner for the given stage,
// pointing at obs (which may be nil — the hook then never fires). Stage is
// a free-form string; the v1.5.0 standard values are "ask",
// "reflection_decision", "grader", "planner", "judge_eval". A nil inner
// returns nil so callers' nil-handling in rag.New continues to work.
func wrapCounting(inner generate.Model, stage string, observer *Observer) generate.Model {
	if inner == nil {
		return nil
	}
	return countingModel{inner: inner, stage: stage, observer: observer}
}

func (m countingModel) Generate(ctx context.Context, req generate.Request) (generate.Response, error) {
	obs.CounterFrom(ctx).AddGenerate(1)
	resp, err := m.inner.Generate(ctx, req)
	if err != nil {
		return resp, err
	}
	usage := deriveTokenUsage(req, resp)
	obs.StageUsageFrom(ctx).Append(m.stage, usage)
	if m.observer != nil && m.observer.OnGenerateUsage != nil {
		m.observer.OnGenerateUsage(ctx, m.stage, usage)
	}
	// v1.7.0 C2: cumulative-token budget check. Runs AFTER Append so
	// the StageUsageAccumulator records the entry that tipped us over,
	// and AFTER OnGenerateUsage so observers see the same trace as
	// unbudgeted callers. A zero/unset budget (TokenBudgetFrom == 0)
	// is "unlimited" — preserves v1.6.0 behavior byte-for-byte.
	//
	// PartialDiagnostics is left zero here; Ask.wrapBudgetError fills
	// it with the full assembly (Metrics, StageTokenUsage snapshot,
	// partial Reflection rounds) at every return site.
	budget := obs.TokenBudgetFrom(ctx)
	if budget > 0 {
		used := obs.StageUsageFrom(ctx).TotalSoFar()
		if used > budget {
			return resp, &BudgetExceededError{
				Stage:  m.stage,
				Used:   used,
				Budget: budget,
			}
		}
	}
	return resp, nil
}
