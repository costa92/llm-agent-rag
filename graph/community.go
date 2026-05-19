package graph

import (
	"context"
	"fmt"
	"sort"
)

// Community is one cluster in a detected community hierarchy. Level 0 is the
// finest partition; higher levels group lower-level communities into
// communities-of-communities. IDs are a deterministic function of the
// level and the cluster's members, so a given graph always yields the same
// hierarchy — community detection is reproducible, golden-testable output.
type Community struct {
	ID          string   // ID is the deterministic community identifier.
	Level       int      // Level is the hierarchy level; 0 is the finest partition.
	ParentID    string   // ParentID is the enclosing community's ID; "" at the top level.
	EntityIDs   []string // EntityIDs are the member entity IDs, always sorted.
	RelationIDs []string // RelationIDs are the member relation IDs, always sorted (level 0 only).
}

// CommunityDetector partitions a Graph into a community hierarchy.
// Implementations are deterministic: the same graph always yields the same
// []Community, byte-for-byte. LouvainDetector is the hierarchical default;
// LabelPropagationDetector is the simpler single-level alternative.
type CommunityDetector interface {
	// Detect partitions g into a deterministic community hierarchy.
	Detect(ctx context.Context, g Graph) ([]Community, error)
}

// LabelPropagationDetector is a deterministic label-propagation community
// detector. Each entity starts in its own community; entities are then
// visited in sorted ID order and each is moved to the community most common
// among its neighbors (ties broken by lowest community ID). The sweep
// repeats until no entity moves or a fixed iteration cap is hit. It produces
// a single level (Level 0) — no hierarchy.
type LabelPropagationDetector struct{}

// labelPropagationMaxIters caps the propagation sweeps so a pathological
// graph cannot loop forever; convergence is typically far quicker.
const labelPropagationMaxIters = 100

// Detect implements CommunityDetector.
func (LabelPropagationDetector) Detect(_ context.Context, g Graph) ([]Community, error) {
	ids := entityIDs(g)
	if len(ids) == 0 {
		return nil, nil
	}

	adj := buildAdjacency(g)

	// label[id] is the current community label of each entity. Every entity
	// starts as its own singleton community.
	label := make(map[string]string, len(ids))
	for _, id := range ids {
		label[id] = id
	}

	for iter := 0; iter < labelPropagationMaxIters; iter++ {
		moved := false
		for _, id := range ids {
			best, ok := dominantNeighborLabel(adj[id], label, label[id])
			if ok && best != label[id] {
				label[id] = best
				moved = true
			}
		}
		if !moved {
			break
		}
	}

	return communitiesFromLabels(g, label, 0, ""), nil
}

// dominantNeighborLabel returns the label an entity should adopt given its
// neighbors' current labels — the label carried by the greatest total
// incident edge weight. Determinism and stability are both required:
//   - the candidate scan runs over a sorted label slice — no map-order leak;
//   - on a weight tie the entity keeps its current label (cur) when cur is
//     among the maxima — a node anchored to its community is not dislodged
//     by an equal pull;
//   - otherwise the tie breaks toward the maximal label sharing the longest
//     common prefix with cur, then toward the lexically lowest. Preferring a
//     label most like the entity's own keeps a single bridge edge from
//     leaking a node into the cluster on the far side of the bridge, which
//     is what stops label propagation from collapsing separable clusters
//     into one community.
//
// The bool is false when the entity has no neighbors.
func dominantNeighborLabel(neighbors map[string]float64, label map[string]string, cur string) (string, bool) {
	if len(neighbors) == 0 {
		return "", false
	}
	weightByLabel := map[string]float64{}
	for nbr, w := range neighbors {
		weightByLabel[label[nbr]] += w
	}
	// Drain into a sorted slice so iteration is deterministic.
	labels := make([]string, 0, len(weightByLabel))
	for l := range weightByLabel {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	// Highest incident weight over any single label.
	maxW := weightByLabel[labels[0]]
	for _, l := range labels[1:] {
		if weightByLabel[l] > maxW {
			maxW = weightByLabel[l]
		}
	}
	// Stay put if the current label is among the maxima.
	if weightByLabel[cur] == maxW {
		return cur, true
	}
	// Pick the maximal label closest to cur (longest shared prefix), then
	// lexically lowest. labels is sorted, so the first qualifying label at
	// the best prefix length is the lowest.
	best := ""
	bestPrefix := -1
	for _, l := range labels {
		if weightByLabel[l] != maxW {
			continue
		}
		if p := commonPrefixLen(l, cur); p > bestPrefix {
			best, bestPrefix = l, p
		}
	}
	return best, true
}

// commonPrefixLen returns the number of leading bytes a and b share.
func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// entityIDs returns the graph's entity IDs in sorted order — the canonical
// iteration order for every detector.
func entityIDs(g Graph) []string {
	ids := make([]string, 0, len(g.Entities))
	for _, e := range g.Entities {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	return ids
}

// edgeWeight is the shared weight convention: Relation.Weight when positive,
// otherwise 1.0.
func edgeWeight(r Relation) float64 {
	if r.Weight > 0 {
		return r.Weight
	}
	return 1.0
}

// buildAdjacency builds an undirected weighted adjacency map over entity IDs.
// Each relation contributes its weight to both endpoints; a self-loop or a
// relation whose endpoint is not a known entity is skipped. Parallel edges
// sum.
func buildAdjacency(g Graph) map[string]map[string]float64 {
	known := make(map[string]bool, len(g.Entities))
	for _, e := range g.Entities {
		known[e.ID] = true
	}
	adj := make(map[string]map[string]float64, len(g.Entities))
	for id := range known {
		adj[id] = map[string]float64{}
	}
	for _, r := range g.Relations {
		if r.Source == r.Target || !known[r.Source] || !known[r.Target] {
			continue
		}
		w := edgeWeight(r)
		adj[r.Source][r.Target] += w
		adj[r.Target][r.Source] += w
	}
	return adj
}

// communityID is the deterministic community-ID rule: a function of the
// level and the lowest-sorted member ID. members must be non-empty and
// sorted.
func communityID(level int, members []string) string {
	return fmt.Sprintf("L%d-%s", level, members[0])
}

// communitiesFromLabels turns a label map (entityID -> community label) into
// sorted []Community at the given level. Each distinct label becomes one
// community; EntityIDs, RelationIDs and the community slice itself are all
// sorted. parentID is applied to every emitted community (used when a
// coarsening pass attributes a parent). RelationIDs are filled at level 0
// only — a relation belongs to a community when both endpoints carry the
// same label.
func communitiesFromLabels(g Graph, label map[string]string, level int, parentID string) []Community {
	// Group entity IDs by label.
	members := map[string][]string{}
	for id, l := range label {
		members[l] = append(members[l], id)
	}

	// Stable community ID per label, computed from sorted members.
	idByLabel := make(map[string]string, len(members))
	for l, ids := range members {
		sort.Strings(ids)
		members[l] = ids
		idByLabel[l] = communityID(level, ids)
	}

	// Relations belong to their community at level 0.
	rels := map[string][]string{}
	if level == 0 {
		for _, r := range g.Relations {
			ls, okS := label[r.Source]
			lt, okT := label[r.Target]
			if okS && okT && ls == lt {
				rels[ls] = append(rels[ls], r.ID)
			}
		}
	}

	out := make([]Community, 0, len(members))
	for l, ids := range members {
		relIDs := rels[l]
		sort.Strings(relIDs)
		out = append(out, Community{
			ID:          idByLabel[l],
			Level:       level,
			ParentID:    parentID,
			EntityIDs:   ids,
			RelationIDs: relIDs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
