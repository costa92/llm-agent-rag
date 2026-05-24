package eval_test

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
)

// TestBenchmarkResult_Markdown_EmptyDataset pins the empty-dataset
// contract: Examples renders as 0 and every other metric renders as
// n/a (math.NaN()). AdoptedRound section is omitted (nil counts).
func TestBenchmarkResult_Markdown_EmptyDataset(t *testing.T) {
	r := eval.BenchmarkResult{
		Dataset: eval.AnswerDataset{Name: "empty"},
		Metrics: eval.BenchmarkMetrics{
			Examples:                0,
			ExactMatch:              math.NaN(),
			F1Token:                 math.NaN(),
			RequiredPhraseRecall:    math.NaN(),
			ReflectionRoundsMean:    math.NaN(),
			GraderAdoptionRate:      math.NaN(),
			FollowupQueriesUsedMean: math.NaN(),
			ActiveRetrievalFireRate: math.NaN(),
			MeanGroundedness:        math.NaN(),
			MeanAnswerRelevance:     math.NaN(),
		},
	}
	md := r.Markdown()
	if !strings.Contains(md, "# Benchmark: empty") {
		t.Fatalf("header missing: %s", md)
	}
	if !strings.Contains(md, "| Examples | 0 |") {
		t.Fatalf("Examples row missing or non-zero in empty dataset: %s", md)
	}
	// Every NaN field must render as "n/a".
	for _, field := range []string{
		"ExactMatch", "F1Token", "RequiredPhraseRecall",
		"ReflectionRoundsMean", "GraderAdoptionRate",
		"FollowupQueriesUsedMean", "ActiveRetrievalFireRate",
		"MeanGroundedness", "MeanAnswerRelevance",
	} {
		row := fmt.Sprintf("| %s | n/a |", field)
		if !strings.Contains(md, row) {
			t.Fatalf("missing NaN row %q in:\n%s", row, md)
		}
	}
	if strings.Contains(md, "## AdoptedRound Distribution") {
		t.Fatalf("AdoptedRound section should be omitted on nil counts: %s", md)
	}
}

// TestBenchmarkResult_Markdown_FullMetrics pins that every metric row is
// emitted in declaration order when all metrics are finite. Counts the
// 9 rows after Examples and verifies their values are formatted with
// three decimal places.
func TestBenchmarkResult_Markdown_FullMetrics(t *testing.T) {
	r := eval.BenchmarkResult{
		Dataset: eval.AnswerDataset{Name: "full"},
		Metrics: eval.BenchmarkMetrics{
			Examples:                100,
			ExactMatch:              0.823,
			F1Token:                 0.71,
			RequiredPhraseRecall:    0.5,
			ReflectionRoundsMean:    1.6,
			GraderAdoptionRate:      0.4,
			FollowupQueriesUsedMean: 2.0,
			ActiveRetrievalFireRate: 0.75,
			MeanGroundedness:        0.91,
			MeanAnswerRelevance:     0.78,
		},
	}
	md := r.Markdown()
	cases := []struct {
		name, want string
	}{
		{"Examples", "| Examples | 100 |"},
		{"ExactMatch", "| ExactMatch | 0.823 |"},
		{"F1Token", "| F1Token | 0.710 |"},
		{"RequiredPhraseRecall", "| RequiredPhraseRecall | 0.500 |"},
		{"ReflectionRoundsMean", "| ReflectionRoundsMean | 1.600 |"},
		{"GraderAdoptionRate", "| GraderAdoptionRate | 0.400 |"},
		{"FollowupQueriesUsedMean", "| FollowupQueriesUsedMean | 2.000 |"},
		{"ActiveRetrievalFireRate", "| ActiveRetrievalFireRate | 0.750 |"},
		{"MeanGroundedness", "| MeanGroundedness | 0.910 |"},
		{"MeanAnswerRelevance", "| MeanAnswerRelevance | 0.780 |"},
	}
	for _, c := range cases {
		if !strings.Contains(md, c.want) {
			t.Fatalf("missing %s row %q in:\n%s", c.name, c.want, md)
		}
	}
}

