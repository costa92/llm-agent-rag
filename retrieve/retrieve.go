// Package retrieve fetches candidate chunks for a query. Retriever is the
// central seam; the package ships frozen concrete retrievers — Dense,
// Lexical, Hybrid, Graph, MultiHop, Structure, and Variant — plus the
// query-shaping seams QueryDecomposer, QueryPreprocessor, QueryEmbedder,
// EntityLinker, and SectionPlanner for callers that supply their own.
package retrieve

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/costa92/llm-agent-rag/advanced"
	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/store"
	"github.com/costa92/llm-agent-rag/tree"
)

// Request is a retrieval request: the query plus every per-call retrieval
// knob (routing, query expansion, structure, graph, and tree expansion).
type Request struct {
	Query                        string         // Query is the search query text.
	Namespace                    string         // Namespace scopes the search to one namespace.
	TopK                         int            // TopK caps the number of hits returned.
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
	EnableStepBack               bool           // EnableStepBack turns on step-back (higher-level) query expansion.
	EnableStructure              bool           // EnableStructure turns on structure-aware retrieval.
	EnableGraph                  bool           // EnableGraph turns on graph retrieval.
	EnableTreeExpansion          bool           // EnableTreeExpansion turns on document-tree neighbor expansion.
	ExpansionDepth               int            // ExpansionDepth bounds tree-expansion depth.
	QueryVariants                []string       // QueryVariants are pre-computed alternate queries to search.
}

// PreprocessResult is the output of a QueryPreprocessor: the query variants to
// search and a partial Trace.
type PreprocessResult struct {
	QueryVariants []string // QueryVariants are the queries to run, original first.
	Trace         Trace    // Trace is the partial retrieval trace from preprocessing.
}

// RouteCandidate is one candidate section route considered by auto-routing.
type RouteCandidate struct {
	Path       []string // Path is the section route this candidate represents.
	Score      float64  // Score is the candidate's raw routing score.
	Confidence float64  // Confidence is the normalized routing confidence.
	Queries    []string // Queries are the queries associated with this route.
	Signals    []string // Signals names the routing signals that produced the candidate.
	Selected   bool     // Selected is true when the candidate was chosen for search.
	Reason     string   // Reason explains why the candidate was selected or rejected.
}

// RoutePolicyTrace records how the auto-route policy decided fanout vs.
// convergence for one request.
type RoutePolicyTrace struct {
	Mode                string   // Mode is the policy outcome, e.g. "fanout" or "converged".
	ConfidenceThreshold float64  // ConfidenceThreshold is the keep threshold applied to candidates.
	ConfidenceGap       float64  // ConfidenceGap is the convergence gap threshold.
	Gap                 float64  // Gap is the observed top-1 vs top-2 confidence gap.
	Fanout              int      // Fanout is the number of routes searched.
	CandidateCount      int      // CandidateCount is how many candidates were considered.
	SelectedCount       int      // SelectedCount is how many candidates were searched.
	Rationale           []string // Rationale explains the policy decision step by step.
}

// TrajectoryStep records one route searched during a multi-route retrieval.
type TrajectoryStep struct {
	Route            []string // Route is the section route searched in this step.
	Confidence       float64  // Confidence is the route's routing confidence.
	Mode             string   // Mode is the routing mode for this step.
	HitCount         int      // HitCount is the number of hits the step returned.
	HitIDs           []string // HitIDs are the chunk IDs the step returned.
	MatchedSections  []string // MatchedSections are the sections the step matched.
	ExpandedSections []string // ExpandedSections are the sections added by expansion.
	Rationale        string   // Rationale explains the step.
}

// SectionPlannerDecision is the output of a SectionPlanner: which route
// candidates to search and how.
type SectionPlannerDecision struct {
	Selected  []RouteCandidate // Selected are the candidates to search.
	Mode      string           // Mode is the planning outcome, e.g. "fanout" or "converged".
	Fanout    int              // Fanout is how many routes will be searched.
	Gap       float64          // Gap is the observed top-1 vs top-2 confidence gap.
	Rationale []string         // Rationale explains the planning decision.
}

// SectionPlanner decides which route candidates to search for a request. It
// is the section-routing seam; GapAwareSectionPlanner is the built-in.
type SectionPlanner interface {
	// Plan selects route candidates to search for req.
	Plan(ctx context.Context, req Request, candidates []RouteCandidate) (SectionPlannerDecision, error)
}

// GapAwareSectionPlanner is the built-in SectionPlanner. It converges to the
// top route when its confidence lead exceeds the request's gap threshold and
// otherwise fans out across the strongest candidates.
type GapAwareSectionPlanner struct{}

// Plan selects route candidates for req, converging or fanning out by gap.
func (GapAwareSectionPlanner) Plan(_ context.Context, req Request, candidates []RouteCandidate) (SectionPlannerDecision, error) {
	if len(candidates) == 0 {
		return SectionPlannerDecision{
			Mode:      "single",
			Fanout:    1,
			Rationale: []string{"no route candidates available"},
		}, nil
	}
	filtered, rationale := filterRouteCandidates(candidates, req.AutoRouteConfidenceThreshold)
	if len(filtered) == 0 {
		return SectionPlannerDecision{
			Mode:      "single",
			Fanout:    1,
			Rationale: append([]string(nil), rationale...),
		}, nil
	}
	gap := 0.0
	converged := false
	if req.AutoRouteConfidenceGap > 0 && len(filtered) >= 2 {
		gap = filtered[0].Confidence - filtered[1].Confidence
		if gap >= req.AutoRouteConfidenceGap {
			rationale = append(rationale, "converged: top-1 dominates by gap="+formatFloat(gap)+" >= threshold "+formatFloat(req.AutoRouteConfidenceGap))
			filtered = filtered[:1]
			converged = true
		} else {
			rationale = append(rationale, "fanout: top-2 within gap="+formatFloat(gap)+" < threshold "+formatFloat(req.AutoRouteConfidenceGap))
		}
	}
	fanout := req.AutoRouteFanout
	if fanout <= 0 {
		fanout = 1
	}
	if converged {
		fanout = 1
	}
	if len(filtered) > fanout {
		filtered = filtered[:fanout]
	}
	mode := "fanout"
	if converged {
		mode = "converged"
	}
	markSelectedCandidates(filtered, fanout)
	return SectionPlannerDecision{
		Selected:  filtered,
		Mode:      mode,
		Fanout:    fanout,
		Gap:       gap,
		Rationale: append([]string(nil), rationale...),
	}, nil
}

