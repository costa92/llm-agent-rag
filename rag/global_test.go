package rag

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/store"
)

// globalScriptedModel is a deterministic generate.Model for the AskGlobal
// tests. It distinguishes the map step from the reduce step by the request's
// SystemPrompt and serves a fixed response for each, recording every call so
// a test can assert how many map/reduce generations ran.
type globalScriptedModel struct {
	mu        sync.Mutex
	mapText   string
	reduceTxt string
	mapCalls  int
	reduce    int
}

func (m *globalScriptedModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case strings.HasPrefix(req.SystemPrompt, "You are answering a whole-corpus question using ONE community summary"):
		m.mapCalls++
		return generate.Response{Text: m.mapText}, nil
	case strings.HasPrefix(req.SystemPrompt, "You are answering a whole-corpus question.\nYou are given"):
		m.reduce++
		return generate.Response{Text: m.reduceTxt}, nil
	default:
		return generate.Response{Text: ""}, nil
	}
}

// countingSummarizer wraps a CommunitySummarizer and counts Summarize calls,
// so a test can prove a report came from the cache (no extra call) or a
// re-summarize (a call).
type countingSummarizer struct {
	inner graph.CommunitySummarizer
	calls int
}

func (c *countingSummarizer) Summarize(ctx context.Context, comm graph.Community, g graph.Graph) (graph.CommunityReport, error) {
	c.calls++
	return c.inner.Summarize(ctx, comm, g)
}

// staticSummarizer is a deterministic CommunitySummarizer: it returns a fixed
// title/summary for any community, with the correct ContentHash.
type staticSummarizer struct{}

func (staticSummarizer) Summarize(_ context.Context, c graph.Community, _ graph.Graph) (graph.CommunityReport, error) {
	return graph.CommunityReport{
		CommunityID: c.ID,
		Title:       "Theme " + c.ID,
		Summary:     "A summary of community " + c.ID + ".",
		ContentHash: graph.CommunityContentHash(c),
	}, nil
}

// globalTestGraph builds four dense triangles joined by weaker bridges so
// LouvainDetector coarsens them into a multi-level hierarchy. Entity names
// carry distinctive tokens so query-token overlap ranking is exercisable.
func globalTestGraph() graph.Graph {
	ent := func(id, name string) graph.Entity {
		return graph.Entity{ID: id, Name: name, Type: "t", Description: name + " description"}
	}
	rel := func(s, d string, w float64) graph.Relation {
		return graph.Relation{ID: s + "::" + d, Source: s, Target: d, Relation: "r", Weight: w}
	}
	var ents []graph.Entity
	var rels []graph.Relation
	groups := map[string][]string{
		"a": {"alpha", "andes", "atlas"},
		"b": {"bravo", "borneo", "baltic"},
		"c": {"carbon", "cobalt", "copper"},
		"d": {"delta", "denali", "drake"},
	}
	for _, p := range []string{"a", "b", "c", "d"} {
		names := groups[p]
		n1, n2, n3 := p+"1", p+"2", p+"3"
		ents = append(ents, ent(n1, names[0]), ent(n2, names[1]), ent(n3, names[2]))
		rels = append(rels, rel(n1, n2, 5), rel(n2, n3, 5), rel(n1, n3, 5))
	}
	rels = append(rels,
		rel("a1", "b1", 2),
		rel("c1", "d1", 2),
		rel("b3", "c3", 1),
	)
	return graph.Graph{Entities: ents, Relations: rels}
}

// newGlobalTestStore builds an in-memory store, persists globalTestGraph
// under namespace ns, detects communities with LouvainDetector and persists
// them. It returns the store and the detected community set.
func newGlobalTestStore(t *testing.T, ns string) (*store.InMemoryStore, []graph.Community) {
	t.Helper()
	st := store.NewInMemoryStore(32)
	ctx := context.Background()
	g := globalTestGraph()
	if err := st.UpsertGraph(ctx, ns, g); err != nil {
		t.Fatalf("UpsertGraph: %v", err)
	}
	comms, err := graph.LouvainDetector{}.Detect(ctx, g)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(comms) == 0 {
		t.Fatalf("LouvainDetector produced no communities")
	}
	if err := st.UpsertCommunities(ctx, ns, comms); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}
	return st, comms
}

