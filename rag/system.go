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

	"github.com/costa92/llm-agent-rag/compress"
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
//
// Compatibility note: this exported struct may grow additively over time, so
// keyed composite literals are recommended.
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
	// OriginalQuestion is the caller's pre-condense question on a
	// System.AskConversation run; empty for an ordinary Ask. Additive.
	OriginalQuestion string
	// CondensedQuery is the standalone query AskConversation derived from
	// OriginalQuestion plus conversation history; empty for an ordinary
	// Ask. Additive.
	CondensedQuery string
	// CompressedChunkIDs lists the chunks whose Content contextual
	// compression shortened on this run; empty when compression is off or
	// nothing shrank. Additive.
	CompressedChunkIDs []string
}

// ChunkScore is the per-chunk grading evidence produced by a Grader for
// one reflection round: the hit ID, its relevance to the query, its
// support for the round's answer, and a short reason for downstream
// debugging.
//
// Compatibility note: this exported struct may grow additively over time, so
// keyed composite literals are recommended.
type ChunkScore struct {
	HitID     string  // HitID identifies the scored hit (the chunk ID).
	Relevance float64 // Relevance is the grader's 0.0-1.0 query-relevance score.
	Support   float64 // Support is the grader's 0.0-1.0 answer-support score.
	Reason    string  // Reason is the grader's short human explanation (e.g. raw reply).
}

// ReflectionDecision is the round-level reflection outcome.
type ReflectionDecision string

const (
	// ReflectionDecisionStop keeps the current round and terminates reflection.
	ReflectionDecisionStop ReflectionDecision = "stop"
	// ReflectionDecisionContinue runs another round without changing the query.
	ReflectionDecisionContinue ReflectionDecision = "continue"
	// ReflectionDecisionRewriteAndContinue rewrites the query before the next round.
	ReflectionDecisionRewriteAndContinue ReflectionDecision = "rewrite_and_continue"
)

// ReflectionDiagnostics attributes one reflection-capable System.Ask run:
// how many rounds executed, which round was adopted, and the per-round
// retrieval and decision signals gathered along the way.
//
// Compatibility note: this exported struct may grow additively over time, so
// keyed composite literals are recommended.
type ReflectionDiagnostics struct {
	Mode               ReflectionMode               // Mode is the configured reflection policy.
	Rounds             int                          // Rounds is kept for consistency with existing API naming and records how many reflection rounds actually ran.
	AdoptedRound       int                          // AdoptedRound is the final round chosen for the answer.
	StopReason         string                       // StopReason explains why reflection stopped.
	FailureFallback    bool                         // FailureFallback reports whether fail-open returned a prior round.
	FailureReason      string                       // FailureReason records the reflection failure cause, if any.
	DecisionModelCalls int                          // DecisionModelCalls counts reflection decision-model invocations.
	RewriteModelCalls  int                          // RewriteModelCalls counts reflection rewrite-model invocations.
	RoundDetails       []ReflectionRoundDiagnostics // RoundDetails records the per-round signals and decisions.
	// FollowupQueriesUsed is the total count of active-retrieval
	// follow-up queries the QueryPlanner emitted (and were executed)
	// across all rounds of this Ask call. Zero when active retrieval
	// was off, no planner was configured, or no round triggered it.
	FollowupQueriesUsed int
}