// Trace is the full retrieval trace for one request — every routing,
// expansion, fusion, and graph decision the retriever made.
type Trace struct {
	OriginalQuery       string              // OriginalQuery is the query as submitted.
	EffectiveQuery      string              // EffectiveQuery is the query actually searched after preprocessing.
	QueryVariants       []string            // QueryVariants are every query variant searched.
	RoutePath           []string            // RoutePath is the pinned section route, if any.
	AutoRoutePath       []string            // AutoRoutePath is the route auto-routing selected.
	AutoRouteCandidates []RouteCandidate    // AutoRouteCandidates are the candidates auto-routing considered.
	RoutePolicy         RoutePolicyTrace    // RoutePolicy records the routing-policy decision.
	SearchPath          []string            // SearchPath is the route ultimately searched.
	MatchedSections     []string            // MatchedSections are the sections that matched.
	ExpandedSections    []string            // ExpandedSections are sections added by expansion.
	ExpandedChunkIDs    []string            // ExpandedChunkIDs are chunks added by expansion.
	SelectedChunkIDs    []string            // SelectedChunkIDs are the chunks returned.
	SearchTrajectory    []TrajectoryStep    // SearchTrajectory records each route searched.
	Fusion              []FusionAttribution // Fusion records per-chunk hybrid-fusion attribution.
	Metrics             obs.Metrics         // Metrics is the cost-and-latency record for the retrieval.
	Hops                []HopAttribution    // Hops records multi-hop sub-query attribution.
	Graph               GraphTrace          // Graph records graph-retrieval traversal.
}

// FusionAttribution records how each retrieval signal ranked one chunk during
// reciprocal rank fusion. A rank of 0 means the signal did not return the
// chunk. RRFScore is the chunk's summed RRF contribution across all signals.
type FusionAttribution struct {
	ChunkID       string  // ChunkID identifies the chunk this attribution describes.
	DenseRank     int     // DenseRank is the chunk's rank from dense retrieval; 0 if absent.
	LexicalRank   int     // LexicalRank is the chunk's rank from lexical retrieval; 0 if absent.
	StructureRank int     // StructureRank is the chunk's rank from structure retrieval; 0 if absent.
	GraphRank     int     // GraphRank is the chunk's rank from graph retrieval; 0 if absent.
	RRFScore      float64 // RRFScore is the chunk's summed reciprocal-rank-fusion score.
}

// QueryPreprocessor shapes a query before retrieval — expansion, HyDE,
// routing. It is the query-shaping seam; NoopPreprocessor and
// LLMExpansionPreprocessor are the built-ins.
type QueryPreprocessor interface {
	// Process produces the query variants to search for req.
	Process(ctx context.Context, req Request) (PreprocessResult, error)
}

// NoopPreprocessor is a QueryPreprocessor that passes the query through
// unchanged.
type NoopPreprocessor struct{}

// Process returns req's query unchanged as the sole variant.
func (NoopPreprocessor) Process(_ context.Context, req Request) (PreprocessResult, error) {
	return PreprocessResult{
		QueryVariants: []string{req.Query},
		Trace: Trace{
			OriginalQuery:       req.Query,
			EffectiveQuery:      req.Query,
			QueryVariants:       []string{req.Query},
			RoutePath:           append([]string(nil), req.RoutePath...),
			AutoRoutePath:       append([]string(nil), req.RoutePath...),
			AutoRouteCandidates: routeCandidatesFromPath(req.Query, req.RoutePath),
			SelectedChunkIDs:    nil,
		},
	}, nil
}

// LLMExpansionPreprocessor is a QueryPreprocessor that uses an LLM to expand
// the query via multi-query expansion, HyDE, and step-back prompting when the
// request enables them.
type LLMExpansionPreprocessor struct {
	Model generate.Model // Model generates the expansion queries.
}

// Process expands req's query with MQE, HyDE, and step-back variants when enabled.
func (p LLMExpansionPreprocessor) Process(ctx context.Context, req Request) (PreprocessResult, error) {
	variants := uniqueQueries(req.Query)
	if req.EnableMQE {
		if p.Model == nil {
			return PreprocessResult{}, advanced.ErrModelRequired
		}
		count := req.MQECount
		if count <= 0 {
			count = 3
		}
		expansions, err := advanced.ExpandQuery(ctx, p.Model, req.Query, count)
		if err != nil {
			return PreprocessResult{}, err
		}
		variants = appendUniqueQueries(variants, expansions...)
	}
	if req.EnableHyDE {
		if p.Model == nil {
			return PreprocessResult{}, advanced.ErrModelRequired
		}
		hypo, err := advanced.GenerateHypothetical(ctx, p.Model, req.Query)
		if err != nil {
			return PreprocessResult{}, err
		}
		variants = appendUniqueQueries(variants, hypo)
	}
	if req.EnableStepBack {
		if p.Model == nil {
			return PreprocessResult{}, advanced.ErrModelRequired
		}
		stepback, err := advanced.GenerateStepBack(ctx, p.Model, req.Query)
		if err != nil {
			return PreprocessResult{}, err
		}
		variants = appendUniqueQueries(variants, stepback)
	}
	if len(variants) == 0 {
		variants = []string{req.Query}
	}
	return PreprocessResult{
		QueryVariants: variants,
		Trace: Trace{
			OriginalQuery:       req.Query,
			EffectiveQuery:      variants[0],
			QueryVariants:       append([]string(nil), variants...),
			RoutePath:           append([]string(nil), req.RoutePath...),
			AutoRoutePath:       append([]string(nil), req.RoutePath...),
			AutoRouteCandidates: routeCandidatesFromPath(req.Query, req.RoutePath),
			SelectedChunkIDs:    nil,
		},
	}, nil
}

// QueryEmbedder turns a query string into a vector. It is the query-embedding
// seam DenseRetriever depends on.
type QueryEmbedder interface {
	// Embed returns the embedding vector for text.
	Embed(ctx context.Context, text string) (embed.Vector, error)
}

// Retriever fetches candidate chunks for a request. It is the central
// retrieval seam; the package ships the frozen concrete retrievers.
type Retriever interface {
	// Retrieve returns the hits for req along with a retrieval Trace.
	Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error)
}

// ErrBaseRetrieverRequired is returned by composite retrievers when their
// wrapped base Retriever is nil.
var ErrBaseRetrieverRequired = errors.New("retrieve: base retriever required")

// VariantRetriever runs a base Retriever across multiple section routes
// chosen by a SectionPlanner and fuses the results.
type VariantRetriever struct {
	Base    Retriever      // Base is the underlying retriever run per route.
	Planner SectionPlanner // Planner selects the routes to search.
}

