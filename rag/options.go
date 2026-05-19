package rag

import (
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

type SearchOptions struct {
	TopK                         int
	Namespace                    string
	Filters                      map[string]any
	SecurityFilters              map[string]any
	RoutePath                    []string
	EnableAutoRoute              bool
	AutoRouteMinScore            float64
	AutoRouteMaxCandidates       int
	AutoRouteConfidenceThreshold float64
	AutoRouteFanout              int
	AutoRouteConfidenceGap       float64
	EnableMQE                    bool
	EnableHyDE                   bool
	MQECount                     int
	EnableRerank                 bool
	EnableStructure              bool
	EnableGraph                  bool
	EnableTreeExpansion          bool
	ExpansionDepth               int
}

type AskOptions struct {
	Search    SearchOptions
	Template  prompt.Template
	Metadata  map[string]any
	MaxTokens int
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
}

type Options struct {
	Splitter         ingest.Splitter
	Embedder         embed.Embedder
	Store            store.Store
	Model            generate.Model
	Template         prompt.Template
	Preprocessor     retrieve.QueryPreprocessor
	Retriever        retrieve.Retriever
	Reranker         rerank.Reranker
	Packer           pack.Packer
	MaxChars         int
	Observer         Observer
	Redactor         guard.Redactor
	InjectionScanner guard.InjectionScanner
	SanitizeMode     guard.SanitizeMode
	EntityExtractor  graph.EntityExtractor
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
}
