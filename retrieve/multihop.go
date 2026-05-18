package retrieve

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/store"
)

// QueryDecomposer splits a (possibly compound) query into sub-queries. A
// non-compound query decomposes to itself.
type QueryDecomposer interface {
	Decompose(ctx context.Context, query string) ([]string, error)
}

// HopAttribution records one sub-query of a multi-hop retrieval and how
// many hits it returned.
type HopAttribution struct {
	SubQuery string
	HitCount int
}

var conjunctionRe = regexp.MustCompile(`(?i)\s+and\s+`)

// HeuristicDecomposer splits a query on the conjunction "and". It uses no
// model and is deterministic. It is best-effort — it may over-split a
// phrase such as "black and white".
type HeuristicDecomposer struct{}

// Decompose splits query on "and"; a query with no usable split decomposes
// to itself.
func (HeuristicDecomposer) Decompose(_ context.Context, query string) ([]string, error) {
	parts := splitConjunctions(query)
	if len(parts) <= 1 {
		return []string{query}, nil
	}
	return parts, nil
}

func splitConjunctions(query string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, p := range conjunctionRe.Split(query, -1) {
		p = strings.TrimSpace(p)
		if len([]rune(p)) < 3 {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

const decomposeSystemPrompt = "You decompose a user's question into " +
	"independent sub-questions. Output one sub-question per line and nothing " +
	"else. If the question is already a single question, output it unchanged."

// LLMDecomposer decomposes a compound query into sub-queries by prompting a
// generate.Model. MaxSubQueries caps the result (<= 0 → 5). A nil Model
// decomposes a query to itself.
type LLMDecomposer struct {
	Model         generate.Model
	MaxSubQueries int
}

// Decompose prompts the model and parses one sub-query per line.
func (d LLMDecomposer) Decompose(ctx context.Context, query string) ([]string, error) {
	if d.Model == nil {
		return []string{query}, nil
	}
	resp, err := d.Model.Generate(ctx, generate.Request{
		SystemPrompt: decomposeSystemPrompt,
		Messages:     []generate.Message{{Role: "user", Content: query}},
	})
	if err != nil {
		return nil, err
	}
	subs := parseSubQueries(resp.Text)
	if len(subs) == 0 {
		return []string{query}, nil
	}
	max := d.MaxSubQueries
	if max <= 0 {
		max = 5
	}
	if len(subs) > max {
		subs = subs[:max]
	}
	return subs, nil
}

func parseSubQueries(text string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimLeft(line, "-*0123456789.) \t"))
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
	}
	return out
}

// MultiHopRetriever decomposes a compound query into sub-queries, retrieves
// for each sub-query through Base, and merges the sub-retrievals. A
// non-compound query decomposes to itself, so it costs a single
// Base.Retrieve call — multi-hop is then a transparent pass-through.
type MultiHopRetriever struct {
	Base       Retriever
	Decomposer QueryDecomposer
}

// Retrieve runs one Base retrieval per sub-query and merges the results
// (dedup by Chunk.ID keeping the max score, then sort by score and
// truncate to req.TopK). The returned Trace records per-hop attribution.
func (m MultiHopRetriever) Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error) {
	if m.Base == nil {
		return nil, Trace{}, ErrBaseRetrieverRequired
	}
	decomposer := m.Decomposer
	if decomposer == nil {
		decomposer = HeuristicDecomposer{}
	}
	subQueries, err := decomposer.Decompose(ctx, req.Query)
	if err != nil {
		return nil, Trace{}, err
	}
	if len(subQueries) == 0 {
		subQueries = []string{req.Query}
	}

	type rankedHit struct {
		hit   store.Hit
		order int
	}
	merged := make(map[string]rankedHit)
	nextOrder := 0
	hops := make([]HopAttribution, 0, len(subQueries))
	trace := Trace{OriginalQuery: req.Query, EffectiveQuery: req.Query}

	for i, sq := range subQueries {
		subReq := req
		subReq.Query = sq
		subReq.QueryVariants = nil
		hits, subTrace, err := m.Base.Retrieve(ctx, subReq)
		if err != nil {
			return nil, Trace{}, err
		}
		hops = append(hops, HopAttribution{SubQuery: sq, HitCount: len(hits)})
		if i == 0 {
			trace.RoutePath = append([]string(nil), subTrace.RoutePath...)
			trace.AutoRoutePath = append([]string(nil), subTrace.AutoRoutePath...)
			trace.RoutePolicy = subTrace.RoutePolicy
		}
		trace.MatchedSections = appendUniqueStrings(trace.MatchedSections, subTrace.MatchedSections...)
		trace.ExpandedSections = appendUniqueStrings(trace.ExpandedSections, subTrace.ExpandedSections...)
		trace.ExpandedChunkIDs = appendUniqueStrings(trace.ExpandedChunkIDs, subTrace.ExpandedChunkIDs...)
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
	for _, rh := range merged {
		out = append(out, rh)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].hit.Score != out[j].hit.Score {
			return out[i].hit.Score > out[j].hit.Score
		}
		return out[i].order < out[j].order
	})
	if req.TopK > 0 && len(out) > req.TopK {
		out = out[:req.TopK]
	}
	hits := make([]store.Hit, 0, len(out))
	ids := make([]string, 0, len(out))
	for _, rh := range out {
		hits = append(hits, rh.hit)
		ids = append(ids, rh.hit.Chunk.ID)
	}
	trace.Hops = hops
	trace.SelectedChunkIDs = ids
	return hits, trace, nil
}
