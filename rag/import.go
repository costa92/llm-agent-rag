package rag

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/guard"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/store"
)

// Import ingests a batch of documents — splitting, embedding, optional PII
// redaction and graph extraction, and storing the resulting chunks.
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
	redactionCounts := map[string]int{}
	var graphEnts []graph.Entity
	var graphRels []graph.Relation
	graphStore, isGraphStore := s.store.(store.GraphStore)
	persistGraph := s.entityExtractor != nil && isGraphStore
	var staleGraphChunkIDs []string
	importStart := time.Now()
	embedStart := time.Now()
	for _, doc := range docs {
		// Redact PII before splitting so chunks, vectors, and the store
		// never see raw PII. A nil redactor leaves content untouched.
		if s.redactor != nil {
			rr := s.redactor.Redact(doc.Content)
			doc.Content = rr.Text
			for _, red := range rr.Redactions {
				redactionCounts[red.Kind] += red.Count
			}
		}
		if opts.ReplaceSource && doc.SourceID != "" {
			if persistGraph {
				// Capture this source's prior chunk IDs so the graph can be
				// reconciled — its stale contributions removed — before the
				// re-extracted subgraph is merged in.
				old, err := s.store.List(ctx, opts.Namespace, store.Filter{
					ingest.MetadataSourceIDKey: doc.SourceID,
				}, nil)
				if err != nil {
					return ingest.ImportResult{}, fmt.Errorf("rag: list existing source %s: %w", doc.SourceID, err)
				}
				for _, c := range old {
					staleGraphChunkIDs = append(staleGraphChunkIDs, c.ID)
				}
			}
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
			// Extract the knowledge graph post-split. A nil extractor
			// leaves the graph unbuilt and Import behaves as before.
			if s.entityExtractor != nil {
				ents, rels, err := s.entityExtractor.Extract(ctx, chunk.ID, chunk.Content)
				if err != nil {
					return ingest.ImportResult{}, fmt.Errorf("rag: extract graph from chunk %s: %w", chunk.ID, err)
				}
				graphEnts = append(graphEnts, ents...)
				graphRels = append(graphRels, rels...)
			}
		}
	}
	embedDuration := time.Since(embedStart)
	upsertStart := time.Now()
	if err := s.store.Upsert(ctx, chunks); err != nil {
		return ingest.ImportResult{}, fmt.Errorf("rag: upsert: %w", err)
	}
	upsertDuration := time.Since(upsertStart)
	metrics := obs.Metrics{
		TotalDuration: time.Since(importStart),
		Stages: []obs.StageTiming{
			{Stage: "embed", Duration: embedDuration},
			{Stage: "upsert", Duration: upsertDuration},
		},
		Calls: obs.CallCounts{Embed: embedCount},
	}
	res.Metrics = metrics
	res.Redactions = redactionSummary(redactionCounts)
	if s.entityExtractor != nil {
		// Fuzzy entity resolution runs as an opt-in pre-pass before
		// Canonicalize's exact-match merge. It rewrites near-duplicate
		// entity names — and the relation endpoints that reference them —
		// to a shared canonical surface form. With the NoopEntityResolver
		// default this is a no-op and the graph is byte-identical.
		resolvedEnts, resolvedRels, err := s.entityResolver.Resolve(ctx, graphEnts, graphRels)
		if err != nil {
			return ingest.ImportResult{}, fmt.Errorf("rag: resolve entities: %w", err)
		}
		g := graph.Canonicalize(resolvedEnts, resolvedRels)
		res.Graph = &g
	}
	// Persist the graph when the store implements store.GraphStore. On a
	// ReplaceSource re-ingest, reconcile first (drop the stale
	// contributions) then union-merge the re-extracted subgraph. A store
	// that is not a GraphStore degrades gracefully — res.Graph is still
	// returned, just not persisted.
	if persistGraph {
		if len(staleGraphChunkIDs) > 0 {
			if err := graphStore.RemoveGraphBySource(ctx, opts.Namespace, staleGraphChunkIDs); err != nil {
				return ingest.ImportResult{}, fmt.Errorf("rag: reconcile graph: %w", err)
			}
		}
		if res.Graph != nil {
			if err := graphStore.UpsertGraph(ctx, opts.Namespace, *res.Graph); err != nil {
				return ingest.ImportResult{}, fmt.Errorf("rag: persist graph: %w", err)
			}
		}
	}
	// Detect communities once the namespace graph is fully persisted. Reading
	// the snapshot *after* UpsertGraph (and after RemoveGraphBySource on a
	// ReplaceSource re-ingest) means a re-ingest re-detects the whole
	// namespace automatically — replace-all UpsertCommunities reconciles
	// (KG3-7). A store that is not a CommunityStore, or no detector
	// configured, degrades gracefully: no detection, no error.
	if cs, ok := s.store.(store.CommunityStore); ok && s.communityDetector != nil {
		snap, err := cs.GraphSnapshot(ctx, opts.Namespace)
		if err != nil {
			return ingest.ImportResult{}, fmt.Errorf("rag: detect communities: %w", err)
		}
		communities, err := s.communityDetector.Detect(ctx, snap)
		if err != nil {
			return ingest.ImportResult{}, fmt.Errorf("rag: detect communities: %w", err)
		}
		if err := cs.UpsertCommunities(ctx, opts.Namespace, communities); err != nil {
			return ingest.ImportResult{}, fmt.Errorf("rag: detect communities: %w", err)
		}
		if res.Graph != nil {
			res.Graph.Communities = communities
		}
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
			Metrics:       metrics,
			Redactions:    append([]guard.Redaction(nil), res.Redactions...),
		})
	}
	return res, nil
}

// ImportFrom reads documents from src and ingests them, the same pipeline as
// Import but sourced from an ingest.Source rather than an in-memory slice.
func (s *System) ImportFrom(ctx context.Context, src ingest.Source, opts ingest.ImportOptions) (ingest.ImportResult, error) {
	docs, err := src.Documents(ctx)
	if err != nil {
		return ingest.ImportResult{}, err
	}
	return s.Import(ctx, docs, opts)
}

// redactionSummary turns per-kind redaction counts into a deterministic
// (kind-sorted) slice for ImportResult/ImportTrace.
func redactionSummary(counts map[string]int) []guard.Redaction {
	if len(counts) == 0 {
		return nil
	}
	out := make([]guard.Redaction, 0, len(counts))
	for kind, n := range counts {
		out = append(out, guard.Redaction{Kind: kind, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
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
