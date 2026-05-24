package rag_test

import (
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/rag"
	"github.com/costa92/llm-agent-rag/rerank"
	"github.com/costa92/llm-agent-rag/retrieve"
)

// sampleDiagnostics builds a fully-populated Diagnostics{} for the
// round-trip test: every sub-section non-zero, including a non-nil
// EvidenceSubgraph to exercise the structural projection.
func sampleDiagnostics() rag.Diagnostics {
	return rag.Diagnostics{
		HitCount:         3,
		ReturnedChunkIDs: []string{"c1", "c2", "c3"},
		PromptChunkIDs:   []string{"c1", "c2"},
		MatchedSections:  []string{"intro/overview"},
		ExpandedChunkIDs: []string{"c4"},
		AutoRouteCandidates: []retrieve.RouteCandidate{{
			Path: []string{"docs", "intro"}, Score: 0.7, Confidence: 0.8,
			Queries: []string{"q"}, Signals: []string{"lex"},
			Selected: true, Reason: "top1",
		}},
		RoutePolicy: retrieve.RoutePolicyTrace{
			Mode: "fanout", ConfidenceThreshold: 0.5,
			ConfidenceGap: 0.1, Gap: 0.05, Fanout: 2,
			CandidateCount: 3, SelectedCount: 2,
			Rationale: []string{"gap<thr"},
		},
		SearchTrajectory: []retrieve.TrajectoryStep{{
			Route: []string{"docs"}, Confidence: 0.7, Mode: "fanout",
			HitCount: 2, HitIDs: []string{"c1", "c2"},
			MatchedSections: []string{"intro"}, ExpandedSections: nil,
			Rationale: "primary route",
		}},
		RerankScores: []rerank.RerankScore{{
			ChunkID: "c1", InputScore: 0.5, OutputScore: 0.9,
			InputRank: 2, OutputRank: 1, RankDelta: 1,
		}},
		Metrics: obs.Metrics{
			TotalDuration: 5 * time.Millisecond,
			Stages: []obs.StageTiming{
				{Stage: "retrieve", Duration: 2 * time.Millisecond},
				{Stage: "generate", Duration: 3 * time.Millisecond},
			},
			Calls: obs.CallCounts{Embed: 1, Generate: 2},
			Tokens: obs.TokenUsage{
				PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120,
				Estimated: false,
			},
			StageTokenUsage: []obs.StageTokenUsage{{
				Stage: "ask", Usage: obs.TokenUsage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
			}},
		},
		InjectionFindings: []rag.InjectionFinding{{
			ChunkID: "c5", Patterns: []string{"prompt"}, Action: "neutralized",
		}},
		GraphTrace: retrieve.GraphTrace{
			SeedEntityIDs:    []string{"e1"},
			ReachedEntityIDs: []string{"e1", "e2"},
			MaxHop:           2,
			CommunityIDs:     []string{"cm1"},
			EvidenceSubgraph: &graph.Subgraph{
				Entities: []graph.Entity{
					{ID: "e1", Name: "A"},
					{ID: "e2", Name: "B"},
				},
				Relations: []graph.Relation{
					{ID: "r1", Source: "e1", Target: "e2", Relation: "x"},
				},
				Depth: map[string]int{"e1": 0, "e2": 1},
			},
		},
		Global: rag.GlobalDiagnostics{
			CommunityIDs: []string{"cm1", "cm2"},
			MapScores:    map[string]int{"cm1": 80, "cm2": 30},
			MapCalls:     2,
			ReduceCalls:  1,
			ConsultedReports: []graph.CommunityReport{
				{CommunityID: "cm1", Title: "T1", Summary: "S1", ContentHash: "h1"},
			},
		},
		Drift: rag.DriftDiagnostics{
			PrimerCommunityIDs: []string{"cm1"},
			Rounds:             2,
			RoundEntityIDs:     [][]string{{"e1"}, {"e2"}},
			ConsultedReports: []graph.CommunityReport{
				{CommunityID: "cm1", Title: "T1", Summary: "S1", ContentHash: "h1"},
			},
		},
		Reflection: rag.ReflectionDiagnostics{
			Mode:                rag.ReflectionMode("rule"),
			Rounds:              2,
			AdoptedRound:        1,
			StopReason:          "decision_stop",
			FailureFallback:     false,
			FailureReason:       "",
			DecisionModelCalls:  1,
			RewriteModelCalls:   0,
			FollowupQueriesUsed: 0,
			RoundDetails: []rag.ReflectionRoundDiagnostics{{
				Round:            1,
				InputQuery:       "q",
				EffectiveQuery:   "q",
				ReturnedChunkIDs: []string{"c1"},
				PromptChunkIDs:   []string{"c1"},
				UniqueDocCount:   1,
				TopScore:         0.9,
				Decision:         rag.ReflectionDecisionStop,
				DecisionReason:   "good",
				ChunkScores: []rag.ChunkScore{{
					HitID: "c1", Relevance: 0.9, Support: 0.85, Reason: "supports",
				}},
			}},
		},
	}
}

