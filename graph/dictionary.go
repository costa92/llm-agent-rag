package graph

import (
	"context"
	"sort"
	"strings"
)

// DictionaryEntityExtractor is a deterministic, zero-LLM EntityExtractor:
// it matches a caller-supplied gazetteer (term -> entity type) against
// chunk text, case-insensitively. It is the no-LLM default and the basis
// for reproducible tests. Two terms co-occurring in a chunk yield a
// "co-occurs" relation.
type DictionaryEntityExtractor struct {
	Terms map[string]string // term -> entity type
}

// Extract implements EntityExtractor deterministically — output order is
// stable for a given gazetteer and text.
func (d DictionaryEntityExtractor) Extract(_ context.Context, chunkID, text string) ([]Entity, []Relation, error) {
	lower := strings.ToLower(text)
	terms := make([]string, 0, len(d.Terms))
	for term := range d.Terms {
		if term != "" {
			terms = append(terms, term)
		}
	}
	sort.Strings(terms) // deterministic order

	var ents []Entity
	for _, term := range terms {
		if strings.Contains(lower, strings.ToLower(term)) {
			ents = append(ents, Entity{
				Name:           term,
				Type:           d.Terms[term],
				SourceChunkIDs: []string{chunkID},
			})
		}
	}
	var rels []Relation
	for i := 0; i < len(ents); i++ {
		for j := i + 1; j < len(ents); j++ {
			rels = append(rels, Relation{
				Source:         ents[i].Name,
				Target:         ents[j].Name,
				Relation:       "co-occurs",
				SourceChunkIDs: []string{chunkID},
				Weight:         1,
			})
		}
	}
	return ents, rels, nil
}