// Retrieve plans section routes for req and fuses the base retriever's hits.
func (r VariantRetriever) Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error) {
	if r.Base == nil {
		return nil, Trace{}, ErrBaseRetrieverRequired
	}
	planner := r.Planner
	if planner == nil {
		planner = GapAwareSectionPlanner{}
	}
	variants := req.QueryVariants
	if len(variants) == 0 {
		variants = []string{req.Query}
	}
	type rankedHit struct {
		hit   store.Hit
		order int
	}
	merged := make(map[string]rankedHit)
	nextOrder := 0
	trace := Trace{
		OriginalQuery:  req.Query,
		EffectiveQuery: req.Query,
		QueryVariants:  append([]string(nil), variants...),
		RoutePath:      append([]string(nil), req.RoutePath...),
		AutoRoutePath:  append([]string(nil), req.RoutePath...),
	}
	for _, query := range variants {
		subReq := req
		subReq.Query = query
		subReq.QueryVariants = nil
		hits, subTrace, err := retrieveWithRoutePolicy(ctx, r.Base, planner, subReq)
		if err != nil {
			return nil, Trace{}, err
		}
		if trace.EffectiveQuery == req.Query && subTrace.EffectiveQuery != "" {
			trace.EffectiveQuery = subTrace.EffectiveQuery
		}
		if len(trace.AutoRoutePath) == 0 && len(subTrace.AutoRoutePath) > 0 {
			trace.AutoRoutePath = append([]string(nil), subTrace.AutoRoutePath...)
		}
		if len(trace.RoutePath) == 0 && len(subTrace.RoutePath) > 0 {
			trace.RoutePath = append([]string(nil), subTrace.RoutePath...)
		}
		if trace.RoutePolicy.Mode == "" && subTrace.RoutePolicy.Mode != "" {
			trace.RoutePolicy = subTrace.RoutePolicy
		}
		if len(trace.Graph.SeedEntityIDs) == 0 && len(subTrace.Graph.SeedEntityIDs) > 0 {
			trace.Graph = subTrace.Graph
		}
		trace.AutoRouteCandidates = mergeRouteCandidates(trace.AutoRouteCandidates, subTrace.AutoRouteCandidates)
		trace.SearchPath = appendUniqueStrings(trace.SearchPath, subTrace.SearchPath...)
		trace.MatchedSections = appendUniqueStrings(trace.MatchedSections, subTrace.MatchedSections...)
		trace.ExpandedSections = appendUniqueStrings(trace.ExpandedSections, subTrace.ExpandedSections...)
		trace.ExpandedChunkIDs = appendUniqueStrings(trace.ExpandedChunkIDs, subTrace.ExpandedChunkIDs...)
		for _, step := range subTrace.SearchTrajectory {
			trace.SearchTrajectory = append(trace.SearchTrajectory, TrajectoryStep{
				Route:            append([]string(nil), step.Route...),
				Confidence:       step.Confidence,
				Mode:             step.Mode,
				HitCount:         step.HitCount,
				HitIDs:           append([]string(nil), step.HitIDs...),
				MatchedSections:  append([]string(nil), step.MatchedSections...),
				ExpandedSections: append([]string(nil), step.ExpandedSections...),
				Rationale:        step.Rationale,
			})
		}
		for _, hit := range hits {
			prev, ok := merged[hit.Chunk.ID]
			if !ok {
				merged[hit.Chunk.ID] = rankedHit{hit: hit, order: nextOrder}
				nextOrder++
				continue
			}
			if hit.Score > prev.hit.Score {
				prev.hit = hit
				merged[hit.Chunk.ID] = prev
			}
		}
	}
	out := make([]rankedHit, 0, len(merged))
	for _, hit := range merged {
		out = append(out, hit)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].hit.Score == out[j].hit.Score {
			return out[i].order < out[j].order
		}
		return out[i].hit.Score > out[j].hit.Score
	})
	limit := len(out)
	if req.TopK > 0 && limit > req.TopK {
		limit = req.TopK
	}
	hits := make([]store.Hit, 0, limit)
	for _, hit := range out[:limit] {
		hits = append(hits, hit.hit)
	}
	trace.SelectedChunkIDs = collectChunkIDs(hits)
	return hits, trace, nil
}

func retrieveWithRoutePolicy(ctx context.Context, base Retriever, planner SectionPlanner, req Request) ([]store.Hit, Trace, error) {
	if base == nil {
		return nil, Trace{}, ErrBaseRetrieverRequired
	}
	if planner == nil {
		planner = GapAwareSectionPlanner{}
	}
	if len(req.RoutePath) > 0 || !req.EnableAutoRoute || req.AutoRouteFanout <= 1 {
		hits, trace, err := base.Retrieve(ctx, req)
		if err != nil {
			return nil, Trace{}, err
		}
		policyRationale := []string{"fanout disabled or explicit route path provided"}
		if req.AutoRouteConfidenceGap > 0 {
			policyRationale = append(policyRationale, "gap policy inactive: fanout disabled")
		}
		trace.RoutePolicy = RoutePolicyTrace{
			Mode:          "single",
			ConfidenceGap: req.AutoRouteConfidenceGap,
			Fanout:        1,
			Rationale:     policyRationale,
		}
		trace.SearchTrajectory = []TrajectoryStep{singleTrajectoryStep(req, trace, hits)}
		return hits, trace, nil
	}
	probeReq := req
	probeReq.TopK = maxInt(req.TopK, 8)
	hits, trace, err := base.Retrieve(ctx, probeReq)
	if err != nil {
		return nil, Trace{}, err
	}
	decision, err := planner.Plan(ctx, req, trace.AutoRouteCandidates)
	if err != nil {
		return nil, Trace{}, err
	}
	candidates := decision.Selected
	if len(candidates) == 0 {
		trace.RoutePolicy = RoutePolicyTrace{
			Mode:                "single",
			ConfidenceThreshold: req.AutoRouteConfidenceThreshold,
			ConfidenceGap:       req.AutoRouteConfidenceGap,
			Fanout:              1,
			CandidateCount:      len(trace.AutoRouteCandidates),
			SelectedCount:       0,
			Rationale:           append([]string(nil), decision.Rationale...),
		}
		return hits, trace, nil
	}
	type rankedHit struct {
		hit   store.Hit
		order int
	}
	merged := make(map[string]rankedHit)
	nextOrder := 0
	mergedTrace := trace
	mergedTrace.RoutePath = append([]string(nil), candidates[0].Path...)
	mergedTrace.AutoRoutePath = append([]string(nil), candidates[0].Path...)
	mergedTrace.AutoRouteCandidates = cloneRouteCandidates(candidates)
	mode := decision.Mode
	if mode == "" {
		mode = "fanout"
	}
	mergedTrace.RoutePolicy = RoutePolicyTrace{
		Mode:                mode,
		ConfidenceThreshold: effectiveThreshold(req.AutoRouteConfidenceThreshold),
		ConfidenceGap:       req.AutoRouteConfidenceGap,
		Gap:                 decision.Gap,
		Fanout:              decision.Fanout,
		CandidateCount:      len(trace.AutoRouteCandidates),
		SelectedCount:       len(candidates),
		Rationale:           append([]string(nil), decision.Rationale...),
	}
	mergedTrace.SearchPath = nil
	mergedTrace.MatchedSections = nil
	mergedTrace.ExpandedSections = nil
	mergedTrace.ExpandedChunkIDs = nil
	mergedTrace.SearchTrajectory = nil
	for _, candidate := range candidates {
		routeReq := req
		routeReq.RoutePath = append([]string(nil), candidate.Path...)
		routeReq.EnableAutoRoute = false
		routeHits, routeTrace, err := base.Retrieve(ctx, routeReq)
		if err != nil {
			return nil, Trace{}, err
		}
		mergedTrace.SearchPath = appendUniqueStrings(mergedTrace.SearchPath, routeTrace.SearchPath...)
		mergedTrace.MatchedSections = appendUniqueStrings(mergedTrace.MatchedSections, routeTrace.MatchedSections...)
		mergedTrace.ExpandedSections = appendUniqueStrings(mergedTrace.ExpandedSections, routeTrace.ExpandedSections...)
		mergedTrace.ExpandedChunkIDs = appendUniqueStrings(mergedTrace.ExpandedChunkIDs, routeTrace.ExpandedChunkIDs...)
		mergedTrace.SearchTrajectory = append(mergedTrace.SearchTrajectory, TrajectoryStep{
			Route:            append([]string(nil), candidate.Path...),
			Confidence:       candidate.Confidence,
			Mode:             mode,
			HitCount:         len(routeHits),
			HitIDs:           collectChunkIDs(routeHits),
			MatchedSections:  append([]string(nil), routeTrace.MatchedSections...),
			ExpandedSections: append([]string(nil), routeTrace.ExpandedSections...),
			Rationale:        candidate.Reason,
		})
		for _, hit := range routeHits {
			boosted := hit
			boosted.Score = hit.Score + candidate.Confidence
			prev, ok := merged[hit.Chunk.ID]
			if !ok {
				merged[hit.Chunk.ID] = rankedHit{hit: boosted, order: nextOrder}
				nextOrder++
				continue
			}
			if boosted.Score > prev.hit.Score {
				prev.hit = boosted
				merged[hit.Chunk.ID] = prev
			}
		}
	}
	out := make([]rankedHit, 0, len(merged))
	for _, hit := range merged {
		out = append(out, hit)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].hit.Score == out[j].hit.Score {
			return out[i].order < out[j].order
		}
		return out[i].hit.Score > out[j].hit.Score
	})
	limit := len(out)
	if req.TopK > 0 && limit > req.TopK {
		limit = req.TopK
	}
	finalHits := make([]store.Hit, 0, limit)
	for _, hit := range out[:limit] {
		finalHits = append(finalHits, hit.hit)
	}
	mergedTrace.SelectedChunkIDs = collectChunkIDs(finalHits)
	return finalHits, mergedTrace, nil
}

