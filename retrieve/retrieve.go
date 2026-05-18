package retrieve

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/costa92/llm-agent-rag/advanced"
	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/store"
	"github.com/costa92/llm-agent-rag/tree"
)

type Request struct {
	Query                        string
	Namespace                    string
	TopK                         int
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
	EnableStructure              bool
	EnableTreeExpansion          bool
	ExpansionDepth               int
	QueryVariants                []string
}

type PreprocessResult struct {
	QueryVariants []string
	Trace         Trace
}

type RouteCandidate struct {
	Path       []string
	Score      float64
	Confidence float64
	Queries    []string
	Signals    []string
	Selected   bool
	Reason     string
}

type RoutePolicyTrace struct {
	Mode                string
	ConfidenceThreshold float64
	ConfidenceGap       float64
	Gap                 float64
	Fanout              int
	CandidateCount      int
	SelectedCount       int
	Rationale           []string
}

type TrajectoryStep struct {
	Route            []string
	Confidence       float64
	Mode             string
	HitCount         int
	HitIDs           []string
	MatchedSections  []string
	ExpandedSections []string
	Rationale        string
}

type SectionPlannerDecision struct {
	Selected  []RouteCandidate
	Mode      string
	Fanout    int
	Gap       float64
	Rationale []string
}

type SectionPlanner interface {
	Plan(ctx context.Context, req Request, candidates []RouteCandidate) (SectionPlannerDecision, error)
}

type GapAwareSectionPlanner struct{}

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

type Trace struct {
	OriginalQuery       string
	EffectiveQuery      string
	QueryVariants       []string
	RoutePath           []string
	AutoRoutePath       []string
	AutoRouteCandidates []RouteCandidate
	RoutePolicy         RoutePolicyTrace
	SearchPath          []string
	MatchedSections     []string
	ExpandedSections    []string
	ExpandedChunkIDs    []string
	SelectedChunkIDs    []string
	SearchTrajectory    []TrajectoryStep
	Fusion              []FusionAttribution
	Metrics             obs.Metrics
	Hops                []HopAttribution
}

// FusionAttribution records how each retrieval signal ranked one chunk during
// reciprocal rank fusion. A rank of 0 means the signal did not return the
// chunk. RRFScore is the chunk's summed RRF contribution across all signals.
type FusionAttribution struct {
	ChunkID       string
	DenseRank     int
	LexicalRank   int
	StructureRank int
	RRFScore      float64
}

type QueryPreprocessor interface {
	Process(ctx context.Context, req Request) (PreprocessResult, error)
}

type NoopPreprocessor struct{}

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

type LLMExpansionPreprocessor struct {
	Model generate.Model
}

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

type QueryEmbedder interface {
	Embed(ctx context.Context, text string) (embed.Vector, error)
}

type Retriever interface {
	Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error)
}

var ErrBaseRetrieverRequired = errors.New("retrieve: base retriever required")

type VariantRetriever struct {
	Base    Retriever
	Planner SectionPlanner
}

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

type DenseRetriever struct {
	Embedder QueryEmbedder
	Store    store.Store
}

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
	K1 float64
	B  float64
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

type LexicalRetriever struct {
	Store  store.Store
	Params BM25Params
}

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

type StructureRetriever struct {
	Store store.Store
}

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

type HybridRetriever struct {
	Dense     Retriever
	Lexical   Retriever
	Structure Retriever
	// RRFConstant is the k constant in the reciprocal rank fusion formula
	// 1/(k + rank). Zero resolves to the standard default of 60.
	RRFConstant float64
}

func (r HybridRetriever) Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error) {
	denseHits, denseTrace, err := r.Dense.Retrieve(ctx, req)
	if err != nil {
		return nil, Trace{}, err
	}
	lexHits, _, err := r.Lexical.Retrieve(ctx, req)
	if err != nil {
		return nil, Trace{}, err
	}

	var structureHits []store.Hit
	var structureTrace Trace
	if req.EnableStructure && r.Structure != nil {
		structureHits, structureTrace, err = r.Structure.Retrieve(ctx, req)
		if err != nil {
			return nil, Trace{}, err
		}
	}

	fused := make(map[string]store.Hit, len(denseHits)+len(lexHits))
	rrfScores := make(map[string]float64, len(denseHits)+len(lexHits)+len(structureHits))

	k := r.RRFConstant
	if k == 0 {
		k = 60
	}
	denseRank := make(map[string]int, len(denseHits))
	lexRank := make(map[string]int, len(lexHits))
	structRank := make(map[string]int, len(structureHits))
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
