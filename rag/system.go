package rag

import (
	"context"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/pack"
	"github.com/costa92/llm-agent-rag/prompt"
	"github.com/costa92/llm-agent-rag/rerank"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

type Answer struct {
	Text        string
	Hits        []store.Hit
	Prompt      generate.Request
	Citations   []Citation
	Diagnostics Diagnostics
	Trace       Trace
}

type Citation struct {
	ChunkID     string
	DocID       string
	Namespace   string
	Title       string
	SectionID   string
	SectionPath []string
	Score       float64
}

type Diagnostics struct {
	HitCount            int
	ReturnedChunkIDs    []string
	PromptChunkIDs      []string
	MatchedSections     []string
	ExpandedChunkIDs    []string
	AutoRouteCandidates []retrieve.RouteCandidate
	RoutePolicy         retrieve.RoutePolicyTrace
	SearchTrajectory    []retrieve.TrajectoryStep
}

type Trace struct {
	Question            string
	Namespace           string
	TopK                int
	Filters             map[string]any
	SecurityFilters     map[string]any
	RoutePath           []string
	AutoRoutePath       []string
	AutoRouteCandidates []retrieve.RouteCandidate
	RoutePolicy         retrieve.RoutePolicyTrace
	SearchPath          []string
	MatchedSections     []string
	ExpandedSections    []string
	ExpandedChunkIDs    []string
	RerankedChunkIDs    []string
	PackedChunkIDs      []string
	DroppedChunkIDs     []string
	SelectedChunkIDs    []string
	SearchTrajectory    []retrieve.TrajectoryStep
}

type System struct {
	splitter ingest.Splitter
	embedder embed.Embedder
	store    store.Store
	model    generate.Model
	template prompt.Template
	pre      retrieve.QueryPreprocessor
	ret      retrieve.Retriever
	reranker rerank.Reranker
	packer   pack.Packer
	maxChars int
	observer Observer
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
	pre := opts.Preprocessor
	if pre == nil {
		pre = retrieve.LLMExpansionPreprocessor{Model: opts.Model}
	}
	ret := opts.Retriever
	if ret == nil {
		ret = retrieve.VariantRetriever{
			Base: retrieve.HybridRetriever{
				Dense:     retrieve.DenseRetriever{Embedder: emb, Store: st},
				Lexical:   retrieve.LexicalRetriever{Store: st},
				Structure: retrieve.StructureRetriever{Store: st},
			},
		}
	}
	rr := opts.Reranker
	if rr == nil {
		rr = rerank.HeuristicReranker{}
	}
	pk := opts.Packer
	if pk == nil {
		pk = pack.GreedyTokenPacker{}
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
		pre:      pre,
		ret:      ret,
		reranker: rr,
		packer:   pk,
		maxChars: maxChars,
		observer: opts.Observer,
	}
}

func (s *System) Remove(ctx context.Context, id string) error {
	return s.store.Remove(ctx, id)
}

func (s *System) Stats(ctx context.Context, namespace string) (store.Stats, error) {
	return s.store.Stats(ctx, namespace)
}

func (s *System) Model() generate.Model {
	return s.model
}
