package rag

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/store"
)

// defaultMaxCommunities is the sane default cap GlobalOptions.MaxCommunities
// falls back to when left at or below zero.
const defaultMaxCommunities = 8

// globalMapSystemPrompt instructs the map step: judge one community report's
// relevance to the question and write a partial answer plus a self-rated
// helpfulness score.
const globalMapSystemPrompt = `You are answering a whole-corpus question using ONE community summary at a time.
You are given a community report (a title and a paragraph) and a user question.
Decide how much this community contributes to answering the question.

Output exactly this shape, and nothing else:
Score: <an integer 0-100, how helpful this community is>
<one paragraph: what this community contributes to the answer, or why it is irrelevant>

No markdown, no fences, no commentary.`

// globalReduceSystemPrompt instructs the reduce step: synthesize the
// surviving per-community partial answers into one final answer.
const globalReduceSystemPrompt = `You are answering a whole-corpus question.
You are given the user question and several partial answers, each drawn from one community of a knowledge graph, ordered most-helpful first.
Synthesize them into a single, coherent final answer to the question.

Write the answer directly. No markdown, no fences, no commentary.`

// AskGlobal answers a whole-corpus question by map-reduce over community
// reports. It is a SEPARATE answer path from Ask: it never calls s.retrieve,
// the reranker, or the packer. The flow is select -> lazy report -> map ->
// reduce:
//
//   - Select the coarsest community level (highest Level — the broadest
//     themes). If that level has more than opts.MaxCommunities communities,
//     rank them by query-token overlap with member entity names and cap.
//   - For each selected community, look the report up on the CommunityStore;
//     reuse it iff found and its ContentHash still matches the live
//     community, otherwise summarize lazily and cache the result.
//   - Map: prompt the model once per report for a partial answer and a
//     self-rated helpfulness score.
//   - Reduce: drop score-0 partials, rank the survivors by score, and prompt
//     the model once to synthesize the final answer.
//
// A store that does not implement store.CommunityStore, or a namespace with
// no communities, yields an empty Answer and no error — graceful degradation,
// like a missing graph signal in v0.7. A nil model returns ErrModelRequired;
// a missing summarizer needed for a cache miss returns
// ErrCommunitySummarizerRequired.
func (s *System) AskGlobal(ctx context.Context, question string, opts GlobalOptions) (Answer, error) {
	if s.model == nil {
		return Answer{}, ErrModelRequired
	}
	// AskGlobal is top-level: install a fresh obs.Counter so the map and
	// reduce generations — and any lazy summarization — are counted.
	counter := obs.NewCounter()
	ctx = obs.WithCounter(ctx, counter)
	metrics := obs.Metrics{}
	start := time.Now()

	// A store with no community capability cannot support global search.
	cs, ok := s.store.(store.CommunityStore)
	if !ok {
		metrics.Calls = counter.Counts()
		metrics.TotalDuration = time.Since(start)
		return Answer{Diagnostics: Diagnostics{Metrics: metrics}}, nil
	}

	communities, err := cs.Communities(ctx, opts.Namespace)
	if err != nil {
		return Answer{}, err
	}
	if len(communities) == 0 {
		metrics.Calls = counter.Counts()
		metrics.TotalDuration = time.Since(start)
		return Answer{Diagnostics: Diagnostics{Metrics: metrics}}, nil
	}

	// Select stage: coarsest level, then a query-relevance cap.
	stageStart := time.Now()
	selected := selectCommunities(ctx, cs, opts.Namespace, communities, question, opts.MaxCommunities)
	metrics.Stages = append(metrics.Stages, obs.StageTiming{Stage: "select", Duration: time.Since(stageStart)})

	// Lazy reports: one report per selected community, generated on a cache
	// miss or a stale ContentHash and persisted back.
	stageStart = time.Now()
	reports, err := s.communityReports(ctx, cs, opts.Namespace, selected)
	if err != nil {
		return Answer{}, err
	}
	metrics.Stages = append(metrics.Stages, obs.StageTiming{Stage: "report", Duration: time.Since(stageStart)})

	// Map stage: one generation per report -> a scored partial answer.
	stageStart = time.Now()
	partials := make([]globalPartial, 0, len(reports))
	mapScores := make(map[string]int, len(reports))
	communityIDs := make([]string, 0, len(reports))
	for _, r := range reports {
		communityIDs = append(communityIDs, r.CommunityID)
		resp, err := s.model.Generate(ctx, generate.Request{
			SystemPrompt: globalMapSystemPrompt,
			Messages:     []generate.Message{{Role: "user", Content: globalMapPrompt(r, question)}},
		})
		if err != nil {
			return Answer{}, err
		}
		score, text := parseGlobalMap(resp.Text)
		mapScores[r.CommunityID] = score
		partials = append(partials, globalPartial{CommunityID: r.CommunityID, Score: score, Text: text})
	}
	mapCalls := len(reports)
	metrics.Stages = append(metrics.Stages, obs.StageTiming{Stage: "map", Duration: time.Since(stageStart)})

	// Reduce stage: drop the score-0 partials, rank by score (tie-break by
	// community ID), and synthesize one final answer.
	stageStart = time.Now()
	survivors := make([]globalPartial, 0, len(partials))
	for _, p := range partials {
		if p.Score > 0 {
			survivors = append(survivors, p)
		}
	}
	sort.SliceStable(survivors, func(i, j int) bool {
		if survivors[i].Score != survivors[j].Score {
			return survivors[i].Score > survivors[j].Score
		}
		return survivors[i].CommunityID < survivors[j].CommunityID
	})

	var finalText string
	reduceCalls := 0
	if len(survivors) == 0 {
		finalText = "No relevant community information was found to answer this question."
	} else {
		resp, err := s.model.Generate(ctx, generate.Request{
			SystemPrompt: globalReduceSystemPrompt,
			Messages:     []generate.Message{{Role: "user", Content: globalReducePrompt(survivors, question)}},
		})
		if err != nil {
			return Answer{}, err
		}
		reduceCalls = 1
		finalText = strings.TrimSpace(resp.Text)
	}
	metrics.Stages = append(metrics.Stages, obs.StageTiming{Stage: "reduce", Duration: time.Since(stageStart)})

	metrics.Calls = counter.Counts()
	metrics.TotalDuration = time.Since(start)

	return Answer{
		Text: finalText,
		Diagnostics: Diagnostics{
			Metrics: metrics,
			Global: GlobalDiagnostics{
				CommunityIDs:     communityIDs,
				MapScores:        mapScores,
				MapCalls:         mapCalls,
				ReduceCalls:      reduceCalls,
				ConsultedReports: reports,
			},
		},
		Trace: Trace{
			Question:  question,
			Namespace: opts.Namespace,
		},
	}, nil
}

