package rag

import (
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
)

// TestReflectionTrace_CapturesRawDecisionText pins the requirement that the
// reflection model's raw reply text is preserved verbatim in both
// Diagnostics.Reflection.RoundDetails[i].RawDecisionText and
// Trace.Reflection.Rounds[i].RawDecisionText. D5 closure.
func TestReflectionTrace_CapturesRawDecisionText(t *testing.T) {
	rawReply := "decision=rewrite_and_continue\nreason=need better\nrewrite=better query"
	model := &scriptedReflectionModel{
		responses: []generate.Response{
			{Text: "answer round 1"},
			{Text: rawReply},
			{Text: "answer round 2"},
			{Text: "decision=stop\nreason=ok"},
		},
	}
	sys := New(Options{
		Model:        model,
		Preprocessor: conditionalRewritePreprocessor{},
	})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "zzberlin Berlin is in Germany."},
		{ID: "doc2", Content: "zzparis Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search:   SearchOptions{Namespace: "geo", TopK: 1},
		Template: promptRoutingTemplate{},
		Reflection: &ReflectionOptions{
			Mode:         ReflectionModeModel,
			MaxRounds:    2,
			AllowRewrite: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Diagnostics.Reflection.RoundDetails) < 1 {
		t.Fatalf("RoundDetails empty, want >=1 round")
	}
	got := ans.Diagnostics.Reflection.RoundDetails[0].RawDecisionText
	if got != rawReply {
		t.Fatalf("Diagnostics.Reflection.RoundDetails[0].RawDecisionText = %q, want %q", got, rawReply)
	}
	if len(ans.Trace.Reflection.Rounds) < 1 {
		t.Fatalf("Trace.Reflection.Rounds empty, want >=1")
	}
	gotTrace := ans.Trace.Reflection.Rounds[0].RawDecisionText
	if gotTrace != rawReply {
		t.Fatalf("Trace.Reflection.Rounds[0].RawDecisionText = %q, want %q", gotTrace, rawReply)
	}
}

// TestReflectionTrace_CapturesDecisionPrompt pins the requirement that the
// user-content portion of the reflection decision prompt is preserved on
// Diagnostics.Reflection.RoundDetails[i].DecisionPrompt. D5 closure.
func TestReflectionTrace_CapturesDecisionPrompt(t *testing.T) {
	t.Run("model mode populates DecisionPrompt", func(t *testing.T) {
		rawReply := "decision=stop\nreason=enough"
		model := &scriptedReflectionModel{
			responses: []generate.Response{
				{Text: "answer round 1"},
				{Text: rawReply},
			},
		}
		sys := New(Options{Model: model})
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
				Mode:         ReflectionModeModel,
				MaxRounds:    2,
				AllowRewrite: true,
			},
		})
		if err != nil {
			t.Fatalf("Ask(): %v", err)
		}
		if len(ans.Diagnostics.Reflection.RoundDetails) < 1 {
			t.Fatalf("RoundDetails empty, want >=1 round")
		}
		got := ans.Diagnostics.Reflection.RoundDetails[0].DecisionPrompt
		if !strings.Contains(got, "Original question:") {
			t.Fatalf("DecisionPrompt = %q, want substring %q", got, "Original question:")
		}
		if !strings.Contains(got, "Current answer:") {
			t.Fatalf("DecisionPrompt = %q, want substring %q", got, "Current answer:")
		}
		if !strings.Contains(got, "Allow rewrite: true") {
			t.Fatalf("DecisionPrompt = %q, want substring %q", got, "Allow rewrite: true")
		}
	})

	t.Run("rule mode leaves DecisionPrompt empty", func(t *testing.T) {
		sys := New(Options{Model: fakeModel{}})
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
		if len(ans.Diagnostics.Reflection.RoundDetails) < 1 {
			t.Fatalf("RoundDetails empty, want >=1 round")
		}
		got := ans.Diagnostics.Reflection.RoundDetails[0].DecisionPrompt
		if got != "" {
			t.Fatalf("rule-mode DecisionPrompt = %q, want empty (no model call)", got)
		}
		gotRaw := ans.Diagnostics.Reflection.RoundDetails[0].RawDecisionText
		if gotRaw != "" {
			t.Fatalf("rule-mode RawDecisionText = %q, want empty (no model call)", gotRaw)
		}
	})
}
