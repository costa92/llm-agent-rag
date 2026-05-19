package graph

import (
	"reflect"
	"testing"
)

// pathEnt is a terse Subgraph entity constructor for the path tests.
func pathEnt(id string) Entity { return Entity{ID: id, Name: id, Type: "t"} }

// pathRel builds a directed relation with an explicit weight and optional
// provenance chunk IDs.
func pathRel(id, src, dst string, weight float64, chunks ...string) Relation {
	return Relation{
		ID:             id,
		Source:         src,
		Target:         dst,
		Relation:       "r",
		Weight:         weight,
		SourceChunkIDs: chunks,
	}
}

// shortVsLongSubgraph connects "a" and "d" two ways:
//   - a short, high-weight direct edge a—d (weight 5);
//   - a longer, low-weight detour a—b—d (weights 1 and 1).
//
// Any sensible ranker puts the short strong path first.
func shortVsLongSubgraph() Subgraph {
	return Subgraph{
		Entities: []Entity{pathEnt("a"), pathEnt("b"), pathEnt("d")},
		Relations: []Relation{
			pathRel("a::d", "a", "d", 5),
			pathRel("a::b", "a", "b", 1),
			pathRel("b::d", "b", "d", 1),
		},
		Depth: map[string]int{"a": 0, "b": 1, "d": 1},
	}
}

func TestWeightedPathRankerShortStrongPathFirst(t *testing.T) {
	sub := shortVsLongSubgraph()
	got := WeightedPathRanker{}.RankPaths(sub, [][2]string{{"a", "d"}})

	if len(got) != 2 {
		t.Fatalf("RankPaths: got %d paths, want 2: %+v", len(got), got)
	}
	// Golden: the short direct path ranks first.
	if want := []string{"a", "d"}; !reflect.DeepEqual(got[0].EntityIDs, want) {
		t.Fatalf("rank-1 EntityIDs = %v, want %v", got[0].EntityIDs, want)
	}
	if want := []string{"a", "b", "d"}; !reflect.DeepEqual(got[1].EntityIDs, want) {
		t.Fatalf("rank-2 EntityIDs = %v, want %v", got[1].EntityIDs, want)
	}
	if !(got[0].Score > got[1].Score) {
		t.Fatalf("expected short path Score (%v) > long path Score (%v)",
			got[0].Score, got[1].Score)
	}
	// Golden absolute scores: direct path = 1 edge => 5.0; detour = 2 edges
	// => 0.5 * (1*1) = 0.5.
	if got[0].Score != 5.0 {
		t.Fatalf("short path Score = %v, want 5.0", got[0].Score)
	}
	if got[1].Score != 0.5 {
		t.Fatalf("long path Score = %v, want 0.5", got[1].Score)
	}
	// RelationIDs accompany the traversal.
	if want := []string{"a::d"}; !reflect.DeepEqual(got[0].RelationIDs, want) {
		t.Fatalf("rank-1 RelationIDs = %v, want %v", got[0].RelationIDs, want)
	}
	if want := []string{"a::b", "b::d"}; !reflect.DeepEqual(got[1].RelationIDs, want) {
		t.Fatalf("rank-2 RelationIDs = %v, want %v", got[1].RelationIDs, want)
	}
}

func TestWeightedPathRankerDeterministic(t *testing.T) {
	sub := shortVsLongSubgraph()
	pairs := [][2]string{{"a", "d"}, {"d", "a"}, {"a", "b"}}
	first := WeightedPathRanker{}.RankPaths(sub, pairs)
	second := WeightedPathRanker{}.RankPaths(sub, pairs)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("ranking not byte-identical across runs:\n first  = %+v\n second = %+v",
			first, second)
	}
}

func TestWeightedPathRankerProvenanceOverlap(t *testing.T) {
	// Two two-hop routes from "a" to "d" with identical structure and edge
	// weights; the only difference is provenance. The "x" route's two hops
	// are co-attested by a shared chunk "c1"; the "y" route's hops cite
	// disjoint chunks. The co-attested route must score higher.
	sub := Subgraph{
		Entities: []Entity{pathEnt("a"), pathEnt("d"), pathEnt("x"), pathEnt("y")},
		Relations: []Relation{
			pathRel("a::x", "a", "x", 1, "c1"),
			pathRel("x::d", "x", "d", 1, "c1"),
			pathRel("a::y", "a", "y", 1, "c2"),
			pathRel("y::d", "y", "d", 1, "c3"),
		},
		Depth: map[string]int{"a": 0, "x": 1, "y": 1, "d": 2},
	}
	got := WeightedPathRanker{}.RankPaths(sub, [][2]string{{"a", "d"}})
	if len(got) != 2 {
		t.Fatalf("RankPaths: got %d paths, want 2: %+v", len(got), got)
	}
	// The co-attested ("x") route ranks first.
	if want := []string{"a", "x", "d"}; !reflect.DeepEqual(got[0].EntityIDs, want) {
		t.Fatalf("rank-1 EntityIDs = %v, want %v (co-attested route)",
			got[0].EntityIDs, want)
	}
	if !(got[0].Score > got[1].Score) {
		t.Fatalf("expected co-attested path Score (%v) > scattered path Score (%v)",
			got[0].Score, got[1].Score)
	}
	// Golden: scattered route = 0.5 * 1 * 1 = 0.5; co-attested route gets
	// the (1 + provenanceBonus) factor => 0.5 * 1.1 = 0.55.
	if got[1].Score != 0.5 {
		t.Fatalf("scattered path Score = %v, want 0.5", got[1].Score)
	}
	if got[0].Score != 0.55 {
		t.Fatalf("co-attested path Score = %v, want 0.55", got[0].Score)
	}
}

