package embed

import (
	"context"
	"errors"
	"testing"
)

// fakeBatchEmbedder is a deterministic BatchEmbedder used to pin the
// interface shape and to verify the import fast path. It returns one
// distinct fixed-dimension Vector per input text, in input order.
type fakeBatchEmbedder struct {
	dim  int
	calls int
}

func (f *fakeBatchEmbedder) Embed(_ context.Context, text string) (Vector, error) {
	v := make(Vector, f.dim)
	if f.dim > 0 {
		// Encode a stable signature of the text into the first slot so
		// callers can verify ordering. Use len(text) as the signature.
		v[0] = float32(len(text))
	}
	return v, nil
}

func (f *fakeBatchEmbedder) Dimension() int { return f.dim }

func (f *fakeBatchEmbedder) EmbedBatch(ctx context.Context, texts []string) ([]Vector, error) {
	f.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]Vector, len(texts))
	for i, t := range texts {
		v := make(Vector, f.dim)
		if f.dim > 0 {
			v[0] = float32(len(t))
		}
		out[i] = v
	}
	return out, nil
}

// TestBatchEmbedderInterfaceShape pins the BatchEmbedder interface
// signature at compile time: any drift in the method set fails the
// build. This is the v1.0.2 additive-capability contract.
func TestBatchEmbedderInterfaceShape(t *testing.T) {
	var _ BatchEmbedder = (*fakeBatchEmbedder)(nil)
}

// TestBatchEmbedderReturnsVectorsInInputOrder asserts the batch path
// returns one Vector per input text, in the same order as the inputs.
func TestBatchEmbedderReturnsVectorsInInputOrder(t *testing.T) {
	be := &fakeBatchEmbedder{dim: 4}
	texts := []string{"a", "bb", "ccc", "dddd"}
	got, err := be.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(got) != len(texts) {
		t.Fatalf("len(EmbedBatch) = %d, want %d", len(got), len(texts))
	}
	for i, t1 := range texts {
		if got[i][0] != float32(len(t1)) {
			t.Fatalf("vector[%d][0] = %v, want %v (input order broken)", i, got[i][0], float32(len(t1)))
		}
	}
}

// TestBatchEmbedderPreservesContextCancellation asserts that a
// pre-cancelled context propagates out of EmbedBatch as the cancellation
// error — the batch path must not silently mask ctx.Err().
func TestBatchEmbedderPreservesContextCancellation(t *testing.T) {
	be := &fakeBatchEmbedder{dim: 4}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := be.EmbedBatch(ctx, []string{"x"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
