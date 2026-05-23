package rag

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
)

// TestNoopQueryPlanner_ReturnsNil pins the no-op contract: regardless
// of inputs, NoopQueryPlanner.PlanFollowups returns (nil, nil) — the
// safe default that disables active retrieval at the planner seam.
func TestNoopQueryPlanner_ReturnsNil(t *testing.T) {
	p := NoopQueryPlanner{}
	got, err := p.PlanFollowups(context.Background(), "anything", "prev answer", []ChunkScore{
		{HitID: "c1", Relevance: 0.2},
	})
	if err != nil {
		t.Fatalf("NoopQueryPlanner err = %v, want nil", err)
	}
	if got != nil {
		t.Fatalf("NoopQueryPlanner got = %v, want nil", got)
	}
}

// TestQueryPlanner_InterfaceImplementations compile-checks that the two
// shipped planners satisfy QueryPlanner. The var assignment alone is
// the test — if a planner stops satisfying the interface, the package
// fails to compile.
func TestQueryPlanner_InterfaceImplementations(t *testing.T) {
	var (
		_ QueryPlanner = NoopQueryPlanner{}
		_ QueryPlanner = PromptQueryPlanner{}
	)
}

// scriptedPlannerModel returns a fixed sequence of model replies and
// records each request for assertions.
type scriptedPlannerModel struct {
	responses []generate.Response
	errors    []error
	requests  []generate.Request
}

func (m *scriptedPlannerModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	m.requests = append(m.requests, req)
	idx := len(m.requests) - 1
	if idx < len(m.errors) && m.errors[idx] != nil {
		return generate.Response{}, m.errors[idx]
	}
	if idx < len(m.responses) {
		return m.responses[idx], nil
	}
	return generate.Response{Text: ""}, nil
}

// TestPromptQueryPlanner_ParsesLineDelimitedQueries pins the happy
// path: a line-delimited model reply parses into one query per line,
// trimmed of leading/trailing whitespace, in order.
func TestPromptQueryPlanner_ParsesLineDelimitedQueries(t *testing.T) {
	model := &scriptedPlannerModel{
		responses: []generate.Response{
			{Text: "follow up one\nfollow up two\nfollow up three\n"},
		},
	}
	p := PromptQueryPlanner{Model: model, MaxQueries: 3}
	got, err := p.PlanFollowups(context.Background(), "original q", "prev answer", nil)
	if err != nil {
		t.Fatalf("PlanFollowups err = %v, want nil", err)
	}
	want := []string{"follow up one", "follow up two", "follow up three"}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, q := range want {
		if got[i] != q {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], q)
		}
	}
}

// TestPromptQueryPlanner_StripsBulletAndNumberPrefixes pins the
// defensive parser: bullet prefixes ("- ", "* "), numbered prefixes
// ("1. ", "1) "), surrounding whitespace, and empty lines are all
// normalized away.
func TestPromptQueryPlanner_StripsBulletAndNumberPrefixes(t *testing.T) {
	model := &scriptedPlannerModel{
		responses: []generate.Response{
			{Text: "- query alpha\n* query beta\n1. query gamma\n2) query delta\n\n   query epsilon  \n"},
		},
	}
	p := PromptQueryPlanner{Model: model, MaxQueries: 5}
	got, err := p.PlanFollowups(context.Background(), "q", "", nil)
	if err != nil {
		t.Fatalf("PlanFollowups err = %v, want nil", err)
	}
	want := []string{"query alpha", "query beta", "query gamma", "query delta", "query epsilon"}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, q := range want {
		if got[i] != q {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], q)
		}
	}
}

// TestPromptQueryPlanner_FailsOpenOnModelError pins the fail-open
// contract: a model.Generate error returns (nil, nil), not an error.
// Active retrieval must keep working even when the planner model is
// flaky.
func TestPromptQueryPlanner_FailsOpenOnModelError(t *testing.T) {
	model := &scriptedPlannerModel{
		errors: []error{errors.New("planner boom")},
	}
	p := PromptQueryPlanner{Model: model}
	got, err := p.PlanFollowups(context.Background(), "q", "", nil)
	if err != nil {
		t.Fatalf("PlanFollowups err = %v, want nil (fail-open on model error)", err)
	}
	if got != nil {
		t.Fatalf("PlanFollowups got = %v, want nil on model error", got)
	}
}

// TestPromptQueryPlanner_FailsOpenOnEmptyReply pins fail-open for an
// empty (or all-whitespace) model reply: the planner returns (nil, nil)
// rather than an empty-slice success — the active-retrieval driver can
// short-circuit on nil cheaply.
func TestPromptQueryPlanner_FailsOpenOnEmptyReply(t *testing.T) {
	model := &scriptedPlannerModel{
		responses: []generate.Response{{Text: "  \n\t\n  "}},
	}
	p := PromptQueryPlanner{Model: model}
	got, err := p.PlanFollowups(context.Background(), "q", "", nil)
	if err != nil {
		t.Fatalf("PlanFollowups err = %v, want nil (fail-open on empty reply)", err)
	}
	if got != nil {
		t.Fatalf("PlanFollowups got = %v, want nil on empty reply", got)
	}
}

// TestPromptQueryPlanner_ReturnsErrorWhenModelNil pins parity with
// PromptGrader: a nil Model is the one configuration error the planner
// surfaces explicitly — callers can detect the misconfiguration without
// hunting through silent no-op behavior.
func TestPromptQueryPlanner_ReturnsErrorWhenModelNil(t *testing.T) {
	var p PromptQueryPlanner // Model nil
	_, err := p.PlanFollowups(context.Background(), "q", "", nil)
	if err == nil {
		t.Fatalf("PlanFollowups err = nil, want error for nil Model")
	}
	if !strings.Contains(err.Error(), "Model") {
		t.Fatalf("PlanFollowups err = %v, want error mentioning Model", err)
	}
}

// TestPromptQueryPlanner_RespectsMaxQueries pins that the parser caps
// output at MaxQueries even when the model emits more lines.
func TestPromptQueryPlanner_RespectsMaxQueries(t *testing.T) {
	model := &scriptedPlannerModel{
		responses: []generate.Response{
			{Text: "one\ntwo\nthree\nfour\nfive\n"},
		},
	}
	p := PromptQueryPlanner{Model: model, MaxQueries: 2}
	got, err := p.PlanFollowups(context.Background(), "q", "", nil)
	if err != nil {
		t.Fatalf("PlanFollowups err = %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (cap MaxQueries=2)", len(got))
	}
}