func TestWeightedPathRankerNoPathBetweenPair(t *testing.T) {
	// "a"—"b" connected; "z" is an isolated entity. The (a,z) pair has no
	// path — no RankedPath, no panic.
	sub := Subgraph{
		Entities:  []Entity{pathEnt("a"), pathEnt("b"), pathEnt("z")},
		Relations: []Relation{pathRel("a::b", "a", "b", 1)},
		Depth:     map[string]int{"a": 0, "b": 1, "z": 0},
	}
	got := WeightedPathRanker{}.RankPaths(sub, [][2]string{{"a", "z"}})
	if len(got) != 0 {
		t.Fatalf("RankPaths over a disconnected pair = %+v, want empty", got)
	}
}

func TestWeightedPathRankerEmptyInputs(t *testing.T) {
	// Empty subgraph.
	if got := (WeightedPathRanker{}).RankPaths(Subgraph{}, [][2]string{{"a", "b"}}); len(got) != 0 {
		t.Fatalf("empty subgraph: got %+v, want empty", got)
	}
	// Empty seedPairs.
	if got := (WeightedPathRanker{}).RankPaths(shortVsLongSubgraph(), nil); len(got) != 0 {
		t.Fatalf("nil seedPairs: got %+v, want empty", got)
	}
}

func TestWeightedPathRankerMaxPathLenCap(t *testing.T) {
	// "a"—"b"—"c"—"d" is a chain: the (a,d) pair is reachable only via 3
	// edges, beyond the maxPathLen cap of 2 — no path is returned.
	sub := Subgraph{
		Entities: []Entity{pathEnt("a"), pathEnt("b"), pathEnt("c"), pathEnt("d")},
		Relations: []Relation{
			pathRel("a::b", "a", "b", 1),
			pathRel("b::c", "b", "c", 1),
			pathRel("c::d", "c", "d", 1),
		},
		Depth: map[string]int{"a": 0, "b": 1, "c": 2, "d": 3},
	}
	got := WeightedPathRanker{}.RankPaths(sub, [][2]string{{"a", "d"}})
	if len(got) != 0 {
		t.Fatalf("3-edge-only pair beyond maxPathLen: got %+v, want empty", got)
	}
	// A 2-edge pair on the same chain is still found.
	got2 := WeightedPathRanker{}.RankPaths(sub, [][2]string{{"a", "c"}})
	if len(got2) != 1 {
		t.Fatalf("2-edge pair (a,c): got %d paths, want 1: %+v", len(got2), got2)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got2[0].EntityIDs, want) {
		t.Fatalf("(a,c) path = %v, want %v", got2[0].EntityIDs, want)
	}
}

func TestWeightedPathRankerSimplePathNoRepeatedEntity(t *testing.T) {
	// A triangle a—b—c with a direct a—c edge. Paths from a to c within 2
	// edges: the direct a—c and the detour a—b—c. Neither repeats an
	// entity; there is no a—b—a—c style walk.
	sub := Subgraph{
		Entities: []Entity{pathEnt("a"), pathEnt("b"), pathEnt("c")},
		Relations: []Relation{
			pathRel("a::b", "a", "b", 1),
			pathRel("b::c", "b", "c", 1),
			pathRel("a::c", "a", "c", 1),
		},
		Depth: map[string]int{"a": 0, "b": 1, "c": 1},
	}
	got := WeightedPathRanker{}.RankPaths(sub, [][2]string{{"a", "c"}})
	if len(got) != 2 {
		t.Fatalf("triangle (a,c): got %d paths, want exactly 2: %+v", len(got), got)
	}
	for _, p := range got {
		seen := map[string]bool{}
		for _, id := range p.EntityIDs {
			if seen[id] {
				t.Fatalf("path %v repeats entity %q — not a simple path", p.EntityIDs, id)
			}
			seen[id] = true
		}
	}
}