// diagnosticsApproxEqual compares two Diagnostics for the key fields the
// codec round-trips. It's a structural equality check that tolerates the
// Subgraph projection (entity IDs / edge IDs / max hop only).
func diagnosticsApproxEqual(t *testing.T, in, out rag.Diagnostics) {
	t.Helper()
	if in.HitCount != out.HitCount {
		t.Errorf("HitCount: in=%d out=%d", in.HitCount, out.HitCount)
	}
	if len(in.ReturnedChunkIDs) != len(out.ReturnedChunkIDs) {
		t.Errorf("ReturnedChunkIDs len: in=%d out=%d", len(in.ReturnedChunkIDs), len(out.ReturnedChunkIDs))
	}
	if in.RoutePolicy.Mode != out.RoutePolicy.Mode {
		t.Errorf("RoutePolicy.Mode: in=%q out=%q", in.RoutePolicy.Mode, out.RoutePolicy.Mode)
	}
	if in.Metrics.TotalDuration != out.Metrics.TotalDuration {
		t.Errorf("Metrics.TotalDuration: in=%v out=%v", in.Metrics.TotalDuration, out.Metrics.TotalDuration)
	}
	if len(in.Metrics.Stages) != len(out.Metrics.Stages) {
		t.Errorf("Metrics.Stages len: in=%d out=%d", len(in.Metrics.Stages), len(out.Metrics.Stages))
	}
	if in.Metrics.Tokens.TotalTokens != out.Metrics.Tokens.TotalTokens {
		t.Errorf("Metrics.Tokens.TotalTokens: in=%d out=%d", in.Metrics.Tokens.TotalTokens, out.Metrics.Tokens.TotalTokens)
	}
	if in.GraphTrace.MaxHop != out.GraphTrace.MaxHop {
		t.Errorf("GraphTrace.MaxHop: in=%d out=%d", in.GraphTrace.MaxHop, out.GraphTrace.MaxHop)
	}
	// Subgraph projection: entity IDs and edge IDs round-trip; other fields
	// drop. Documented contract.
	if in.GraphTrace.EvidenceSubgraph != nil {
		if out.GraphTrace.EvidenceSubgraph == nil {
			t.Errorf("EvidenceSubgraph: dropped to nil on round-trip")
		} else {
			if len(out.GraphTrace.EvidenceSubgraph.Entities) != len(in.GraphTrace.EvidenceSubgraph.Entities) {
				t.Errorf("EvidenceSubgraph entity count: in=%d out=%d",
					len(in.GraphTrace.EvidenceSubgraph.Entities),
					len(out.GraphTrace.EvidenceSubgraph.Entities))
			}
			if len(out.GraphTrace.EvidenceSubgraph.Relations) != len(in.GraphTrace.EvidenceSubgraph.Relations) {
				t.Errorf("EvidenceSubgraph relation count: in=%d out=%d",
					len(in.GraphTrace.EvidenceSubgraph.Relations),
					len(out.GraphTrace.EvidenceSubgraph.Relations))
			}
		}
	}
	if in.Global.MapCalls != out.Global.MapCalls {
		t.Errorf("Global.MapCalls: in=%d out=%d", in.Global.MapCalls, out.Global.MapCalls)
	}
	if in.Drift.Rounds != out.Drift.Rounds {
		t.Errorf("Drift.Rounds: in=%d out=%d", in.Drift.Rounds, out.Drift.Rounds)
	}
	if in.Reflection.AdoptedRound != out.Reflection.AdoptedRound {
		t.Errorf("Reflection.AdoptedRound: in=%d out=%d", in.Reflection.AdoptedRound, out.Reflection.AdoptedRound)
	}
	if len(in.Reflection.RoundDetails) != len(out.Reflection.RoundDetails) {
		t.Errorf("Reflection.RoundDetails len: in=%d out=%d", len(in.Reflection.RoundDetails), len(out.Reflection.RoundDetails))
	}
}

