package prompt

import (
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/store"
)

func TestDefaultQATemplateRender(t *testing.T) {
	req, err := DefaultQATemplate{}.Render(context.Background(), RenderContext{
		Question: "Where is Paris?",
		Hits: []store.Hit{{Chunk: store.StoredChunk{ID: "doc1#chunk-0", Content: "Paris is in France."}}},
	})
	if err != nil {
		t.Fatalf("Render(): %v", err)
	}
	if req.SystemPrompt == "" {
		t.Fatal("SystemPrompt empty")
	}
	if len(req.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(req.Messages))
	}
	if !strings.Contains(req.Messages[0].Content, "doc1#chunk-0") {
		t.Fatalf("rendered content missing chunk id: %q", req.Messages[0].Content)
	}
}