// ReflectionRoundDiagnostics records the retrieval and decision summary for
// one reflection round.
//
// Compatibility note: this exported struct may grow additively over time, so
// keyed composite literals are recommended.
type ReflectionRoundDiagnostics struct {
	Round            int                // Round is the 1-based reflection round index.
	InputQuery       string             // InputQuery is the query fed into the round.
	EffectiveQuery   string             // EffectiveQuery is the retriever's final effective query.
	RewrittenQuery   string             // RewrittenQuery is the next-round rewrite produced by reflection.
	ReturnedChunkIDs []string           // ReturnedChunkIDs are the chunks returned by retrieval.
	PromptChunkIDs   []string           // PromptChunkIDs are the chunks packed into the answer prompt.
	UniqueDocCount   int                // UniqueDocCount is the number of unique documents supporting the round.
	TopScore         float64            // TopScore is the top retrieval score for the round.
	Decision         ReflectionDecision // Decision is the round-level reflection outcome.
	DecisionMode     ReflectionMode     // DecisionMode is the policy that made the round decision.
	DecisionReason   string             // DecisionReason explains why the round decision was made.
	// RawDecisionText is the model's full raw reply for the reflection
	// decision call, captured verbatim for post-hoc debugging. It is
	// empty in rule mode (no model call) and in hybrid mode rounds
	// where the rule path stops first.
	RawDecisionText string
	// DecisionPrompt is the user-content portion of the reflection
	// decision prompt sent to the model. It is empty when no model
	// decision occurred this round.
	DecisionPrompt string
	// RoutePath is the pinned section route for THIS round, if any.
	// Captured per round so multi-round reflection runs do not lose
	// the earlier rounds' routing decisions to last-round-wins.
	RoutePath []string
	// AutoRoutePath is the route auto-routing selected for THIS round.
	// Captured per round (see RoutePath).
	AutoRoutePath []string
	// AutoRouteCandidates are the candidates auto-routing considered
	// for THIS round. Captured per round (see RoutePath).
	AutoRouteCandidates []retrieve.RouteCandidate
	// SearchTrajectory records each route searched for THIS round.
	// Captured per round (see RoutePath).
	SearchTrajectory []retrieve.TrajectoryStep
	// GraphTrace records the graph-retrieval traversal for THIS round.
	// Captured per round (see RoutePath).
	GraphTrace retrieve.GraphTrace
	// ChunkScores carries the per-chunk grading evidence for THIS round.
	// Populated only when ReflectionOptions.EnableChunkGrading is true and
	// a Grader is configured; empty otherwise.
	ChunkScores []ChunkScore
	// FollowupQueries are the active-retrieval follow-up search queries
	// the QueryPlanner emitted for THIS round, in dispatch order. Empty
	// when EnableActiveRetrieval is false, no planner is configured, the
	// seed retrieval's max relevance is already above the floor, or the
	// per-Ask follow-up budget is exhausted.
	FollowupQueries []string
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
//
// Compatibility note: this exported struct may grow additively over time, so
// keyed composite literals are recommended.
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
//
// Compatibility note: this exported struct may grow additively over time, so
// keyed composite literals are recommended.
type ReflectionTrace struct {
	Mode         ReflectionMode         // Mode is the configured reflection policy.
	AdoptedRound int                    // AdoptedRound is the final round chosen for the answer.
	StopReason   string                 // StopReason explains why reflection stopped.
	Rounds       []ReflectionRoundTrace // Rounds records the per-round trace trail.
}

// ReflectionRoundTrace records the observer-facing per-round reflection
// details for one Ask round.
//
// Compatibility note: this exported struct may grow additively over time, so
// keyed composite literals are recommended.
type ReflectionRoundTrace struct {
	Round            int                // Round is the 1-based reflection round index.
	InputQuery       string             // InputQuery is the query fed into the round.
	EffectiveQuery   string             // EffectiveQuery is the retriever's final effective query.
	RewrittenQuery   string             // RewrittenQuery is the next-round rewrite produced by reflection.
	ReturnedChunkIDs []string           // ReturnedChunkIDs are the chunks returned by retrieval.
	PromptChunkIDs   []string           // PromptChunkIDs are the chunks packed into the answer prompt.
	Decision         ReflectionDecision // Decision is the round-level reflection outcome.
	DecisionReason   string             // DecisionReason explains why the round decision was made.
	// RawDecisionText mirrors ReflectionRoundDiagnostics.RawDecisionText.
	// It is empty when no model decision occurred this round.
	RawDecisionText string
	// AutoRoutePath is the route auto-routing selected for THIS round.
	// Captured per round so multi-round reflection runs do not lose
	// earlier rounds' routing decisions to last-round-wins.
	AutoRoutePath []string
	// ChunkScores mirrors ReflectionRoundDiagnostics.ChunkScores — the
	// per-chunk grading evidence for THIS round. Populated only when
	// ReflectionOptions.EnableChunkGrading is true and a Grader is
	// configured; empty otherwise.
	ChunkScores []ChunkScore
	// FollowupQueries mirrors
	// ReflectionRoundDiagnostics.FollowupQueries — the active-retrieval
	// follow-up search queries the QueryPlanner emitted for THIS round.
	// Empty when active retrieval was off or did not fire this round.
	FollowupQueries []string
}

// System is the top-level RAG pipeline and the front door of the SDK. It is
// constructed by New and exposes the Ask, AskGlobal, AskDrift, Search, and
// Import operations.
type System struct {
	splitter ingest.Splitter
	embedder embed.Embedder
	store    store.Store
	model    generate.Model
	// reflectionModel is the per-stage counting wrapper used by the
	// reflection-decision leg (v1.5.0 CostObserver). It is built by New
	// alongside model so each leg attributes its Generate calls to a
	// distinct stage tag.
	reflectionModel generate.Model
	// globalMapModel and globalReduceModel are the per-sub-stage counting
	// wrappers used inside AskGlobal (v1.5.1). The map model tags every
	// per-community map-step Generate call with StageAskGlobalMap; the
	// reduce model tags the synthesis Generate call with
	// StageAskGlobalReduce. Both are nil when opts.Model is nil so AskGlobal
	// still returns ErrModelRequired.
	globalMapModel    generate.Model
	globalReduceModel generate.Model
	// driftPrimerModel, driftLocalModel, and driftSynthModel are the per-
	// sub-stage counting wrappers used inside AskDrift (v1.5.1). They tag
	// the primer map step, the local-round body, and the synthesis call
	// with StageAskDriftPrimer, StageAskDriftLocal, and StageAskDriftSynth
	// respectively. All three are nil when opts.Model is nil.
	driftPrimerModel generate.Model
	driftLocalModel  generate.Model
	driftSynthModel  generate.Model
	template         prompt.Template
	pre              retrieve.QueryPreprocessor
	ret              retrieve.Retriever
	reranker         rerank.Reranker
	packer           pack.Packer
	maxChars         int
	observer         Observer
	redactor         guard.Redactor

	injectionScanner guard.InjectionScanner
	sanitizeMode     guard.SanitizeMode

	entityExtractor     graph.EntityExtractor
	entityResolver      graph.EntityResolver
	communityDetector   graph.CommunityDetector
	communitySummarizer graph.CommunitySummarizer

	grader Grader

	queryPlanner QueryPlanner
	condenser    QueryCondenser
	compressor   compress.Compressor
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
	splitter := opts.Splitter
	if splitter == nil {
		splitter = ingest.CharSplitter{Overlap: 50}
	}
	tpl := opts.Template
	if tpl == nil {
		tpl = prompt.DefaultQATemplate{}
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
	// Allocate the System first so the per-stage counting models can carry
	// a stable pointer into s.observer. This way a single
	// Observer.OnGenerateUsage hook value reaches every stage even though
	// Observer is value-copied into the System struct.
	s := &System{
		splitter: splitter,
		embedder: emb,
		store:    st,
		template: tpl,
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

		grader: opts.Grader,

		queryPlanner: opts.QueryPlanner,
		condenser:    opts.QueryCondenser,
		compressor:   opts.Compressor,
	}
	// Build the per-stage counting models. A nil opts.Model stays nil so
	// Ask still returns ErrModelRequired; both s.model and s.reflectionModel
	// remain nil in that case.
	if opts.Model != nil {
		s.model = wrapCounting(opts.Model, StageAsk, &s.observer)
		s.reflectionModel = wrapCounting(opts.Model, StageReflectionDecision, &s.observer)
		// v1.5.1 sub-stage wrappers — each tags its inner Generate calls
		// with a distinct stage. AskGlobal/AskDrift route through these
		// instead of s.model so cost-observers can attribute their map /
		// reduce / primer / local / synth legs separately.
		s.globalMapModel = wrapCounting(opts.Model, StageAskGlobalMap, &s.observer)
		s.globalReduceModel = wrapCounting(opts.Model, StageAskGlobalReduce, &s.observer)
		s.driftPrimerModel = wrapCounting(opts.Model, StageAskDriftPrimer, &s.observer)
		s.driftLocalModel = wrapCounting(opts.Model, StageAskDriftLocal, &s.observer)
		s.driftSynthModel = wrapCounting(opts.Model, StageAskDriftSynth, &s.observer)
	}
	// Type-assert the shipped PromptGrader / PromptQueryPlanner and rebuild
	// them with stage-tagged counting models. Custom user-supplied
	// implementations are intentionally NOT auto-wrapped — they would
	// need their own seam to know which model to substitute. Documented
	// on Observer.OnGenerateUsage and in CHANGELOG v1.5.0 compat.
	if opts.Model != nil {
		if pg, ok := s.grader.(PromptGrader); ok {
			s.grader = PromptGrader{Model: wrapCounting(pg.Model, StageGrader, &s.observer)}
		}
		if pp, ok := s.queryPlanner.(PromptQueryPlanner); ok {
			s.queryPlanner = PromptQueryPlanner{
				Model:      wrapCounting(pp.Model, StagePlanner, &s.observer),
				MaxQueries: pp.MaxQueries,
			}
		}
	}
	pre := opts.Preprocessor
	if pre == nil {
		// LLMExpansionPreprocessor keeps the "ask"-tagged model — sub-stage
		// distinction (e.g. "ask_mqe", "ask_hyde") is future-additive.
		pre = retrieve.LLMExpansionPreprocessor{Model: s.model}
	}
	s.pre = pre
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
	s.ret = ret
	return s
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

// effectiveGrader returns the configured Grader, or a NoopGrader when
// none was set. Callers can rely on the returned value being non-nil so
// the EnableChunkGrading wiring always produces deterministic scores
// even on misconfiguration.
func (s *System) effectiveGrader() Grader {
	if s.grader == nil {
		return NoopGrader{}
	}
	return s.grader
}

// effectiveQueryPlanner returns the configured QueryPlanner, or a
// NoopQueryPlanner when none was set. Callers can rely on the returned
// value being non-nil so the EnableActiveRetrieval wiring always has a
// planner to consult — even on misconfiguration, active retrieval then
// degrades to a no-op rather than breaking the Ask call. Mirrors
// effectiveGrader.
func (s *System) effectiveQueryPlanner() QueryPlanner {
	if s.queryPlanner == nil {
		return NoopQueryPlanner{}
	}
	return s.queryPlanner
}

// effectiveCompressor returns the configured Compressor, or a
// compress.NoopCompressor when none was set, so the compression stage is
// always callable and defaults to a no-op. Mirrors effectiveQueryPlanner.
func (s *System) effectiveCompressor() compress.Compressor {
	if s.compressor == nil {
		return compress.NoopCompressor{}
	}
	return s.compressor
}
