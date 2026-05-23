// Package rag — orchestration-layer overview — is the front door of the
// llm-agent-rag SDK. System is the top-level RAG pipeline; New constructs
// one from an Options value wiring an ingester, retriever, model, and store.
// System exposes three answer paths: Ask is the standard retrieve-pack-
// generate path, AskGlobal answers from GraphRAG community summaries, and
// AskDrift runs the DRIFT global-then-local search. Search and Import expose
// the retrieval and ingest stages directly, and Observer receives per-run
// Trace callbacks for instrumentation.
package rag

import (
	"context"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/guard"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/pack"
	"github.com/costa92/llm-agent-rag/prompt"
	"github.com/costa92/llm-agent-rag/rerank"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

// Answer is the result of an answer-path call (Ask, AskGlobal, AskDrift):
// the generated text plus its supporting hits, prompt, citations, and traces.
type Answer struct {
	Text        string           // Text is the generated answer.
	Hits        []store.Hit      // Hits are the retrieved chunks the answer was generated from.
	Prompt      generate.Request // Prompt is the generation request sent to the model.
	Citations   []Citation       // Citations link the answer back to its source chunks.
	Diagnostics Diagnostics      // Diagnostics is the per-run diagnostic detail.
	Trace       Trace            // Trace is the per-run trace passed to an Observer.
}

// Citation links an answer back to one source chunk and its document section.
type Citation struct {
	ChunkID     string   // ChunkID identifies the cited chunk.
	DocID       string   // DocID identifies the cited chunk's document.
	Namespace   string   // Namespace is the cited chunk's namespace.
	Title       string   // Title is the cited document's title.
	SectionID   string   // SectionID identifies the chunk's section.
	SectionPath []string // SectionPath is the heading breadcrumb to the chunk.
	Score       float64  // Score is the chunk's retrieval relevance.
}

// Diagnostics is the per-run diagnostic detail behind an Answer — every
// routing, rerank, injection, and graph signal the pipeline produced.
type Diagnostics struct {
	HitCount            int                       // HitCount is the number of hits retrieved.
	ReturnedChunkIDs    []string                  // ReturnedChunkIDs are the chunk IDs the retriever returned.
	PromptChunkIDs      []string                  // PromptChunkIDs are the chunk IDs packed into the prompt.
	MatchedSections     []string                  // MatchedSections are the sections that matched.
	ExpandedChunkIDs    []string                  // ExpandedChunkIDs are chunks added by tree expansion.
	AutoRouteCandidates []retrieve.RouteCandidate // AutoRouteCandidates are the candidates auto-routing considered.
	RoutePolicy         retrieve.RoutePolicyTrace // RoutePolicy records the routing-policy decision.
	SearchTrajectory    []retrieve.TrajectoryStep // SearchTrajectory records each route searched.
	RerankScores        []rerank.RerankScore      // RerankScores is the per-chunk rerank explainability.
	Metrics             obs.Metrics               // Metrics is the cost-and-latency record for the run.
	InjectionFindings   []InjectionFinding        // InjectionFindings records prompt-injection screening results.
	GraphTrace          retrieve.GraphTrace       // GraphTrace records graph-retrieval traversal.
	// Global attributes a System.AskGlobal map-reduce run. It is the zero
	// value for an ordinary Ask — the field is additive.
	Global GlobalDiagnostics
	// Drift attributes a System.AskDrift run — the primer, the bounded
	// local follow-up loop, and the synthesis. It is the zero value for an
	// ordinary Ask or AskGlobal — the field is additive.
	Drift DriftDiagnostics
	// Reflection attributes a bounded self-reflection Ask run. It is the
	// zero value for an ordinary single-round Ask — the field is additive.
	Reflection ReflectionDiagnostics
}

// ReflectionDiagnostics attributes one reflection-capable System.Ask run:
// how many rounds executed, which round was adopted, and the per-round
// retrieval and decision signals gathered along the way.
type ReflectionDiagnostics struct {
	Mode               ReflectionMode               // Mode is the configured reflection policy.
	Rounds             int                          // Rounds is the number of reflection rounds actually run.
	AdoptedRound       int                          // AdoptedRound is the final round chosen for the answer.
	StopReason         string                       // StopReason explains why reflection stopped.
	FailureFallback    bool                         // FailureFallback reports whether fail-open returned a prior round.
	FailureReason      string                       // FailureReason records the reflection failure cause, if any.
	DecisionModelCalls int                          // DecisionModelCalls counts reflection decision-model invocations.
	RewriteModelCalls  int                          // RewriteModelCalls counts reflection rewrite-model invocations.
	RoundDetails       []ReflectionRoundDiagnostics // RoundDetails records the per-round signals and decisions.
}

// ReflectionRoundDiagnostics records the retrieval and decision summary for
// one reflection round.
type ReflectionRoundDiagnostics struct {
	Round            int            // Round is the 1-based reflection round index.
	InputQuery       string         // InputQuery is the query fed into the round.
	EffectiveQuery   string         // EffectiveQuery is the retriever's final effective query.
	RewrittenQuery   string         // RewrittenQuery is the next-round rewrite produced by reflection.
	ReturnedChunkIDs []string       // ReturnedChunkIDs are the chunks returned by retrieval.
	PromptChunkIDs   []string       // PromptChunkIDs are the chunks packed into the answer prompt.
	UniqueDocCount   int            // UniqueDocCount is the number of unique documents supporting the round.
	TopScore         float64        // TopScore is the top retrieval score for the round.
	Decision         string         // Decision is stop, continue, or rewrite_and_continue.
	DecisionMode     ReflectionMode // DecisionMode is the policy that made the round decision.
	DecisionReason   string         // DecisionReason explains why the round decision was made.
}

// DriftDiagnostics attributes one System.AskDrift run: which communities the
// primer mapped, how many local follow-up rounds actually ran, the seed
// entity IDs propagated into each round, and the primer's consulted reports.
type DriftDiagnostics struct {
	// PrimerCommunityIDs are the communities the primer pass mapped over,
	// in selection order. It is empty when the store has no community
	// capability or the namespace has no communities.
	PrimerCommunityIDs []string
	// Rounds is the number of local follow-up rounds actually run. It never
	// exceeds the hard cap (driftMaxRounds) — the loop terminates early
	// when no new follow-up entities surface.
	Rounds int
	// RoundEntityIDs are the seed entity IDs each local round traversed
	// from, sorted and deduped, indexed by round (len == Rounds).
	RoundEntityIDs [][]string
	// ConsultedReports are the primer's community reports — the answer's
	// grounding context. It mirrors GlobalDiagnostics.ConsultedReports so
	// eval.DriftEvaluator can read grounding off the Answer without store
	// plumbing.
	ConsultedReports []graph.CommunityReport
}

// GlobalDiagnostics attributes one System.AskGlobal run: which communities
// were consulted, the per-community helpfulness score the map step parsed,
// and how many model calls the map and reduce steps made.
type GlobalDiagnostics struct {
	// CommunityIDs are the consulted communities, in selection order.
	CommunityIDs []string
	// MapScores is the parsed helpfulness score (0-100) per community ID.
	MapScores map[string]int
	// MapCalls counts the per-community map generations.
	MapCalls int
	// ReduceCalls counts the reduce-step generations (0 or 1).
	ReduceCalls int
	// ConsultedReports are the community reports AskGlobal actually mapped
	// over — the lazily-loaded or freshly-generated reports for the selected
	// communities, in selection order. It is the answer's grounding context:
	// an evaluator (eval.GlobalEvaluator) reads it off the Answer and judges
	// global-search groundedness against it, without needing store plumbing.
	ConsultedReports []graph.CommunityReport
}

// Trace is the per-run trace an Observer receives — the inputs and the
// routing/rerank/pack pipeline decisions for one Ask.
type Trace struct {
	Question            string                    // Question is the query that was asked.
	Namespace           string                    // Namespace is the namespace the run searched.
	TopK                int                       // TopK is the retrieval cutoff used.
	Filters             map[string]any            // Filters are the metadata filters applied.
	SecurityFilters     map[string]any            // SecurityFilters are the access-control filters applied.
	RoutePath           []string                  // RoutePath is the pinned section route, if any.
	AutoRoutePath       []string                  // AutoRoutePath is the route auto-routing selected.
	AutoRouteCandidates []retrieve.RouteCandidate // AutoRouteCandidates are the candidates auto-routing considered.
	RoutePolicy         retrieve.RoutePolicyTrace // RoutePolicy records the routing-policy decision.
	SearchPath          []string                  // SearchPath is the route ultimately searched.
	MatchedSections     []string                  // MatchedSections are the sections that matched.
	ExpandedSections    []string                  // ExpandedSections are sections added by expansion.
	ExpandedChunkIDs    []string                  // ExpandedChunkIDs are chunks added by expansion.
	RerankedChunkIDs    []string                  // RerankedChunkIDs are the chunk IDs after reranking.
	PackedChunkIDs      []string                  // PackedChunkIDs are the chunk IDs packed into the prompt.
	DroppedChunkIDs     []string                  // DroppedChunkIDs are chunks dropped by the packer.
	SelectedChunkIDs    []string                  // SelectedChunkIDs are the chunks the answer used.
	SearchTrajectory    []retrieve.TrajectoryStep // SearchTrajectory records each route searched.
	Reflection          ReflectionTrace           // Reflection is the additive self-reflection trace detail.
}

// ReflectionTrace is the observer-facing trace summary for a reflection Ask
// run: configured mode, adopted round, stop reason, and the per-round trail.
type ReflectionTrace struct {
	Mode         ReflectionMode         // Mode is the configured reflection policy.
	AdoptedRound int                    // AdoptedRound is the final round chosen for the answer.
	StopReason   string                 // StopReason explains why reflection stopped.
	Rounds       []ReflectionRoundTrace // Rounds records the per-round trace trail.
}

// ReflectionRoundTrace records the observer-facing per-round reflection
// details for one Ask round.
type ReflectionRoundTrace struct {
	Round            int      // Round is the 1-based reflection round index.
	InputQuery       string   // InputQuery is the query fed into the round.
	EffectiveQuery   string   // EffectiveQuery is the retriever's final effective query.
	RewrittenQuery   string   // RewrittenQuery is the next-round rewrite produced by reflection.
	ReturnedChunkIDs []string // ReturnedChunkIDs are the chunks returned by retrieval.
	PromptChunkIDs   []string // PromptChunkIDs are the chunks packed into the answer prompt.
	Decision         string   // Decision is stop, continue, or rewrite_and_continue.
	DecisionReason   string   // DecisionReason explains why the round decision was made.
}

// System is the top-level RAG pipeline and the front door of the SDK. It is
// constructed by New and exposes the Ask, AskGlobal, AskDrift, Search, and
// Import operations.
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
	redactor guard.Redactor

	injectionScanner guard.InjectionScanner
	sanitizeMode     guard.SanitizeMode

	entityExtractor     graph.EntityExtractor
	entityResolver      graph.EntityResolver
	communityDetector   graph.CommunityDetector
	communitySummarizer graph.CommunitySummarizer
}

