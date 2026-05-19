package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
)

// scriptedModel is a deterministic generate.Model stub for graph tests.
type scriptedModel struct {
	text string
	err  error
}

func (m scriptedModel) Generate(_ context.Context, _ generate.Request) (generate.Response, error) {
	if m.err != nil {
		return generate.Response{}, m.err
	}
	return generate.Response{Text: m.text}, nil
}

func TestLLMEntityExtractorCleanOutput(t *testing.T) {
	model := scriptedModel{text: "ENTITY | Ada Lovelace | person | first programmer\n" +
		"ENTITY | Analytical Engine | machine | early computer\n" +
		"RELATION | Ada Lovelace | Analytical Engine | wrote algorithm for | her notes"}
	ents, rels, err := LLMEntityExtractor{Model: model}.Extract(context.Background(), "c1", "some text")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(ents) != 2 {
		t.Fatalf("entities = %d, want 2: %+v", len(ents), ents)
	}
	if ents[0].Name != "Ada Lovelace" || ents[0].Type != "person" || ents[0].Description != "first programmer" {
		t.Fatalf("entity[0] = %+v", ents[0])
	}
	if len(ents[0].SourceChunkIDs) != 1 || ents[0].SourceChunkIDs[0] != "c1" {
		t.Fatalf("entity provenance = %v, want [c1]", ents[0].SourceChunkIDs)
	}
	if len(rels) != 1 || rels[0].Source != "Ada Lovelace" ||
		rels[0].Target != "Analytical Engine" || rels[0].Relation != "wrote algorithm for" {
		t.Fatalf("relations = %+v", rels)
	}
	if len(rels[0].SourceChunkIDs) != 1 || rels[0].SourceChunkIDs[0] != "c1" || rels[0].Weight != 1 {
		t.Fatalf("relation provenance/weight = %+v", rels[0])
	}
}

func TestLLMEntityExtractorLenientParsing(t *testing.T) {
	model := scriptedModel{text: "Here is the extraction:\n" +
		"ENTITY | Paris | city | capital of France\n" +
		"garbage line with no pipes\n" +
		"ENTITY | Berlin\n" + // missing type + description — kept, fields default empty
		"RELATION | Paris | Berlin\n" + // too few fields — dropped
		"RELATION | Paris | Berlin | sister city | twinned\n" +
		"done."}
	ents, rels, err := LLMEntityExtractor{Model: model}.Extract(context.Background(), "c9", "x")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(ents) != 2 {
		t.Fatalf("entities = %d, want 2 (Paris, Berlin): %+v", len(ents), ents)
	}
	if ents[1].Name != "Berlin" || ents[1].Type != "" || ents[1].Description != "" {
		t.Fatalf("Berlin entity = %+v, want name Berlin / empty type+desc", ents[1])
	}
	if len(rels) != 1 || rels[0].Relation != "sister city" {
		t.Fatalf("relations = %+v, want one valid sister-city relation", rels)
	}
}

func TestLLMEntityExtractorNilModel(t *testing.T) {
	_, _, err := LLMEntityExtractor{}.Extract(context.Background(), "c1", "x")
	if !errors.Is(err, ErrEntityExtractorModelRequired) {
		t.Fatalf("nil model → %v, want ErrEntityExtractorModelRequired", err)
	}
}

func TestLLMEntityExtractorModelError(t *testing.T) {
	model := scriptedModel{err: errors.New("boom")}
	if _, _, err := (LLMEntityExtractor{Model: model}).Extract(context.Background(), "c1", "x"); err == nil {
		t.Fatalf("model error: want a propagated error")
	}
}