// coarsestLevel returns the highest Level among comms.
func coarsestLevel(comms []graph.Community) int {
	max := comms[0].Level
	for _, c := range comms[1:] {
		if c.Level > max {
			max = c.Level
		}
	}
	return max
}

// TestAskGlobalMapReduce drives the happy path: AskGlobal selects the
// coarsest-level communities, lazily summarizes them (cache miss ->
// summarizer is called), maps and reduces, and returns the reduce output as
// Answer.Text with Diagnostics.Global populated. A second AskGlobal reuses
// the cached reports — the summarizer is not called again.
func TestAskGlobalMapReduce(t *testing.T) {
	st, comms := newGlobalTestStore(t, "kb")
	model := &globalScriptedModel{
		mapText:   "Score: 80\nThis community is highly relevant to the question.",
		reduceTxt: "The synthesized final answer drawn from the communities.",
	}
	summarizer := &countingSummarizer{inner: staticSummarizer{}}
	sys := New(Options{
		Model:               model,
		Store:               st,
		CommunitySummarizer: summarizer,
	})
	ctx := context.Background()

	ans, err := sys.AskGlobal(ctx, "what are the themes", GlobalOptions{Namespace: "kb"})
	if err != nil {
		t.Fatalf("AskGlobal: %v", err)
	}
	if ans.Text != "The synthesized final answer drawn from the communities." {
		t.Fatalf("Answer.Text = %q, want the reduce output", ans.Text)
	}

	// The selected set must be exactly the coarsest level.
	level := coarsestLevel(comms)
	wantCount := 0
	for _, c := range comms {
		if c.Level == level {
			wantCount++
		}
	}
	if got := len(ans.Diagnostics.Global.CommunityIDs); got != wantCount {
		t.Fatalf("consulted %d communities, want %d (the coarsest level)", got, wantCount)
	}
	if ans.Diagnostics.Global.MapCalls != wantCount {
		t.Fatalf("MapCalls = %d, want %d", ans.Diagnostics.Global.MapCalls, wantCount)
	}
	if ans.Diagnostics.Global.ReduceCalls != 1 {
		t.Fatalf("ReduceCalls = %d, want 1", ans.Diagnostics.Global.ReduceCalls)
	}
	if model.mapCalls != wantCount || model.reduce != 1 {
		t.Fatalf("model calls: map=%d reduce=%d, want map=%d reduce=1", model.mapCalls, model.reduce, wantCount)
	}
	for _, id := range ans.Diagnostics.Global.CommunityIDs {
		if ans.Diagnostics.Global.MapScores[id] != 80 {
			t.Fatalf("MapScores[%s] = %d, want 80", id, ans.Diagnostics.Global.MapScores[id])
		}
	}
	// Cache miss on the first call: one Summarize per consulted community.
	if summarizer.calls != wantCount {
		t.Fatalf("summarizer called %d times, want %d (one per consulted community)", summarizer.calls, wantCount)
	}

	// Second call: every report is a fresh cache hit — no re-summarize.
	before := summarizer.calls
	if _, err := sys.AskGlobal(ctx, "what are the themes", GlobalOptions{Namespace: "kb"}); err != nil {
		t.Fatalf("AskGlobal #2: %v", err)
	}
	if summarizer.calls != before {
		t.Fatalf("summarizer called %d more times on a cache hit, want 0", summarizer.calls-before)
	}
}

