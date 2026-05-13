package rag

import (
	"context"
	"fmt"

	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/store"
)

func (s *System) Import(ctx context.Context, docs []ingest.Document, opts ingest.ImportOptions) (ingest.ImportResult, error) {
	splitter := opts.Splitter
	if splitter == nil {
		splitter = s.splitter
	}
	maxChars := opts.MaxChars
	if maxChars <= 0 {
		maxChars = s.maxChars
	}
	var chunks []store.StoredChunk
	var res ingest.ImportResult
	for _, doc := range docs {
		docChunks := splitter.Split(doc, maxChars)
		res.Documents++
		res.Chunks += len(docChunks)
		for _, chunk := range docChunks {
			vec, err := s.embedder.Embed(ctx, chunk.Content)
			if err != nil {
				return ingest.ImportResult{}, fmt.Errorf("rag: embed chunk %s: %w", chunk.ID, err)
			}
			chunks = append(chunks, store.StoredChunk{
				ID:        chunk.ID,
				Namespace: opts.Namespace,
				DocID:     chunk.DocID,
				Title:     chunk.Title,
				Content:   chunk.Content,
				Vector:    vec,
				Metadata:  chunk.Metadata,
			})
			res.ChunkIDs = append(res.ChunkIDs, chunk.ID)
		}
	}
	if err := s.store.Upsert(ctx, chunks); err != nil {
		return ingest.ImportResult{}, fmt.Errorf("rag: upsert: %w", err)
	}
	return res, nil
}

func (s *System) ImportFrom(ctx context.Context, src ingest.Source, opts ingest.ImportOptions) (ingest.ImportResult, error) {
	docs, err := src.Documents(ctx)
	if err != nil {
		return ingest.ImportResult{}, err
	}
	return s.Import(ctx, docs, opts)
}
