//go:build llmagent

package llmagent

import (
	"context"
	"testing"

	corellm "github.com/costa92/llm-agent/llm"
	"github.com/costa92/llm-agent-rag/generate"
)

func TestModelAdapterGenerate(t *testing.T) {
	model := corellm.NewScriptedLLM(corellm.WithResponses(corellm.Response{Text: "ok"}))
	adapter := ModelAdapter{Inner: model}
	resp, err := adapter.Generate(context.Background(), generate.Request{
		SystemPrompt: "be concise",
		Messages:     []generate.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Generate(): %v", err)
	}
	if resp.Text != "ok" {
		t.Fatalf("Text = %q, want ok", resp.Text)
	}
}
