package embed

import (
	"context"
	"testing"
)

func TestHashEmbedderDefaultDimension(t *testing.T) {
	h := NewHashEmbedder(0)
	if got := h.Dimension(); got != 32 {
		t.Fatalf("Dimension() = %d, want 32", got)
	}
}

func TestHashEmbedderDeterministic(t *testing.T) {
	h := NewHashEmbedder(8)
	a, _ := h.Embed(context.Background(), "hello world")
	b, _ := h.Embed(context.Background(), "hello world")
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("vector mismatch at %d", i)
		}
	}
}
