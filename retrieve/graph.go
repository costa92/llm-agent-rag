package retrieve

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/store"
)

// GraphTrace attributes a graph-traversal retrieval: the seed entities the
// query linked to, the entities reached by traversal, and the deepest hop.
type GraphTrace struct {
	SeedEntityIDs    []string
	ReachedEntityIDs []string
	MaxHop           int
}

// EntityLinker maps a query to seed entities in a graph store.
type EntityLinker interface {
	Link(ctx context.Context, query, namespace string, gs store.GraphStore) ([]graph.Entity, error)
}

// LexicalEntityLinker resolves a query to seed entities by matching its
// whitespace tokens (and the whole query) against entity names. It needs
// no embedder — the zero-LLM default linker.
type LexicalEntityLinker struct{}

// Link implements EntityLinker.
func (LexicalEntityLinker) Link(ctx context.Context, query, namespace string, gs store.GraphStore) ([]graph.Entity, error) {
	candidates := append(strings.Fields(query), strings.TrimSpace(query))
	ents, err := gs.FindEntities(ctx, namespace, candidates)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(ents))
	out := make([]graph.Entity, 0, len(ents))
	for _, e := range ents {
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	return out, nil
}

// GraphRetriever retrieves by traversing the knowledge graph: it links the
// query to seed entities, expands their bounded neighborhood, and returns
// the entities' provenance chunks scored by graph proximity. A store that
// does not implement store.GraphStore yields an empty result and no error.
type GraphRetriever struct {
	Linker   EntityLinker
	Store    store.Store // type-asserted for store.GraphStore
	MaxDepth int          // default 1, hard cap 2
	HopDecay float64      // proximity score decay per hop, default 0.5
}

// Retrieve implements Retriever.
func (r GraphRetriever) Retrieve(ctx context.Context, req Request) ([]store.Hit, Trace, error) {
	gs, ok := r.Store.(store.GraphStore)
	if !ok {
		return nil, Trace{}, nil
	}
	linker := r.Linker
	if linker == nil {
		linker = LexicalEntityLinker{}
	}
	depth := r.MaxDepth
	if depth <= 0 {
		depth = 1
	}
	if depth > 2 {
		depth = 2
	}
	decay := r.HopDecay
	if decay <= 0 {
		decay = 0.5
	}

	seeds, err := linker.Link(ctx, req.Query, req.Namespace, gs)
	if err != nil {
		return nil, Trace{}, err
	}
	trace := Trace{OriginalQuery: req.Query, EffectiveQuery: req.Query}
	if len(seeds) == 0 {
		return nil, trace, nil
	}
	seedIDs := make([]string, 0, len(seeds))
	for _, e := range seeds {
		seedIDs = append(seedIDs, e.ID)
	}
	sub, err := gs.Neighborhood(ctx, req.Namespace, seedIDs, depth)
	if err != nil {
		return nil, Trace{}, err
	}

	// Map each provenance chunk to its best (lowest-hop) reaching entity.
	bestHop := map[string]int{}
	for _, e := range sub.Entities {
		hop := sub.Depth[e.ID]
		for _, cid := range e.SourceChunkIDs {
			if h, seen := bestHop[cid]; !seen || hop < h {
				bestHop[cid] = hop
			}
		}
	}
	cids := make([]string, 0, len(bestHop))
	for cid := range bestHop {
		cids = append(cids, cid)
	}
	sort.Strings(cids)

	hits := make([]store.Hit, 0, len(cids))
	for _, cid := range cids {
		chunk, err := r.Store.Get(ctx, cid)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return nil, Trace{}, err
		}
		hits = append(hits, store.Hit{
			Chunk: chunk,
			Score: math.Pow(decay, float64(bestHop[cid])),
		})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Chunk.ID < hits[j].Chunk.ID
	})
	if req.TopK > 0 && len(hits) > req.TopK {
		hits = hits[:req.TopK]
	}

	reached := make([]string, 0, len(sub.Entities))
	maxHop := 0
	for _, e := range sub.Entities {
		reached = append(reached, e.ID)
		if h := sub.Depth[e.ID]; h > maxHop {
			maxHop = h
		}
	}
	sort.Strings(reached)
	sort.Strings(seedIDs)
	trace.Graph = GraphTrace{SeedEntityIDs: seedIDs, ReachedEntityIDs: reached, MaxHop: maxHop}
	selected := make([]string, 0, len(hits))
	for _, h := range hits {
		selected = append(selected, h.Chunk.ID)
	}
	trace.SelectedChunkIDs = selected
	return hits, trace, nil
}
