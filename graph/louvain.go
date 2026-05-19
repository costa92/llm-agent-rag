package graph

import (
	"context"
	"sort"
)

// LouvainDetector is a deterministic, pure-stdlib Louvain community detector.
// It runs the standard two-phase Louvain method — (1) local modularity-gain
// greedy moves, then (2) coarsening the resulting communities into
// super-nodes — and repeats. Each coarsening pass yields one hierarchy
// level: Level 0 is the finest partition; each higher level groups the
// communities below it, linked by ParentID.
//
// Determinism is total and intentional: nodes are visited in sorted ID
// order, every map is drained into a sorted slice before iteration, and
// modularity-gain ties break toward the lowest community ID. There is no
// randomness and no random restarts — the same graph always yields the same
// hierarchy.
type LouvainDetector struct {
	// Resolution scales the modularity null-model term. Values > 1 favor
	// smaller communities, < 1 favor larger ones. Resolution <= 0 is
	// treated as 1.0 (classic modularity).
	Resolution float64
}

// louvainMaxLevels caps the hierarchy depth so a degenerate graph cannot
// coarsen forever; in practice coarsening stops far sooner (when a pass
// makes no merges).
const louvainMaxLevels = 64

// louvainMaxPassIters caps the greedy-move sweeps within a single level.
const louvainMaxPassIters = 100

// node is one super-node in a coarsened working graph. members are the
// finest-level (Level 0) entity IDs collapsed into it.
type louvainNode struct {
	id      string   // working ID, == members[0] of this super-node
	members []string // finest-level entity IDs, sorted
}

// Detect implements CommunityDetector. It returns the full community
// hierarchy: every level from 0 (finest) upward, sorted by ID.
func (d LouvainDetector) Detect(_ context.Context, g Graph) ([]Community, error) {
	res := d.Resolution
	if res <= 0 {
		res = 1.0
	}

	ids := entityIDs(g)
	if len(ids) == 0 {
		return nil, nil
	}

	// Working graph for the current level. Initially each entity is its own
	// super-node; adjacency carries summed undirected edge weights.
	nodes := make([]louvainNode, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, louvainNode{id: id, members: []string{id}})
	}
	adj := buildAdjacency(g)

	// levels[i] holds the communities detected at hierarchy level i.
	var levels [][]Community

	for level := 0; level < louvainMaxLevels; level++ {
		// Partition the current working graph by modularity-gain moves.
		partition := louvainPass(nodes, adj, res)

		// Project the working-graph partition down onto the finest-level
		// entity IDs: every entity is labeled by the community its
		// super-node was assigned to.
		entityLabel := make(map[string]string, len(ids))
		for _, n := range nodes {
			label := partition[n.id]
			for _, m := range n.members {
				entityLabel[m] = label
			}
		}

		levelComms := communitiesFromLabels(g, entityLabel, level, "")
		levels = append(levels, levelComms)

		// Coarsen for the next level. If this pass made no merges
		// (one community per working node) the hierarchy has converged —
		// emit this level and stop.
		if len(levelComms) >= len(nodes) {
			break
		}
		nodes, adj = coarsen(nodes, adj, partition)
	}

	// Wire ParentID: each level-N community's parent is the level-(N+1)
	// community that contains the same finest entities. The top level keeps
	// ParentID "".
	for i := 0; i+1 < len(levels); i++ {
		assignParents(levels[i], levels[i+1])
	}

	// Flatten, sorted by (level, ID), for deterministic output.
	var all []Community
	for _, lvl := range levels {
		all = append(all, lvl...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Level != all[j].Level {
			return all[i].Level < all[j].Level
		}
		return all[i].ID < all[j].ID
	})
	return all, nil
}