// PrewarmCommunityReports eagerly fills the community-report cache for a
// namespace: it walks every community and generates+persists any report that
// is missing or stale (its cached ContentHash no longer matches the live
// community). It returns the number of reports generated.
//
// It is the thin opt-in eager mode (keystone KG3-2): the same summarizer and
// the same CommunityStore-backed cache as the lazy AskGlobal path, just
// called ahead of time so the first global query runs all-cache-hits. A
// report that is already fresh is left untouched and not counted.
//
// A store that does not implement store.CommunityStore yields 0, nil —
// graceful degradation, like the lazy path. A cache miss with no configured
// summarizer returns ErrCommunitySummarizerRequired.
func (s *System) PrewarmCommunityReports(ctx context.Context, namespace string) (int, error) {
	cs, ok := s.store.(store.CommunityStore)
	if !ok {
		return 0, nil
	}
	communities, err := cs.Communities(ctx, namespace)
	if err != nil {
		return 0, err
	}
	if len(communities) == 0 {
		return 0, nil
	}

	var snapshot graph.Graph
	snapshotLoaded := false

	generated := 0
	for _, c := range communities {
		want := graph.CommunityContentHash(c)
		if cached, ok, err := cs.CommunityReport(ctx, namespace, c.ID); err != nil {
			return generated, err
		} else if ok && cached.ContentHash == want {
			continue // already fresh — leave it
		}
		// Cache miss or stale hash: summarize and persist.
		if s.communitySummarizer == nil {
			return generated, ErrCommunitySummarizerRequired
		}
		if !snapshotLoaded {
			g, err := cs.GraphSnapshot(ctx, namespace)
			if err != nil {
				return generated, err
			}
			snapshot = g
			snapshotLoaded = true
		}
		report, err := s.communitySummarizer.Summarize(ctx, c, snapshot)
		if err != nil {
			return generated, err
		}
		report.CommunityID = c.ID
		report.ContentHash = want
		if err := cs.PutCommunityReport(ctx, namespace, report); err != nil {
			return generated, err
		}
		generated++
	}
	return generated, nil
}

