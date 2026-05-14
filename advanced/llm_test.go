package advanced

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
)

type scriptedModel struct {
	resp string
	err  error
}

func (m scriptedModel) Generate(_ context.Context, _ generate.Request) (generate.Response, error) {
	if m.err != nil {
		return generate.Response{}, m.err
	}
	return generate.Response{Text: m.resp}, nil
}

func TestExpandQueryDedupesAndStripsNumbering(t *testing.T) {
	got, err := ExpandQuery(context.Background(), scriptedModel{
		resp: "1. go dependency management\n- Go modules\nmodule system\n\nmodule system",
	}, "go modules", 3)
	if err != nil {
		t.Fatalf("ExpandQuery(): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0] != "go dependency management" || got[1] != "module system" {
		t.Fatalf("got = %#v", got)
	}
}

func TestExpandQueryRequiresModel(t *testing.T) {
	_, err := ExpandQuery(context.Background(), nil, "go modules", 2)
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

func TestGenerateHypotheticalTrimsWhitespace(t *testing.T) {
	got, err := GenerateHypothetical(context.Background(), scriptedModel{
		resp: "  Go modules manage dependencies.  \n",
	}, "what are go modules?")
	if err != nil {
		t.Fatalf("GenerateHypothetical(): %v", err)
	}
	if !strings.Contains(got, "modules") {
		t.Fatalf("got = %q", got)
	}
	if strings.HasPrefix(got, " ") || strings.HasSuffix(got, "\n") {
		t.Fatalf("got not trimmed: %q", got)
	}
}
