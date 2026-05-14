package rag

import (
	"context"

	"github.com/costa92/llm-agent-rag/pack"
	"github.com/costa92/llm-agent-rag/prompt"
	"github.com/costa92/llm-agent-rag/rerank"
	"github.com/costa92/llm-agent-rag/store"
)

func (s *System) Ask(ctx context.Context, question string, opts AskOptions) (Answer, error) {
	if s.model == nil {
		return Answer{}, ErrModelRequired
	}
	hits, err := s.Retrieve(ctx, question, opts.Search)
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
			ChunkID:   hit.Chunk.ID,
			DocID:     hit.Chunk.DocID,
			Namespace: hit.Chunk.Namespace,
			Title:     hit.Chunk.Title,
			Score:     hit.Score,
		})
	}
	return Answer{
		Text:      resp.Text,
		Hits:      packedHits,
		Prompt:    req,
		Citations: citations,
		Diagnostics: Diagnostics{
			HitCount:         len(hits),
			ReturnedChunkIDs: append([]string(nil), chunkIDs(hits)...),
			PromptChunkIDs:   append([]string(nil), ids...),
		},
		Trace: Trace{
			Question:         question,
			Namespace:        opts.Search.Namespace,
			TopK:             opts.Search.TopK,
			Filters:          copyMap(opts.Search.Filters),
			SecurityFilters:  copyMap(opts.Search.SecurityFilters),
			RerankedChunkIDs: append([]string(nil), rerankedIDs...),
			PackedChunkIDs:   append([]string(nil), packedIDs...),
			DroppedChunkIDs:  append([]string(nil), droppedIDs...),
			SelectedChunkIDs: append([]string(nil), ids...),
		},
	}, nil
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
