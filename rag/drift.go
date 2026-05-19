package rag

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"strconv"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/pack"
	"github.com/costa92/llm-agent-rag/store"
)

// DriftOptions defaults and the hard round cap. The local follow-up loop is
// bounded by construction: Rounds is clamped into [1, driftMaxRounds] before
// the loop runs, so it can never exceed driftMaxRounds iterations.
const (
	driftDefaultMaxCommunities = 8
	driftDefaultRounds         = 2
	driftMaxRounds             = 3
	driftDefaultTopK           = 8
)

// driftLocalSystemPrompt instructs one local follow-up round: answer the
// question from the supplied neighborhood context and name the entities a
// next hop should explore.
const driftLocalSystemPrompt = `You are answering a question using a slice of a knowledge graph — a few entities and the passages they were extracted from.
Write a partial answer to the question from ONLY this context.
Then, on a final line, name any entities that look worth exploring next to answer the question more fully.

Output exactly this shape, and nothing else:
<one paragraph: your partial answer from this context>
Follow-up: <comma-separated entity names, or "none">

No markdown, no fences, no other commentary.`

// driftSynthesisSystemPrompt instructs the synthesis step: fold the primer
// partials and every local round's partial into one final answer.
const driftSynthesisSystemPrompt = `You are answering a question using DRIFT search.
You are given the user question, partial answers from a global primer pass over community summaries, and partial answers from a sequence of local graph-exploration rounds.
Synthesize them into a single, coherent final answer to the question.

Write the answer directly. No markdown, no fences, no commentary.`

// AskDrift answers a question by DRIFT hybrid search: a global primer pass, a
// hard-bounded local follow-up loop, and a synthesis step. It is a SEPARATE
// answer path — it never calls s.retrieve, the reranker, the Ask packer
// pipeline, or branches inside AskGlobal. Instead it orchestrates AskGlobal's
// unexported helpers (the primer) and direct graph traversal (the local loop):
//
//   - Primer: select the coarsest-level communities, lazily resolve their
//     reports, and run AskGlobal's map step — one generation per report for a
//     scored partial answer. The highest-scoring communities' member entities
//     are the local loop's round-0 seed entity IDs.
//   - Local follow-up loop: for each of up to opts.Rounds rounds, traverse the
//     1-hop neighborhood of the current seed entities, pack their provenance
//     chunks into context, ask the model for a partial answer plus a list of
//     follow-up entity names, and resolve those names into the next round's
//     seeds. The loop is hard-bounded — it terminates on the first of: the
//     round cap; the model emitting no new follow-up entities; no new
//     reachable entities.
//   - Synthesis: one generation folds the primer partials and every round's
//     partial into the final Answer.Text.
//
// A store that does not implement store.CommunityStore, or a namespace with
// no communities, makes the primer empty — AskDrift degrades to a pure
// local-only answer (or an empty Answer when there is also no graph), no
// error. A nil model returns ErrModelRequired; a missing summarizer needed
// for a primer cache miss returns ErrCommunitySummarizerRequired.
func (s *System) AskDrift(ctx context.Context, question string, opts DriftOptions) (Answer, error) {
	if s.model == nil {
		return Answer{}, ErrModelRequired
	}
	// AskDrift is top-level: install a fresh obs.Counter so the primer map
	// generations, every local-round generation, the synthesis generation,
	// and any lazy summarization are counted.
	counter := obs.NewCounter()
	ctx = obs.WithCounter(ctx, counter)
	metrics := obs.Metrics{}
	start := time.Now()

	rounds := opts.Rounds
	if rounds <= 0 {
		rounds = driftDefaultRounds
	}
	if rounds > driftMaxRounds {
		rounds = driftMaxRounds
	}
	topK := opts.TopK
	if topK <= 0 {
		topK = driftDefaultTopK
	}

	// Optional store capabilities. Either absent degrades gracefully — a
	// missing CommunityStore empties the primer, a missing GraphStore
	// empties the local loop.
	cs, hasCommunities := s.store.(store.CommunityStore)
	gs, hasGraph := s.store.(store.GraphStore)

	// --- Primer -----------------------------------------------------------
	// Reuse AskGlobal's unexported helpers in-package: selectCommunities ->
	// communityReports -> the map step. No AskGlobal refactor.
	stageStart := time.Now()
	primer, err := s.driftPrimer(ctx, cs, hasCommunities, question, opts)
	if err != nil {
		return Answer{}, err
	}
	metrics.Stages = append(metrics.Stages, obs.StageTiming{Stage: "primer", Duration: time.Since(stageStart)})

	// --- Local follow-up loop --------------------------------------------
	stageStart = time.Now()
	loop := s.driftLocalLoop(ctx, gs, hasGraph, question, opts.Namespace, primer.seedEntityIDs, rounds, topK)
	if loop.err != nil {
		return Answer{}, loop.err
	}
	metrics.Stages = append(metrics.Stages, obs.StageTiming{Stage: "local", Duration: time.Since(stageStart)})

	// --- Synthesis --------------------------------------------------------
	stageStart = time.Now()
	finalText, err := s.driftSynthesize(ctx, question, primer.partials, loop.partials)
	if err != nil {
		return Answer{}, err
	}
	metrics.Stages = append(metrics.Stages, obs.StageTiming{Stage: "synthesis", Duration: time.Since(stageStart)})

	metrics.Calls = counter.Counts()
	metrics.TotalDuration = time.Since(start)

	return Answer{
		Text: finalText,
		Diagnostics: Diagnostics{
			Metrics: metrics,
			Drift: DriftDiagnostics{
				PrimerCommunityIDs: primer.communityIDs,
				Rounds:             len(loop.roundEntityIDs),
				RoundEntityIDs:     loop.roundEntityIDs,
				ConsultedReports:   primer.reports,
			},
		},
		Trace: Trace{
			Question:  question,
			Namespace: opts.Namespace,
		},
	}, nil
}

