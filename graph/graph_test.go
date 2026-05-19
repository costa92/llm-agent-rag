package graph

import "testing"

func TestGraphTypesCompose(t *testing.T) {
	g := Graph{
		Entities:  []Entity{{ID: "person:ada", Name: "Ada", Type: "person"}},
		Relations: []Relation{{ID: "r1", Source: "person:ada", Target: "org:acme", Relation: "works_at"}},
	}
	if len(g.Entities) != 1 || len(g.Relations) != 1 {
		t.Fatalf("Graph did not hold its members: %+v", g)
	}
}
