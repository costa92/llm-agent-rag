package graph

import "testing"

func TestCanonicalizeMergesEntitiesAcrossChunks(t *testing.T) {
	ents := []Entity{
		{Name: "Ada Lovelace", Type: "person", Description: "programmer", SourceChunkIDs: []string{"c1"}},
		{Name: "ada lovelace", Type: "person", Description: "mathematician", SourceChunkIDs: []string{"c2"}},
		{Name: "Babbage", Type: "person", SourceChunkIDs: []string{"c2"}},
	}
	rels := []Relation{
		{Source: "Ada Lovelace", Target: "Babbage", Relation: "collaborated with", SourceChunkIDs: []string{"c1"}, Weight: 1},
		{Source: "Ada Lovelace", Target: "Nobody", Relation: "knows", SourceChunkIDs: []string{"c1"}, Weight: 1},
	}
	g := Canonicalize(ents, rels)

	if len(g.Entities) != 2 {
		t.Fatalf("entities = %d, want 2 (Ada merged + Babbage): %+v", len(g.Entities), g.Entities)
	}
	var ada Entity
	for _, e := range g.Entities {
		if e.ID == "person:ada lovelace" {
			ada = e
		}
	}
	if ada.ID == "" {
		t.Fatalf("merged Ada entity not found: %+v", g.Entities)
	}
	if len(ada.SourceChunkIDs) != 2 {
		t.Fatalf("Ada provenance = %v, want both c1 and c2", ada.SourceChunkIDs)
	}
	if ada.Description != "programmer; mathematician" {
		t.Fatalf("Ada description = %q, want the two descriptions merged", ada.Description)
	}
	if len(g.Relations) != 1 {
		t.Fatalf("relations = %d, want 1 (the unknown-endpoint relation dropped): %+v", len(g.Relations), g.Relations)
	}
	if g.Relations[0].Source != "person:ada lovelace" || g.Relations[0].Target != "person:babbage" {
		t.Fatalf("relation endpoints not resolved to canonical ids: %+v", g.Relations[0])
	}
}

func TestCanonicalizeMergesRelations(t *testing.T) {
	ents := []Entity{
		{Name: "A", Type: "t", SourceChunkIDs: []string{"c1"}},
		{Name: "B", Type: "t", SourceChunkIDs: []string{"c1"}},
	}
	rels := []Relation{
		{Source: "A", Target: "B", Relation: "rel", SourceChunkIDs: []string{"c1"}, Weight: 1},
		{Source: "A", Target: "B", Relation: "rel", SourceChunkIDs: []string{"c2"}, Weight: 1},
	}
	g := Canonicalize(ents, rels)
	if len(g.Relations) != 1 {
		t.Fatalf("relations = %d, want 1 (the duplicate merged)", len(g.Relations))
	}
	if g.Relations[0].Weight != 2 {
		t.Fatalf("merged relation Weight = %v, want 2 (summed)", g.Relations[0].Weight)
	}
	if len(g.Relations[0].SourceChunkIDs) != 2 {
		t.Fatalf("merged relation provenance = %v, want c1 and c2", g.Relations[0].SourceChunkIDs)
	}
}
