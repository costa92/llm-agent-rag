package eval_test

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/rag"
	"github.com/costa92/llm-agent-rag/store"
)

type fakeModel struct{}

func (fakeModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	if len(req.Messages) > 0 {
		return generate.Response{Text: req.Messages[0].Content}, nil
	}
	return generate.Response{}, nil
}

// TestSeedDatasetMeetsBaselineMetrics is the CI gate. A regression in
// route policy, fanout, namespace isolation, or grounding will drop one
// of the four headline metrics below the threshold and fail this test.
func TestSeedDatasetMeetsBaselineMetrics(t *testing.T) {
	sys := rag.New(rag.Options{
		Model:    fakeModel{},
		Splitter: ingest.NewMarkdownSplitter(500, 50),
	})

	// Corpus exercises Phase 11 structure-aware retrieval shapes:
	// - per-section sub-headings (route discrimination)
	// - distractor namespace
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
		{
			ID:      "noise",
			Content: "# Noise\nUnrelated text about programming and operating systems.",
		},
	}, ingest.ImportOptions{Namespace: "other"})
	if err != nil {
		t.Fatalf("Import other: %v", err)
	}

	dataset := eval.Dataset{
		Name: "phase11-seed",
		TopK: 3,
		Examples: []eval.Example{
			{
				Query:        "paris museums",
				Namespace:    "geo",
				GoldDocIDs:   []string{"cities"},
				GoldChunkIDs: []string{"cities:1"}, // Travel chunk
				Notes:        "single dominant route (Travel)",
			},
			{
				Query:        "history museums",
				Namespace:    "geo",
				GoldDocIDs:   []string{"cities"},
				GoldChunkIDs: []string{"cities:2", "cities:3"}, // History or History-museums
				Notes:        "ambiguous between two routes",
			},
			{
				Query:        "french pastries",
				Namespace:    "geo",
				GoldDocIDs:   []string{"cuisine"},
				GoldChunkIDs: []string{"cuisine:1"},
				Notes:        "cross-document retrieval",
			},
			{
				Query:        "programming notes",
				Namespace:    "other",
				GoldDocIDs:   []string{"noise"},
				GoldChunkIDs: []string{"noise:0"},
				Notes:        "namespace isolation: hit lives only in 'other'",
			},
		},
	}

	// Baseline eval runs hybrid retrieval without auto-route narrowing so
	// the dataset scores the full-corpus ranking. Auto-route quality is
	// covered by unit tests in retrieve/ and rag/.
	ev := eval.Evaluator{
		Retriever: sys,
		Options:   rag.SearchOptions{},
	}
	res, err := ev.Run(context.Background(), dataset)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Per-metric thresholds — chosen to be tight against today's pipeline
	// so any regression in retrieval ranking, namespace isolation, or
	// content grounding breaks this test.
	//
	// PrecisionAtK is naturally capped at 1/k when each example has one
	// gold doc, so its bar is lower; the strong regression signal lives
	// in MRR (rank-1 quality), RecallAtK (coverage), and GroundingAtK
	// (chunk-level match).
	const (
		minPrecision = 0.25
		minRecall    = 0.75
		minMRR       = 0.75
		minGrounding = 0.5
	)
	if res.Metrics.PrecisionAtK < minPrecision {
		t.Fatalf("PrecisionAtK = %v, want >= %v.\nper-example: %+v", res.Metrics.PrecisionAtK, minPrecision, res.PerExample)
	}
	if res.Metrics.RecallAtK < minRecall {
		t.Fatalf("RecallAtK = %v, want >= %v.\nper-example: %+v", res.Metrics.RecallAtK, minRecall, res.PerExample)
	}
	if res.Metrics.MRR < minMRR {
		t.Fatalf("MRR = %v, want >= %v.\nper-example: %+v", res.Metrics.MRR, minMRR, res.PerExample)
	}
	if res.Metrics.GroundingAtK < minGrounding {
		t.Fatalf("GroundingAtK = %v, want >= %v.\nper-example: %+v", res.Metrics.GroundingAtK, minGrounding, res.PerExample)
	}

	if res.Metrics.Examples != len(dataset.Examples) {
		t.Fatalf("Metrics.Examples = %d, want %d", res.Metrics.Examples, len(dataset.Examples))
	}
	if res.Metrics.TopK != dataset.TopK {
		t.Fatalf("Metrics.TopK = %d, want %d", res.Metrics.TopK, dataset.TopK)
	}
}

func TestEvaluatorRunRejectsNilRetriever(t *testing.T) {
	_, err := eval.Evaluator{}.Run(context.Background(), eval.Dataset{TopK: 1})
	if err == nil {
		t.Fatalf("expected error for nil retriever")
	}
}

func TestEvaluatorRunRejectsZeroTopK(t *testing.T) {
	_, err := eval.Evaluator{Retriever: stubRetriever{}}.Run(context.Background(), eval.Dataset{Name: "empty"})
	if err == nil {
		t.Fatalf("expected error for zero TopK")
	}
}

// stubRetriever satisfies eval.Retriever without doing anything real.
type stubRetriever struct{}

func (stubRetriever) Retrieve(_ context.Context, _ string, _ rag.SearchOptions) ([]store.Hit, error) {
	return nil, nil
}
