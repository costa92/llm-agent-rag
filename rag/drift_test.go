package rag

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/store"
)

// driftScriptedModel is a deterministic generate.Model for the AskDrift
// tests. It keys responses by the request's SystemPrompt so the three DRIFT
// stages are distinguishable: the primer map step, each local follow-up
// round, and the synthesis. localResponses is served one entry per local
// round (the loop body); a round past the slice's end gets the last entry,
// which lets a test make the model emit follow-ups indefinitely to exercise
// the round cap. zeroScoreMarkers names report substrings whose map step
// should score 0 — so a test can keep some communities out of the primer's
// seed set.
type driftScriptedModel struct {
	mu               sync.Mutex
	mapText          string
	zeroScoreMarkers []string
	localResponses   []string
	synthesisText    string

	mapCalls   int
	localCalls int
	synthCalls int
}

func (m *driftScriptedModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case strings.HasPrefix(req.SystemPrompt, "You are answering a whole-corpus question using ONE community summary"):
		m.mapCalls++
		content := ""
		if len(req.Messages) > 0 {
			content = req.Messages[0].Content
		}
		for _, marker := range m.zeroScoreMarkers {
			if strings.Contains(content, marker) {
				return generate.Response{Text: "Score: 0\nThis community is irrelevant."}, nil
			}
		}
		return generate.Response{Text: m.mapText}, nil
	case strings.HasPrefix(req.SystemPrompt, "You are answering a question using a slice of a knowledge graph"):
		idx := m.localCalls
		m.localCalls++
		if len(m.localResponses) == 0 {
			return generate.Response{Text: "A local partial answer.\nFollow-up: none"}, nil
		}
		if idx >= len(m.localResponses) {
			idx = len(m.localResponses) - 1
		}
		return generate.Response{Text: m.localResponses[idx]}, nil
	case strings.HasPrefix(req.SystemPrompt, "You are answering a question using DRIFT search."):
		m.synthCalls++
		return generate.Response{Text: m.synthesisText}, nil
	default:
		return generate.Response{Text: ""}, nil
	}
}