// globalPartial is one community's map-step output: a self-rated helpfulness
// score and the partial answer text.
type globalPartial struct {
	CommunityID string
	Score       int
	Text        string
}

// selectCommunities picks the communities AskGlobal will map over. v0.8 fixes
// on the coarsest level (highest Level — the broadest themes). When that level
// holds more than maxCommunities communities, they are ranked by query-token
// overlap with their member entity names (lowercase whitespace tokens) and the
// top maxCommunities are kept; ties — including a zero-overlap query — break by
// community ID, so the selection is fully deterministic.
func selectCommunities(ctx context.Context, cs store.CommunityStore, namespace string, communities []graph.Community, question string, maxCommunities int) []graph.Community {
	if maxCommunities <= 0 {
		maxCommunities = defaultMaxCommunities
	}

	// Coarsest level only.
	maxLevel := communities[0].Level
	for _, c := range communities[1:] {
		if c.Level > maxLevel {
			maxLevel = c.Level
		}
	}
	coarse := make([]graph.Community, 0, len(communities))
	for _, c := range communities {
		if c.Level == maxLevel {
			coarse = append(coarse, c)
		}
	}
	// Communities() returns its set sorted by ID; the coarse filter preserves
	// that order. The community-ID tie-break below relies on it.
	if len(coarse) <= maxCommunities {
		return coarse
	}

	// Too many: rank by query-token overlap with member entity names.
	tokens := queryTokens(question)
	names := entityNamesByID(ctx, cs, namespace)
	type scored struct {
		c       graph.Community
		overlap int
	}
	ranked := make([]scored, 0, len(coarse))
	for _, c := range coarse {
		ranked = append(ranked, scored{c: c, overlap: communityOverlap(c, names, tokens)})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].overlap != ranked[j].overlap {
			return ranked[i].overlap > ranked[j].overlap
		}
		return ranked[i].c.ID < ranked[j].c.ID
	})
	out := make([]graph.Community, 0, maxCommunities)
	for _, r := range ranked[:maxCommunities] {
		out = append(out, r.c)
	}
	// Restore community-ID order so the consulted set is deterministic and
	// stable regardless of the ranking's internal ordering.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// queryTokens lowercases the question and splits it on whitespace — the same
// zero-LLM tokenization idiom as retrieve.LexicalEntityLinker.
func queryTokens(question string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, tok := range strings.Fields(strings.ToLower(question)) {
		out[tok] = struct{}{}
	}
	return out
}

// entityNamesByID reads the namespace graph snapshot and maps each entity ID
// to its name. A snapshot error degrades to an empty map — overlap ranking
// then falls back to the community-ID tie-break.
func entityNamesByID(ctx context.Context, cs store.CommunityStore, namespace string) map[string]string {
	g, err := cs.GraphSnapshot(ctx, namespace)
	if err != nil {
		return map[string]string{}
	}
	names := make(map[string]string, len(g.Entities))
	for _, e := range g.Entities {
		names[e.ID] = e.Name
	}
	return names
}

// communityOverlap counts how many query tokens appear among a community's
// member entity names (each name lowercased and split on whitespace).
func communityOverlap(c graph.Community, names map[string]string, tokens map[string]struct{}) int {
	if len(tokens) == 0 {
		return 0
	}
	overlap := 0
	for _, id := range c.EntityIDs {
		for _, word := range strings.Fields(strings.ToLower(names[id])) {
			if _, ok := tokens[word]; ok {
				overlap++
			}
		}
	}
	return overlap
}