// TestAskGlobalStaleReportReSummarizes verifies that a cached report whose
// ContentHash no longer matches the live community triggers a re-summarize.
func TestAskGlobalStaleReportReSummarizes(t *testing.T) {
	st, comms := newGlobalTestStore(t, "kb")
	ctx := context.Background()

	// Pre-seed a stale report for every coarsest-level community.
	level := coarsestLevel(comms)
	var coarse []graph.Community
	for _, c := range comms {
		if c.Level == level {
			coarse = append(coarse, c)
			if err := st.PutCommunityReport(ctx, "kb", graph.CommunityReport{
				CommunityID: c.ID,
				Title:       "stale title",
				Summary:     "stale summary",
				ContentHash: "stale-hash-does-not-match",
			}); err != nil {
				t.Fatalf("PutCommunityReport: %v", err)
			}
		}
	}

	model := &globalScriptedModel{
		mapText:   "Score: 50\nA partial answer.",
		reduceTxt: "Final answer.",
	}
	summarizer := &countingSummarizer{inner: staticSummarizer{}}
	sys := New(Options{Model: model, Store: st, CommunitySummarizer: summarizer})

	ans, err := sys.AskGlobal(ctx, "themes", GlobalOptions{Namespace: "kb"})
	if err != nil {
		t.Fatalf("AskGlobal: %v", err)
	}
	if summarizer.calls != len(coarse) {
		t.Fatalf("stale reports: summarizer called %d times, want %d (one re-summarize per community)", summarizer.calls, len(coarse))
	}
	if ans.Text != "Final answer." {
		t.Fatalf("Answer.Text = %q, want the reduce output", ans.Text)
	}
	// The store now holds fresh reports — their ContentHash matches.
	for _, c := range coarse {
		report, ok, err := st.CommunityReport(ctx, "kb", c.ID)
		if err != nil || !ok {
			t.Fatalf("CommunityReport(%s): ok=%v err=%v", c.ID, ok, err)
		}
		if report.ContentHash != graph.CommunityContentHash(c) {
			t.Fatalf("community %s: cached report still stale after AskGlobal", c.ID)
		}
	}
}

// TestAskGlobalNonCommunityStore verifies graceful degradation: a store that
// does not implement store.CommunityStore yields an empty Answer, no error.
func TestAskGlobalNonCommunityStore(t *testing.T) {
	st := plainStore{Store: store.NewInMemoryStore(32)}
	sys := New(Options{
		Model:               &globalScriptedModel{},
		Store:               st,
		CommunitySummarizer: staticSummarizer{},
	})
	ans, err := sys.AskGlobal(context.Background(), "anything", GlobalOptions{Namespace: "kb"})
	if err != nil {
		t.Fatalf("AskGlobal over a non-CommunityStore: %v", err)
	}
	if ans.Text != "" || len(ans.Diagnostics.Global.CommunityIDs) != 0 {
		t.Fatalf("non-CommunityStore: Answer = %+v, want empty", ans)
	}
}

// TestAskGlobalNoCommunities verifies graceful degradation: a namespace with
// no detected communities yields an empty Answer, no error.
func TestAskGlobalNoCommunities(t *testing.T) {
	st := store.NewInMemoryStore(32)
	sys := New(Options{
		Model:               &globalScriptedModel{},
		Store:               st,
		CommunitySummarizer: staticSummarizer{},
	})
	ans, err := sys.AskGlobal(context.Background(), "anything", GlobalOptions{Namespace: "empty"})
	if err != nil {
		t.Fatalf("AskGlobal over an empty namespace: %v", err)
	}
	if ans.Text != "" || len(ans.Diagnostics.Global.CommunityIDs) != 0 {
		t.Fatalf("empty namespace: Answer = %+v, want empty", ans)
	}
}

// TestAskGlobalMaxCommunitiesCaps verifies MaxCommunities caps the consulted
// set: with the cap set below the coarsest level's size, exactly
// MaxCommunities communities are mapped.
func TestAskGlobalMaxCommunitiesCaps(t *testing.T) {
	st, comms := newGlobalTestStore(t, "kb")
	level := coarsestLevel(comms)
	coarseCount := 0
	for _, c := range comms {
		if c.Level == level {
			coarseCount++
		}
	}
	if coarseCount < 2 {
		t.Skipf("coarsest level has %d communities — need >=2 to exercise the cap", coarseCount)
	}

	model := &globalScriptedModel{
		mapText:   "Score: 60\nPartial.",
		reduceTxt: "Final.",
	}
	sys := New(Options{Model: model, Store: st, CommunitySummarizer: staticSummarizer{}})

	limit := coarseCount - 1
	ans, err := sys.AskGlobal(context.Background(), "carbon cobalt copper",
		GlobalOptions{Namespace: "kb", MaxCommunities: limit})
	if err != nil {
		t.Fatalf("AskGlobal: %v", err)
	}
	if got := len(ans.Diagnostics.Global.CommunityIDs); got != limit {
		t.Fatalf("MaxCommunities=%d: consulted %d communities, want %d", limit, got, limit)
	}
	if ans.Diagnostics.Global.MapCalls != limit {
		t.Fatalf("MapCalls = %d, want %d", ans.Diagnostics.Global.MapCalls, limit)
	}
}

