package rag

import (
	"github.com/costa92/llm-agent-rag/guard"
	"github.com/costa92/llm-agent-rag/store"
)

// InjectionFinding records one retrieved chunk that the injection scanner
// flagged, and how Ask handled it.
type InjectionFinding struct {
	ChunkID  string
	Patterns []string
	Action   string // "neutralized" or "dropped"
}

// sanitizeHits screens packed hits for prompt-injection content before
// prompt assembly. A suspicious hit is dropped or has its content
// neutralized per the configured SanitizeMode; non-suspicious hits pass
// through unchanged. It returns the surviving hits (with neutralized
// content where applicable) and a finding per flagged chunk.
func (s *System) sanitizeHits(hits []store.Hit) ([]store.Hit, []InjectionFinding) {
	out := make([]store.Hit, 0, len(hits))
	var findings []InjectionFinding
	for _, hit := range hits {
		verdict := s.injectionScanner.Scan(hit.Chunk.Content)
		if !verdict.Suspicious {
			out = append(out, hit)
			continue
		}
		if s.sanitizeMode == guard.Drop {
			findings = append(findings, InjectionFinding{
				ChunkID:  hit.Chunk.ID,
				Patterns: verdict.Patterns,
				Action:   "dropped",
			})
			continue
		}
		// guard.Neutralize (default): keep the chunk, defang its content.
		hit.Chunk.Content = guard.NeutralizeText(hit.Chunk.Content)
		out = append(out, hit)
		findings = append(findings, InjectionFinding{
			ChunkID:  hit.Chunk.ID,
			Patterns: verdict.Patterns,
			Action:   "neutralized",
		})
	}
	return out, findings
}