// communityReports resolves a report per selected community: a cache hit on
// the CommunityStore is reused iff its ContentHash still matches the live
// community; otherwise the report is generated lazily via the configured
// summarizer and persisted back. A missing summarizer on a miss returns
// ErrCommunitySummarizerRequired.
func (s *System) communityReports(ctx context.Context, cs store.CommunityStore, namespace string, selected []graph.Community) ([]graph.CommunityReport, error) {
	var snapshot graph.Graph
	snapshotLoaded := false

	reports := make([]graph.CommunityReport, 0, len(selected))
	for _, c := range selected {
		want := graph.CommunityContentHash(c)
		if cached, ok, err := cs.CommunityReport(ctx, namespace, c.ID); err != nil {
			return nil, err
		} else if ok && cached.ContentHash == want {
			reports = append(reports, cached)
			continue
		}
		// Cache miss or stale hash: summarize lazily.
		if s.communitySummarizer == nil {
			return nil, ErrCommunitySummarizerRequired
		}
		if !snapshotLoaded {
			g, err := cs.GraphSnapshot(ctx, namespace)
			if err != nil {
				return nil, err
			}
			snapshot = g
			snapshotLoaded = true
		}
		report, err := s.communitySummarizer.Summarize(ctx, c, snapshot)
		if err != nil {
			return nil, err
		}
		report.CommunityID = c.ID
		report.ContentHash = want
		if err := cs.PutCommunityReport(ctx, namespace, report); err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	return reports, nil
}

// globalMapPrompt renders one community report and the question into the map
// step's user message.
func globalMapPrompt(r graph.CommunityReport, question string) string {
	var b strings.Builder
	b.WriteString("Community report\n")
	b.WriteString("Title: ")
	b.WriteString(r.Title)
	b.WriteString("\n")
	b.WriteString(r.Summary)
	b.WriteString("\n\nQuestion: ")
	b.WriteString(question)
	return b.String()
}

// globalReducePrompt renders the question and the surviving partial answers
// (most-helpful first) into the reduce step's user message.
func globalReducePrompt(survivors []globalPartial, question string) string {
	var b strings.Builder
	b.WriteString("Question: ")
	b.WriteString(question)
	b.WriteString("\n\nPartial answers (most helpful first):\n")
	for i, p := range survivors {
		b.WriteString("- [")
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString("] ")
		b.WriteString(p.Text)
		b.WriteString("\n")
	}
	return b.String()
}

// parseGlobalMap leniently extracts a helpfulness score and the partial
// answer from a map-step response. The first line carrying a "Score: <int>"
// marker (case-insensitive, fences tolerated) sets the score; every other
// non-fence line joins into the partial answer. A response with no parseable
// score scores 0 — the reduce step then drops it.
func parseGlobalMap(out string) (score int, text string) {
	var lines []string
	scoreFound := false
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "```") { // skip markdown fences
			continue
		}
		if !scoreFound {
			if rest, ok := cutScorePrefix(line); ok {
				score = clampScore(parseLeadingInt(rest))
				scoreFound = true
				continue
			}
		}
		lines = append(lines, line)
	}
	return score, strings.Join(lines, " ")
}

// cutScorePrefix returns the text after a leading "Score:" marker
// (case-insensitive) and whether the line carried one.
func cutScorePrefix(line string) (string, bool) {
	const marker = "score:"
	if len(line) >= len(marker) && strings.EqualFold(line[:len(marker)], marker) {
		return line[len(marker):], true
	}
	return "", false
}

// parseLeadingInt reads the leading run of digits in s (after trimming
// whitespace) as an int. A string with no leading digits yields 0.
func parseLeadingInt(s string) int {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		return 0
	}
	return n
}

// clampScore pins a parsed helpfulness score into the [0,100] range.
func clampScore(n int) int {
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}