// driftPrimerResult holds the primer pass's output: the scored partial
// answers, the consulted community IDs and reports, and the round-0 seed
// entity IDs (the highest-scoring communities' members, sorted and deduped).
type driftPrimerResult struct {
	partials      []globalPartial
	communityIDs  []string
	reports       []graph.CommunityReport
	seedEntityIDs []string
}

// driftPrimer runs the primer pass: select communities, resolve reports, and
// run AskGlobal's map step. The seed entity IDs are the members of the
// communities that scored above zero — i.e. the communities the map step
// judged relevant. When the store has no community capability or the
// namespace has no communities the primer is empty (no error).
func (s *System) driftPrimer(ctx context.Context, cs store.CommunityStore, hasCommunities bool, question string, opts DriftOptions) (driftPrimerResult, error) {
	var res driftPrimerResult
	if !hasCommunities {
		return res, nil
	}
	communities, err := cs.Communities(ctx, opts.Namespace)
	if err != nil {
		return res, err
	}
	if len(communities) == 0 {
		return res, nil
	}

	selected := selectCommunities(ctx, cs, opts.Namespace, communities, question, opts.MaxCommunities)
	reports, err := s.communityReports(ctx, cs, opts.Namespace, selected)
	if err != nil {
		return res, err
	}
	res.reports = reports

	// Map step: one generation per report -> a scored partial answer. This
	// mirrors AskGlobal's map stage exactly, reusing the same helpers.
	byID := make(map[string]graph.Community, len(selected))
	for _, c := range selected {
		byID[c.ID] = c
	}
	seeds := map[string]struct{}{}
	for _, r := range reports {
		res.communityIDs = append(res.communityIDs, r.CommunityID)
		resp, err := s.model.Generate(ctx, generate.Request{
			SystemPrompt: globalMapSystemPrompt,
			Messages:     []generate.Message{{Role: "user", Content: globalMapPrompt(r, question)}},
		})
		if err != nil {
			return driftPrimerResult{}, err
		}
		score, text := parseGlobalMap(resp.Text)
		res.partials = append(res.partials, globalPartial{CommunityID: r.CommunityID, Score: score, Text: text})
		// A community the map step judged relevant seeds the local loop.
		if score > 0 {
			for _, id := range byID[r.CommunityID].EntityIDs {
				seeds[id] = struct{}{}
			}
		}
	}
	// When no community scored above zero, fall back to seeding from every
	// consulted community so the local loop still has somewhere to start.
	if len(seeds) == 0 {
		for _, r := range reports {
			for _, id := range byID[r.CommunityID].EntityIDs {
				seeds[id] = struct{}{}
			}
		}
	}
	res.seedEntityIDs = sortedKeys(seeds)
	return res, nil
}

