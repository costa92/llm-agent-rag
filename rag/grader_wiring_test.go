package rag

import (
	"context"
	"errors"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/store"
)

// recordingGrader records each grading call so tests can assert that
// EnableChunkGrading actually plumbs through to a per-hit, per-round
// invocation.
type recordingGrader struct {
	relevanceCalls []recordingGraderCall
	supportCalls   []recordingGraderCall
	relScore       float64
	supScore       float64
	err            error
}

type recordingGraderCall struct {
	query  string
	answer string
	hitID  string
}

func (g *recordingGrader) ScoreRelevance(_ context.Context, query string, hit store.Hit) (float64, string, error) {
	g.relevanceCalls = append(g.relevanceCalls, recordingGraderCall{query: query, hitID: hit.Chunk.ID})
	if g.err != nil {
		return 0, "", g.err
	}
	return g.relScore, "rec-rel", nil
}

func (g *recordingGrader) ScoreSupport(_ context.Context, answer string, hit store.Hit) (float64, string, error) {
	g.supportCalls = append(g.supportCalls, recordingGraderCall{answer: answer, hitID: hit.Chunk.ID})
	if g.err != nil {
		return 0, "", g.err
	}
	return g.supScore, "rec-sup", nil
}

// TestOptions_GraderField pins that Options carries an additive Grader
// field — the v1.1.0 brief wires the Grader dependency into the System
// via this struct.
func TestOptions_GraderField(t *testing.T) {
	g := &recordingGrader{}
	opts := Options{Grader: g}
	if opts.Grader == nil {
		t.Fatalf("Grader = nil, want recording grader")
	}
}

// TestAskReflection_PopulatesChunkScoresWhenGradingEnabled pins the
// end-to-end behavior: EnableChunkGrading=true plus a configured
// Grader fills RoundDetails[i].ChunkScores and
// Trace.Reflection.Rounds[i].ChunkScores with one entry per packed
// hit. With grading disabled (default) the slices stay empty.
func TestAskReflection_PopulatesChunkScoresWhenGradingEnabled(t *testing.T) {
	grader := &recordingGrader{relScore: 0.9, supScore: 0.8}
	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "the answer"}},
	}
	sys := New(Options{Model: model, Grader: grader})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:               ReflectionModeRule,
			MaxRounds:          1,
			EnableChunkGrading: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Diagnostics.Reflection.RoundDetails) != 1 {
		t.Fatalf("len(RoundDetails) = %d, want 1", len(ans.Diagnostics.Reflection.RoundDetails))
	}
	got := ans.Diagnostics.Reflection.RoundDetails[0].ChunkScores
	if len(got) != 1 {
		t.Fatalf("len(ChunkScores) = %d, want 1", len(got))
	}
	if got[0].Relevance != 0.9 {
		t.Fatalf("ChunkScores[0].Relevance = %v, want 0.9", got[0].Relevance)
	}
	if got[0].Support != 0.8 {
		t.Fatalf("ChunkScores[0].Support = %v, want 0.8", got[0].Support)
	}
	if got[0].HitID == "" {
		t.Fatalf("ChunkScores[0].HitID = empty, want a chunk id")
	}
	// Trace-side mirrors Diagnostics.
	if len(ans.Trace.Reflection.Rounds) != 1 {
		t.Fatalf("len(Trace.Reflection.Rounds) = %d, want 1", len(ans.Trace.Reflection.Rounds))
	}
	traceScores := ans.Trace.Reflection.Rounds[0].ChunkScores
	if len(traceScores) != 1 {
		t.Fatalf("len(Trace.Reflection.Rounds[0].ChunkScores) = %d, want 1", len(traceScores))
	}
	// Grader was actually called.
	if len(grader.relevanceCalls) == 0 {
		t.Fatalf("grader.relevanceCalls empty, want >=1 call")
	}
	if len(grader.supportCalls) == 0 {
		t.Fatalf("grader.supportCalls empty, want >=1 call")
	}
}

// TestAskReflection_GradingDisabledByDefault pins the backward-
// compatibility contract: a reflection run with EnableChunkGrading
// at its zero value (false) does not call the configured Grader and
// leaves ChunkScores empty — exactly v1.0.x behavior.
func TestAskReflection_GradingDisabledByDefault(t *testing.T) {
	grader := &recordingGrader{relScore: 0.9, supScore: 0.8}
	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "the answer"}},
	}
	sys := New(Options{Model: model, Grader: grader})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:      ReflectionModeRule,
			MaxRounds: 1,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(grader.relevanceCalls) != 0 {
		t.Fatalf("grader.relevanceCalls = %d, want 0 when EnableChunkGrading=false", len(grader.relevanceCalls))
	}
	if len(ans.Diagnostics.Reflection.RoundDetails) >= 1 {
		if len(ans.Diagnostics.Reflection.RoundDetails[0].ChunkScores) != 0 {
			t.Fatalf("ChunkScores = %v, want empty when grading disabled", ans.Diagnostics.Reflection.RoundDetails[0].ChunkScores)
		}
	}
}

// TestAskReflection_GradingFailureIsNonFatal pins the fail-open
// contract: a Grader that returns errors must not break the Ask call.
// The round still succeeds, ChunkScores stays empty for the failed
// round, and the answer is still returned.
func TestAskReflection_GradingFailureIsNonFatal(t *testing.T) {
	grader := &recordingGrader{err: errors.New("grader boom")}
	model := &scriptedReflectionModel{
		responses: []generate.Response{{Text: "the answer"}},
	}
	sys := New(Options{Model: model, Grader: grader})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:               ReflectionModeRule,
			MaxRounds:          1,
			EnableChunkGrading: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask err = %v, want nil (grading failure must be non-fatal)", err)
	}
	if ans.Text == "" {
		t.Fatalf("ans.Text empty, want non-empty answer despite grader failure")
	}
}
