package graph

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
)

// twoClusterGraph builds a fixed graph with two dense triangles —
// {a1,a2,a3} and {b1,b2,b3} — joined by a single bridge edge a1—b1. Any
// sane community detector splits it into exactly those two clusters.
func twoClusterGraph() Graph {
	ent := func(id string) Entity { return Entity{ID: id, Name: id, Type: "t"} }
	rel := func(s, d string) Relation {
		return Relation{ID: s + "::" + d, Source: s, Target: d, Relation: "r", Weight: 1}
	}
	return Graph{
		Entities: []Entity{
			ent("a1"), ent("a2"), ent("a3"),
			ent("b1"), ent("b2"), ent("b3"),
		},
		Relations: []Relation{
			rel("a1", "a2"), rel("a2", "a3"), rel("a1", "a3"),
			rel("b1", "b2"), rel("b2", "b3"), rel("b1", "b3"),
			rel("a1", "b1"), // the single bridge
		},
	}
}

// memberSets returns the sorted set of sorted entity-ID slices for the
// communities at the given level — order-independent cluster comparison.
func memberSets(comms []Community, level int) [][]string {
	var sets [][]string
	for _, c := range comms {
		if c.Level == level {
			ids := append([]string(nil), c.EntityIDs...)
			sort.Strings(ids)
			sets = append(sets, ids)
		}
	}
	sort.Slice(sets, func(i, j int) bool {
		return fmt.Sprint(sets[i]) < fmt.Sprint(sets[j])
	})
	return sets
}

