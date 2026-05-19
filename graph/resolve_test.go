package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/costa92/llm-agent-rag/embed"
)

// scriptedEmbedder is a deterministic embed.Embedder for resolver tests:
// it returns a fixed vector per text. An unknown text maps to a zero
// vector (cosine similarity 0 against everything). This keeps the
// EmbeddingEntityResolver tests off any live embedding call.
type scriptedEmbedder struct {
	dim     int
	vectors map[string]embed.Vector
	err     error
}

func (s scriptedEmbedder) Dimension() int { return s.dim }

func (s scriptedEmbedder) Embed(_ context.Context, text string) (embed.Vector, error) {
	if s.err != nil {
		return nil, s.err
	}
	if v, ok := s.vectors[text]; ok {
		return v, nil
	}
	return make(embed.Vector, s.dim), nil
}

// findEntity returns the first entity with the given (original) name after
// rewriting — callers look up by the rewritten Name.
func entityNames(ents []Entity) map[string]int {
	out := map[string]int{}
	for _, e := range ents {
		out[e.Name]++
	}
	return out
}

func TestEmbeddingEntityResolverMergesNearDuplicates(t *testing.T) {
	// "Acme" and "Acme Corp" embed to near-identical vectors; "Globex"
	// embeds far away. All are orgs.
	emb := scriptedEmbedder{dim: 2, vectors: map[string]embed.Vector{
		"Acme":      {1, 0},
		"Acme Corp": {0.999, 0.0447}, // cosine ~= 0.999 vs Acme
		"Globex":    {0, 1},
	}}
	ents := []Entity{
		{Name: "Acme", Type: "org", SourceChunkIDs: []string{"c1"}},
		{Name: "Acme Corp", Type: "org", SourceChunkIDs: []string{"c2"}},
		{Name: "Globex", Type: "org", SourceChunkIDs: []string{"c3"}},
	}
	rels := []Relation{
		{Source: "Acme", Target: "Globex", Relation: "competes with"},
	}
	r := EmbeddingEntityResolver{Embedder: emb}
	gotEnts, gotRels, err := r.Resolve(context.Background(), ents, rels)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// "Acme" and "Acme Corp" collapse to the longer canonical name.
	names := entityNames(gotEnts)
	if names["Acme Corp"] != 2 {
		t.Fatalf("entity names = %v, want both Acme entities rewritten to \"Acme Corp\"", names)
	}
	if names["Globex"] != 1 {
		t.Fatalf("entity names = %v, want Globex untouched", names)
	}
	if names["Acme"] != 0 {
		t.Fatalf("entity names = %v, want no entity still named \"Acme\"", names)
	}
	// The relation endpoint followed the rewrite.
	if len(gotRels) != 1 || gotRels[0].Source != "Acme Corp" || gotRels[0].Target != "Globex" {
		t.Fatalf("relation = %+v, want Source rewritten to \"Acme Corp\"", gotRels)
	}
}

