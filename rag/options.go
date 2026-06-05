package rag

import (
	"github.com/costa92/llm-agent-rag/compress"
	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/guard"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/pack"
	"github.com/costa92/llm-agent-rag/prompt"
	"github.com/costa92/llm-agent-rag/rerank"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

// SearchOptions configures System.Search — the retrieval-only path — and is
// also embedded in AskOptions.
type SearchOptions struct {
	TopK                         int            // TopK caps the number of hits returned.
	Namespace                    string         // Namespace scopes the search to one namespace.
	Filters                      map[string]any // Filters restricts results by chunk metadata.
	SecurityFilters              map[string]any // SecurityFilters applies caller-enforced access control.
	RoutePath                    []string       // RoutePath pins retrieval to an explicit section route.
	EnableAutoRoute              bool           // EnableAutoRoute turns on automatic section routing.
	AutoRouteMinScore            float64        // AutoRouteMinScore is the minimum score for an auto-route candidate.
	AutoRouteMaxCandidates       int            // AutoRouteMaxCandidates caps how many route candidates are considered.
	AutoRouteConfidenceThreshold float64        // AutoRouteConfidenceThreshold is the minimum confidence to keep a candidate.
	AutoRouteFanout              int            // AutoRouteFanout caps how many routes are searched in parallel.
	AutoRouteConfidenceGap       float64        // AutoRouteConfidenceGap converges to top-1 when its lead exceeds this.
	EnableMQE                    bool           // EnableMQE turns on multi-query expansion.
	EnableHyDE                   bool           // EnableHyDE turns on hypothetical-document expansion.
	MQECount                     int            // MQECount is the number of expansion queries to generate.
	EnableRerank                 bool           // EnableRerank turns on reranking of retrieved hits.
	EnableStructure              bool           // EnableStructure turns on structure-aware retrieval.
	EnableGraph                  bool           // EnableGraph turns on graph retrieval.
	EnableTreeExpansion          bool           // EnableTreeExpansion turns on document-tree neighbor expansion.
	ExpansionDepth               int            // ExpansionDepth bounds tree-expansion depth.
	EnableCompression            bool           // EnableCompression turns on contextual compression of retrieved chunks.
}

// ReflectionMode selects how Ask evaluates whether to stop, continue, or
// rewrite across bounded self-reflection rounds. The zero value and
// ReflectionModeOff both disable reflection.
type ReflectionMode string

const (
	// ReflectionModeOff disables reflection explicitly.
	ReflectionModeOff ReflectionMode = "off"
	// ReflectionModeRule enables threshold-based reflection decisions.
	ReflectionModeRule ReflectionMode = "rule"
	// ReflectionModeModel enables model-judged reflection decisions.
	ReflectionModeModel ReflectionMode = "model"
	// ReflectionModeHybrid enables rule-first, model-assisted reflection decisions.
	ReflectionModeHybrid ReflectionMode = "hybrid"
)

// SelectionMode selects which reflection round's answer is adopted at
// the end of the multi-round loop. The zero value is
// SelectionModeLastRound, preserving v1.0.x semantics.
type SelectionMode int

const (
	// SelectionModeLastRound adopts the last completed reflection round.
	// This is the v1.0.x default and the zero-value of SelectionMode.
	SelectionModeLastRound SelectionMode = iota
	// SelectionModeBestByScore adopts the round with the highest weighted
	// aggregate ChunkScores at the end of the loop:
	//   score(round) = GraderRelevanceWeight*mean(relevance)
	//                  + GraderSupportWeight*mean(support)
	// Loop semantics (when to stop, when to rewrite) are unchanged — this
	// flag only affects which round's answer/citations are returned.
	SelectionModeBestByScore
)

// ReflectionOptions configures the optional bounded self-reflection loop for
// System.Ask. Reflection is disabled when AskOptions.Reflection is nil, when
// Mode is the zero value, or when Mode is ReflectionModeOff.
//
// Compatibility note: this exported struct may grow additively over time, so
// keyed composite literals are recommended.
type ReflectionOptions struct {
	Mode             ReflectionMode // Mode selects the reflection policy.
	MaxRounds        int            // MaxRounds bounds the number of Ask rounds.
	MinHits          int            // MinHits is the minimum retrieved-hit threshold.
	MinScore         float64        // MinScore is the minimum top-score threshold.
	MinUniqueDocs    int            // MinUniqueDocs is the minimum unique-document threshold.
	RequireCitations bool           // RequireCitations requires prompt citations before stopping.
	AllowRewrite     bool           // AllowRewrite permits query rewrites between rounds.
	FailOpen         bool           // FailOpen returns the best usable round after reflection failure.
	// EnableChunkGrading turns on the per-chunk grading pass. When true,
	// the System's configured Grader is called for every hit after each
	// retrieval round and the scores are recorded on
	// ReflectionRoundDiagnostics.ChunkScores and
	// ReflectionRoundTrace.ChunkScores. Default false preserves v1.0.x
	// behavior (no grading call).
	EnableChunkGrading bool
	// GraderRelevanceWeight is the weight of mean(ChunkScores.Relevance)
	// in the SelectionModeBestByScore aggregate. A value <= 0 defaults to
	// 0.5 when SelectionModeBestByScore is active.
	GraderRelevanceWeight float64
	// GraderSupportWeight is the weight of mean(ChunkScores.Support) in
	// the SelectionModeBestByScore aggregate. A value <= 0 defaults to
	// 0.5 when SelectionModeBestByScore is active.
	GraderSupportWeight float64
	// SelectionMode picks which reflection round's answer is adopted at
	// the end of the loop. Zero value SelectionModeLastRound preserves
	// v1.0.x semantics.
	SelectionMode SelectionMode
	// AdaptiveRetrieval, when true, forces one extra reflection round if
	// the max ChunkScores.Relevance for the latest round is below
	// AdaptiveRetrievalThreshold (subject to MaxRounds). Requires
	// EnableChunkGrading. Default false preserves v1.0.x behavior.
	AdaptiveRetrieval bool
	// AdaptiveRetrievalThreshold is the relevance threshold below which
	// AdaptiveRetrieval forces an extra round. A value <= 0 defaults to
	// 0.6 when AdaptiveRetrieval is active.
	AdaptiveRetrievalThreshold float64
	// EnableActiveRetrieval, when true, runs a follow-up retrieval pass
	// WITHIN a reflection round when the seed retrieval's max chunk
	// relevance is below ActiveRetrievalRelevanceFloor. The configured
	// QueryPlanner emits up to MaxFollowupQueries extra search queries
	// (subject to MaxFollowupQueriesPerAsk across the whole Ask), each
	// is retrieved, and the union of seed + follow-up hits is graded /
	// packed before generation. Default false preserves v1.1.x behavior.
	//
	// Composition: active retrieval and AllowRewrite are orthogonal.
	// Active fires WITHIN a round (unions follow-up hits with the seed
	// retrieval before grading/packing); rewrite drives the NEXT
	// round's input query. Both can be on simultaneously.
	EnableActiveRetrieval bool
	// MaxFollowupQueries caps how many follow-up retrievals a single
	// reflection round fires. A value <= 0 defaults to 2 when
	// EnableActiveRetrieval is true.
	MaxFollowupQueries int
	// MaxFollowupQueriesPerAsk caps the total number of follow-up
	// retrievals consumed across all rounds of a single Ask call. A
	// value <= 0 defaults to 4 when EnableActiveRetrieval is true.
	MaxFollowupQueriesPerAsk int
	// ActiveRetrievalRelevanceFloor is the max-chunk-relevance threshold
	// below which active retrieval fires. A value <= 0 defaults to 0.4
	// when EnableActiveRetrieval is true.
	ActiveRetrievalRelevanceFloor float64
	// ParallelFollowups, when true, fires the per-round follow-up
	// retrievals concurrently using a bounded worker pool
	// (sync.WaitGroup + buffered-channel semaphore). Default false
	// preserves v1.2.0 sequential dispatch. Determinism invariants:
	// merged hits remain sorted by score (stable), and
	// ReflectionRoundDiagnostics.FollowupQueries is always in planner
	// output order — never completion order — regardless of this flag.
	ParallelFollowups bool
	// MaxFollowupConcurrency caps the fan-out when ParallelFollowups
	// is true. A value <= 0 defaults to MaxFollowupQueries (the
	// per-round cap). Ignored when ParallelFollowups is false.
	MaxFollowupConcurrency int
}

// AskOptions configures System.Ask — the standard retrieve-pack-generate
// answer path. Reflection is disabled when Reflection is nil, when
// Reflection.Mode is the zero value, or when it is ReflectionModeOff.
//
// Compatibility note: this exported struct may grow additively over time, so
// keyed composite literals are recommended.
type AskOptions struct {
	Search    SearchOptions   // Search configures the retrieval stage.
	Template  prompt.Template // Template overrides the prompt template; nil uses the System default.
	Metadata  map[string]any  // Metadata is caller-supplied passthrough sent to the model.
	MaxTokens int             // MaxTokens caps the packed context token budget.
	// MaxTotalTokens caps the cumulative TotalTokens across every Generate
	// call within one Ask (ask + reflection_decision + grader + planner +
	// any sub-stages). A value <= 0 (default zero) is unlimited and
	// preserves v1.6.0 behavior byte-for-byte. When non-zero and the
	// cumulative StageTokenUsage exceeds this cap after any successful
	// Generate, Ask aborts and returns a *BudgetExceededError with
	// PartialDiagnostics carrying the trace collected up to the abort.
	//
	// Scope: v1.7.0 enforcement is Ask-only. AskGlobal and AskDrift ignore
	// the budget — tracked for a future minor version.
	MaxTotalTokens int
	Reflection     *ReflectionOptions // Reflection configures the optional bounded self-reflection loop.
	// QueryPlanner is a per-Ask override for the active-retrieval
	// QueryPlanner. When non-nil it takes precedence over
	// Options.QueryPlanner for this Ask call only (the system-level
	// planner is unaffected). When nil, the resolved planner falls back
	// to the system-level QueryPlanner — and then to NoopQueryPlanner
	// if neither is set. Active retrieval still requires
	// Reflection.EnableActiveRetrieval=true to fire.
	QueryPlanner QueryPlanner
}

// GlobalOptions configures System.AskGlobal — the map-reduce global-search
// answer path over community reports. It is intentionally small: v0.8 fixes
// global search on the coarsest community level, so the only knob is how many
// communities to consult.
type GlobalOptions struct {
	// Namespace selects which namespace's community hierarchy to search.
	Namespace string
	// MaxCommunities caps how many coarsest-level communities feed the
	// map-reduce. A value <= 0 selects a sane default (defaultMaxCommunities).
	// When the coarsest level has more communities than this, AskGlobal ranks
	// them by query-token overlap with member entity names and keeps the top
	// MaxCommunities (ties broken by community ID).
	MaxCommunities int
	// MaxTotalTokens caps the cumulative TotalTokens across every Generate
	// call within one AskGlobal — the per-community map calls plus the
	// reduce call. A value <= 0 (default zero) is unlimited and preserves
	// v1.8.0 behavior byte-for-byte. When non-zero and the cumulative
	// StageTokenUsage exceeds this cap after any successful sub-stage
	// Generate, AskGlobal aborts and returns (Answer{}, *BudgetExceededError)
	// with PartialDiagnostics.Global carrying the trace collected up to
	// the abort (CommunityIDs/MapScores/MapCalls/ConsultedReports). The
	// BudgetExceededError.Stage carries the sub-stage tag that tripped
	// (StageAskGlobalMap or StageAskGlobalReduce). v1.9.0.
	MaxTotalTokens int
}

// DriftOptions configures System.AskDrift — the DRIFT hybrid-search answer
// path: a global primer pass, a hard-bounded local follow-up loop, and a
// synthesis step. It is a third answer path alongside Ask and AskGlobal.
type DriftOptions struct {
	// Namespace selects which namespace's community hierarchy and entity
	// graph DRIFT searches.
	Namespace string
	// MaxCommunities caps how many coarsest-level communities the primer
	// maps over (passed straight to selectCommunities). A value <= 0
	// selects a sane default (driftDefaultMaxCommunities).
	MaxCommunities int
	// Rounds bounds the local follow-up loop. A value <= 0 defaults to
	// driftDefaultRounds; a value above driftMaxRounds is clamped down to
	// driftMaxRounds — the loop is hard-bounded by construction.
	Rounds int
	// TopK caps how many provenance chunks each local round packs into the
	// model context. A value <= 0 selects driftDefaultTopK.
	TopK int
	// MaxTotalTokens caps the cumulative TotalTokens across every Generate
	// call within one AskDrift — the per-community primer-map calls, every
	// local-round Generate, and the final synthesis call. A value <= 0
	// (default zero) is unlimited and preserves v1.8.0 behavior
	// byte-for-byte. When non-zero and the cumulative StageTokenUsage
	// exceeds this cap after any successful sub-stage Generate, AskDrift
	// aborts and returns (Answer{}, *BudgetExceededError) with
	// PartialDiagnostics.Drift carrying the trace collected up to the
	// abort (PrimerCommunityIDs/Rounds/RoundEntityIDs/ConsultedReports).
	// The BudgetExceededError.Stage carries the sub-stage tag that tripped
	// (StageAskDriftPrimer, StageAskDriftLocal, or StageAskDriftSynth).
	// v1.9.0.
	MaxTotalTokens int
}

// Options is the construction config for a System — every dependency New
// wires together. Unset fields fall back to the SDK's built-in defaults.
type Options struct {
	Splitter         ingest.Splitter            // Splitter is the chunking strategy; nil defaults to a CharSplitter.
	Embedder         embed.Embedder             // Embedder produces vectors; nil defaults to a HashEmbedder.
	Store            store.Store                // Store is the storage backend; nil defaults to an InMemoryStore.
	Model            generate.Model             // Model is the generation model; nil disables the answer paths.
	Template         prompt.Template            // Template is the prompt template; nil defaults to DefaultQATemplate.
	Preprocessor     retrieve.QueryPreprocessor // Preprocessor shapes queries; nil defaults to LLMExpansionPreprocessor.
	Retriever        retrieve.Retriever         // Retriever fetches candidates; nil defaults to the hybrid retriever.
	Reranker         rerank.Reranker            // Reranker re-scores hits; nil defaults to HeuristicReranker.
	Packer           pack.Packer                // Packer builds the context; nil defaults to GreedyTokenPacker.
	MaxChars         int                        // MaxChars is the default chunk size; <= 0 defaults to 500.
	Observer         Observer                   // Observer receives per-run trace callbacks.
	Redactor         guard.Redactor             // Redactor removes PII during ingest.
	InjectionScanner guard.InjectionScanner     // InjectionScanner screens retrieved content for prompt injection.
	SanitizeMode     guard.SanitizeMode         // SanitizeMode controls how flagged content is handled.
	EntityExtractor  graph.EntityExtractor      // EntityExtractor extracts the knowledge graph during ingest.
	// EntityResolver, when set, runs as an opt-in pre-pass in Import that
	// merges near-duplicate entities (e.g. "Acme" / "Acme Corp") by
	// embedding similarity before graph.Canonicalize's exact-match merge.
	// A nil resolver defaults to graph.NoopEntityResolver{} — ingestion is
	// then byte-identical to pre-fuzzy-resolution behavior.
	EntityResolver graph.EntityResolver
	// CommunitySummarizer, when set, writes the community reports that
	// System.AskGlobal maps over. AskGlobal generates reports lazily on a
	// cache miss (or a stale ContentHash) and persists them on the store's
	// store.CommunityStore. A nil summarizer makes AskGlobal return
	// ErrCommunitySummarizerRequired the first time a report must be built.
	CommunitySummarizer graph.CommunitySummarizer
	// CommunityDetector, when set together with a store that implements
	// store.CommunityStore, makes Import detect a community hierarchy over
	// the namespace graph after it is persisted. A nil detector (or a store
	// that is not a CommunityStore) leaves communities undetected — Import
	// behaves exactly as before.
	CommunityDetector graph.CommunityDetector
	// Grader, when set, is the per-chunk Grader called during a
	// reflection round when ReflectionOptions.EnableChunkGrading is true.
	// A nil Grader with grading enabled falls back to NoopGrader (every
	// chunk scores 0.5) so the wiring stays functional.
	Grader Grader
	// QueryPlanner, when set, is the planner consulted by the active
	// retrieval pass when ReflectionOptions.EnableActiveRetrieval is
	// true. A nil planner with active retrieval enabled falls back to
	// NoopQueryPlanner (no follow-ups) so the wiring stays functional —
	// active retrieval then degrades to a no-op without breaking the
	// Ask call.
	QueryPlanner QueryPlanner
	// Compressor, when set, shrinks retrieved chunk content to
	// query-relevant material between rerank and pack on a System.Ask run
	// that sets SearchOptions.EnableCompression. A nil Compressor defaults
	// to compress.NoopCompressor (no compression).
	Compressor compress.Compressor
}