// driftTestGraph builds four dense triangles joined by weak bridges (the
// AskGlobal test graph), with every entity carrying a provenance chunk so the
// local loop has passages to pack. Entity names are unique so FindEntities
// resolves a follow-up name to exactly one entity.
func driftTestGraph() graph.Graph {
	ent := func(id, name string) graph.Entity {
		return graph.Entity{
			ID:             id,
			Name:           name,
			Type:           "t",
			Description:    name + " description",
			SourceChunkIDs: []string{"chunk-" + id},
		}
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

// newDriftTestStore builds an in-memory store, persists driftTestGraph and a
// provenance chunk per entity, detects communities and persists them. It
// returns the store and the detected community set.
func newDriftTestStore(t *testing.T, ns string) (*store.InMemoryStore, []graph.Community) {
	t.Helper()
	st := store.NewInMemoryStore(32)
	ctx := context.Background()
	g := driftTestGraph()
	if err := st.UpsertGraph(ctx, ns, g); err != nil {
		t.Fatalf("UpsertGraph: %v", err)
	}
	// One provenance chunk per entity — the local loop loads these via Get.
	zero := make(embed.Vector, 32)
	chunks := make([]store.StoredChunk, 0, len(g.Entities))
	for _, e := range g.Entities {
		chunks = append(chunks, store.StoredChunk{
			ID:        "chunk-" + e.ID,
			Namespace: ns,
			DocID:     "doc-" + e.ID,
			Content:   "Passage about " + e.Name + ".",
			Vector:    zero,
		})
	}
	if err := st.Upsert(ctx, chunks); err != nil {
		t.Fatalf("Upsert chunks: %v", err)
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

// TestAskDriftPrimerLoopSynthesis drives the happy path: AskDrift runs the
// primer map step, the bounded local follow-up loop, and the synthesis. The
// final Answer.Text is the synthesis output, and Diagnostics.Drift records
// the primer communities, the rounds run, and the per-round seed entity IDs.
func TestAskDriftPrimerLoopSynthesis(t *testing.T) {
	st, comms := newDriftTestStore(t, "kb")
	model := &driftScriptedModel{
		mapText: "Score: 80\nThis community contributes to the answer.",
		// Community L1-b1 (the bravo/borneo/baltic triangle) scores 0, so its
		// members are NOT primer seeds — that lets round 1's "bravo"
		// follow-up resolve to a genuinely-new entity (b1).
		zeroScoreMarkers: []string{"L1-b1"},
		localResponses: []string{
			// Round 0 emits a follow-up that resolves to a real entity.
			"Round 0 partial answer.\nFollow-up: bravo",
			// Round 1 emits none — the loop terminates after this round.
			"Round 1 partial answer.\nFollow-up: none",
		},
		synthesisText: "The synthesized DRIFT final answer.",
	}
	sys := New(Options{Model: model, Store: st, CommunitySummarizer: staticSummarizer{}})
	ctx := context.Background()

	ans, err := sys.AskDrift(ctx, "what are the themes", DriftOptions{Namespace: "kb"})
	if err != nil {
		t.Fatalf("AskDrift: %v", err)
	}
	if ans.Text != "The synthesized DRIFT final answer." {
		t.Fatalf("Answer.Text = %q, want the synthesis output", ans.Text)
	}

	// The primer mapped the coarsest level.
	level := coarsestLevel(comms)
	wantPrimer := 0
	for _, c := range comms {
		if c.Level == level {
			wantPrimer++
		}
	}
	if got := len(ans.Diagnostics.Drift.PrimerCommunityIDs); got != wantPrimer {
		t.Fatalf("PrimerCommunityIDs = %d, want %d (the coarsest level)", got, wantPrimer)
	}
	if model.mapCalls != wantPrimer {
		t.Fatalf("map calls = %d, want %d", model.mapCalls, wantPrimer)
	}
	if len(ans.Diagnostics.Drift.ConsultedReports) != wantPrimer {
		t.Fatalf("ConsultedReports = %d, want %d", len(ans.Diagnostics.Drift.ConsultedReports), wantPrimer)
	}

	// The loop ran two rounds (round 0 surfaced a follow-up, round 1 did not).
	if ans.Diagnostics.Drift.Rounds != 2 {
		t.Fatalf("Drift.Rounds = %d, want 2", ans.Diagnostics.Drift.Rounds)
	}
	if model.localCalls != 2 {
		t.Fatalf("local calls = %d, want 2", model.localCalls)
	}
	if len(ans.Diagnostics.Drift.RoundEntityIDs) != 2 {
		t.Fatalf("RoundEntityIDs has %d rounds, want 2", len(ans.Diagnostics.Drift.RoundEntityIDs))
	}
	// Round-0 seeds are the primer communities' member entities — non-empty
	// and sorted.
	r0 := ans.Diagnostics.Drift.RoundEntityIDs[0]
	if len(r0) == 0 {
		t.Fatalf("round 0 had no seed entities")
	}
	for i := 1; i < len(r0); i++ {
		if r0[i-1] >= r0[i] {
			t.Fatalf("round 0 seed entity IDs not sorted/deduped: %v", r0)
		}
	}
	// Round-1 seeds came from the "bravo" follow-up -> entity b1.
	r1 := ans.Diagnostics.Drift.RoundEntityIDs[1]
	if len(r1) != 1 || r1[0] != "b1" {
		t.Fatalf("round 1 seeds = %v, want [b1] (resolved from follow-up \"bravo\")", r1)
	}
	if model.synthCalls != 1 {
		t.Fatalf("synthesis calls = %d, want 1", model.synthCalls)
	}
}

// TestAskDriftLoopTerminatesEarly verifies the loop stops as soon as the
// scripted model emits no follow-up entities: with the round budget at 3 but
// round 0 emitting "none", exactly one round runs.
func TestAskDriftLoopTerminatesEarly(t *testing.T) {
	st, _ := newDriftTestStore(t, "kb")
	model := &driftScriptedModel{
		mapText:        "Score: 50\nA partial.",
		localResponses: []string{"Only round partial.\nFollow-up: none"},
		synthesisText:  "Final.",
	}
	sys := New(Options{Model: model, Store: st, CommunitySummarizer: staticSummarizer{}})

	ans, err := sys.AskDrift(context.Background(), "themes", DriftOptions{Namespace: "kb", Rounds: 3})
	if err != nil {
		t.Fatalf("AskDrift: %v", err)
	}
	if ans.Diagnostics.Drift.Rounds != 1 {
		t.Fatalf("Drift.Rounds = %d, want 1 (loop terminates on no follow-ups)", ans.Diagnostics.Drift.Rounds)
	}
	if ans.Diagnostics.Drift.Rounds >= 3 {
		t.Fatalf("Drift.Rounds = %d reached the cap — early termination did not fire", ans.Diagnostics.Drift.Rounds)
	}
	if model.localCalls != 1 {
		t.Fatalf("local calls = %d, want 1", model.localCalls)
	}
}

// TestAskDriftRoundCapHolds verifies the loop is hard-bounded: even when the
// model keeps emitting fresh follow-up entities every round, the loop never
// runs more than driftMaxRounds (3) rounds.
func TestAskDriftRoundCapHolds(t *testing.T) {
	st, _ := newDriftTestStore(t, "kb")
	// Only community L1-a1 scores non-zero, so the primer seeds just the
	// a* triangle. Each round then names a different, not-yet-seen entity
	// so the loop always has new seeds — the round cap is the only thing
	// that can stop it.
	model := &driftScriptedModel{
		mapText:          "Score: 90\nPartial.",
		zeroScoreMarkers: []string{"L1-b1", "L1-c1", "L1-d1"},
		localResponses: []string{
			"R0.\nFollow-up: bravo",
			"R1.\nFollow-up: carbon",
			"R2.\nFollow-up: delta",
			"R3.\nFollow-up: denali",
			"R4.\nFollow-up: drake",
		},
		synthesisText: "Final.",
	}
	sys := New(Options{Model: model, Store: st, CommunitySummarizer: staticSummarizer{}})

	// Ask for 10 rounds — the option must be clamped to driftMaxRounds.
	ans, err := sys.AskDrift(context.Background(), "themes", DriftOptions{Namespace: "kb", Rounds: 10})
	if err != nil {
		t.Fatalf("AskDrift: %v", err)
	}
	if ans.Diagnostics.Drift.Rounds != driftMaxRounds {
		t.Fatalf("Drift.Rounds = %d, want %d (the hard round cap)", ans.Diagnostics.Drift.Rounds, driftMaxRounds)
	}
	if model.localCalls != driftMaxRounds {
		t.Fatalf("local calls = %d, want %d", model.localCalls, driftMaxRounds)
	}
}

// TestAskDriftNonCommunityStore verifies graceful degradation: a store that
// implements neither CommunityStore nor GraphStore yields an empty primer and
// an empty local loop — AskDrift returns the no-information answer, no error.
func TestAskDriftNonCommunityStore(t *testing.T) {
	st := plainStore{Store: store.NewInMemoryStore(32)}
	model := &driftScriptedModel{synthesisText: "should not be reached"}
	sys := New(Options{Model: model, Store: st, CommunitySummarizer: staticSummarizer{}})

	ans, err := sys.AskDrift(context.Background(), "anything", DriftOptions{Namespace: "kb"})
	if err != nil {
		t.Fatalf("AskDrift over a non-CommunityStore: %v", err)
	}
	if len(ans.Diagnostics.Drift.PrimerCommunityIDs) != 0 {
		t.Fatalf("non-CommunityStore: PrimerCommunityIDs = %v, want empty", ans.Diagnostics.Drift.PrimerCommunityIDs)
	}
	if ans.Diagnostics.Drift.Rounds != 0 {
		t.Fatalf("non-GraphStore: Drift.Rounds = %d, want 0", ans.Diagnostics.Drift.Rounds)
	}
	if !strings.Contains(strings.ToLower(ans.Text), "no information was found") {
		t.Fatalf("Answer.Text = %q, want the graceful no-information text", ans.Text)
	}
	if model.synthCalls != 0 {
		t.Fatalf("synthesis called %d times, want 0 (nothing to synthesize)", model.synthCalls)
	}
}

// TestAskDriftEmptyNamespaceLocalOnly verifies graceful degradation when the
// namespace has no detected communities: the primer is empty, the local loop
// has no seeds and runs zero rounds, and AskDrift still returns without error.
func TestAskDriftEmptyNamespaceLocalOnly(t *testing.T) {
	st := store.NewInMemoryStore(32)
	model := &driftScriptedModel{synthesisText: "unused"}
	sys := New(Options{Model: model, Store: st, CommunitySummarizer: staticSummarizer{}})

	ans, err := sys.AskDrift(context.Background(), "anything", DriftOptions{Namespace: "empty"})
	if err != nil {
		t.Fatalf("AskDrift over an empty namespace: %v", err)
	}
	if len(ans.Diagnostics.Drift.PrimerCommunityIDs) != 0 {
		t.Fatalf("empty namespace: PrimerCommunityIDs = %v, want empty", ans.Diagnostics.Drift.PrimerCommunityIDs)
	}
	if ans.Diagnostics.Drift.Rounds != 0 {
		t.Fatalf("empty namespace: Drift.Rounds = %d, want 0", ans.Diagnostics.Drift.Rounds)
	}
	if !strings.Contains(strings.ToLower(ans.Text), "no information was found") {
		t.Fatalf("Answer.Text = %q, want the graceful no-information text", ans.Text)
	}
}

// TestAskDriftNoModel verifies AskDrift returns ErrModelRequired with no model.
func TestAskDriftNoModel(t *testing.T) {
	st, _ := newDriftTestStore(t, "kb")
	sys := New(Options{Store: st, CommunitySummarizer: staticSummarizer{}})
	_, err := sys.AskDrift(context.Background(), "themes", DriftOptions{Namespace: "kb"})
	if err != ErrModelRequired {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

// TestAskDriftMalformedLocalOutput verifies the lenient follow-up parse: a
// local response with no "Follow-up:" marker yields the whole text as the
// partial and no follow-ups — the loop then terminates safely.
func TestAskDriftMalformedLocalOutput(t *testing.T) {
	st, _ := newDriftTestStore(t, "kb")
	model := &driftScriptedModel{
		mapText:        "Score: 40\nPartial.",
		localResponses: []string{"A partial answer with no follow-up marker at all."},
		synthesisText:  "Final.",
	}
	sys := New(Options{Model: model, Store: st, CommunitySummarizer: staticSummarizer{}})

	ans, err := sys.AskDrift(context.Background(), "themes", DriftOptions{Namespace: "kb", Rounds: 3})
	if err != nil {
		t.Fatalf("AskDrift: %v", err)
	}
	if ans.Diagnostics.Drift.Rounds != 1 {
		t.Fatalf("Drift.Rounds = %d, want 1 (malformed output -> no follow-ups -> terminate)", ans.Diagnostics.Drift.Rounds)
	}
}
