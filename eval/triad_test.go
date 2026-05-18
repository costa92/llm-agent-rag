package eval_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/rag"
)

// wordOverlapJudge is a deterministic stub Judge for the CI gate: real
// LLM-as-judge quality cannot be verified without a model. Groundedness is
// the fraction of context passages sharing a word with the answer; answer
// relevance is 1 if the answer shares a word with the query, else 0.
type wordOverlapJudge struct{}

func (wordOverlapJudge) Judge(_ context.Context, req eval.JudgeRequest) (eval.Judgement, error) {
	answerWords := wordSet(req.Answer)
	supported := 0
	for _, passage := range req.Context {
		if sharesWord(answerWords, passage) {
			supported++
		}
	}
	groundedness := 0.0
	if len(req.Context) > 0 {
		groundedness = float64(supported) / float64(len(req.Context))
	}
	relevance := 0.0
	if sharesWord(answerWords, req.Query) {
		relevance = 1.0
	}
	return eval.Judgement{
		Groundedness:    groundedness,
		AnswerRelevance: relevance,
		Rationale:       "word-overlap stub",
	}, nil
}

func wordSet(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, w := range strings.Fields(strings.ToLower(s)) {
		out[w] = struct{}{}
	}
	return out
}

func sharesWord(set map[string]struct{}, s string) bool {
	for _, w := range strings.Fields(strings.ToLower(s)) {
		if _, ok := set[w]; ok {
			return true
		}
	}
	return false
}

// TestTriadGateMeetsBaseline is the RAG-Triad CI gate. It runs the seed
// dataset through the full Ask pipeline and a deterministic stub judge: a
// retrieval regression drops a retrieval metric below threshold, and a
// plumbing regression drops the generation metric count.
func TestTriadGateMeetsBaseline(t *testing.T) {
	sys := rag.New(rag.Options{
		Model:    fakeModel{},
		Splitter: ingest.NewMarkdownSplitter(500, 50),
	})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{
			ID: "cities",
			Content: "# Cities\nOverview of European capitals.\n" +
				"## Travel\nMuseums and cafes in Paris.\n" +
				"## History\nHistory and capital notes for Berlin.\n" +
				"## History-museums\nHistorical museums across Europe.",
		},
		{
			ID:      "cuisine",
			Content: "# Cuisine\nRegional dishes.\n## Pastry\nFrench pastries.\n",
		},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import geo: %v", err)
	}
	_, err = sys.Import(context.Background(), []ingest.Document{
		{ID: "noise", Content: "# Noise\nUnrelated text about programming and operating systems."},
	}, ingest.ImportOptions{Namespace: "other"})
	if err != nil {
		t.Fatalf("Import other: %v", err)
	}

	dataset := eval.Dataset{
		Name: "phase16-triad-seed",
		TopK: 3,
		Examples: []eval.Example{
			{Query: "paris museums", Namespace: "geo", GoldDocIDs: []string{"cities"}, GoldChunkIDs: []string{"cities:1"}},
			{Query: "history museums", Namespace: "geo", GoldDocIDs: []string{"cities"}, GoldChunkIDs: []string{"cities:2", "cities:3"}},
			{Query: "french pastries", Namespace: "geo", GoldDocIDs: []string{"cuisine"}, GoldChunkIDs: []string{"cuisine:1"}},
			{Query: "programming notes", Namespace: "other", GoldDocIDs: []string{"noise"}, GoldChunkIDs: []string{"noise:0"}},
		},
	}

	res, err := eval.TriadEvaluator{Asker: sys, Judge: wordOverlapJudge{}}.Run(context.Background(), dataset)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	const (
		minPrecision = 0.25
		minRecall    = 0.75
		minMRR       = 0.75
		minGrounding = 0.5
	)
	if res.Retrieval.PrecisionAtK < minPrecision {
		t.Fatalf("PrecisionAtK = %v, want >= %v\nper-example: %+v", res.Retrieval.PrecisionAtK, minPrecision, res.PerExample)
	}
	if res.Retrieval.RecallAtK < minRecall {
		t.Fatalf("RecallAtK = %v, want >= %v\nper-example: %+v", res.Retrieval.RecallAtK, minRecall, res.PerExample)
	}
	if res.Retrieval.MRR < minMRR {
		t.Fatalf("MRR = %v, want >= %v\nper-example: %+v", res.Retrieval.MRR, minMRR, res.PerExample)
	}
	if res.Retrieval.GroundingAtK < minGrounding {
		t.Fatalf("GroundingAtK = %v, want >= %v\nper-example: %+v", res.Retrieval.GroundingAtK, minGrounding, res.PerExample)
	}

	if res.Generation.Examples != len(dataset.Examples) {
		t.Fatalf("Generation.Examples = %d, want %d", res.Generation.Examples, len(dataset.Examples))
	}
	if res.Generation.MeanGroundedness < 0 || res.Generation.MeanGroundedness > 1 {
		t.Fatalf("MeanGroundedness = %v, want within [0,1]", res.Generation.MeanGroundedness)
	}
	if res.Generation.MeanAnswerRelevance < 0 || res.Generation.MeanAnswerRelevance > 1 {
		t.Fatalf("MeanAnswerRelevance = %v, want within [0,1]", res.Generation.MeanAnswerRelevance)
	}
	if len(res.PerExample) != len(dataset.Examples) {
		t.Fatalf("PerExample len = %d, want %d", len(res.PerExample), len(dataset.Examples))
	}

	var buf bytes.Buffer
	if err := eval.WriteJSONL(&buf, res); err != nil {
		t.Fatalf("WriteJSONL: %v", err)
	}
	lines := strings.Count(strings.TrimSpace(buf.String()), "\n") + 1
	if lines != len(dataset.Examples)+1 {
		t.Fatalf("WriteJSONL produced %d lines, want %d (per-example + summary)", lines, len(dataset.Examples)+1)
	}
	if res.Summary() == "" {
		t.Fatalf("Summary() is empty")
	}
}

func TestTriadEvaluatorRequiresAskerAndJudge(t *testing.T) {
	ds := eval.Dataset{Name: "d", TopK: 3, Examples: []eval.Example{{Query: "q"}}}
	if _, err := (eval.TriadEvaluator{Judge: wordOverlapJudge{}}).Run(context.Background(), ds); err == nil {
		t.Fatalf("Run with nil Asker: want error")
	}
	sys := rag.New(rag.Options{Model: fakeModel{}})
	if _, err := (eval.TriadEvaluator{Asker: sys}).Run(context.Background(), ds); err == nil {
		t.Fatalf("Run with nil Judge: want error")
	}
}