// driftLocalLoopResult holds the local follow-up loop's output: one partial
// answer per round and the seed entity IDs each round traversed from.
type driftLocalLoopResult struct {
	partials       []string
	roundEntityIDs [][]string
	err            error
}

// driftLocalLoop runs the hard-bounded local follow-up loop. For each round
// (up to rounds, the already-clamped cap) it traverses the 1-hop neighborhood
// of the current seed entities, packs their provenance chunks into context,
// asks the model for a partial answer plus follow-up entity names, and
// resolves those names into the next round's seeds. It terminates on the
// first of: the round cap; an empty seed set; no new follow-up entities; no
// new reachable entities.
func (s *System) driftLocalLoop(ctx context.Context, gs store.GraphStore, hasGraph bool, question, namespace string, seedEntityIDs []string, rounds, topK int) driftLocalLoopResult {
	var res driftLocalLoopResult
	if !hasGraph {
		return res
	}

	seeds := append([]string(nil), seedEntityIDs...)
	seen := map[string]struct{}{}
	for _, id := range seeds {
		seen[id] = struct{}{}
	}

	for round := 0; round < rounds; round++ {
		if len(seeds) == 0 {
			break // no entities to explore — terminate early
		}
		res.roundEntityIDs = append(res.roundEntityIDs, append([]string(nil), seeds...))

		sub, err := gs.Neighborhood(ctx, namespace, seeds, 1)
		if err != nil {
			res.err = err
			return res
		}

		// Collect the subgraph entities' provenance chunks and load them.
		hits := s.driftChunkHits(ctx, sub.Entities)
		packed, err := s.driftPack(ctx, question, hits, topK)
		if err != nil {
			res.err = err
			return res
		}

		resp, err := s.model.Generate(ctx, generate.Request{
			SystemPrompt: driftLocalSystemPrompt,
			Messages:     []generate.Message{{Role: "user", Content: driftLocalPrompt(question, packed)}},
		})
		if err != nil {
			res.err = err
			return res
		}
		partial, followups := parseDriftLocal(resp.Text)
		res.partials = append(res.partials, partial)

		if len(followups) == 0 {
			break // model surfaced no follow-ups — terminate early
		}
		found, err := gs.FindEntities(ctx, namespace, followups)
		if err != nil {
			res.err = err
			return res
		}
		// Next round's seeds: only entities not already explored.
		nextSeeds := map[string]struct{}{}
		for _, e := range found {
			if _, ok := seen[e.ID]; ok {
				continue
			}
			seen[e.ID] = struct{}{}
			nextSeeds[e.ID] = struct{}{}
		}
		if len(nextSeeds) == 0 {
			break // no new reachable entities — terminate early
		}
		seeds = sortedKeys(nextSeeds)
	}
	return res
}

// driftChunkHits resolves a subgraph's entities into their provenance chunks.
// It dedupes chunk IDs, loads each via s.store.Get (skipping store.ErrNotFound
// — provenance may reference a since-removed chunk), and returns the hits in
// deterministic chunk-ID order.
func (s *System) driftChunkHits(ctx context.Context, entities []graph.Entity) []store.Hit {
	ids := map[string]struct{}{}
	for _, e := range entities {
		for _, cid := range e.SourceChunkIDs {
			ids[cid] = struct{}{}
		}
	}
	hits := make([]store.Hit, 0, len(ids))
	for _, cid := range sortedKeys(ids) {
		chunk, err := s.store.Get(ctx, cid)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			// Any other Get error is non-fatal here: a single unreadable
			// provenance chunk should not abort the whole DRIFT run.
			continue
		}
		hits = append(hits, store.Hit{Chunk: chunk, Score: 1})
	}
	return hits
}

// driftPack packs up to topK provenance chunks into the round's model
// context. A nil packer (or fewer hits than topK) passes the hits through; a
// configured packer trims by token budget.
func (s *System) driftPack(ctx context.Context, question string, hits []store.Hit, topK int) ([]store.Hit, error) {
	if len(hits) > topK {
		hits = hits[:topK]
	}
	if s.packer == nil || len(hits) == 0 {
		return hits, nil
	}
	res, err := s.packer.Pack(ctx, pack.Request{Question: question, Hits: hits})
	if err != nil {
		return nil, err
	}
	return res.Hits, nil
}

