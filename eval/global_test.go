package eval_test

import (
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/rag"
	"github.com/costa92/llm-agent-rag/store"
)

// globalEvalModel is a deterministic generate.Model for the global-search
// eval gate. AskGlobal drives three kinds of generation — community
// summarization, the per-community map step, and the reduce step — and this
// model serves a fixed reply for each, keyed off the request's SystemPrompt,
// so the whole harness is reproducible with no live calls.
type globalEvalModel struct{}

func (globalEvalModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	switch {
	case strings.HasPrefix(req.SystemPrompt, "You summarize one community"):
		return generate.Response{Text: "Title: Theme Report\nThis community covers themed knowledge-graph entities."}, nil
	case strings.HasPrefix(req.SystemPrompt, "You are answering a whole-corpus question using ONE community summary"):
		return generate.Response{Text: "Score: 75\nThis community contributes themed knowledge to the answer."}, nil
	case strings.HasPrefix(req.SystemPrompt, "You are answering a whole-corpus question.\nYou are given"):
		return generate.Response{Text: "The corpus describes several themed communities of knowledge-graph entities."}, nil
	default:
		return generate.Response{Text: ""}, nil
	}
}

// recordingJudge is a deterministic stub Judge for the global-search gate. It
// records each JudgeRequest so a test can prove the evaluator passed the
// consulted-report context, and scores groundedness as the fraction of context
// passages sharing a word with the answer (answer relevance: 1 if the answer
// shares a word with the query, else 0).
type recordingJudge struct {
	requests []eval.JudgeRequest
}

func (j *recordingJudge) Judge(_ context.Context, req eval.JudgeRequest) (eval.Judgement, error) {
	j.requests = append(j.requests, req)
	answerWords := globalWordSet(req.Answer)
	supported := 0
	for _, passage := range req.Context {
		if globalSharesWord(answerWords, passage) {
			supported++
		}
	}
	groundedness := 0.0
	if len(req.Context) > 0 {
		groundedness = float64(supported) / float64(len(req.Context))
	}
	relevance := 0.0
	if globalSharesWord(answerWords, req.Query) {
		relevance = 1.0
	}
	return eval.Judgement{
		Groundedness:    groundedness,
		AnswerRelevance: relevance,
		Rationale:       "global word-overlap stub",
	}, nil
}

func globalWordSet(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, w := range strings.Fields(strings.ToLower(s)) {
		out[w] = struct{}{}
	}
	return out
}

func globalSharesWord(set map[string]struct{}, s string) bool {
	for _, w := range strings.Fields(strings.ToLower(s)) {
		if _, ok := set[w]; ok {
			return true
		}
	}
	return false
}

// globalEvalTestSystem builds a rag.System over an in-memory store seeded with
// a small knowledge graph and its detected communities, under namespace ns.
// The graph is four dense triangles joined by weak bridges so LouvainDetector
// coarsens them into a multi-level hierarchy — the same shape the rag package
// exercises AskGlobal with.
func globalEvalTestSystem(t *testing.T, ns string) *rag.System {
	t.Helper()
	ctx := context.Background()
	st := store.NewInMemoryStore(32)

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
	rels = append(rels, rel("a1", "b1", 2), rel("c1", "d1", 2), rel("b3", "c3", 1))
	g := graph.Graph{Entities: ents, Relations: rels}

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

	return rag.New(rag.Options{
		Model:               globalEvalModel{},
		Store:               st,
		CommunitySummarizer: graph.LLMCommunitySummarizer{Model: globalEvalModel{}},
	})
}

// TestGlobalEvalGate is the global-search CI gate. It runs a dataset of
// whole-corpus questions through GlobalEvaluator over a scripted model and a
// scripted judge — no live calls — and asserts every example is scored, the
// means land in [0,1], and the judge is handed the consulted-report context.
func TestGlobalEvalGate(t *testing.T) {
	sys := globalEvalTestSystem(t, "kb")
	dataset := eval.Dataset{
		Name: "phase25-global-seed",
		TopK: 3, // unused by global search; present for dataset shape parity.
		Examples: []eval.Example{
			{Query: "what themes does the corpus cover", Namespace: "kb"},
			{Query: "describe the knowledge-graph communities", Namespace: "kb"},
			{Query: "summarize the corpus entities", Namespace: "kb"},
		},
	}

	judge := &recordingJudge{}
	res, err := eval.GlobalEvaluator{Asker: sys, Judge: judge}.Run(context.Background(), dataset)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Examples != len(dataset.Examples) {
		t.Fatalf("Examples = %d, want %d", res.Examples, len(dataset.Examples))
	}
	if len(res.PerExample) != len(dataset.Examples) {
		t.Fatalf("PerExample len = %d, want %d", len(res.PerExample), len(dataset.Examples))
	}
	if res.MeanGroundedness < 0 || res.MeanGroundedness > 1 {
		t.Fatalf("MeanGroundedness = %v, want within [0,1]", res.MeanGroundedness)
	}
	if res.MeanAnswerRelevance < 0 || res.MeanAnswerRelevance > 1 {
		t.Fatalf("MeanAnswerRelevance = %v, want within [0,1]", res.MeanAnswerRelevance)
	}
	// The scripted reduce answer shares words with every question, so the
	// stub judge scores answer relevance 1.0 for all examples.
	if res.MeanAnswerRelevance != 1.0 {
		t.Fatalf("MeanAnswerRelevance = %v, want 1.0 (scripted answer is on-topic)", res.MeanAnswerRelevance)
	}
	// The scripted answer mentions "communities" and "entities" — words that
	// appear in the consulted reports' summaries — so groundedness is > 0:
	// proof the consulted-report context reached the judge.
	if res.MeanGroundedness <= 0 {
		t.Fatalf("MeanGroundedness = %v, want > 0 (consulted reports should ground the answer)", res.MeanGroundedness)
	}

	// Every judged request must carry the consulted-report context — that is
	// the global-search grounding signal, not chunk hits.
	if len(judge.requests) != len(dataset.Examples) {
		t.Fatalf("judge saw %d requests, want %d", len(judge.requests), len(dataset.Examples))
	}
	for i, req := range judge.requests {
		if len(req.Context) == 0 {
			t.Fatalf("judge request %d had no context — consulted reports were not passed", i)
		}
		for j, passage := range req.Context {
			if strings.TrimSpace(passage) == "" {
				t.Fatalf("judge request %d context[%d] is empty", i, j)
			}
		}
	}

	// Each per-example result records the communities AskGlobal consulted.
	for i, ex := range res.PerExample {
		if len(ex.CommunityIDs) == 0 {
			t.Fatalf("PerExample[%d] consulted no communities", i)
		}
	}

	if res.Summary() == "" {
		t.Fatalf("Summary() is empty")
	}
}

// TestGlobalEvaluatorRequiresAskerAndJudge verifies the nil-guard: Run errors
// when the GlobalAsker or the Judge is missing.
func TestGlobalEvaluatorRequiresAskerAndJudge(t *testing.T) {
	ds := eval.Dataset{Name: "d", TopK: 3, Examples: []eval.Example{{Query: "q", Namespace: "kb"}}}
	if _, err := (eval.GlobalEvaluator{Judge: &recordingJudge{}}).Run(context.Background(), ds); err == nil {
		t.Fatalf("Run with nil Asker: want error")
	}
	sys := globalEvalTestSystem(t, "kb")
	if _, err := (eval.GlobalEvaluator{Asker: sys}).Run(context.Background(), ds); err == nil {
		t.Fatalf("Run with nil Judge: want error")
	}
}