// TestBenchmarkResult_Markdown_OmitsAdoptedRoundSectionWhenNil pins that
// an empty AdoptedRoundCounts elides the histogram section entirely.
func TestBenchmarkResult_Markdown_OmitsAdoptedRoundSectionWhenNil(t *testing.T) {
	r := eval.BenchmarkResult{
		Dataset: eval.AnswerDataset{Name: "no-reflection"},
		Metrics: eval.BenchmarkMetrics{
			Examples:           5,
			AdoptedRoundCounts: nil,
		},
	}
	if strings.Contains(r.Markdown(), "AdoptedRound Distribution") {
		t.Fatalf("AdoptedRound section should be absent")
	}
	r.Metrics.AdoptedRoundCounts = []int{}
	if strings.Contains(r.Markdown(), "AdoptedRound Distribution") {
		t.Fatalf("AdoptedRound section should be absent for empty slice")
	}
}

// TestBenchmarkResult_Markdown_RendersAdoptedRoundHistogram pins the
// histogram rendering: [0,5,10] → three rows with bucket index 0/1/2
// and counts 0/5/10.
func TestBenchmarkResult_Markdown_RendersAdoptedRoundHistogram(t *testing.T) {
	r := eval.BenchmarkResult{
		Dataset: eval.AnswerDataset{Name: "h"},
		Metrics: eval.BenchmarkMetrics{
			Examples:           15,
			AdoptedRoundCounts: []int{0, 5, 10},
		},
	}
	md := r.Markdown()
	if !strings.Contains(md, "## AdoptedRound Distribution") {
		t.Fatalf("histogram section missing")
	}
	for _, want := range []string{
		"| Bucket | Count |",
		"| 0 | 0 |",
		"| 1 | 5 |",
		"| 2 | 10 |",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("histogram row missing %q in:\n%s", want, md)
		}
	}
}

// TestBenchmarkResult_Markdown_NaNRendersAsNa pins that any NaN metric
// renders as "n/a" rather than %.3f.
func TestBenchmarkResult_Markdown_NaNRendersAsNa(t *testing.T) {
	r := eval.BenchmarkResult{
		Dataset: eval.AnswerDataset{Name: "n"},
		Metrics: eval.BenchmarkMetrics{
			Examples:         3,
			MeanGroundedness: math.NaN(),
		},
	}
	if !strings.Contains(r.Markdown(), "| MeanGroundedness | n/a |") {
		t.Fatalf("NaN should render as n/a: %s", r.Markdown())
	}
}

// TestBenchmarkResult_Markdown_HeaderIncludesDatasetName pins the title
// "# Benchmark: <Dataset.Name>".
func TestBenchmarkResult_Markdown_HeaderIncludesDatasetName(t *testing.T) {
	r := eval.BenchmarkResult{Dataset: eval.AnswerDataset{Name: "foo"}}
	if !strings.HasPrefix(strings.TrimLeft(r.Markdown(), " \n"), "# Benchmark: foo") {
		t.Fatalf("header missing: %s", r.Markdown())
	}
}

// TestBenchmarkResult_Markdown_StructureContractStable pins the section
// ordering: header → Metrics table → optional AdoptedRound section. We
// assert each section appears strictly after the prior one.
func TestBenchmarkResult_Markdown_StructureContractStable(t *testing.T) {
	r := eval.BenchmarkResult{
		Dataset: eval.AnswerDataset{Name: "s"},
		Metrics: eval.BenchmarkMetrics{
			Examples:           1,
			AdoptedRoundCounts: []int{1},
		},
	}
	md := r.Markdown()
	hi := strings.Index(md, "# Benchmark:")
	mi := strings.Index(md, "## Metrics")
	ai := strings.Index(md, "## AdoptedRound Distribution")
	if hi < 0 || mi < 0 || ai < 0 {
		t.Fatalf("missing section in:\n%s", md)
	}
	if !(hi < mi && mi < ai) {
		t.Fatalf("section ordering violated: hi=%d mi=%d ai=%d\n%s", hi, mi, ai, md)
	}
}