// TestAskGlobalSummarizerRequired verifies that a cache miss with no
// configured summarizer returns ErrCommunitySummarizerRequired.
func TestAskGlobalSummarizerRequired(t *testing.T) {
	st, _ := newGlobalTestStore(t, "kb")
	sys := New(Options{
		Model: &globalScriptedModel{mapText: "Score: 10\nx", reduceTxt: "y"},
		Store: st,
		// No CommunitySummarizer.
	})
	_, err := sys.AskGlobal(context.Background(), "themes", GlobalOptions{Namespace: "kb"})
	if err != ErrCommunitySummarizerRequired {
		t.Fatalf("err = %v, want ErrCommunitySummarizerRequired", err)
	}
}

// TestAskGlobalNoModel verifies AskGlobal returns ErrModelRequired with no model.
func TestAskGlobalNoModel(t *testing.T) {
	st, _ := newGlobalTestStore(t, "kb")
	sys := New(Options{Store: st, CommunitySummarizer: staticSummarizer{}})
	_, err := sys.AskGlobal(context.Background(), "themes", GlobalOptions{Namespace: "kb"})
	if err != ErrModelRequired {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

// TestPrewarmCommunityReports verifies the eager prewarm path: prewarm fills
// the report cache and returns the community count; a subsequent AskGlobal is
// then all cache hits — the summarizer is not called again; and a prewarm
// after a graph change regenerates only the now-stale reports.
func TestPrewarmCommunityReports(t *testing.T) {
	st, comms := newGlobalTestStore(t, "kb")
	summarizer := &countingSummarizer{inner: staticSummarizer{}}
	sys := New(Options{
		Model:               &globalScriptedModel{mapText: "Score: 70\nx", reduceTxt: "y"},
		Store:               st,
		CommunitySummarizer: summarizer,
	})
	ctx := context.Background()

	// Prewarm generates a report for every community in the namespace.
	warmed, err := sys.PrewarmCommunityReports(ctx, "kb")
	if err != nil {
		t.Fatalf("PrewarmCommunityReports: %v", err)
	}
	if warmed != len(comms) {
		t.Fatalf("prewarmed %d reports, want %d (one per community)", warmed, len(comms))
	}
	if summarizer.calls != len(comms) {
		t.Fatalf("summarizer called %d times, want %d", summarizer.calls, len(comms))
	}
	// Every community now has a fresh cached report.
	for _, c := range comms {
		report, ok, err := st.CommunityReport(ctx, "kb", c.ID)
		if err != nil || !ok {
			t.Fatalf("CommunityReport(%s): ok=%v err=%v", c.ID, ok, err)
		}
		if report.ContentHash != graph.CommunityContentHash(c) {
			t.Fatalf("community %s: prewarmed report is not fresh", c.ID)
		}
	}

	// A second prewarm finds every report fresh — nothing regenerated.
	before := summarizer.calls
	warmed, err = sys.PrewarmCommunityReports(ctx, "kb")
	if err != nil {
		t.Fatalf("PrewarmCommunityReports #2: %v", err)
	}
	if warmed != 0 {
		t.Fatalf("second prewarm regenerated %d reports, want 0 (all fresh)", warmed)
	}
	if summarizer.calls != before {
		t.Fatalf("second prewarm called the summarizer %d more times, want 0", summarizer.calls-before)
	}

	// AskGlobal after a prewarm is all cache hits — no summarizer call.
	before = summarizer.calls
	if _, err := sys.AskGlobal(ctx, "what are the themes", GlobalOptions{Namespace: "kb"}); err != nil {
		t.Fatalf("AskGlobal after prewarm: %v", err)
	}
	if summarizer.calls != before {
		t.Fatalf("AskGlobal after prewarm called the summarizer %d times, want 0 (cache hits)", summarizer.calls-before)
	}

	// A graph change makes the affected reports stale; prewarm regenerates
	// only those. Re-persist the community set with one community's
	// membership changed — its CommunityContentHash flips, its cached report
	// goes stale, every other community's report stays fresh.
	changed := make([]graph.Community, len(comms))
	copy(changed, comms)
	changed[0].EntityIDs = append(append([]string(nil), changed[0].EntityIDs...), "extra-entity")
	if err := st.UpsertCommunities(ctx, "kb", changed); err != nil {
		t.Fatalf("UpsertCommunities (changed): %v", err)
	}

	// Count how many of the new community set are stale against the cache.
	stale := 0
	for _, c := range changed {
		report, ok, err := st.CommunityReport(ctx, "kb", c.ID)
		if err != nil {
			t.Fatalf("CommunityReport(%s): %v", c.ID, err)
		}
		if !ok || report.ContentHash != graph.CommunityContentHash(c) {
			stale++
		}
	}
	if stale != 1 {
		t.Fatalf("graph change produced %d stale reports, want exactly 1", stale)
	}

	before = summarizer.calls
	warmed, err = sys.PrewarmCommunityReports(ctx, "kb")
	if err != nil {
		t.Fatalf("PrewarmCommunityReports after graph change: %v", err)
	}
	if warmed != stale {
		t.Fatalf("prewarm after graph change regenerated %d reports, want %d (only the stale ones)", warmed, stale)
	}
	if summarizer.calls-before != stale {
		t.Fatalf("prewarm after graph change called the summarizer %d times, want %d", summarizer.calls-before, stale)
	}
}

// TestPrewarmCommunityReportsNonCommunityStore verifies graceful degradation:
// a store that is not a store.CommunityStore yields 0, nil.
func TestPrewarmCommunityReportsNonCommunityStore(t *testing.T) {
	st := plainStore{Store: store.NewInMemoryStore(32)}
	sys := New(Options{
		Model:               &globalScriptedModel{},
		Store:               st,
		CommunitySummarizer: staticSummarizer{},
	})
	n, err := sys.PrewarmCommunityReports(context.Background(), "kb")
	if err != nil {
		t.Fatalf("PrewarmCommunityReports over a non-CommunityStore: %v", err)
	}
	if n != 0 {
		t.Fatalf("non-CommunityStore: prewarmed %d, want 0", n)
	}
}

// TestPrewarmCommunityReportsNoCommunities verifies graceful degradation: a
// namespace with no detected communities yields 0, nil.
func TestPrewarmCommunityReportsNoCommunities(t *testing.T) {
	st := store.NewInMemoryStore(32)
	sys := New(Options{
		Model:               &globalScriptedModel{},
		Store:               st,
		CommunitySummarizer: staticSummarizer{},
	})
	n, err := sys.PrewarmCommunityReports(context.Background(), "empty")
	if err != nil {
		t.Fatalf("PrewarmCommunityReports over an empty namespace: %v", err)
	}
	if n != 0 {
		t.Fatalf("empty namespace: prewarmed %d, want 0", n)
	}
}

// TestPrewarmCommunityReportsSummarizerRequired verifies a cache miss with no
// configured summarizer returns ErrCommunitySummarizerRequired.
func TestPrewarmCommunityReportsSummarizerRequired(t *testing.T) {
	st, _ := newGlobalTestStore(t, "kb")
	sys := New(Options{
		Model: &globalScriptedModel{},
		Store: st,
		// No CommunitySummarizer.
	})
	_, err := sys.PrewarmCommunityReports(context.Background(), "kb")
	if err != ErrCommunitySummarizerRequired {
		t.Fatalf("err = %v, want ErrCommunitySummarizerRequired", err)
	}
}

// TestAskGlobalDropsZeroScorePartials verifies the reduce step drops every
// score-0 partial: when no community is helpful the answer is the graceful
// "no relevant community information" text and no reduce call is made.
func TestAskGlobalDropsZeroScorePartials(t *testing.T) {
	st, _ := newGlobalTestStore(t, "kb")
	model := &globalScriptedModel{
		mapText:   "Score: 0\nThis community is irrelevant.",
		reduceTxt: "should not be used",
	}
	sys := New(Options{Model: model, Store: st, CommunitySummarizer: staticSummarizer{}})

	ans, err := sys.AskGlobal(context.Background(), "themes", GlobalOptions{Namespace: "kb"})
	if err != nil {
		t.Fatalf("AskGlobal: %v", err)
	}
	if !strings.Contains(strings.ToLower(ans.Text), "no relevant community information") {
		t.Fatalf("Answer.Text = %q, want the graceful no-survivor text", ans.Text)
	}
	if ans.Diagnostics.Global.ReduceCalls != 0 {
		t.Fatalf("ReduceCalls = %d, want 0 — no survivors means no reduce call", ans.Diagnostics.Global.ReduceCalls)
	}
	if model.reduce != 0 {
		t.Fatalf("reduce model called %d times, want 0", model.reduce)
	}
}
