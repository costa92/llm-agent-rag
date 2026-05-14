//go:build llmagent

package llmagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corellm "github.com/costa92/llm-agent/llm"
	ragcore "github.com/costa92/llm-agent-rag/rag"
)

func TestAsToolNamespaceIsolation(t *testing.T) {
	sys := ragcore.New(ragcore.Options{Model: ModelAdapter{
		Inner: corellm.NewScriptedLLM(corellm.WithResponses(corellm.Response{Text: "ok"})),
	}})
	tool := AsTool(sys)
	ctx := context.Background()

	_, err := tool.Execute(ctx, []byte(`{"action":"add_text","text":"go modules belong to alpha","namespace":"alpha"}`))
	if err != nil {
		t.Fatalf("add_text alpha: %v", err)
	}
	_, err = tool.Execute(ctx, []byte(`{"action":"add_text","text":"rust cargo belongs to beta","namespace":"beta"}`))
	if err != nil {
		t.Fatalf("add_text beta: %v", err)
	}

	out, err := tool.Execute(ctx, []byte(`{"action":"search","query":"go modules","namespace":"alpha","top_k":5}`))
	if err != nil {
		t.Fatalf("search alpha: %v", err)
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("alpha search missing alpha content: %s", out)
	}
	if strings.Contains(out, "beta") {
		t.Fatalf("alpha search leaked beta content: %s", out)
	}
}

func TestAsToolSchemaIsValidJSON(t *testing.T) {
	sys := ragcore.New(ragcore.Options{})
	tool := AsTool(sys)
	var v map[string]any
	if err := json.Unmarshal(tool.Schema(), &v); err != nil {
		t.Fatalf("schema not valid JSON: %v", err)
	}
}