func filterRouteCandidates(candidates []RouteCandidate, threshold float64) ([]RouteCandidate, []string) {
	if len(candidates) == 0 {
		return nil, []string{"no route candidates available"}
	}
	threshold = effectiveThreshold(threshold)
	out := make([]RouteCandidate, 0, len(candidates))
	rationale := make([]string, 0, len(candidates)+1)
	rationale = append(rationale, "applied confidence threshold "+formatFloat(threshold))
	for _, candidate := range candidates {
		if candidate.Confidence < threshold {
			rationale = append(rationale, "rejected "+strings.Join(candidate.Path, " > ")+" confidence="+formatFloat(candidate.Confidence))
			continue
		}
		candidate.Reason = "kept: confidence >= threshold"
		out = append(out, candidate)
		rationale = append(rationale, "kept "+strings.Join(candidate.Path, " > ")+" confidence="+formatFloat(candidate.Confidence))
	}
	if len(out) == 0 {
		fallback := candidates[0]
		fallback.Reason = "fallback: top candidate retained after thresholding"
		out = append(out, fallback)
		rationale = append(rationale, "fallback to top candidate "+strings.Join(fallback.Path, " > "))
	}
	return out, rationale
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// DenseRetriever is the dense-vector concrete Retriever: it embeds the query
// and searches the store by vector similarity.
type DenseRetriever struct {
	Embedder QueryEmbedder // Embedder turns the query into a vector.
	Store    store.Store   // Store is searched by vector similarity.
}

// Retrieve embeds req's query and returns the store's nearest-vector hits.
func (r DenseRetriever) Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error) {
	req, autoRouteCandidates := withAutoRoute(req, r.Store)
	vec, err := r.Embedder.Embed(ctx, req.Query)
	if err != nil {
		return nil, Trace{}, err
	}
	if len(req.RoutePath) > 0 {
		return r.retrieveWithinRoute(ctx, req, vec, autoRouteCandidates)
	}
	hits, err := r.Store.Search(ctx, store.Query{
		Namespace:       req.Namespace,
		Vector:          vec,
		TopK:            req.TopK,
		Filters:         store.Filter(req.Filters),
		SecurityFilters: store.Filter(req.SecurityFilters),
	})
	if err != nil {
		return nil, Trace{}, err
	}
	return hits, Trace{
		OriginalQuery:       req.Query,
		EffectiveQuery:      req.Query,
		QueryVariants:       []string{req.Query},
		RoutePath:           append([]string(nil), req.RoutePath...),
		AutoRoutePath:       append([]string(nil), req.RoutePath...),
		AutoRouteCandidates: routeTraceCandidates(req, autoRouteCandidates),
		SelectedChunkIDs:    collectChunkIDs(hits),
	}, nil
}

