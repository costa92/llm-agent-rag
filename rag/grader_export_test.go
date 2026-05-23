package rag

import (
	"testing"

	"github.com/costa92/llm-agent-rag/store"
)

// TestExportGraderDataset_EmptyWhenReflectionDisabled pins that an Answer
// with no Reflection diagnostics (single-round Ask) yields no examples.
func TestExportGraderDataset_EmptyWhenReflectionDisabled(t *testing.T) {
	ans := Answer{
		Text: "single-round answer",
		Trace: Trace{
			Question: "what is rag?",
		},
		// Diagnostics.Reflection is the zero value — no RoundDetails.
	}
	got := ExportGraderDataset(ans)
	if len(got) != 0 {
		t.Fatalf("len(ExportGraderDataset) = %d, want 0 when reflection disabled", len(got))
	}
}

// TestExportGraderDataset_EmptyWhenGradingDisabled pins that an Answer
// with reflection rounds but no per-round ChunkScores yields no examples.
func TestExportGraderDataset_EmptyWhenGradingDisabled(t *testing.T) {
	ans := Answer{
		Text: "two-round answer, no grading",
		Trace: Trace{
			Question: "what is rag?",
		},
		Diagnostics: Diagnostics{
			Reflection: ReflectionDiagnostics{
				Mode:         ReflectionModeRule,
				Rounds:       2,
				AdoptedRound: 2,
				RoundDetails: []ReflectionRoundDiagnostics{
					{Round: 1, PromptChunkIDs: []string{"c1", "c2"}},
					{Round: 2, PromptChunkIDs: []string{"c1", "c2"}},
				},
			},
		},
	}
	got := ExportGraderDataset(ans)
	if len(got) != 0 {
		t.Fatalf("len(ExportGraderDataset) = %d, want 0 when grading disabled", len(got))
	}
}