// New constructs a System from opts, filling unset dependencies with the
// SDK's built-in defaults (hash embedder, in-memory store, hybrid retriever,
// heuristic reranker, greedy packer).
func New(opts Options) *System {
	emb := opts.Embedder
	if emb == nil {
		emb = embed.NewHashEmbedder(32)
	}
	st := opts.Store
	if st == nil {
		st = store.NewInMemoryStore(emb.Dimension())
	}
	// Wrap the embedder and model in counting decorators so model calls
	// nested inside the default retriever/preprocessor wiring are recorded
	// by the obs.Counter on the call context. A caller-supplied Retriever
	// or Preprocessor holds whatever embedder/model the caller passed and
	// is left as-is. A nil model stays nil so Ask still returns
	// ErrModelRequired.
	//
	// If the caller's embedder also implements embed.BatchEmbedder
	// (the v1.0.2 optional sibling capability), wrap with the
	// batch-aware variant so a downstream type-assertion in Import
	// can engage the batch fast path. Otherwise fall back to the
	// plain Embedder-only wrapper — preserving the v1 behavior
	// exactly for callers that only implement Embedder.
	if be, ok := emb.(embed.BatchEmbedder); ok {
		emb = countingBatchEmbedder{inner: emb, innerBatch: be}
	} else {
		emb = countingEmbedder{inner: emb}
	}
	var model generate.Model
	if opts.Model != nil {
		model = countingModel{inner: opts.Model}
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
		pre = retrieve.LLMExpansionPreprocessor{Model: model}
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
	entityResolver := opts.EntityResolver
	if entityResolver == nil {
		entityResolver = graph.NoopEntityResolver{}
	}
	return &System{
		splitter: splitter,
		embedder: emb,
		store:    st,
		model:    model,
		template: tpl,
		pre:      pre,
		ret:      ret,
		reranker: rr,
		packer:   pk,
		maxChars: maxChars,
		observer: opts.Observer,
		redactor: opts.Redactor,

		injectionScanner: opts.InjectionScanner,
		sanitizeMode:     opts.SanitizeMode,

		entityExtractor:     opts.EntityExtractor,
		entityResolver:      entityResolver,
		communityDetector:   opts.CommunityDetector,
		communitySummarizer: opts.CommunitySummarizer,
	}
}

// Remove deletes the chunk with the given ID from the store.
func (s *System) Remove(ctx context.Context, id string) error {
	return s.store.Remove(ctx, id)
}

// Stats returns chunk-count and dimension statistics for a namespace.
func (s *System) Stats(ctx context.Context, namespace string) (store.Stats, error) {
	return s.store.Stats(ctx, namespace)
}

// Model returns the System's generation model, or nil if none was configured.
func (s *System) Model() generate.Model {
	return s.model
}