func (r DenseRetriever) retrieveWithinRoute(ctx context.Context, req Request, vec embed.Vector, autoRouteCandidates []RouteCandidate) ([]store.Hit, Trace, error) {
	chunks, err := r.Store.List(ctx, req.Namespace, store.Filter(req.Filters), store.Filter(req.SecurityFilters))
	if err != nil {
		return nil, Trace{}, err
	}
	hits := make([]store.Hit, 0, len(chunks))
	for _, chunk := range chunks {
		if !chunkInRoute(chunk, req.RoutePath) {
			continue
		}
		hits = append(hits, store.Hit{
			Chunk: chunk,
			Score: embed.CosineSimilarity(vec, chunk.Vector),
		})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if req.TopK > 0 && len(hits) > req.TopK {
		hits = hits[:req.TopK]
	}
	return hits, Trace{
		OriginalQuery:       req.Query,
		EffectiveQuery:      req.Query,
		QueryVariants:       []string{req.Query},
		RoutePath:           append([]string(nil), req.RoutePath...),
		AutoRoutePath:       append([]string(nil), req.RoutePath...),
		AutoRouteCandidates: routeTraceCandidates(req, autoRouteCandidates),
		SelectedChunkIDs:    collectChunkIDs(hits),
	}, nil
}

// BM25Params holds the Okapi BM25 tuning constants. The zero value resolves
// to the standard defaults (K1 1.2, B 0.75) via orDefault.
type BM25Params struct {
	K1 float64 // K1 is the BM25 term-frequency saturation constant (default 1.2).
	B  float64 // B is the BM25 length-normalization constant (default 0.75).
}

func (p BM25Params) orDefault() BM25Params {
	if p.K1 == 0 {
		p.K1 = 1.2
	}
	if p.B == 0 {
		p.B = 0.75
	}
	return p
}

// bm25Scores ranks a corpus against query tokens with Okapi BM25. The IDF
// term uses the non-negative ln(1 + (N-df+0.5)/(df+0.5)) variant. Corpus
// statistics (N, df, avgdl) are computed over the supplied chunks only.
func bm25Scores(queryTokens []string, corpus []store.StoredChunk, p BM25Params) map[string]float64 {
	if len(queryTokens) == 0 || len(corpus) == 0 {
		return map[string]float64{}
	}
	p = p.orDefault()

	counts := make(map[string]map[string]int, len(corpus))
	lengths := make(map[string]int, len(corpus))
	df := make(map[string]int)
	totalLen := 0
	for _, chunk := range corpus {
		toks := tokenize(chunk.Content)
		c := make(map[string]int, len(toks))
		for _, t := range toks {
			c[t]++
		}
		counts[chunk.ID] = c
		lengths[chunk.ID] = len(toks)
		totalLen += len(toks)
		for t := range c {
			df[t]++
		}
	}
	n := float64(len(corpus))
	avgdl := float64(totalLen) / n
	if avgdl == 0 {
		return map[string]float64{}
	}

	queryTerms := make([]string, 0, len(queryTokens))
	seen := make(map[string]struct{}, len(queryTokens))
	for _, t := range queryTokens {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		queryTerms = append(queryTerms, t)
	}
	idf := make(map[string]float64, len(queryTerms))
	for _, t := range queryTerms {
		d := float64(df[t])
		idf[t] = math.Log(1 + (n-d+0.5)/(d+0.5))
	}

	scores := make(map[string]float64, len(corpus))
	for id, c := range counts {
		dl := float64(lengths[id])
		if dl == 0 {
			continue
		}
		var score float64
		for _, t := range queryTerms {
			tf := float64(c[t])
			if tf == 0 {
				continue
			}
			norm := tf + p.K1*(1-p.B+p.B*dl/avgdl)
			score += idf[t] * (tf * (p.K1 + 1)) / norm
		}
		if score > 0 {
			scores[id] = score
		}
	}
	return scores
}

// LexicalRetriever is the lexical concrete Retriever: it ranks chunks by
// Okapi BM25, using the store's native LexicalSearcher when available.
type LexicalRetriever struct {
	Store  store.Store // Store is searched for keyword matches.
	Params BM25Params  // Params is the BM25 tuning configuration.
}

// Retrieve returns req's BM25-ranked lexical hits.
func (r LexicalRetriever) Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error) {
	req, autoRouteCandidates := withAutoRoute(req, r.Store)

	if searcher, ok := r.Store.(store.LexicalSearcher); ok && len(req.RoutePath) == 0 {
		hits, err := searcher.LexicalSearch(ctx, store.Query{
			Namespace:       req.Namespace,
			Text:            req.Query,
			TopK:            req.TopK,
			Filters:         store.Filter(req.Filters),
			SecurityFilters: store.Filter(req.SecurityFilters),
		})
		if err != nil {
			return nil, Trace{}, err
		}
		return hits, Trace{
			OriginalQuery:       req.Query,
			EffectiveQuery:      req.Query,
			QueryVariants:       []string{req.Query},
			RoutePath:           append([]string(nil), req.RoutePath...),
			AutoRoutePath:       append([]string(nil), req.RoutePath...),
			AutoRouteCandidates: routeTraceCandidates(req, autoRouteCandidates),
			SelectedChunkIDs:    collectChunkIDs(hits),
		}, nil
	}

	chunks, err := r.Store.List(ctx, req.Namespace, store.Filter(req.Filters), store.Filter(req.SecurityFilters))
	if err != nil {
		return nil, Trace{}, err
	}
	routed := make([]store.StoredChunk, 0, len(chunks))
	for _, chunk := range chunks {
		if !chunkInRoute(chunk, req.RoutePath) {
			continue
		}
		routed = append(routed, chunk)
	}
	scores := bm25Scores(tokenize(req.Query), routed, r.Params)
	hits := make([]store.Hit, 0, len(routed))
	for _, chunk := range routed {
		score, ok := scores[chunk.ID]
		if !ok || score <= 0 {
			continue
		}
		hits = append(hits, store.Hit{
			Chunk: chunk,
			Score: score,
		})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Chunk.ID < hits[j].Chunk.ID
	})
	if req.TopK > 0 && len(hits) > req.TopK {
		hits = hits[:req.TopK]
	}
	return hits, Trace{
		OriginalQuery:       req.Query,
		EffectiveQuery:      req.Query,
		QueryVariants:       []string{req.Query},
		RoutePath:           append([]string(nil), req.RoutePath...),
		AutoRoutePath:       append([]string(nil), req.RoutePath...),
		AutoRouteCandidates: routeTraceCandidates(req, autoRouteCandidates),
		SelectedChunkIDs:    collectChunkIDs(hits),
	}, nil
}

// StructureRetriever is the structure-aware concrete Retriever: it ranks
// chunks using document section structure and headings.
type StructureRetriever struct {
	Store store.Store // Store is searched for structural matches.
}

