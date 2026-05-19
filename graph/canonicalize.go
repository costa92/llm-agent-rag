package graph

import (
	"sort"
	"strings"
)

// Canonicalize merges raw per-chunk extractions into one Graph. Entities
// merge by (normalized name, type) — exact-match, case-folded; each
// canonical entity gets a stable ID ("type:normname"), a
// concatenated-deduped Description, and unioned provenance. Relation
// Source/Target names resolve to canonical entity IDs; a relation whose
// endpoint matches no entity is dropped. Relations merge by
// (Source, Relation, Target). Output is deterministically ordered.
//
// Canonicalize expects raw extractor output — relation endpoints are
// entity names, not IDs. Exact-match only; fuzzy resolution is a v0.8 item.
func Canonicalize(entities []Entity, relations []Relation) Graph {
	type entAcc struct {
		ent   Entity
		descs []string
		seen  map[string]bool
	}
	accs := map[string]*entAcc{}
	for _, e := range entities {
		norm := NormalizeName(e.Name)
		if norm == "" {
			continue
		}
		id := e.Type + ":" + norm
		acc := accs[id]
		if acc == nil {
			acc = &entAcc{
				ent:  Entity{ID: id, Name: e.Name, Type: e.Type, Metadata: e.Metadata},
				seen: map[string]bool{},
			}
			accs[id] = acc
		}
		if d := strings.TrimSpace(e.Description); d != "" && !acc.seen[d] {
			acc.seen[d] = true
			acc.descs = append(acc.descs, d)
		}
		acc.ent.SourceChunkIDs = unionStrings(acc.ent.SourceChunkIDs, e.SourceChunkIDs)
	}
	outEnts := make([]Entity, 0, len(accs))
	for _, acc := range accs {
		acc.ent.Description = strings.Join(acc.descs, "; ")
		sort.Strings(acc.ent.SourceChunkIDs)
		outEnts = append(outEnts, acc.ent)
	}
	sort.Slice(outEnts, func(i, j int) bool { return outEnts[i].ID < outEnts[j].ID })

	// name -> canonical id, built from sorted entities so a name shared by
	// multiple typed entities resolves deterministically (highest id wins).
	byName := make(map[string]string, len(outEnts))
	for _, e := range outEnts {
		byName[NormalizeName(e.Name)] = e.ID
	}

	type relAcc struct {
		rel    Relation
		weight float64
	}
	relAccs := map[string]*relAcc{}
	for _, r := range relations {
		src, ok := byName[NormalizeName(r.Source)]
		if !ok {
			continue
		}
		dst, ok := byName[NormalizeName(r.Target)]
		if !ok {
			continue
		}
		id := src + "::" + r.Relation + "::" + dst
		acc := relAccs[id]
		if acc == nil {
			acc = &relAcc{rel: Relation{
				ID:          id,
				Source:      src,
				Target:      dst,
				Relation:    r.Relation,
				Description: strings.TrimSpace(r.Description),
			}}
			relAccs[id] = acc
		}
		acc.weight += r.Weight
		acc.rel.SourceChunkIDs = unionStrings(acc.rel.SourceChunkIDs, r.SourceChunkIDs)
	}
	outRels := make([]Relation, 0, len(relAccs))
	for _, acc := range relAccs {
		acc.rel.Weight = acc.weight
		sort.Strings(acc.rel.SourceChunkIDs)
		outRels = append(outRels, acc.rel)
	}
	sort.Slice(outRels, func(i, j int) bool { return outRels[i].ID < outRels[j].ID })

	return Graph{Entities: outEnts, Relations: outRels}
}

// NormalizeName is the canonical-name normalization used for entity
// merging and lookup: case-folded and trimmed.
func NormalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func unionStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, group := range [][]string{a, b} {
		for _, s := range group {
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
