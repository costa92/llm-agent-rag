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
// Stub: implementation in next commit (RED phase of TDD).
func ExportGraderDataset(ans Answer) []GraderExample {
	_ = ans
	return nil
}
