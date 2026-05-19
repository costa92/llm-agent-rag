package graph

import (
	"context"
	"errors"
	"strings"

	"github.com/costa92/llm-agent-rag/generate"
)

// ErrEntityExtractorModelRequired is returned by LLMEntityExtractor when no
// generate.Model is configured.
var ErrEntityExtractorModelRequired = errors.New("graph: entity extractor requires a generate.Model")

const extractSystemPrompt = `You extract a knowledge graph from a document chunk.
Identify the SALIENT entities (people, organizations, places, products, concepts) — not every noun, only things worth a graph node — and the relations between them.

Output one record per line, pipe-delimited, and nothing else:
ENTITY | name | type | short description
RELATION | source entity name | target entity name | relation | short description

No commentary, no numbering, no markdown.`

// LLMEntityExtractor extracts entities and relations from chunk text by
// prompting a generate.Model. Its output is parsed leniently — malformed
// lines are dropped, never fatal.
type LLMEntityExtractor struct {
	Model generate.Model
}

// Extract implements EntityExtractor. Extracted entities are pre-canonical
// (empty ID); relation Source/Target hold entity names. Both carry chunkID
// as provenance.
func (e LLMEntityExtractor) Extract(ctx context.Context, chunkID, text string) ([]Entity, []Relation, error) {
	if e.Model == nil {
		return nil, nil, ErrEntityExtractorModelRequired
	}
	resp, err := e.Model.Generate(ctx, generate.Request{
		SystemPrompt: extractSystemPrompt,
		Messages:     []generate.Message{{Role: "user", Content: text}},
	})
	if err != nil {
		return nil, nil, err
	}
	ents, rels := parseExtraction(resp.Text, chunkID)
	return ents, rels, nil
}

// parseExtraction leniently parses pipe-delimited ENTITY/RELATION lines.
// A line that does not start with ENTITY/RELATION, or has too few fields,
// is dropped without error.
func parseExtraction(out, chunkID string) ([]Entity, []Relation) {
	var ents []Entity
	var rels []Relation
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := splitPipe(line)
		switch strings.ToUpper(fields[0]) {
		case "ENTITY":
			// ENTITY | name | type | description
			if len(fields) < 2 || fields[1] == "" {
				continue
			}
			ents = append(ents, Entity{
				Name:           fields[1],
				Type:           field(fields, 2),
				Description:    field(fields, 3),
				SourceChunkIDs: []string{chunkID},
			})
		case "RELATION":
			// RELATION | source | target | relation | description
			if len(fields) < 4 || fields[1] == "" || fields[2] == "" || fields[3] == "" {
				continue
			}
			rels = append(rels, Relation{
				Source:         fields[1],
				Target:         fields[2],
				Relation:       fields[3],
				Description:    field(fields, 4),
				SourceChunkIDs: []string{chunkID},
				Weight:         1,
			})
		}
	}
	return ents, rels
}

func splitPipe(line string) []string {
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func field(fields []string, i int) string {
	if i < len(fields) {
		return fields[i]
	}
	return ""
}
