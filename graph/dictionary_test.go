package graph

import (
	"context"
	"testing"
)

func TestDictionaryEntityExtractor(t *testing.T) {
	d := DictionaryEntityExtractor{Terms: map[string]string{
		"Ada Lovelace":      "person",
		"Analytical Engine": "machine",
		"Babbage":           "person",
	}}
	ents, rels, err := d.Extract(context.Background(), "c1",
		"Ada Lovelace wrote notes on the Analytical Engine.")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(ents) != 2 {
		t.Fatalf("entities = %d, want 2 (Ada Lovelace, Analytical Engine): %+v", len(ents), ents)
	}
	for _, e := range ents {
		if e.Name == "Babbage" {
			t.Fatalf("Babbage extracted but not present in the text")
		}
		if len(e.SourceChunkIDs) != 1 || e.SourceChunkIDs[0] != "c1" {
			t.Fatalf("entity %q provenance = %v, want [c1]", e.Name, e.SourceChunkIDs)
		}
	}
	if len(rels) != 1 || rels[0].Relation != "co-occurs" {
		t.Fatalf("relations = %+v, want one co-occurs", rels)
	}

	ents2, rels2, _ := d.Extract(context.Background(), "c1",
		"Ada Lovelace wrote notes on the Analytical Engine.")
	if len(ents2) != len(ents) || len(rels2) != len(rels) || ents2[0].Name != ents[0].Name {
		t.Fatalf("extractor is not deterministic")
	}
}
