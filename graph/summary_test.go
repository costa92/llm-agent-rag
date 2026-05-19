package graph

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// summaryTestGraph is a tiny fixed graph: two entities joined by one
// relation, used to exercise the summarizer's prompt building.
func summaryTestGraph() Graph {
	return Graph{
		Entities: []Entity{
			{ID: "t:a", Name: "Ada Lovelace", Type: "person", Description: "first programmer"},
			{ID: "t:b", Name: "Analytical Engine", Type: "machine", Description: "early mechanical computer"},
		},
		Relations: []Relation{
			{ID: "t:a::r::t:b", Source: "Ada Lovelace", Target: "Analytical Engine", Relation: "wrote algorithm for", Description: "her published notes"},
		},
	}
}

func summaryTestCommunity() Community {
	return Community{
		ID:          "L0-t:a",
		Level:       0,
		EntityIDs:   []string{"t:a", "t:b"},
		RelationIDs: []string{"t:a::r::t:b"},
	}
}

func TestLLMCommunitySummarizerCleanOutput(t *testing.T) {
	model := scriptedModel{text: "Title: Early Computing Pioneers\n" +
		"Ada Lovelace wrote the first algorithm intended for the Analytical Engine, an early mechanical computer."}
	c := summaryTestCommunity()
	report, err := LLMCommunitySummarizer{Model: model}.Summarize(context.Background(), c, summaryTestGraph())
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if report.Title != "Early Computing Pioneers" {
		t.Fatalf("title = %q, want Early Computing Pioneers", report.Title)
	}
	if !strings.Contains(report.Summary, "Analytical Engine") {
		t.Fatalf("summary = %q, want it to mention the Analytical Engine", report.Summary)
	}
	if report.CommunityID != c.ID {
		t.Fatalf("CommunityID = %q, want %q", report.CommunityID, c.ID)
	}
	if report.ContentHash != CommunityContentHash(c) {
		t.Fatalf("ContentHash = %q, want %q", report.ContentHash, CommunityContentHash(c))
	}
}

func TestLLMCommunitySummarizerLenientParsing(t *testing.T) {
	// No title line, leading preamble, a code fence — must parse without error.
	model := scriptedModel{text: "```\n" +
		"Here is the report you asked for.\n" +
		"This community is about early computing and the people behind it.\n" +
		"```"}
	c := summaryTestCommunity()
	report, err := LLMCommunitySummarizer{Model: model}.Summarize(context.Background(), c, summaryTestGraph())
	if err != nil {
		t.Fatalf("Summarize on malformed output: %v", err)
	}
	if report.Title != "" {
		t.Fatalf("title = %q, want empty (no title line)", report.Title)
	}
	if !strings.Contains(report.Summary, "early computing") {
		t.Fatalf("summary = %q, want the body lines joined", report.Summary)
	}
	if strings.Contains(report.Summary, "```") {
		t.Fatalf("summary = %q, want code fences stripped", report.Summary)
	}
	if report.ContentHash != CommunityContentHash(c) {
		t.Fatalf("ContentHash not set on lenient parse: %q", report.ContentHash)
	}
}

func TestLLMCommunitySummarizerNilModel(t *testing.T) {
	_, err := LLMCommunitySummarizer{}.Summarize(context.Background(), summaryTestCommunity(), summaryTestGraph())
	if !errors.Is(err, ErrCommunitySummarizerModelRequired) {
		t.Fatalf("nil model -> %v, want ErrCommunitySummarizerModelRequired", err)
	}
}

func TestLLMCommunitySummarizerModelError(t *testing.T) {
	model := scriptedModel{err: errors.New("boom")}
	if _, err := (LLMCommunitySummarizer{Model: model}).Summarize(context.Background(), summaryTestCommunity(), summaryTestGraph()); err == nil {
		t.Fatalf("model error: want a propagated error")
	}
}

func TestCommunityContentHashDeterministic(t *testing.T) {
	c := summaryTestCommunity()
	h1 := CommunityContentHash(c)
	h2 := CommunityContentHash(c)
	if h1 != h2 {
		t.Fatalf("hash not deterministic: %q vs %q", h1, h2)
	}
	if h1 == "" {
		t.Fatalf("hash is empty")
	}

	// Member order must not matter — the hash sorts internally.
	reordered := Community{
		ID:          c.ID,
		EntityIDs:   []string{"t:b", "t:a"},
		RelationIDs: []string{"t:a::r::t:b"},
	}
	if got := CommunityContentHash(reordered); got != h1 {
		t.Fatalf("hash depends on member order: %q vs %q", got, h1)
	}
}

func TestCommunityContentHashChangesWithMembership(t *testing.T) {
	base := summaryTestCommunity()
	baseHash := CommunityContentHash(base)

	// An added entity must flip the hash.
	withEntity := base
	withEntity.EntityIDs = []string{"t:a", "t:b", "t:c"}
	if CommunityContentHash(withEntity) == baseHash {
		t.Fatalf("hash unchanged after adding an entity")
	}

	// An added relation must flip the hash.
	withRelation := base
	withRelation.RelationIDs = []string{"t:a::r::t:b", "t:b::r::t:c"}
	if CommunityContentHash(withRelation) == baseHash {
		t.Fatalf("hash unchanged after adding a relation")
	}

	// The entity/relation boundary must be honored — moving an ID across it
	// is a real membership change, not the same set.
	a := Community{EntityIDs: []string{"x"}, RelationIDs: nil}
	b := Community{EntityIDs: nil, RelationIDs: []string{"x"}}
	if CommunityContentHash(a) == CommunityContentHash(b) {
		t.Fatalf("hash conflates entity IDs with relation IDs")
	}
}
