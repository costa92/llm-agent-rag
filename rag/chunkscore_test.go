package rag

import (
	"testing"
)

// TestChunkScore_FieldShape pins the additive ChunkScore struct shape
// per the v1.1.0 brief. The fields are read by trace consumers and must
// not be renamed in the v1.x line.
func TestChunkScore_FieldShape(t *testing.T) {
	cs := ChunkScore{
		HitID:     "chunk-1",
		Relevance: 0.7,
		Support:   0.6,
		Reason:    "ok",
	}
	if cs.HitID != "chunk-1" {
		t.Fatalf("HitID = %q, want chunk-1", cs.HitID)
	}
	if cs.Relevance != 0.7 {
		t.Fatalf("Relevance = %v, want 0.7", cs.Relevance)
	}
	if cs.Support != 0.6 {
		t.Fatalf("Support = %v, want 0.6", cs.Support)
	}
	if cs.Reason != "ok" {
		t.Fatalf("Reason = %q, want ok", cs.Reason)
	}
}

// TestChunkScores_OnReflectionRoundDiagnostics pins that
// ReflectionRoundDiagnostics carries a ChunkScores slice field — the
// per-round, per-chunk grading evidence.
func TestChunkScores_OnReflectionRoundDiagnostics(t *testing.T) {
	d := ReflectionRoundDiagnostics{
		ChunkScores: []ChunkScore{{HitID: "c1", Relevance: 0.9, Support: 0.8, Reason: "r"}},
	}
	if len(d.ChunkScores) != 1 {
		t.Fatalf("len(ChunkScores) = %d, want 1", len(d.ChunkScores))
	}
	if d.ChunkScores[0].HitID != "c1" {
		t.Fatalf("HitID = %q, want c1", d.ChunkScores[0].HitID)
	}
}

// TestChunkScores_OnReflectionRoundTrace pins that ReflectionRoundTrace
// also carries ChunkScores — observer-facing trace must see the same
// per-chunk grading evidence as Diagnostics.
func TestChunkScores_OnReflectionRoundTrace(t *testing.T) {
	tr := ReflectionRoundTrace{
		ChunkScores: []ChunkScore{{HitID: "c2", Relevance: 0.4, Support: 0.5, Reason: "noop"}},
	}
	if len(tr.ChunkScores) != 1 {
		t.Fatalf("len(ChunkScores) = %d, want 1", len(tr.ChunkScores))
	}
	if tr.ChunkScores[0].HitID != "c2" {
		t.Fatalf("HitID = %q, want c2", tr.ChunkScores[0].HitID)
	}
}