// Retrieve returns req's structure-aware hits.
func (r StructureRetriever) Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error) {
	req, autoRouteCandidates := withAutoRoute(req, r.Store)
	chunks, err := r.Store.List(ctx, req.Namespace, store.Filter(req.Filters), store.Filter(req.SecurityFilters))
	if err != nil {
		return nil, Trace{}, err
	}
	tokens := tokenize(req.Query)
	path := make([]string, 0, len(tokens))
	sectionSeen := make(map[string]struct{})
	matchedSections := make([]string, 0)
	expandedSections := make([]string, 0)
	merged := make(map[string]store.Hit, len(chunks))
	treesByDoc := make(map[string]*tree.DocumentTree)
	chunksByDoc := make(map[string][]store.StoredChunk)
	titleByDoc := make(map[string]string)
	for _, chunk := range chunks {
		if !chunkInRoute(chunk, req.RoutePath) {
			continue
		}
		chunksByDoc[chunk.DocID] = append(chunksByDoc[chunk.DocID], chunk)
		if _, ok := titleByDoc[chunk.DocID]; !ok {
			titleByDoc[chunk.DocID] = chunk.Title
		}
	}

	for docID, docChunks := range chunksByDoc {
		docTree := tree.BuildStored(docID, titleByDoc[docID], docChunks)
		treesByDoc[docID] = docTree
		for _, section := range docTree.Sections() {
			score := sectionNodeScore(tokens, section)
			if score <= 0 {
				continue
			}
			joinedPath := strings.Join(section.Path, " > ")
			if joinedPath != "" {
				path = append(path, joinedPath)
			}
			sectionID := sectionIDForPath(docChunks, section.Path)
			if sectionID == "" {
				sectionID = section.ID
			}
			if _, ok := sectionSeen[sectionID]; !ok {
				sectionSeen[sectionID] = struct{}{}
				matchedSections = append(matchedSections, sectionID)
			}
			if !containsString(expandedSections, section.ID) {
				expandedSections = append(expandedSections, section.ID)
			}
			leafHits := expandSectionLeaves(section, docChunks, req.ExpansionDepth)
			for _, expanded := range leafHits {
				expanded.Score += score
				expanded.Score += lexicalScore(tokens, expanded.Chunk.Content) * 0.5
				prev, ok := merged[expanded.Chunk.ID]
				if !ok || expanded.Score > prev.Score {
					merged[expanded.Chunk.ID] = expanded
				}
			}
		}
	}

	if len(merged) == 0 {
		for _, chunk := range chunks {
			if !chunkInRoute(chunk, req.RoutePath) {
				continue
			}
			score, matched := structureScore(tokens, chunk)
			if score <= 0 {
				continue
			}
			if matched != "" {
				path = append(path, matched)
			}
			if chunk.SectionID != "" {
				if _, ok := sectionSeen[chunk.SectionID]; !ok {
					sectionSeen[chunk.SectionID] = struct{}{}
					matchedSections = append(matchedSections, chunk.SectionID)
				}
			}
			merged[chunk.ID] = store.Hit{
				Chunk: chunk,
				Score: score,
			}
		}
	}

	hits := make([]store.Hit, 0, len(merged))
	for _, hit := range merged {
		hits = append(hits, hit)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if req.TopK > 0 && len(hits) > req.TopK {
		hits = hits[:req.TopK]
	}
	return hits, Trace{
		OriginalQuery:       req.Query,
		EffectiveQuery:      req.Query,
		QueryVariants:       []string{req.Query},
		RoutePath:           append([]string(nil), req.RoutePath...),
		AutoRoutePath:       append([]string(nil), req.RoutePath...),
		AutoRouteCandidates: routeTraceCandidates(req, autoRouteCandidates),
		SearchPath:          path,
		MatchedSections:     matchedSections,
		ExpandedSections:    expandedSections,
		ExpandedChunkIDs:    collectChunkIDs(hits),
		SelectedChunkIDs:    collectChunkIDs(hits),
	}, nil
}

func sectionNodeScore(tokens []string, section *tree.Node) float64 {
	if len(tokens) == 0 || section == nil {
		return 0
	}
	searchSpace := append([]string(nil), section.Path...)
	if section.Heading != "" {
		searchSpace = append(searchSpace, section.Heading)
	}
	if section.Title != "" {
		searchSpace = append(searchSpace, section.Title)
	}
	var score float64
	for _, candidate := range searchSpace {
		lc := strings.ToLower(candidate)
		for _, tok := range tokens {
			if strings.Contains(lc, tok) {
				score += 2
			}
		}
	}
	if score == 0 {
		return 0
	}
	score += float64(len(section.Path)) * 0.2
	return score
}

func sectionIDForPath(chunks []store.StoredChunk, path []string) string {
	for _, chunk := range chunks {
		if pathEqualsFold(chunk.SectionPath, path) && chunk.SectionID != "" {
			return chunk.SectionID
		}
	}
	return ""
}

func pathEqualsFold(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

// HybridRetriever is the hybrid concrete Retriever: it fuses dense, lexical,
// structure, and (optionally) graph signals via reciprocal rank fusion.
type HybridRetriever struct {
	Dense     Retriever // Dense is the dense-vector signal.
	Lexical   Retriever // Lexical is the BM25 lexical signal.
	Structure Retriever // Structure is the structure-aware signal.
	// Graph, when set and req.EnableGraph is true, contributes a fourth
	// RRF signal from knowledge-graph traversal.
	Graph Retriever
	// RRFConstant is the k constant in the reciprocal rank fusion formula
	// 1/(k + rank). Zero resolves to the standard default of 60.
	RRFConstant float64
}

// Retrieve fuses the configured retrieval signals for req via reciprocal
// rank fusion.
func (r HybridRetriever) Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error) {
	// Fan out Dense, Lexical, Structure, and Graph retrievers concurrently —
	// wall-clock cost drops from sum-of-N to max-of-N. We preserve the
	// original sequential error precedence (Dense > Lexical > Structure >
	// Graph) by writing each goroutine's result into a fixed-index slot and
	// scanning slots in order after wg.Wait(). Disabled retrievers leave
	// their slot at the zero value, identical to the sequential path.
	type slot struct {
		hits  []store.Hit
		trace Trace
		err   error
	}
	var slots [4]slot

	var wg sync.WaitGroup
	run := func(idx int, fn func() ([]store.Hit, Trace, error)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, t, e := fn()
			slots[idx] = slot{hits: h, trace: t, err: e}
		}()
	}
	run(0, func() ([]store.Hit, Trace, error) { return r.Dense.Retrieve(ctx, req) })
	run(1, func() ([]store.Hit, Trace, error) { return r.Lexical.Retrieve(ctx, req) })
	if req.EnableStructure && r.Structure != nil {
		run(2, func() ([]store.Hit, Trace, error) { return r.Structure.Retrieve(ctx, req) })
	}
	if req.EnableGraph && r.Graph != nil {
		run(3, func() ([]store.Hit, Trace, error) { return r.Graph.Retrieve(ctx, req) })
	}
	wg.Wait()

	// Error precedence: pick the first non-nil error in the original
	// sequential order (Dense > Lexical > Structure > Graph).
	for i := 0; i < 4; i++ {
		if slots[i].err != nil {
			return nil, Trace{}, slots[i].err
		}
	}
	denseHits, denseTrace := slots[0].hits, slots[0].trace
	lexHits := slots[1].hits
	structureHits, structureTrace := slots[2].hits, slots[2].trace
	graphHits, graphTrace := slots[3].hits, slots[3].trace

	fused := make(map[string]store.Hit, len(denseHits)+len(lexHits))
	rrfScores := make(map[string]float64, len(denseHits)+len(lexHits)+len(structureHits))

	k := r.RRFConstant
	if k == 0 {
		k = 60
	}
	denseRank := make(map[string]int, len(denseHits))
	lexRank := make(map[string]int, len(lexHits))
	structRank := make(map[string]int, len(structureHits))
	graphRank := make(map[string]int, len(graphHits))
	apply := func(hits []store.Hit, ranks map[string]int) {
		for i, hit := range hits {
			if _, ok := fused[hit.Chunk.ID]; !ok {
				fused[hit.Chunk.ID] = hit
			}
			if _, seen := ranks[hit.Chunk.ID]; !seen {
				ranks[hit.Chunk.ID] = i + 1
			}
			rrfScores[hit.Chunk.ID] += 1.0 / (k + float64(i+1))
		}
	}
	apply(denseHits, denseRank)
	apply(lexHits, lexRank)
	apply(structureHits, structRank)
	apply(graphHits, graphRank)

	out := make([]store.Hit, 0, len(fused))
	for id, hit := range fused {
		hit.Score = rrfScores[id]
		out = append(out, hit)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if req.TopK > 0 && len(out) > req.TopK {
		out = out[:req.TopK]
	}

	fusion := make([]FusionAttribution, 0, len(fused))
	for id := range fused {
		fusion = append(fusion, FusionAttribution{
			ChunkID:       id,
			DenseRank:     denseRank[id],
			LexicalRank:   lexRank[id],
			StructureRank: structRank[id],
			GraphRank:     graphRank[id],
			RRFScore:      rrfScores[id],
		})
	}
	sort.SliceStable(fusion, func(i, j int) bool {
		if fusion[i].RRFScore != fusion[j].RRFScore {
			return fusion[i].RRFScore > fusion[j].RRFScore
		}
		return fusion[i].ChunkID < fusion[j].ChunkID
	})

	return out, Trace{
		OriginalQuery:       req.Query,
		EffectiveQuery:      denseTrace.EffectiveQuery,
		QueryVariants:       append([]string(nil), denseTrace.QueryVariants...),
		RoutePath:           append([]string(nil), denseTrace.RoutePath...),
		AutoRoutePath:       append([]string(nil), denseTrace.AutoRoutePath...),
		AutoRouteCandidates: mergeRouteCandidates(denseTrace.AutoRouteCandidates, structureTrace.AutoRouteCandidates),
		SearchPath:          append([]string(nil), structureTrace.SearchPath...),
		MatchedSections:     append([]string(nil), structureTrace.MatchedSections...),
		ExpandedSections:    append([]string(nil), structureTrace.ExpandedSections...),
		ExpandedChunkIDs:    append([]string(nil), structureTrace.ExpandedChunkIDs...),
		SelectedChunkIDs:    collectChunkIDs(out),
		Fusion:              fusion,
		Graph:               graphTrace.Graph,
	}, nil
}

func findSectionNode(docTree *tree.DocumentTree, path []string) *tree.Node {
	if docTree == nil || docTree.Root == nil || len(path) == 0 {
		return nil
	}
	node := docTree.Root
	for _, segment := range path {
		var next *tree.Node
		for _, child := range node.Children {
			if child.Content != "" {
				continue
			}
			if strings.EqualFold(child.Heading, segment) {
				next = child
				break
			}
		}
		if next == nil {
			return nil
		}
		node = next
	}
	return node
}

func expandSectionLeaves(section *tree.Node, chunks []store.StoredChunk, depth int) []store.Hit {
	if section == nil {
		return nil
	}
	chunkByID := make(map[string]store.StoredChunk, len(chunks))
	for _, chunk := range chunks {
		chunkByID[chunk.ID] = chunk
	}
	maxDepth := depth
	if maxDepth <= 0 {
		maxDepth = 8
	}
	var hits []store.Hit
	var walk func(*tree.Node, int)
	walk = func(node *tree.Node, currentDepth int) {
		if node == nil || currentDepth > maxDepth {
			return
		}
		if node.Content != "" {
			chunk, ok := chunkByID[node.ID]
			if !ok {
				return
			}
			score := 1.5 + float64(maxDepth-currentDepth)*0.1
			if len(chunk.SectionPath) > 0 && len(node.Path) > 0 && pathHasPrefix(chunk.SectionPath, node.Path) {
				score += 0.5
			}
			hits = append(hits, store.Hit{
				Chunk: chunk,
				Score: score,
			})
			return
		}
		for _, child := range node.Children {
			walk(child, currentDepth+1)
		}
	}
	for _, child := range section.Children {
		walk(child, 1)
	}
	return hits
}

func expandedChunkIDs(merged map[string]store.Hit, directHits []store.Hit) []string {
	direct := make(map[string]struct{}, len(directHits))
	for _, hit := range directHits {
		direct[hit.Chunk.ID] = struct{}{}
	}
	out := make([]string, 0, len(merged))
	for id := range merged {
		if _, ok := direct[id]; ok {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func pathHasPrefix(path []string, prefix []string) bool {
	if len(prefix) == 0 || len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if !strings.EqualFold(path[i], prefix[i]) {
			return false
		}
	}
	return true
}

func chunkInRoute(chunk store.StoredChunk, routePath []string) bool {
	if len(routePath) == 0 {
		return true
	}
	return pathHasPrefix(chunk.SectionPath, routePath)
}

func withAutoRoute(req Request, st store.Store) (Request, []RouteCandidate) {
	if len(req.RoutePath) > 0 || !req.EnableAutoRoute || st == nil {
		return req, routeCandidatesFromPath(req.Query, req.RoutePath)
	}
	candidates := proposeRouteCandidates(req, st)
	if len(candidates) == 0 {
		return req, nil
	}
	req.RoutePath = append([]string(nil), candidates[0].Path...)
	return req, candidates
}

func proposeRouteCandidates(req Request, st store.Store) []RouteCandidate {
	chunks, err := st.List(context.Background(), req.Namespace, store.Filter(req.Filters), store.Filter(req.SecurityFilters))
	if err != nil {
		return nil
	}
	tokens := tokenize(req.Query)
	if len(tokens) == 0 {
		return nil
	}
	candidates := make(map[string]RouteCandidate)
	seen := make(map[string]struct{})
	for _, chunk := range chunks {
		if len(chunk.SectionPath) == 0 {
			continue
		}
		key := strings.Join(chunk.SectionPath, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		score, signals := routeScore(tokens, chunk.SectionPath, chunk.Heading, chunk.Title)
		if score <= 0 {
			continue
		}
		candidates[key] = RouteCandidate{
			Path:    append([]string(nil), chunk.SectionPath...),
			Score:   score,
			Queries: []string{req.Query},
			Signals: append([]string(nil), signals...),
		}
	}
	threshold := req.AutoRouteMinScore
	if threshold <= 0 {
		threshold = 2
	}
	out := make([]RouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Score < threshold {
			continue
		}
		out = append(out, candidate)
	}
	sortRouteCandidates(out)
	limit := req.AutoRouteMaxCandidates
	if limit <= 0 {
		limit = 3
	}
	if len(out) > limit {
		out = out[:limit]
	}
	normalizeRouteCandidateConfidence(out)
	return out
}

func routeScore(tokens []string, path []string, heading string, title string) (float64, []string) {
	searchSpace := append([]string(nil), path...)
	if heading != "" {
		searchSpace = append(searchSpace, heading)
	}
	if title != "" {
		searchSpace = append(searchSpace, title)
	}
	var score float64
	var signals []string
	for _, candidate := range searchSpace {
		lc := strings.ToLower(candidate)
		matchedTokens := make([]string, 0, len(tokens))
		for _, tok := range tokens {
			if strings.Contains(lc, tok) {
				score += 2
				matchedTokens = append(matchedTokens, tok)
			}
		}
		if len(matchedTokens) > 0 {
			signals = append(signals, candidate+": "+strings.Join(matchedTokens, ","))
		}
	}
	if score > 0 {
		score += float64(len(path)) * 0.2
	}
	return score, signals
}

func routeCandidatesFromPath(query string, path []string) []RouteCandidate {
	if len(path) == 0 {
		return nil
	}
	return []RouteCandidate{{
		Path:       append([]string(nil), path...),
		Score:      0,
		Confidence: 1,
		Queries:    []string{query},
		Signals:    []string{"explicit_route_path"},
	}}
}

func mergeRouteCandidates(dst []RouteCandidate, src []RouteCandidate) []RouteCandidate {
	type mergedCandidate struct {
		path    []string
		score   float64
		queries []string
		signals []string
	}
	merged := make(map[string]mergedCandidate, len(dst)+len(src))
	for _, candidate := range dst {
		key := strings.Join(candidate.Path, "\x00")
		merged[key] = mergedCandidate{
			path:    append([]string(nil), candidate.Path...),
			score:   candidate.Score,
			queries: append([]string(nil), candidate.Queries...),
			signals: append([]string(nil), candidate.Signals...),
		}
	}
	for _, candidate := range src {
		key := strings.Join(candidate.Path, "\x00")
		entry, ok := merged[key]
		if !ok {
			merged[key] = mergedCandidate{
				path:    append([]string(nil), candidate.Path...),
				score:   candidate.Score,
				queries: append([]string(nil), candidate.Queries...),
				signals: append([]string(nil), candidate.Signals...),
			}
			continue
		}
		entry.score += candidate.Score
		entry.queries = appendUniqueStrings(entry.queries, candidate.Queries...)
		entry.signals = appendUniqueStrings(entry.signals, candidate.Signals...)
		merged[key] = entry
	}
	out := make([]RouteCandidate, 0, len(merged))
	for _, candidate := range merged {
		out = append(out, RouteCandidate{
			Path:    candidate.path,
			Score:   candidate.score,
			Queries: candidate.queries,
			Signals: candidate.signals,
		})
	}
	sortRouteCandidates(out)
	normalizeRouteCandidateConfidence(out)
	return out
}

func sortRouteCandidates(candidates []RouteCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return len(candidates[i].Path) > len(candidates[j].Path)
		}
		return candidates[i].Score > candidates[j].Score
	})
}

func cloneRouteCandidates(src []RouteCandidate) []RouteCandidate {
	if len(src) == 0 {
		return nil
	}
	out := make([]RouteCandidate, 0, len(src))
	for _, candidate := range src {
		out = append(out, RouteCandidate{
			Path:       append([]string(nil), candidate.Path...),
			Score:      candidate.Score,
			Confidence: candidate.Confidence,
			Queries:    append([]string(nil), candidate.Queries...),
			Signals:    append([]string(nil), candidate.Signals...),
			Selected:   candidate.Selected,
			Reason:     candidate.Reason,
		})
	}
	return out
}

func routeTraceCandidates(req Request, candidates []RouteCandidate) []RouteCandidate {
	if len(candidates) > 0 {
		return cloneRouteCandidates(candidates)
	}
	return routeCandidatesFromPath(req.Query, req.RoutePath)
}

func normalizeRouteCandidateConfidence(candidates []RouteCandidate) {
	if len(candidates) == 0 {
		return
	}
	maxScore := candidates[0].Score
	if maxScore <= 0 {
		for i := range candidates {
			candidates[i].Confidence = 0
		}
		return
	}
	for i := range candidates {
		candidates[i].Confidence = candidates[i].Score / maxScore
	}
}

func effectiveThreshold(threshold float64) float64 {
	if threshold <= 0 {
		return 0.6
	}
	return threshold
}

func markSelectedCandidates(candidates []RouteCandidate, fanout int) {
	for i := range candidates {
		if i < fanout {
			candidates[i].Selected = true
			if candidates[i].Reason == "" {
				candidates[i].Reason = "selected for route execution"
			}
		}
	}
}

func formatFloat(v float64) string {
	return strings.TrimRight(strings.TrimRight(sprintfFloat(v), "0"), ".")
}

func sprintfFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

func collectChunkIDs(hits []store.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.Chunk.ID)
	}
	return out
}