func TestEmbeddingEntityResolverSameTypeOnly(t *testing.T) {
	// "Mercury" the planet and "Mercury" the element embed identically,
	// but differ in Type — they must NOT merge even at similarity 1.
	emb := scriptedEmbedder{dim: 2, vectors: map[string]embed.Vector{
		"Mercury":      {1, 0},
		"Mercury (Hg)": {1, 0},
	}}
	ents := []Entity{
		{Name: "Mercury", Type: "planet"},
		{Name: "Mercury (Hg)", Type: "element"},
	}
	gotEnts, _, err := EmbeddingEntityResolver{Embedder: emb}.Resolve(context.Background(), ents, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	names := entityNames(gotEnts)
	if names["Mercury"] != 1 || names["Mercury (Hg)"] != 1 {
		t.Fatalf("entity names = %v, want different-type entities NOT merged", names)
	}
}

func TestEmbeddingEntityResolverKeepsUnrelatedDistinct(t *testing.T) {
	emb := scriptedEmbedder{dim: 3, vectors: map[string]embed.Vector{
		"Paris":  {1, 0, 0},
		"London": {0, 1, 0},
		"Tokyo":  {0, 0, 1},
	}}
	ents := []Entity{
		{Name: "Paris", Type: "city"},
		{Name: "London", Type: "city"},
		{Name: "Tokyo", Type: "city"},
	}
	gotEnts, _, err := EmbeddingEntityResolver{Embedder: emb}.Resolve(context.Background(), ents, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	names := entityNames(gotEnts)
	if names["Paris"] != 1 || names["London"] != 1 || names["Tokyo"] != 1 {
		t.Fatalf("entity names = %v, want all three unrelated cities distinct", names)
	}
}

func TestEmbeddingEntityResolverCanonicalPickDeterministic(t *testing.T) {
	// Three surface forms of one org, all mutually similar. The canonical
	// pick is the longest name; among equal-length names the lexically
	// lowest wins. "International Business Machines" (31) is the longest.
	emb := scriptedEmbedder{dim: 2, vectors: map[string]embed.Vector{
		"IBM":                             {1, 0},
		"I.B.M.":                          {1, 0},
		"International Business Machines": {1, 0},
	}}
	ents := []Entity{
		{Name: "International Business Machines", Type: "org"},
		{Name: "IBM", Type: "org"},
		{Name: "I.B.M.", Type: "org"},
	}
	// Run twice — output must be identical (determinism, KG3-6).
	var first map[string]int
	for i := 0; i < 2; i++ {
		gotEnts, _, err := EmbeddingEntityResolver{Embedder: emb}.Resolve(context.Background(), ents, nil)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		names := entityNames(gotEnts)
		if names["International Business Machines"] != 3 {
			t.Fatalf("entity names = %v, want all collapsed to the longest form", names)
		}
		if i == 0 {
			first = names
		} else if len(first) != len(names) {
			t.Fatalf("non-deterministic output: %v vs %v", first, names)
		}
	}
}

func TestEmbeddingEntityResolverEqualLengthTieBreak(t *testing.T) {
	// "Bob" and "Rob" are equal-length, mutually similar — the canonical
	// pick is the lexically lowest, "Bob".
	emb := scriptedEmbedder{dim: 2, vectors: map[string]embed.Vector{
		"Bob": {1, 0},
		"Rob": {1, 0},
	}}
	ents := []Entity{
		{Name: "Rob", Type: "person"},
		{Name: "Bob", Type: "person"},
	}
	gotEnts, _, err := EmbeddingEntityResolver{Embedder: emb}.Resolve(context.Background(), ents, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	names := entityNames(gotEnts)
	if names["Bob"] != 2 {
		t.Fatalf("entity names = %v, want equal-length tie broken to lexically lowest \"Bob\"", names)
	}
}

func TestEmbeddingEntityResolverThresholdRespected(t *testing.T) {
	// cosine ~= 0.8 between the two names. The default threshold (0.92)
	// keeps them apart; a low explicit threshold merges them.
	emb := scriptedEmbedder{dim: 2, vectors: map[string]embed.Vector{
		"Acme":     {1, 0},
		"Acme Inc": {0.8, 0.6},
	}}
	ents := []Entity{
		{Name: "Acme", Type: "org"},
		{Name: "Acme Inc", Type: "org"},
	}
	defaultOut, _, err := EmbeddingEntityResolver{Embedder: emb}.Resolve(context.Background(), ents, nil)
	if err != nil {
		t.Fatalf("Resolve (default threshold): %v", err)
	}
	if n := entityNames(defaultOut); n["Acme"] != 1 || n["Acme Inc"] != 1 {
		t.Fatalf("entity names = %v, want NO merge below the default threshold", n)
	}
	lowOut, _, err := EmbeddingEntityResolver{Embedder: emb, Threshold: 0.5}.Resolve(context.Background(), ents, nil)
	if err != nil {
		t.Fatalf("Resolve (low threshold): %v", err)
	}
	if n := entityNames(lowOut); n["Acme Inc"] != 2 {
		t.Fatalf("entity names = %v, want a merge with a low threshold", n)
	}
}

func TestNoopEntityResolverIsIdentity(t *testing.T) {
	ents := []Entity{
		{Name: "Acme", Type: "org"},
		{Name: "Acme Corp", Type: "org"},
	}
	rels := []Relation{{Source: "Acme", Target: "Acme Corp", Relation: "aka"}}
	gotEnts, gotRels, err := NoopEntityResolver{}.Resolve(context.Background(), ents, rels)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(gotEnts) != 2 || gotEnts[0].Name != "Acme" || gotEnts[1].Name != "Acme Corp" {
		t.Fatalf("entities = %+v, want input returned unchanged", gotEnts)
	}
	if len(gotRels) != 1 || gotRels[0].Source != "Acme" || gotRels[0].Target != "Acme Corp" {
		t.Fatalf("relations = %+v, want input returned unchanged", gotRels)
	}
}

func TestEmbeddingEntityResolverNilEmbedder(t *testing.T) {
	_, _, err := EmbeddingEntityResolver{}.Resolve(context.Background(), []Entity{{Name: "X"}}, nil)
	if !errors.Is(err, ErrEntityResolverEmbedderRequired) {
		t.Fatalf("nil embedder → %v, want ErrEntityResolverEmbedderRequired", err)
	}
}

func TestEmbeddingEntityResolverEmbedderError(t *testing.T) {
	emb := scriptedEmbedder{dim: 2, err: errors.New("boom")}
	if _, _, err := (EmbeddingEntityResolver{Embedder: emb}).Resolve(context.Background(), []Entity{{Name: "X"}}, nil); err == nil {
		t.Fatalf("embedder error: want a propagated error")
	}
}

func TestEmbeddingEntityResolverEmptyInput(t *testing.T) {
	emb := scriptedEmbedder{dim: 2}
	gotEnts, gotRels, err := EmbeddingEntityResolver{Embedder: emb}.Resolve(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(gotEnts) != 0 || len(gotRels) != 0 {
		t.Fatalf("empty input → %+v / %+v, want empty", gotEnts, gotRels)
	}
}