// driftSynthesize folds the primer partials and every local-round partial
// into the final answer. With no partials at all it returns a graceful
// no-information message and makes no model call.
func (s *System) driftSynthesize(ctx context.Context, question string, primerPartials []globalPartial, localPartials []string) (string, error) {
	prompt := driftSynthesisPrompt(question, primerPartials, localPartials)
	if prompt == "" {
		return "No information was found to answer this question.", nil
	}
	resp, err := s.model.Generate(ctx, generate.Request{
		SystemPrompt: driftSynthesisSystemPrompt,
		Messages:     []generate.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Text), nil
}

// driftLocalPrompt renders one local round's packed context and the question
// into the round's user message.
func driftLocalPrompt(question string, hits []store.Hit) string {
	var b strings.Builder
	b.WriteString("Question: ")
	b.WriteString(question)
	b.WriteString("\n\nContext:\n")
	if len(hits) == 0 {
		b.WriteString("(no passages were available for the current entities)\n")
	}
	for _, h := range hits {
		b.WriteString("- ")
		b.WriteString(strings.TrimSpace(h.Chunk.Content))
		b.WriteString("\n")
	}
	return b.String()
}

// driftSynthesisPrompt renders the question, the primer partials, and the
// local-round partials into the synthesis step's user message. It returns ""
// when there is nothing at all to synthesize.
func driftSynthesisPrompt(question string, primerPartials []globalPartial, localPartials []string) string {
	var primer []string
	for _, p := range primerPartials {
		if p.Score > 0 && strings.TrimSpace(p.Text) != "" {
			primer = append(primer, strings.TrimSpace(p.Text))
		}
	}
	var local []string
	for _, p := range localPartials {
		if strings.TrimSpace(p) != "" {
			local = append(local, strings.TrimSpace(p))
		}
	}
	if len(primer) == 0 && len(local) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Question: ")
	b.WriteString(question)
	if len(primer) > 0 {
		b.WriteString("\n\nPrimer partial answers (global community summaries):\n")
		for i, p := range primer {
			b.WriteString("- [P")
			b.WriteString(strconv.Itoa(i + 1))
			b.WriteString("] ")
			b.WriteString(p)
			b.WriteString("\n")
		}
	}
	if len(local) > 0 {
		b.WriteString("\nLocal exploration partial answers (graph rounds, in order):\n")
		for i, p := range local {
			b.WriteString("- [L")
			b.WriteString(strconv.Itoa(i + 1))
			b.WriteString("] ")
			b.WriteString(p)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// parseDriftLocal leniently parses a local round's response into a partial
// answer and a list of follow-up entity names. The first line carrying a
// "Follow-up:" marker (case-insensitive, fences tolerated) supplies the
// follow-ups — a comma-separated list, with the sentinel "none" yielding an
// empty list; every other non-fence line joins into the partial answer. A
// response with no marker yields the whole text as the partial and no
// follow-ups — the loop then terminates, which is the safe default.
func parseDriftLocal(out string) (partial string, followups []string) {
	var lines []string
	markerFound := false
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "```") { // skip markdown fences
			continue
		}
		if !markerFound {
			if rest, ok := cutFollowupPrefix(line); ok {
				followups = parseFollowupNames(rest)
				markerFound = true
				continue
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, " "), followups
}

// cutFollowupPrefix returns the text after a leading "Follow-up:" marker
// (case-insensitive) and whether the line carried one.
func cutFollowupPrefix(line string) (string, bool) {
	const marker = "follow-up:"
	if len(line) >= len(marker) && strings.EqualFold(line[:len(marker)], marker) {
		return line[len(marker):], true
	}
	return "", false
}

// parseFollowupNames splits a comma-separated follow-up list into trimmed,
// deduped entity names. The sentinel "none" (case-insensitive, as the whole
// list) yields no names.
func parseFollowupNames(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "none") {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, part := range strings.Split(s, ",") {
		name := strings.TrimSpace(part)
		if name == "" || strings.EqualFold(name, "none") {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}

// sortedKeys returns the keys of a set in sorted order — deterministic seed
// and chunk ordering is what makes the DRIFT orchestration golden-testable.
func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