func singleTrajectoryStep(req Request, trace Trace, hits []store.Hit) TrajectoryStep {
	route := append([]string(nil), req.RoutePath...)
	if len(route) == 0 {
		route = append([]string(nil), trace.AutoRoutePath...)
	}
	confidence := 0.0
	if len(trace.AutoRouteCandidates) > 0 {
		confidence = trace.AutoRouteCandidates[0].Confidence
	}
	return TrajectoryStep{
		Route:            route,
		Confidence:       confidence,
		Mode:             "single",
		HitCount:         len(hits),
		HitIDs:           collectChunkIDs(hits),
		MatchedSections:  append([]string(nil), trace.MatchedSections...),
		ExpandedSections: append([]string(nil), trace.ExpandedSections...),
		Rationale:        "single route execution",
	}
}

func structureScore(tokens []string, chunk store.StoredChunk) (float64, string) {
	if len(tokens) == 0 {
		return 0, ""
	}
	searchSpace := append([]string(nil), chunk.SectionPath...)
	if chunk.Heading != "" {
		searchSpace = append(searchSpace, chunk.Heading)
	}
	if chunk.Title != "" {
		searchSpace = append(searchSpace, chunk.Title)
	}
	var score float64
	var matched string
	for _, candidate := range searchSpace {
		lc := strings.ToLower(candidate)
		for _, tok := range tokens {
			if strings.Contains(lc, tok) {
				score += 2
				if matched == "" {
					matched = candidate
				}
			}
		}
	}
	if score == 0 {
		return lexicalScore(tokens, chunk.Content) * 0.5, matched
	}
	if chunk.Heading != "" {
		heading := strings.ToLower(chunk.Heading)
		for _, tok := range tokens {
			if strings.Contains(heading, tok) {
				score += 1.5
			}
		}
	}
	if len(chunk.SectionPath) > 0 {
		score += float64(len(chunk.SectionPath)) * 0.1
	}
	return score, matched
}

func uniqueQueries(query string) []string {
	return appendUniqueQueries(nil, query)
}

func appendUniqueQueries(dst []string, queries ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(queries))
	out := make([]string, 0, len(dst)+len(queries))
	for _, query := range dst {
		trimmed := strings.TrimSpace(query)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	for _, query := range queries {
		trimmed := strings.TrimSpace(query)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func appendUniqueStrings(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	out := make([]string, 0, len(dst)+len(values))
	for _, value := range dst {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func tokenize(text string) []string {
	fields := strings.Fields(strings.ToLower(text))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, ".,;:!?()[]{}\"'")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func lexicalScore(tokens []string, content string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	contentTokens := tokenize(content)
	if len(contentTokens) == 0 {
		return 0
	}
	counts := make(map[string]int, len(contentTokens))
	for _, tok := range contentTokens {
		counts[tok]++
	}
	var score float64
	for _, tok := range tokens {
		if counts[tok] > 0 {
			score += 1
		}
	}
	return score
}