// TestMarshalDiagnostics_RoundTrip is the headline test for the
// Diagnostics codec: fully-populated input round-trips structurally.
func TestMarshalDiagnostics_RoundTrip(t *testing.T) {
	in := sampleDiagnostics()
	b, err := rag.MarshalDiagnostics(in)
	if err != nil {
		t.Fatalf("MarshalDiagnostics: %v", err)
	}
	out, err := rag.UnmarshalDiagnostics(b)
	if err != nil {
		t.Fatalf("UnmarshalDiagnostics: %v", err)
	}
	diagnosticsApproxEqual(t, in, out)
}

// TestMarshalDiagnostics_ZeroValue asserts the zero-value Diagnostics
// round-trips to itself.
func TestMarshalDiagnostics_ZeroValue(t *testing.T) {
	var in rag.Diagnostics
	b, err := rag.MarshalDiagnostics(in)
	if err != nil {
		t.Fatalf("MarshalDiagnostics zero: %v", err)
	}
	out, err := rag.UnmarshalDiagnostics(b)
	if err != nil {
		t.Fatalf("UnmarshalDiagnostics zero: %v", err)
	}
	if out.HitCount != 0 || out.Metrics.TotalDuration != 0 ||
		len(out.ReturnedChunkIDs) != 0 || out.Global.MapCalls != 0 ||
		out.Drift.Rounds != 0 || out.Reflection.AdoptedRound != 0 ||
		out.GraphTrace.EvidenceSubgraph != nil {
		t.Fatalf("zero-value round-trip not zero: %+v", out)
	}
}

// TestUnmarshalDiagnostics_UnknownKey asserts strict unknown-field
// rejection.
func TestUnmarshalDiagnostics_UnknownKey(t *testing.T) {
	raw := []byte(`{"weird":42}`)
	_, err := rag.UnmarshalDiagnostics(raw)
	if err == nil {
		t.Fatalf("UnmarshalDiagnostics accepted unknown key; want error")
	}
	if !strings.Contains(err.Error(), "weird") && !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("error does not mention 'unknown' or 'weird': %v", err)
	}
}

// TestMarshalDiagnostics_NilSubgraph asserts Diagnostics with a nil
// EvidenceSubgraph round-trip cleanly (omitempty on the wire).
func TestMarshalDiagnostics_NilSubgraph(t *testing.T) {
	in := rag.Diagnostics{
		HitCount: 1,
		GraphTrace: retrieve.GraphTrace{
			SeedEntityIDs: []string{"e1"},
			MaxHop:        1,
			// EvidenceSubgraph intentionally nil
		},
	}
	b, err := rag.MarshalDiagnostics(in)
	if err != nil {
		t.Fatalf("MarshalDiagnostics nil-subgraph: %v", err)
	}
	out, err := rag.UnmarshalDiagnostics(b)
	if err != nil {
		t.Fatalf("UnmarshalDiagnostics nil-subgraph: %v", err)
	}
	if out.GraphTrace.EvidenceSubgraph != nil {
		t.Fatalf("nil-subgraph rehydrated as %+v; want nil", out.GraphTrace.EvidenceSubgraph)
	}
	if out.HitCount != 1 || out.GraphTrace.MaxHop != 1 {
		t.Fatalf("non-subgraph fields lost: %+v", out)
	}
}
