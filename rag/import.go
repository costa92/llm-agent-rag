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
	embedCount := 0
	removedChunks := 0
	for _, doc := range docs {
		if opts.ReplaceSource && doc.SourceID != "" {
			removed, err := s.store.RemoveByFilter(ctx, opts.Namespace, store.Filter{
				ingest.MetadataSourceIDKey: doc.SourceID,
			})
			if err != nil {
				return ingest.ImportResult{}, fmt.Errorf("rag: remove existing source %s: %w", doc.SourceID, err)
			}
			removedChunks += removed
		}
		docChunks := splitter.Split(doc, maxChars)
		res.Documents++
		res.Chunks += len(docChunks)
		for _, chunk := range docChunks {
			vec, err := s.embedder.Embed(ctx, chunk.Content)
			if err != nil {
				return ingest.ImportResult{}, fmt.Errorf("rag: embed chunk %s: %w", chunk.ID, err)
			}
			embedCount++
			chunks = append(chunks, store.StoredChunk{
				ID:           chunk.ID,
				Namespace:    opts.Namespace,
				DocID:        chunk.DocID,
				Title:        chunk.Title,
				SectionID:    buildSectionID(opts.Namespace, chunk),
				SectionPath:  metadataStringSlice(chunk.Metadata, ingest.MetadataSectionPathKey),
				Heading:      metadataString(chunk.Metadata, ingest.MetadataHeadingKey),
				HeadingLevel: metadataInt(chunk.Metadata, ingest.MetadataHeadingLevelKey),
				Content:      chunk.Content,
				Vector:       vec,
				Metadata:     chunk.Metadata,
			})
			res.ChunkIDs = append(res.ChunkIDs, chunk.ID)
		}
	}
	if err := s.store.Upsert(ctx, chunks); err != nil {
		return ingest.ImportResult{}, fmt.Errorf("rag: upsert: %w", err)
	}
	if s.observer.OnImport != nil {
		s.observer.OnImport(ctx, ImportTrace{
			Namespace:     opts.Namespace,
			Documents:     res.Documents,
			Chunks:        res.Chunks,
			ChunkIDs:      append([]string(nil), res.ChunkIDs...),
			EmbedCount:    embedCount,
			ReplaceSource: opts.ReplaceSource,
			RemovedChunks: removedChunks,
		})
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

func buildSectionID(namespace string, chunk ingest.Chunk) string {
	if path := metadataStringSlice(chunk.Metadata, ingest.MetadataSectionPathKey); len(path) > 0 {
		return namespace + ":" + chunk.DocID + ":" + path[len(path)-1]
	}
	if heading := metadataString(chunk.Metadata, ingest.MetadataHeadingKey); heading != "" {
		return namespace + ":" + chunk.DocID + ":" + heading
	}
	return ""
}

func metadataString(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, _ := metadata[key].(string)
	return value
}

func metadataInt(metadata map[string]any, key string) int {
	if len(metadata) == 0 {
		return 0
	}
	switch value := metadata[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func metadataStringSlice(metadata map[string]any, key string) []string {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata[key]
	if !ok {
		return nil
	}
	switch value := raw.(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
