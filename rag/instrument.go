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

// countingModel wraps a generate.Model and increments the obs.Counter on
// the call context once per Generate call. New wraps the system model in
// this so generation calls nested inside the default preprocessor wiring
// (LLMExpansionPreprocessor MQE/HyDE) are counted alongside the answer
// generation in Ask.
type countingModel struct {
	inner generate.Model
}

func (m countingModel) Generate(ctx context.Context, req generate.Request) (generate.Response, error) {
	obs.CounterFrom(ctx).AddGenerate(1)
	return m.inner.Generate(ctx, req)
}
