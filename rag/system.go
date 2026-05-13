package rag

import (
	"context"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/prompt"
	"github.com/costa92/llm-agent-rag/store"
)

type Answer struct {
	Text   string
	Hits   []store.Hit
	Prompt generate.Request
}

type System struct {
	splitter ingest.Splitter
	embedder embed.Embedder
	store    store.Store
	model    generate.Model
	template prompt.Template
	maxChars int
}

func New(opts Options) *System {
	emb := opts.Embedder
	if emb == nil {
		emb = embed.NewHashEmbedder(32)
	}
	st := opts.Store
	if st == nil {
		st = store.NewInMemoryStore(emb.Dimension())
	}
	splitter := opts.Splitter
	if splitter == nil {
		splitter = ingest.CharSplitter{Overlap: 50}
	}
	tpl := opts.Template
	if tpl == nil {
		tpl = prompt.DefaultQATemplate{}
	}
	maxChars := opts.MaxChars
	if maxChars <= 0 {
		maxChars = 500
	}
	return &System{
		splitter: splitter,
		embedder: emb,
		store:    st,
		model:    opts.Model,
		template: tpl,
		maxChars: maxChars,
	}
}

func (s *System) Remove(ctx context.Context, id string) error {
	return s.store.Remove(ctx, id)
}

func (s *System) Stats(ctx context.Context, namespace string) (store.Stats, error) {
	return s.store.Stats(ctx, namespace)
}