// louvainPass runs greedy modularity-gain moves over a working graph until
// no node moves (or the iteration cap). It returns a partition map:
// workingNodeID -> the representative ID of the community it belongs to.
// The representative is the lowest working-node ID in the community, so the
// partition is a deterministic function of the inputs.
func louvainPass(nodes []louvainNode, adj map[string]map[string]float64, resolution float64) map[string]string {
	// community[nodeID] = current community key (a node ID acting as the key)
	community := make(map[string]string, len(nodes))
	for _, n := range nodes {
		community[n.id] = n.id
	}

	// degree[nodeID] = summed incident edge weight (self-loops counted once
	// per the 2m convention below).
	degree := make(map[string]float64, len(nodes))
	twoM := 0.0
	for _, n := range nodes {
		for _, w := range adj[n.id] {
			degree[n.id] += w
			twoM += w
		}
	}
	if twoM == 0 {
		// No edges — every node stays its own community.
		return community
	}

	// commDegree[communityKey] = summed degree of the community's members.
	commDegree := make(map[string]float64, len(nodes))
	for _, n := range nodes {
		commDegree[community[n.id]] += degree[n.id]
	}

	order := make([]string, 0, len(nodes))
	for _, n := range nodes {
		order = append(order, n.id)
	}
	sort.Strings(order)

	for iter := 0; iter < louvainMaxPassIters; iter++ {
		moved := false
		for _, nodeID := range order {
			cur := community[nodeID]

			// Sum of edge weight from nodeID into each candidate community.
			toComm := map[string]float64{}
			for nbr, w := range adj[nodeID] {
				if nbr == nodeID {
					continue
				}
				toComm[community[nbr]] += w
			}

			// Remove nodeID from its current community before evaluating
			// gains — classic Louvain isolates the node first.
			commDegree[cur] -= degree[nodeID]

			// Evaluate candidate communities in sorted order: the node's
			// own (now isolated) community plus every neighbor community.
			candidates := map[string]bool{cur: true}
			for c := range toComm {
				candidates[c] = true
			}
			candList := make([]string, 0, len(candidates))
			for c := range candidates {
				candList = append(candList, c)
			}
			sort.Strings(candList)

			bestComm := cur
			bestGain := 0.0
			for _, c := range candList {
				// Modularity gain of moving nodeID into community c:
				//   k_in/m - resolution * k_node * sigma_tot / (2m^2)
				// Constants common to all candidates drop out; we compare
				// the gain relative to the isolated node.
				gain := toComm[c]/twoM - resolution*degree[nodeID]*commDegree[c]/(twoM*twoM)
				if gain > bestGain || (gain == bestGain && c < bestComm) {
					bestGain, bestComm = gain, c
				}
			}

			// Re-insert into the chosen community.
			commDegree[bestComm] += degree[nodeID]
			if bestComm != cur {
				community[nodeID] = bestComm
				moved = true
			}
		}
		if !moved {
			break
		}
	}

	// Normalize community keys to the lowest member node ID — a stable,
	// deterministic representative.
	rep := map[string]string{}
	for _, nodeID := range order {
		c := community[nodeID]
		if r, ok := rep[c]; !ok || nodeID < r {
			rep[c] = nodeID
		}
	}
	out := make(map[string]string, len(nodes))
	for _, nodeID := range order {
		out[nodeID] = rep[community[nodeID]]
	}
	return out
}

// coarsen collapses each community of the partition into a single super-node
// and aggregates edge weights. The new node's members are the union of its
// constituents' finest-level members (sorted); its working ID is the lowest
// such member. Returns the coarsened node list (sorted by ID) and adjacency.
func coarsen(nodes []louvainNode, adj map[string]map[string]float64, partition map[string]string) ([]louvainNode, map[string]map[string]float64) {
	// Gather finest-level members per community representative.
	membersByRep := map[string][]string{}
	for _, n := range nodes {
		rep := partition[n.id]
		membersByRep[rep] = append(membersByRep[rep], n.members...)
	}

	// Build the new super-nodes; the super-node ID is the lowest member.
	newNodes := make([]louvainNode, 0, len(membersByRep))
	repToNewID := map[string]string{}
	for rep, members := range membersByRep {
		sort.Strings(members)
		newID := members[0]
		repToNewID[rep] = newID
		newNodes = append(newNodes, louvainNode{id: newID, members: members})
	}
	sort.Slice(newNodes, func(i, j int) bool { return newNodes[i].id < newNodes[j].id })

	// Aggregate edges. An edge between two old nodes maps to an edge between
	// their communities' new IDs; intra-community edges become self-loops
	// (kept — they carry weight that matters for the next level's degrees).
	newAdj := make(map[string]map[string]float64, len(newNodes))
	for _, n := range newNodes {
		newAdj[n.id] = map[string]float64{}
	}
	// Walk old adjacency in sorted order for determinism. adj is symmetric;
	// each undirected edge is summed from both endpoints, so dividing the
	// accumulated total preserves the original weight.
	srcIDs := make([]string, 0, len(adj))
	for id := range adj {
		srcIDs = append(srcIDs, id)
	}
	sort.Strings(srcIDs)
	for _, src := range srcIDs {
		dstIDs := make([]string, 0, len(adj[src]))
		for id := range adj[src] {
			dstIDs = append(dstIDs, id)
		}
		sort.Strings(dstIDs)
		for _, dst := range dstIDs {
			newSrc := repToNewID[partition[src]]
			newDst := repToNewID[partition[dst]]
			newAdj[newSrc][newDst] += adj[src][dst]
		}
	}
	return newNodes, newAdj
}

// assignParents links each child community to the parent community whose
// finest-member set contains the child's. Both slices are at adjacent
// levels: children one below parents. A child with no containing parent
// keeps ParentID "".
func assignParents(children, parents []Community) {
	// Map each finest entity ID to the parent community that holds it.
	entityToParent := map[string]string{}
	for _, p := range parents {
		for _, e := range p.EntityIDs {
			entityToParent[e] = p.ID
		}
	}
	for i := range children {
		if len(children[i].EntityIDs) == 0 {
			continue
		}
		children[i].ParentID = entityToParent[children[i].EntityIDs[0]]
	}
}
