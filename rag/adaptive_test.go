package rag

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/store"
)

// lowRelevanceGrader returns a constant low relevance — used to drive
// AdaptiveRetrieval into forcing an extra round.
type lowRelevanceGrader struct {
	relevance float64
}

func (g lowRelevanceGrader) ScoreRelevance(_ context.Context, _ string, _ store.Hit) (float64, string, error) {
	return g.relevance, "scripted", nil
}

func (g lowRelevanceGrader) ScoreSupport(_ context.Context, _ string, _ store.Hit) (float64, string, error) {
	return 0.5, "scripted", nil
}

// TestAskReflection_AdaptiveRetrieval_ForcesExtraRoundOnLowRelevance
// pins the contract: when AdaptiveRetrieval is on and the rule decision
// said Stop after round 1 but the max chunk relevance is below
// AdaptiveRetrievalThreshold, the loop runs one extra round (subject to
// MaxRounds). The result has two rounds — the adaptive forcing took
// effect even though the rule policy was satisfied.
func TestAskReflection_AdaptiveRetrieval_ForcesExtraRoundOnLowRelevance(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1"},
			{Text: "answer round 2"},
		},
	}
	grader := lowRelevanceGrader{relevance: 0.2} // < threshold 0.6
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
			MaxRounds:          2,
			EnableChunkGrading: true,
			AdaptiveRetrieval:  true,
			// AdaptiveRetrievalThreshold defaults to 0.6.
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Diagnostics.Reflection.RoundDetails) != 2 {
		t.Fatalf("len(RoundDetails) = %d, want 2 (adaptive should have forced an extra round)",
			len(ans.Diagnostics.Reflection.RoundDetails))
	}
}

// TestAskReflection_AdaptiveRetrieval_NoOpAboveThreshold pins the inverse:
// when AdaptiveRetrieval is on but max relevance is at or above the
// threshold, the loop does not force an extra round — the v1.0.x rule
// decision wins.
func TestAskReflection_AdaptiveRetrieval_NoOpAboveThreshold(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1"},
		},
	}
	grader := lowRelevanceGrader{relevance: 0.95} // > threshold 0.6
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
			MaxRounds:          2,
			EnableChunkGrading: true,
			AdaptiveRetrieval:  true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Diagnostics.Reflection.RoundDetails) != 1 {
		t.Fatalf("len(RoundDetails) = %d, want 1 (no adaptive forcing above threshold)",
			len(ans.Diagnostics.Reflection.RoundDetails))
	}
}

// TestAskReflection_AdaptiveRetrieval_RespectsMaxRounds pins the hard
// cap: AdaptiveRetrieval may force one extra round when relevance is
// low, but it must never push past MaxRounds. With MaxRounds=1 the
// adaptive budget cannot fire, so exactly one round runs.
func TestAskReflection_AdaptiveRetrieval_RespectsMaxRounds(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1"},
		},
	}
	grader := lowRelevanceGrader{relevance: 0.1}
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
			AdaptiveRetrieval:  true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Diagnostics.Reflection.RoundDetails) != 1 {
		t.Fatalf("len(RoundDetails) = %d, want 1 (MaxRounds=1 hard cap)",
			len(ans.Diagnostics.Reflection.RoundDetails))
	}
}

// TestAskReflection_AdaptiveRetrieval_DisabledByDefault pins backward
// compatibility: with AdaptiveRetrieval at its zero-value false, even a
// catastrophically low grader score does not force an extra round.
func TestAskReflection_AdaptiveRetrieval_DisabledByDefault(t *testing.T) {
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1"},
		},
	}
	grader := lowRelevanceGrader{relevance: 0.0}
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
			MaxRounds:          2,
			EnableChunkGrading: true,
			// AdaptiveRetrieval left at zero-value (false).
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Diagnostics.Reflection.RoundDetails) != 1 {
		t.Fatalf("len(RoundDetails) = %d, want 1 (AdaptiveRetrieval=false default)",
			len(ans.Diagnostics.Reflection.RoundDetails))
	}
}
