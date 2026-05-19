package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/ingest"
)

// resolveScriptedEmbedder is a deterministic embed.Embedder for Import
// resolver tests: it returns a fixed vector per known text and a zero
// vector otherwise. The chunk-embedding path tolerates the zero vector;
// only the entity-name texts the resolver embeds need fixed vectors.
type resolveScriptedEmbedder struct {
	dim     int
	vectors map[string]embed.Vector
}

func (r resolveScriptedEmbedder) Dimension() int { return r.dim }

func (r resolveScriptedEmbedder) Embed(_ context.Context, text string) (embed.Vector, error) {
	if v, ok := r.vectors[text]; ok {
		return v, nil
	}
	return make(embed.Vector, r.dim), nil
}

func TestImportWithEntityResolverMergesNearDuplicates(t *testing.T) {
	emb := resolveScriptedEmbedder{dim: 2, vectors: map[string]embed.Vector{
		"Acme":      {1, 0},
		"Acme Corp": {0.999, 0.0447}, // cosine ~= 0.999 vs Acme
		"Globex":    {0, 1},
	}}
	sys := New(Options{
		Model:    fakeModel{},
		Embedder: emb,
		EntityExtractor: graph.DictionaryEntityExtractor{Terms: map[string]string{
			"Acme":      "org",
			"Acme Corp": "org",
			"Globex":    "org",
		}},
		EntityResolver: graph.EmbeddingEntityResolver{Embedder: emb},
	})
	res, err := sys.Import(context.Background(), []ingest.Document{
		// "Acme Corp" also matches the "Acme" gazetteer term, so the
		// extractor yields both surface forms — exactly the near-duplicate
		// case the resolver collapses.
		{ID: "doc1", Content: "Acme Corp competes with Globex."},
	}, ingest.ImportOptions{Namespace: "biz"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Graph == nil {
		t.Fatalf("ImportResult.Graph is nil")
	}
	// After resolution + Canonicalize there is exactly one org node for the
	// Acme variants, plus Globex.
	var acmeNodes int
	var acmeID string
	for _, e := range res.Graph.Entities {
		if e.Type == "org" && (e.Name == "Acme" || e.Name == "Acme Corp") {
			acmeNodes++
			acmeID = e.ID
		}
	}
	if acmeNodes != 1 {
		t.Fatalf("Acme nodes = %d, want 1 canonical node: %+v", acmeNodes, res.Graph.Entities)
	}
	if acmeID != "org:acme corp" {
		t.Fatalf("canonical Acme id = %q, want \"org:acme corp\" (longest surface form)", acmeID)
	}
	// Provenance is unioned onto the canonical node.
	for _, e := range res.Graph.Entities {
		if e.ID == acmeID && len(e.SourceChunkIDs) < 1 {
			t.Fatalf("canonical Acme provenance = %v, want >= 1 chunk", e.SourceChunkIDs)
		}
	}
	// The co-occurs relation survives — its endpoint followed the rewrite,
	// so it is not orphaned by Canonicalize.
	if len(res.Graph.Relations) == 0 {
		t.Fatalf("graph has no relations, want the Acme<->Globex relation to survive resolution")
	}
	var found bool
	for _, rel := range res.Graph.Relations {
		if (rel.Source == acmeID || rel.Target == acmeID) && rel.Relation == "co-occurs" {
			found = true
		}
	}
	if !found {
		t.Fatalf("relations = %+v, want a co-occurs edge touching the canonical Acme node", res.Graph.Relations)
	}
}

func TestImportDefaultResolverUnchanged(t *testing.T) {
	// With no EntityResolver configured the default is NoopEntityResolver
	// and Import is byte-identical to pre-fuzzy-resolution behavior: the
	// two Acme surface forms stay two distinct nodes.
	sys := New(Options{
		Model: fakeModel{},
		EntityExtractor: graph.DictionaryEntityExtractor{Terms: map[string]string{
			"Acme":      "org",
			"Acme Corp": "org",
			"Globex":    "org",
		}},
	})
	res, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Acme Corp competes with Globex."},
	}, ingest.ImportOptions{Namespace: "biz"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Graph == nil {
		t.Fatalf("ImportResult.Graph is nil")
	}
	got := map[string]bool{}
	for _, e := range res.Graph.Entities {
		got[e.ID] = true
	}
	for _, id := range []string{"org:acme", "org:acme corp", "org:globex"} {
		if !got[id] {
			t.Fatalf("default import missing entity %q: %+v", id, res.Graph.Entities)
		}
	}
}
