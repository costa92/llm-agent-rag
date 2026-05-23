package rag

// GraderExample is a single (query, chunk, grader-scores) tuple extracted
// from a completed Ask trace. Use it to build supervised training
// datasets for relevance/support models from real reflection-mode runs.
//
// IMPORTANT: the Relevance and Support scores come from whichever Grader
// was wired (typically PromptGrader, which asks an LLM to emit a number
// in [0.0, 1.0]). They are NOT paper-grade ISREL / ISSUP critic-model
// labels — treat them as machine-generated weak supervision.
//
// Compatibility note: this exported struct may grow additively over
// time, so keyed composite literals are recommended.
type GraderExample struct {
	Query        string  // Query is the original Ask question (Answer.Trace.Question), not a reflection-rewritten query.
	Answer       string  // Answer is the final adopted answer text (Answer.Text).
	ChunkID      string  // ChunkID identifies the scored chunk (ChunkScore.HitID).
	ChunkContent string  // ChunkContent is the chunk text, resolved from Answer.Hits when the chunk ID matches; empty when no Hit carries that ID (e.g. chunk scored in a non-adopted round or dropped by a packer).
	Relevance    float64 // Relevance is the grader's query-relevance score for this chunk in this round.
	Support      float64 // Support is the grader's answer-support score for this chunk in this round.
	Reason       string  // Reason is the grader's short human explanation (e.g. raw reply text).
	Round        int     // Round is the 1-based reflection round that produced this score, matching ReflectionRoundDiagnostics.Round.
	Adopted      bool    // Adopted is true iff Round == ReflectionDiagnostics.AdoptedRound AND ChunkID is in the adopted round's PromptChunkIDs.
}

// ExportGraderDataset extracts all GraderExample records from an Answer's
// reflection trace. It returns an empty slice when reflection was off
// (no RoundDetails), when grading was off (no ChunkScores in any round),
// or when there is otherwise nothing to emit.
//
// The function is pure and read-only: it does not mutate ans, does no
// I/O, and is safe to call from any goroutine. Chunk content is joined
// from Answer.Hits by chunk ID; when no Hit carries a scored chunk's
// ID, ChunkContent is left empty (the chunk may have been scored in a
// non-adopted round or dropped by the packer before it became a Hit on
// the final Answer). This is intentional — the extractor refuses to add
// new exported fields to Answer for the join.
//
// Naming note: the field is called Citations on the brief but
// rag.Citation only carries chunk METADATA, not chunk text — Hits is
// the only Answer-side source of Content. See GraderExample.ChunkContent.
func ExportGraderDataset(ans Answer) []GraderExample {
	rounds := ans.Diagnostics.Reflection.RoundDetails
	if len(rounds) == 0 {
		return nil
	}
	// First pass: count + check we have any scored chunks at all so we
	// can return nil cheaply when grading was disabled.
	total := 0
	for _, r := range rounds {
		total += len(r.ChunkScores)
	}
	if total == 0 {
		return nil
	}
	// Build the chunk-ID → content lookup off Answer.Hits. Hits is the
	// only Answer-side source of chunk text (Citations carries metadata
	// only). When a scored ID has no matching Hit, ChunkContent stays
	// empty per the doc comment.
	contentByID := make(map[string]string, len(ans.Hits))
	for _, hit := range ans.Hits {
		contentByID[hit.Chunk.ID] = hit.Chunk.Content
	}
	// Build the adopted-round PromptChunkIDs set for the Adopted flag.
	adopted := ans.Diagnostics.Reflection.AdoptedRound
	adoptedIDs := make(map[string]struct{})
	for _, r := range rounds {
		if r.Round == adopted {
			for _, id := range r.PromptChunkIDs {
				adoptedIDs[id] = struct{}{}
			}
			break
		}
	}
	out := make([]GraderExample, 0, total)
	for _, r := range rounds {
		for _, cs := range r.ChunkScores {
			ex := GraderExample{
				Query:        ans.Trace.Question,
				Answer:       ans.Text,
				ChunkID:      cs.HitID,
				ChunkContent: contentByID[cs.HitID],
				Relevance:    cs.Relevance,
				Support:      cs.Support,
				Reason:       cs.Reason,
				Round:        r.Round,
			}
			if r.Round == adopted {
				if _, ok := adoptedIDs[cs.HitID]; ok {
					ex.Adopted = true
				}
			}
			out = append(out, ex)
		}
	}
	return out
}