func TestLouvainTwoClusters(t *testing.T) {
	g := twoClusterGraph()
	comms, err := LouvainDetector{}.Detect(context.Background(), g)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	got := memberSets(comms, 0)
	want := [][]string{{"a1", "a2", "a3"}, {"b1", "b2", "b3"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("level-0 communities = %v, want %v", got, want)
	}

	// IDs are the deterministic L{level}-{lowestMember} rule.
	byID := map[string]Community{}
	for _, c := range comms {
		byID[c.ID] = c
	}
	if _, ok := byID["L0-a1"]; !ok {
		t.Fatalf("expected community ID L0-a1; got %v", commIDs(comms))
	}
	if _, ok := byID["L0-b1"]; !ok {
		t.Fatalf("expected community ID L0-b1; got %v", commIDs(comms))
	}

	// RelationIDs: each cluster's three intra-edges, the bridge in neither.
	a := byID["L0-a1"]
	wantRels := []string{"a1::a2", "a1::a3", "a2::a3"}
	if !reflect.DeepEqual(a.RelationIDs, wantRels) {
		t.Fatalf("L0-a1 RelationIDs = %v, want %v", a.RelationIDs, wantRels)
	}
}

func commIDs(comms []Community) []string {
	ids := make([]string, len(comms))
	for i, c := range comms {
		ids[i] = c.ID
	}
	return ids
}

func TestLouvainDeterministic(t *testing.T) {
	g := twoClusterGraph()
	d := LouvainDetector{Resolution: 1.0}
	first, err := d.Detect(context.Background(), g)
	if err != nil {
		t.Fatalf("Detect #1: %v", err)
	}
	second, err := d.Detect(context.Background(), g)
	if err != nil {
		t.Fatalf("Detect #2: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Detect is non-deterministic:\n #1 = %#v\n #2 = %#v", first, second)
	}
}

func TestLabelPropagationTwoClusters(t *testing.T) {
	g := twoClusterGraph()
	comms, err := LabelPropagationDetector{}.Detect(context.Background(), g)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	got := memberSets(comms, 0)
	want := [][]string{{"a1", "a2", "a3"}, {"b1", "b2", "b3"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("label-propagation communities = %v, want %v", got, want)
	}
	// Single level only.
	for _, c := range comms {
		if c.Level != 0 {
			t.Fatalf("label propagation produced level %d, want only 0", c.Level)
		}
	}
}

func TestLabelPropagationDeterministic(t *testing.T) {
	g := twoClusterGraph()
	first, err := LabelPropagationDetector{}.Detect(context.Background(), g)
	if err != nil {
		t.Fatalf("Detect #1: %v", err)
	}
	second, err := LabelPropagationDetector{}.Detect(context.Background(), g)
	if err != nil {
		t.Fatalf("Detect #2: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("label propagation non-deterministic:\n #1 = %#v\n #2 = %#v", first, second)
	}
}

// fourClusterGraph builds four dense triangles. Within each pair of clusters
// the bridges are stronger than the single edge joining the two pairs, so
// Louvain coarsens into a multi-level hierarchy.
func fourClusterGraph() Graph {
	ent := func(id string) Entity { return Entity{ID: id, Name: id, Type: "t"} }
	rel := func(s, d string, w float64) Relation {
		return Relation{ID: s + "::" + d, Source: s, Target: d, Relation: "r", Weight: w}
	}
	var ents []Entity
	var rels []Relation
	for _, p := range []string{"a", "b", "c", "d"} {
		n1, n2, n3 := p+"1", p+"2", p+"3"
		ents = append(ents, ent(n1), ent(n2), ent(n3))
		rels = append(rels,
			rel(n1, n2, 5), rel(n2, n3, 5), rel(n1, n3, 5))
	}
	// Pair (a,b) and pair (c,d): a moderate bridge inside each pair, a weak
	// bridge across pairs — so the hierarchy has a meaningful coarse level.
	rels = append(rels,
		rel("a1", "b1", 2),
		rel("c1", "d1", 2),
		rel("b3", "c3", 1),
	)
	return Graph{Entities: ents, Relations: rels}
}

func TestLouvainHierarchy(t *testing.T) {
	g := fourClusterGraph()
	comms, err := LouvainDetector{}.Detect(context.Background(), g)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	maxLevel := 0
	byID := map[string]Community{}
	for _, c := range comms {
		byID[c.ID] = c
		if c.Level > maxLevel {
			maxLevel = c.Level
		}
	}
	if maxLevel < 1 {
		t.Fatalf("expected a multi-level hierarchy, got max level %d: %v", maxLevel, commIDs(comms))
	}

	// Every non-top-level community must point at a real higher-level
	// community whose entity set is a superset of the child's.
	for _, c := range comms {
		if c.Level == maxLevel {
			if c.ParentID != "" {
				t.Fatalf("top-level community %s has ParentID %q, want empty", c.ID, c.ParentID)
			}
			continue
		}
		if c.ParentID == "" {
			t.Fatalf("non-top community %s (level %d) has no ParentID", c.ID, c.Level)
		}
		parent, ok := byID[c.ParentID]
		if !ok {
			t.Fatalf("community %s ParentID %q resolves to no community", c.ID, c.ParentID)
		}
		if parent.Level != c.Level+1 {
			t.Fatalf("community %s (level %d) parent %s is level %d, want %d",
				c.ID, c.Level, parent.ID, parent.Level, c.Level+1)
		}
		pset := map[string]bool{}
		for _, e := range parent.EntityIDs {
			pset[e] = true
		}
		for _, e := range c.EntityIDs {
			if !pset[e] {
				t.Fatalf("community %s member %s not in parent %s", c.ID, e, parent.ID)
			}
		}
	}
}

func TestDetectorsEmptyAndSingleton(t *testing.T) {
	detectors := map[string]CommunityDetector{
		"louvain":          LouvainDetector{},
		"labelpropagation": LabelPropagationDetector{},
	}
	for name, d := range detectors {
		// Empty graph: no panic, no communities.
		empty, err := d.Detect(context.Background(), Graph{})
		if err != nil {
			t.Fatalf("%s empty graph: %v", name, err)
		}
		if len(empty) != 0 {
			t.Fatalf("%s empty graph yielded %d communities, want 0", name, len(empty))
		}

		// Single isolated entity: exactly one community holding it.
		single := Graph{Entities: []Entity{{ID: "solo", Name: "solo", Type: "t"}}}
		got, err := d.Detect(context.Background(), single)
		if err != nil {
			t.Fatalf("%s single entity: %v", name, err)
		}
		if len(got) != 1 {
			t.Fatalf("%s single entity yielded %d communities, want 1: %v", name, len(got), got)
		}
		if !reflect.DeepEqual(got[0].EntityIDs, []string{"solo"}) {
			t.Fatalf("%s single-entity community members = %v, want [solo]", name, got[0].EntityIDs)
		}
		if got[0].Level != 0 {
			t.Fatalf("%s single-entity community level = %d, want 0", name, got[0].Level)
		}
	}
}
