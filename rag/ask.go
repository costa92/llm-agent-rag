package rag

import (
	"context"

	"github.com/costa92/llm-agent-rag/prompt"
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
	req, err := tpl.Render(ctx, prompt.RenderContext{
		Question:  question,
		Namespace: opts.Search.Namespace,
		Hits:      hits,
		Metadata:  opts.Metadata,
	})
	if err != nil {
		return Answer{}, err
	}
	resp, err := s.model.Generate(ctx, req)
	if err != nil {
		return Answer{}, err
	}
	ids := make([]string, 0, len(hits))
	citations := make([]Citation, 0, len(hits))
	for _, hit := range hits {
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
		Hits:      hits,
		Prompt:    req,
		Citations: citations,
		Diagnostics: Diagnostics{
			HitCount:         len(hits),
			ReturnedChunkIDs: append([]string(nil), ids...),
		},
		Trace: Trace{
			Question:         question,
			Namespace:        opts.Search.Namespace,
			TopK:             opts.Search.TopK,
			Filters:          copyMap(opts.Search.Filters),
			SecurityFilters:  copyMap(opts.Search.SecurityFilters),
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
