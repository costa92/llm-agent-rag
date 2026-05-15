package rag

import (
	"context"

	"github.com/costa92/llm-agent-rag/pack"
	"github.com/costa92/llm-agent-rag/prompt"
	"github.com/costa92/llm-agent-rag/rerank"
	retrievepolicy "github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

func (s *System) Ask(ctx context.Context, question string, opts AskOptions) (Answer, error) {
	if s.model == nil {
		return Answer{}, ErrModelRequired
	}
	hits, retrieveTrace, err := s.retrieve(ctx, question, opts.Search)
	if err != nil {
		return Answer{}, err
	}
	tpl := opts.Template
	if tpl == nil {
		tpl = s.template
	}
	rankedHits := hits
	rerankedIDs := chunkIDs(rankedHits)
	if opts.Search.EnableRerank && s.reranker != nil {
		var rerankTrace rerank.Trace
		rankedHits, rerankTrace, err = s.reranker.Rerank(ctx, rerank.Request{
			Query: question,
			Hits:  hits,
		})
		if err != nil {
			return Answer{}, err
		}
		rerankedIDs = append([]string(nil), rerankTrace.OutputChunkIDs...)
	}
	packedHits := rankedHits
	packedIDs := chunkIDs(packedHits)
	var droppedIDs []string
	matchedSections := traceOrFallbackSections(retrieveTrace, rankedHits)
	searchPath := traceOrFallbackSearchPath(retrieveTrace, rankedHits)
	expandedSections := append([]string(nil), retrieveTrace.ExpandedSections...)
	expandedChunkIDs := append([]string(nil), retrieveTrace.ExpandedChunkIDs...)
	if s.packer != nil {
		packed, err := s.packer.Pack(ctx, pack.Request{
			Question:  question,
			Hits:      rankedHits,
			MaxTokens: opts.MaxTokens,
		})
		if err != nil {
			return Answer{}, err
		}
		packedHits = packed.Hits
		packedIDs = append([]string(nil), packed.Trace.SelectedChunkIDs...)
		droppedIDs = append([]string(nil), packed.Trace.DroppedChunkIDs...)
	}
	req, err := tpl.Render(ctx, prompt.RenderContext{
		Question:  question,
		Namespace: opts.Search.Namespace,
		Hits:      packedHits,
		Metadata:  opts.Metadata,
	})
	if err != nil {
		return Answer{}, err
	}
	resp, err := s.model.Generate(ctx, req)
	if err != nil {
		return Answer{}, err
	}
	ids := make([]string, 0, len(packedHits))
	citations := make([]Citation, 0, len(packedHits))
	for _, hit := range packedHits {
		ids = append(ids, hit.Chunk.ID)
		citations = append(citations, Citation{
			ChunkID:     hit.Chunk.ID,
			DocID:       hit.Chunk.DocID,
			Namespace:   hit.Chunk.Namespace,
			Title:       hit.Chunk.Title,
			SectionID:   hit.Chunk.SectionID,
			SectionPath: append([]string(nil), hit.Chunk.SectionPath...),
			Score:       hit.Score,
		})
	}
	answer := Answer{
		Text:      resp.Text,
		Hits:      packedHits,
		Prompt:    req,
		Citations: citations,
		Diagnostics: Diagnostics{
			HitCount:            len(hits),
			ReturnedChunkIDs:    append([]string(nil), chunkIDs(hits)...),
			PromptChunkIDs:      append([]string(nil), ids...),
			MatchedSections:     append([]string(nil), matchedSections...),
			ExpandedChunkIDs:    append([]string(nil), expandedChunkIDs...),
			AutoRouteCandidates: cloneAskRouteCandidates(retrieveTrace.AutoRouteCandidates),
			RoutePolicy:         retrieveTrace.RoutePolicy,
			SearchTrajectory:    cloneTrajectory(retrieveTrace.SearchTrajectory),
		},
		Trace: Trace{
			Question:            question,
			Namespace:           opts.Search.Namespace,
			TopK:                opts.Search.TopK,
			Filters:             copyMap(opts.Search.Filters),
			SecurityFilters:     copyMap(opts.Search.SecurityFilters),
			RoutePath:           append([]string(nil), retrieveTrace.RoutePath...),
			AutoRoutePath:       append([]string(nil), retrieveTrace.AutoRoutePath...),
			AutoRouteCandidates: cloneAskRouteCandidates(retrieveTrace.AutoRouteCandidates),
			RoutePolicy:         retrieveTrace.RoutePolicy,
			SearchPath:          append([]string(nil), searchPath...),
			MatchedSections:     append([]string(nil), matchedSections...),
			ExpandedSections:    append([]string(nil), expandedSections...),
			ExpandedChunkIDs:    append([]string(nil), expandedChunkIDs...),
			RerankedChunkIDs:    append([]string(nil), rerankedIDs...),
			PackedChunkIDs:      append([]string(nil), packedIDs...),
			DroppedChunkIDs:     append([]string(nil), droppedIDs...),
			SelectedChunkIDs:    append([]string(nil), ids...),
			SearchTrajectory:    cloneTrajectory(retrieveTrace.SearchTrajectory),
		},
	}
	if s.observer.OnAsk != nil {
		s.observer.OnAsk(ctx, answer.Trace)
	}
	return answer, nil
}

func copyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func chunkIDs(hits []store.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.Chunk.ID)
	}
	return out
}

func sectionIDs(hits []store.Hit) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		if hit.Chunk.SectionID == "" {
			continue
		}
		if _, ok := seen[hit.Chunk.SectionID]; ok {
			continue
		}
		seen[hit.Chunk.SectionID] = struct{}{}
		out = append(out, hit.Chunk.SectionID)
	}
	return out
}

func sectionPathTrail(hits []store.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		for _, path := range hit.Chunk.SectionPath {
			out = append(out, path)
		}
	}
	return out
}

func traceOrFallbackSections(trace retrievepolicy.Trace, hits []store.Hit) []string {
	if len(trace.MatchedSections) > 0 {
		return append([]string(nil), trace.MatchedSections...)
	}
	return sectionIDs(hits)
}

func traceOrFallbackSearchPath(trace retrievepolicy.Trace, hits []store.Hit) []string {
	if len(trace.SearchPath) > 0 {
		return append([]string(nil), trace.SearchPath...)
	}
	return sectionPathTrail(hits)
}

func cloneAskRouteCandidates(src []retrievepolicy.RouteCandidate) []retrievepolicy.RouteCandidate {
	if len(src) == 0 {
		return nil
	}
	out := make([]retrievepolicy.RouteCandidate, 0, len(src))
	for _, candidate := range src {
		out = append(out, retrievepolicy.RouteCandidate{
			Path:    append([]string(nil), candidate.Path...),
			Score:   candidate.Score,
			Queries: append([]string(nil), candidate.Queries...),
		})
	}
	return out
}

func cloneTrajectory(src []retrievepolicy.TrajectoryStep) []retrievepolicy.TrajectoryStep {
	if len(src) == 0 {
		return nil
	}
	out := make([]retrievepolicy.TrajectoryStep, 0, len(src))
	for _, step := range src {
		out = append(out, retrievepolicy.TrajectoryStep{
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
	return out
}
