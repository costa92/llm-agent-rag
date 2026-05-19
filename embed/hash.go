package embed

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
)

// HashEmbedder is a deterministic, dependency-free Embedder that hashes tokens
// into a fixed-dimension bag-of-words vector. It is the default embedder for
// tests and offline runs — it needs no model and produces stable output.
type HashEmbedder struct {
	Dim int // Dim is the embedding dimension (vector length).
}

// NewHashEmbedder returns a HashEmbedder producing vectors of the given
// dimension. A dim <= 0 selects a sane default (32).
func NewHashEmbedder(dim int) *HashEmbedder {
	if dim <= 0 {
		dim = 32
	}
	return &HashEmbedder{Dim: dim}
}

// Dimension reports the embedding dimension.
func (h *HashEmbedder) Dimension() int { return h.Dim }

// Embed returns the deterministic hash-bucket embedding of text.
func (h *HashEmbedder) Embed(_ context.Context, text string) (Vector, error) {
	v := make(Vector, h.Dim)
	for _, tok := range tokenize(text) {
		idx := bucketFor(tok, h.Dim)
		v[idx]++
	}
	normalize(v)
	return v, nil
}

// CosineSimilarity returns the cosine similarity of a and b, clamped to
// [0, 1]. Mismatched lengths, empty vectors, or zero-magnitude vectors yield 0.
func CosineSimilarity(a, b Vector) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	sim := dot / (math.Sqrt(na) * math.Sqrt(nb))
	if sim < 0 {
		return 0
	}
	if sim > 1 {
		return 1
	}
	return sim
}

func tokenize(text string) []string {
	text = strings.ToLower(text)
	out := make([]string, 0, len(text)/4)
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func bucketFor(tok string, dim int) int {
	h := fnv.New64a()
	_, _ = h.Write([]byte(tok))
	return int(h.Sum64() % uint64(dim))
}

func normalize(v Vector) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	mag := float32(math.Sqrt(sum))
	for i := range v {
		v[i] /= mag
	}
}