// TestExportGraderDataset_OneRowPerScoredChunkPerRound pins that the
// extractor emits exactly one record per (round, scored chunk) pair, and
// that fields are wired through correctly.
func TestExportGraderDataset_OneRowPerScoredChunkPerRound(t *testing.T) {
	ans := Answer{
		Text: "final answer",
		Trace: Trace{
			Question: "what is rag?",
		},
		Diagnostics: Diagnostics{
			Reflection: ReflectionDiagnostics{
				Mode:         ReflectionModeRule,
				Rounds:       2,
				AdoptedRound: 2,
				RoundDetails: []ReflectionRoundDiagnostics{
					{
						Round:          1,
						PromptChunkIDs: []string{"c1", "c2", "c3"},
						ChunkScores: []ChunkScore{
							{HitID: "c1", Relevance: 0.1, Support: 0.2, Reason: "r1-c1"},
							{HitID: "c2", Relevance: 0.3, Support: 0.4, Reason: "r1-c2"},
							{HitID: "c3", Relevance: 0.5, Support: 0.6, Reason: "r1-c3"},
						},
					},
					{
						Round:          2,
						PromptChunkIDs: []string{"c1", "c2", "c3"},
						ChunkScores: []ChunkScore{
							{HitID: "c1", Relevance: 0.7, Support: 0.8, Reason: "r2-c1"},
							{HitID: "c2", Relevance: 0.75, Support: 0.85, Reason: "r2-c2"},
							{HitID: "c3", Relevance: 0.9, Support: 0.95, Reason: "r2-c3"},
						},
					},
				},
			},
		},
	}
	got := ExportGraderDataset(ans)
	if len(got) != 6 {
		t.Fatalf("len(ExportGraderDataset) = %d, want 6 (2 rounds x 3 chunks)", len(got))
	}
	// Field-by-field equality check on a representative example: round 1, c2.
	var found *GraderExample
	for i := range got {
		if got[i].Round == 1 && got[i].ChunkID == "c2" {
			found = &got[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("did not find example (round=1, chunk=c2) in %d results", len(got))
	}
	if found.Query != "what is rag?" {
		t.Fatalf("Query = %q, want %q", found.Query, "what is rag?")
	}
	if found.Answer != "final answer" {
		t.Fatalf("Answer = %q, want %q", found.Answer, "final answer")
	}
	if found.Relevance != 0.3 {
		t.Fatalf("Relevance = %v, want 0.3", found.Relevance)
	}
	if found.Support != 0.4 {
		t.Fatalf("Support = %v, want 0.4", found.Support)
	}
	if found.Reason != "r1-c2" {
		t.Fatalf("Reason = %q, want r1-c2", found.Reason)
	}
	if found.Adopted {
		t.Fatalf("Adopted = true, want false for round 1 (adopted round is 2)")
	}
}

// TestExportGraderDataset_AdoptedFlagMatchesAdoptedRoundChunkIDs pins
// that Adopted=true iff the example's Round == AdoptedRound AND its
// ChunkID is in PromptChunkIDs of the adopted round. A chunk scored in
// the adopted round but NOT packed into its prompt (dropped by the
// packer) is Adopted=false.
func TestExportGraderDataset_AdoptedFlagMatchesAdoptedRoundChunkIDs(t *testing.T) {
	ans := Answer{
		Text: "adopted-round answer",
		Trace: Trace{
			Question: "q",
		},
		Diagnostics: Diagnostics{
			Reflection: ReflectionDiagnostics{
				Mode:         ReflectionModeRule,
				Rounds:       2,
				AdoptedRound: 2,
				RoundDetails: []ReflectionRoundDiagnostics{
					{
						Round:          1,
						PromptChunkIDs: []string{"c1", "c2"},
						ChunkScores: []ChunkScore{
							{HitID: "c1", Relevance: 0.1},
							{HitID: "c2", Relevance: 0.2},
						},
					},
					{
						Round: 2,
						// c1 and c3 are packed into the adopted round's
						// prompt; c2 is scored but NOT packed (dropped).
						PromptChunkIDs: []string{"c1", "c3"},
						ChunkScores: []ChunkScore{
							{HitID: "c1", Relevance: 0.7},
							{HitID: "c2", Relevance: 0.5},
							{HitID: "c3", Relevance: 0.9},
						},
					},
				},
			},
		},
	}
	got := ExportGraderDataset(ans)
	// 2 (round 1) + 3 (round 2) = 5 examples
	if len(got) != 5 {
		t.Fatalf("len(ExportGraderDataset) = %d, want 5", len(got))
	}
	for _, ex := range got {
		wantAdopted := ex.Round == 2 && (ex.ChunkID == "c1" || ex.ChunkID == "c3")
		if ex.Adopted != wantAdopted {
			t.Fatalf("example (round=%d, chunk=%s): Adopted = %v, want %v",
				ex.Round, ex.ChunkID, ex.Adopted, wantAdopted)
		}
	}
}

// TestExportGraderDataset_ChunkContentResolvedFromCitations pins that
// when a chunk's content is available on Answer.Hits, ChunkContent is
// populated; when no Hit carries that ID, ChunkContent stays empty.
//
// Naming note: the v1.1.1 brief refers to "Citations" as the join, but
// rag.Citation does NOT carry chunk text — only metadata. The actual
// source of chunk content on an Answer is Answer.Hits[i].Chunk.Content.
// The extractor joins by chunk ID and falls back to "" when missing.
func TestExportGraderDataset_ChunkContentResolvedFromCitations(t *testing.T) {
	ans := Answer{
		Text: "a",
		Hits: []store.Hit{
			{Chunk: store.StoredChunk{ID: "c1", Content: "content-of-c1"}, Score: 0.9},
			// c2 intentionally absent — simulates a chunk scored in a
			// later round whose Hit was not retained on the adopted
			// Answer.Hits (or was dropped by a packer/sanitizer).
		},
		Trace: Trace{
			Question: "q",
		},
		Diagnostics: Diagnostics{
			Reflection: ReflectionDiagnostics{
				Mode:         ReflectionModeRule,
				Rounds:       1,
				AdoptedRound: 1,
				RoundDetails: []ReflectionRoundDiagnostics{
					{
						Round:          1,
						PromptChunkIDs: []string{"c1", "c2"},
						ChunkScores: []ChunkScore{
							{HitID: "c1", Relevance: 0.7},
							{HitID: "c2", Relevance: 0.6},
						},
					},
				},
			},
		},
	}
	got := ExportGraderDataset(ans)
	if len(got) != 2 {
		t.Fatalf("len(ExportGraderDataset) = %d, want 2", len(got))
	}
	var c1, c2 *GraderExample
	for i := range got {
		switch got[i].ChunkID {
		case "c1":
			c1 = &got[i]
		case "c2":
			c2 = &got[i]
		}
	}
	if c1 == nil || c2 == nil {
		t.Fatalf("missing example: c1=%v c2=%v", c1, c2)
	}
	if c1.ChunkContent != "content-of-c1" {
		t.Fatalf("c1.ChunkContent = %q, want %q", c1.ChunkContent, "content-of-c1")
	}
	if c2.ChunkContent != "" {
		t.Fatalf("c2.ChunkContent = %q, want \"\" (no matching Hit)", c2.ChunkContent)
	}
}

// TestExportGraderDataset_QueryFieldHoldsOriginalQuestion pins that the
// extractor wires Answer.Trace.Question (the original Ask query) into
// GraderExample.Query — NOT the per-round InputQuery, which may be a
// reflection rewrite.
func TestExportGraderDataset_QueryFieldHoldsOriginalQuestion(t *testing.T) {
	ans := Answer{
		Text: "a",
		Trace: Trace{
			Question: "original question",
		},
		Diagnostics: Diagnostics{
			Reflection: ReflectionDiagnostics{
				Mode:         ReflectionModeRule,
				Rounds:       1,
				AdoptedRound: 1,
				RoundDetails: []ReflectionRoundDiagnostics{
					{
						Round:          1,
						InputQuery:     "rewritten by reflection",
						PromptChunkIDs: []string{"c1"},
						ChunkScores: []ChunkScore{
							{HitID: "c1", Relevance: 0.5},
						},
					},
				},
			},
		},
	}
	got := ExportGraderDataset(ans)
	if len(got) != 1 {
		t.Fatalf("len(ExportGraderDataset) = %d, want 1", len(got))
	}
	if got[0].Query != "original question" {
		t.Fatalf("Query = %q, want %q (Trace.Question, not InputQuery)",
			got[0].Query, "original question")
	}
}
