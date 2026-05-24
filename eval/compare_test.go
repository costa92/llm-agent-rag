package eval_test

import (
	"math"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
)

// findDelta returns the MetricDelta named name from the report or fails.
func findDelta(t *testing.T, r eval.DriftReport, name string) eval.MetricDelta {
	t.Helper()
	for _, d := range r.Deltas {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("no delta named %q in report (%d deltas)", name, len(r.Deltas))
	return eval.MetricDelta{}
}

func emptyMetrics() eval.BenchmarkMetrics {
	return eval.BenchmarkMetrics{
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
	}
}

func benchmarkOf(name string, m eval.BenchmarkMetrics) eval.BenchmarkResult {
	return eval.BenchmarkResult{
		Dataset: eval.AnswerDataset{Name: name, TopK: 5},
		Metrics: m,
	}
}

// TestCompareBenchmarks_HigherBetter_Improved: ExactMatch 0.7 -> 0.8.
func TestCompareBenchmarks_HigherBetter_Improved(t *testing.T) {
	prev := emptyMetrics()
	prev.ExactMatch = 0.7
	curr := emptyMetrics()
	curr.ExactMatch = 0.8
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	d := findDelta(t, r, "ExactMatch")
	if d.Direction != eval.DirectionImproved {
		t.Fatalf("Direction=%q; want %q", d.Direction, eval.DirectionImproved)
	}
}

// TestCompareBenchmarks_HigherBetter_Regressed: ExactMatch 0.8 -> 0.7.
func TestCompareBenchmarks_HigherBetter_Regressed(t *testing.T) {
	prev := emptyMetrics()
	prev.ExactMatch = 0.8
	curr := emptyMetrics()
	curr.ExactMatch = 0.7
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	d := findDelta(t, r, "ExactMatch")
	if d.Direction != eval.DirectionRegressed {
		t.Fatalf("Direction=%q; want %q", d.Direction, eval.DirectionRegressed)
	}
}

// TestCompareBenchmarks_HigherBetter_UnchangedWithinTolerance: delta 1e-10.
func TestCompareBenchmarks_HigherBetter_UnchangedWithinTolerance(t *testing.T) {
	prev := emptyMetrics()
	prev.ExactMatch = 0.5
	curr := emptyMetrics()
	curr.ExactMatch = 0.5 + 1e-10
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	d := findDelta(t, r, "ExactMatch")
	if d.Direction != eval.DirectionUnchanged {
		t.Fatalf("Direction=%q; want %q", d.Direction, eval.DirectionUnchanged)
	}
}

// TestCompareBenchmarks_LowerBetter_FollowupQueriesUsedMean: lower is
// better, so 2.0 -> 1.0 is Improved.
func TestCompareBenchmarks_LowerBetter_FollowupQueriesUsedMean(t *testing.T) {
	prev := emptyMetrics()
	prev.FollowupQueriesUsedMean = 2.0
	curr := emptyMetrics()
	curr.FollowupQueriesUsedMean = 1.0
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	d := findDelta(t, r, "FollowupQueriesUsedMean")
	if d.Direction != eval.DirectionImproved {
		t.Fatalf("Direction=%q; want %q (lower-is-better)", d.Direction, eval.DirectionImproved)
	}
}

// TestCompareBenchmarks_UndefinedPolarity_ReflectionRoundsMean: any
// finite delta -> Undefined.
func TestCompareBenchmarks_UndefinedPolarity_ReflectionRoundsMean(t *testing.T) {
	prev := emptyMetrics()
	prev.ReflectionRoundsMean = 1.0
	curr := emptyMetrics()
	curr.ReflectionRoundsMean = 5.0
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	d := findDelta(t, r, "ReflectionRoundsMean")
	if d.Direction != eval.DirectionUndefined {
		t.Fatalf("Direction=%q; want %q (ambiguous polarity)", d.Direction, eval.DirectionUndefined)
	}
}

// TestCompareBenchmarks_NaNToFinite_Undefined: NaN -> 0.5.
func TestCompareBenchmarks_NaNToFinite_Undefined(t *testing.T) {
	prev := emptyMetrics() // ExactMatch already NaN
	curr := emptyMetrics()
	curr.ExactMatch = 0.5
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	d := findDelta(t, r, "ExactMatch")
	if d.Direction != eval.DirectionUndefined {
		t.Fatalf("Direction=%q; want %q", d.Direction, eval.DirectionUndefined)
	}
	if !math.IsNaN(d.Delta) {
		t.Fatalf("Delta=%v; want NaN", d.Delta)
	}
}

// TestCompareBenchmarks_FiniteToNaN_Undefined.
func TestCompareBenchmarks_FiniteToNaN_Undefined(t *testing.T) {
	prev := emptyMetrics()
	prev.ExactMatch = 0.5
	curr := emptyMetrics()
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	d := findDelta(t, r, "ExactMatch")
	if d.Direction != eval.DirectionUndefined {
		t.Fatalf("Direction=%q; want %q", d.Direction, eval.DirectionUndefined)
	}
}

// TestCompareBenchmarks_NaNToNaN_Undefined.
func TestCompareBenchmarks_NaNToNaN_Undefined(t *testing.T) {
	prev := emptyMetrics()
	curr := emptyMetrics()
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	d := findDelta(t, r, "ExactMatch")
	if d.Direction != eval.DirectionUndefined {
		t.Fatalf("Direction=%q; want %q", d.Direction, eval.DirectionUndefined)
	}
}

// TestCompareBenchmarks_NewAndDroppedExamples: Query set diff, sorted.
func TestCompareBenchmarks_NewAndDroppedExamples(t *testing.T) {
	prev := benchmarkOf("d", emptyMetrics())
	prev.Dataset.Examples = []eval.AnswerExample{
		{Example: eval.Example{Query: "alpha"}},
		{Example: eval.Example{Query: "beta"}},
	}
	curr := benchmarkOf("d", emptyMetrics())
	curr.Dataset.Examples = []eval.AnswerExample{
		{Example: eval.Example{Query: "beta"}},
		{Example: eval.Example{Query: "gamma"}},
	}
	r := eval.CompareBenchmarks(prev, curr)
	wantNew := []string{"gamma"}
	wantDropped := []string{"alpha"}
	if !strSliceEq(r.NewExamples, wantNew) {
		t.Fatalf("NewExamples=%v; want %v", r.NewExamples, wantNew)
	}
	if !strSliceEq(r.DroppedExamples, wantDropped) {
		t.Fatalf("DroppedExamples=%v; want %v", r.DroppedExamples, wantDropped)
	}
}

// TestCompareBenchmarks_DuplicateQueriesCollapseInSetDiff.
func TestCompareBenchmarks_DuplicateQueriesCollapseInSetDiff(t *testing.T) {
	prev := benchmarkOf("d", emptyMetrics())
	prev.Dataset.Examples = []eval.AnswerExample{
		{Example: eval.Example{Query: "a"}},
		{Example: eval.Example{Query: "a"}}, // duplicate in prev
	}
	curr := benchmarkOf("d", emptyMetrics())
	curr.Dataset.Examples = []eval.AnswerExample{
		{Example: eval.Example{Query: "b"}},
		{Example: eval.Example{Query: "b"}}, // duplicate in curr
	}
	r := eval.CompareBenchmarks(prev, curr)
	if !strSliceEq(r.NewExamples, []string{"b"}) {
		t.Fatalf("NewExamples=%v; want [b]", r.NewExamples)
	}
	if !strSliceEq(r.DroppedExamples, []string{"a"}) {
		t.Fatalf("DroppedExamples=%v; want [a]", r.DroppedExamples)
	}
}

// TestCompareBenchmarks_ToleranceBoundary_1e9_Unchanged_1e8_Improved.
func TestCompareBenchmarks_ToleranceBoundary_1e9_Unchanged_1e8_Improved(t *testing.T) {
	cases := []struct {
		delta float64
		want  eval.Direction
		label string
	}{
		{1e-10, eval.DirectionUnchanged, "1e-10 (below 1e-9)"},
		{1e-8, eval.DirectionImproved, "1e-8 (above 1e-9)"},
	}
	for _, c := range cases {
		prev := emptyMetrics()
		prev.ExactMatch = 0.5
		curr := emptyMetrics()
		curr.ExactMatch = 0.5 + c.delta
		r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
		d := findDelta(t, r, "ExactMatch")
		if d.Direction != c.want {
			t.Errorf("delta=%v (%s): Direction=%q; want %q", c.delta, c.label, d.Direction, c.want)
		}
	}
}

// TestCompareBenchmarks_DeltaFieldArithmetic asserts Delta == Curr - Prev
// when both are finite.
func TestCompareBenchmarks_DeltaFieldArithmetic(t *testing.T) {
	prev := emptyMetrics()
	prev.ExactMatch = 0.4
	curr := emptyMetrics()
	curr.ExactMatch = 0.7
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	d := findDelta(t, r, "ExactMatch")
	want := 0.7 - 0.4
	if math.Abs(d.Delta-want) > 1e-12 {
		t.Fatalf("Delta=%v; want ~%v", d.Delta, want)
	}
	if d.Prev != 0.4 || d.Curr != 0.7 {
		t.Fatalf("Prev/Curr fields not propagated: prev=%v curr=%v", d.Prev, d.Curr)
	}
}

// TestDriftReportSummary_FormatsHumanReadable pins the first lines of
// Summary so accidental format changes are caught.
func TestDriftReportSummary_FormatsHumanReadable(t *testing.T) {
	prev := emptyMetrics()
	prev.ExactMatch = 0.7
	curr := emptyMetrics()
	curr.ExactMatch = 0.8
	r := eval.CompareBenchmarks(benchmarkOf("mydata", prev), benchmarkOf("mydata", curr))
	s := r.Summary()
	if !strings.Contains(s, "mydata") {
		t.Fatalf("Summary missing dataset name: %s", s)
	}
	if !strings.Contains(s, "ExactMatch") {
		t.Fatalf("Summary missing ExactMatch metric: %s", s)
	}
	if !strings.Contains(s, "improved") {
		t.Fatalf("Summary missing 'improved': %s", s)
	}
}

func strSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- v1.7.0 C3 DriftReport.Markdown tests ---------------------------------

// TestDriftReportMarkdown_TableHeaderAndOrder pins the pipe-table header
// and the metric order (matches Deltas slice order).
func TestDriftReportMarkdown_TableHeaderAndOrder(t *testing.T) {
	prev := emptyMetrics()
	prev.ExactMatch = 0.7
	prev.F1Token = 0.5
	curr := emptyMetrics()
	curr.ExactMatch = 0.8
	curr.F1Token = 0.6
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	md := r.Markdown()
	// Header row + alignment row.
	if !strings.Contains(md, "| Metric | Prev | Curr | Δ | Direction |") {
		t.Fatalf("Markdown missing header row:\n%s", md)
	}
	if !strings.Contains(md, "| --- | --- | --- | --- | --- |") {
		t.Fatalf("Markdown missing alignment row:\n%s", md)
	}
	// Order: same as Deltas (Examples comes first).
	emIdx := strings.Index(md, "ExactMatch")
	f1Idx := strings.Index(md, "F1Token")
	if emIdx < 0 || f1Idx < 0 {
		t.Fatalf("Markdown missing ExactMatch or F1Token line:\n%s", md)
	}
	if emIdx >= f1Idx {
		t.Fatalf("Markdown ExactMatch (%d) should appear before F1Token (%d):\n%s", emIdx, f1Idx, md)
	}
}

// TestDriftReportMarkdown_NaNRendersNa asserts NaN scalars render as "n/a".
func TestDriftReportMarkdown_NaNRendersNa(t *testing.T) {
	prev := emptyMetrics() // ExactMatch already NaN
	curr := emptyMetrics() // ExactMatch already NaN
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	md := r.Markdown()
	if !strings.Contains(md, "n/a") {
		t.Fatalf("Markdown should render NaN as n/a, got:\n%s", md)
	}
}

// TestDriftReportMarkdown_DirectionLabels asserts all four direction
// labels appear when the dataset surfaces each one.
func TestDriftReportMarkdown_DirectionLabels(t *testing.T) {
	prev := emptyMetrics()
	prev.ExactMatch = 0.5
	prev.F1Token = 0.8
	prev.FollowupQueriesUsedMean = 2.0
	prev.ReflectionRoundsMean = 1.0
	curr := emptyMetrics()
	curr.ExactMatch = 0.7              // improved
	curr.F1Token = 0.6                 // regressed
	curr.FollowupQueriesUsedMean = 2.0 // unchanged
	curr.ReflectionRoundsMean = 5.0    // undefined (polarity undefined)
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	md := r.Markdown()
	wantLabels := []string{"improved", "regressed", "unchanged", "undefined"}
	for _, want := range wantLabels {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown missing direction label %q:\n%s", want, md)
		}
	}
}

// TestDriftReportMarkdown_FooterListsNewAndDropped asserts the New /
// Dropped examples footer renders as bullets when populated.
func TestDriftReportMarkdown_FooterListsNewAndDropped(t *testing.T) {
	prev := benchmarkOf("d", emptyMetrics())
	prev.Dataset.Examples = []eval.AnswerExample{
		{Example: eval.Example{Query: "alpha"}},
		{Example: eval.Example{Query: "beta"}},
	}
	curr := benchmarkOf("d", emptyMetrics())
	curr.Dataset.Examples = []eval.AnswerExample{
		{Example: eval.Example{Query: "beta"}},
		{Example: eval.Example{Query: "gamma"}},
	}
	r := eval.CompareBenchmarks(prev, curr)
	md := r.Markdown()
	if !strings.Contains(md, "**New examples:**") {
		t.Errorf("Markdown missing **New examples:** header:\n%s", md)
	}
	if !strings.Contains(md, "* gamma") {
		t.Errorf("Markdown missing bullet for new example 'gamma':\n%s", md)
	}
	if !strings.Contains(md, "**Dropped examples:**") {
		t.Errorf("Markdown missing **Dropped examples:** header:\n%s", md)
	}
	if !strings.Contains(md, "* alpha") {
		t.Errorf("Markdown missing bullet for dropped example 'alpha':\n%s", md)
	}
}

// --- v1.7.0 C4 HistogramDelta + DriftReport.Histograms -------------------

// TestHistogramDelta_ZeroPadsShorter asserts the Delta is zero-padded to
// max(len(Prev), len(Curr)).
func TestHistogramDelta_ZeroPadsShorter(t *testing.T) {
	prev := emptyMetrics()
	prev.ReflectionRoundsMean = 1.0
	prev.AdoptedRoundCounts = []int{1, 2}
	curr := emptyMetrics()
	curr.ReflectionRoundsMean = 1.0
	curr.AdoptedRoundCounts = []int{1, 2, 3}
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	if len(r.Histograms) == 0 {
		t.Fatalf("Histograms empty; expected AdoptedRoundCounts entry")
	}
	h := r.Histograms[0]
	wantDelta := []int{0, 0, 3}
	if len(h.Delta) != len(wantDelta) {
		t.Fatalf("Delta len = %d, want %d (%v)", len(h.Delta), len(wantDelta), h.Delta)
	}
	for i, want := range wantDelta {
		if h.Delta[i] != want {
			t.Errorf("Delta[%d] = %d, want %d", i, h.Delta[i], want)
		}
	}
}

// TestHistogramDelta_L1Distance_Symmetric asserts |Delta| sums match
// regardless of which side is prev vs curr.
func TestHistogramDelta_L1Distance_Symmetric(t *testing.T) {
	cases := []struct {
		prev, curr []int
		wantL1     float64
	}{
		{[]int{1, 2, 3}, []int{4, 1, 5}, 3 + 1 + 2}, // |3|+|-1|+|2|=6
		{[]int{4, 1, 5}, []int{1, 2, 3}, 3 + 1 + 2}, // |-3|+|1|+|-2|=6
	}
	for i, c := range cases {
		prev := emptyMetrics()
		prev.ReflectionRoundsMean = 1.0
		prev.AdoptedRoundCounts = c.prev
		curr := emptyMetrics()
		curr.ReflectionRoundsMean = 1.0
		curr.AdoptedRoundCounts = c.curr
		r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
		if len(r.Histograms) == 0 {
			t.Fatalf("case %d: Histograms empty", i)
		}
		if math.Abs(r.Histograms[0].L1Distance-c.wantL1) > 1e-9 {
			t.Errorf("case %d: L1=%v, want %v", i, r.Histograms[0].L1Distance, c.wantL1)
		}
	}
}

// TestHistogramDelta_AllEqual_ZeroL1 asserts identical histograms produce L1=0.
func TestHistogramDelta_AllEqual_ZeroL1(t *testing.T) {
	prev := emptyMetrics()
	prev.ReflectionRoundsMean = 1.0
	prev.AdoptedRoundCounts = []int{1, 2, 3}
	curr := emptyMetrics()
	curr.ReflectionRoundsMean = 1.0
	curr.AdoptedRoundCounts = []int{1, 2, 3}
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	if len(r.Histograms) == 0 {
		t.Fatalf("Histograms empty")
	}
	if r.Histograms[0].L1Distance != 0 {
		t.Errorf("L1=%v, want 0", r.Histograms[0].L1Distance)
	}
}

// TestCompareBenchmarks_PopulatesAdoptedRoundCountsHistogram asserts the
// AdoptedRoundCounts histogram is automatically populated by
// CompareBenchmarks with Name == "AdoptedRoundCounts".
func TestCompareBenchmarks_PopulatesAdoptedRoundCountsHistogram(t *testing.T) {
	prev := emptyMetrics()
	prev.ReflectionRoundsMean = 1.0
	prev.AdoptedRoundCounts = []int{5, 10}
	curr := emptyMetrics()
	curr.ReflectionRoundsMean = 1.0
	curr.AdoptedRoundCounts = []int{8, 12}
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	if len(r.Histograms) != 1 {
		t.Fatalf("Histograms len = %d, want 1", len(r.Histograms))
	}
	h := r.Histograms[0]
	if h.Name != "AdoptedRoundCounts" {
		t.Errorf("Histograms[0].Name = %q, want %q", h.Name, "AdoptedRoundCounts")
	}
	if !intSliceEq(h.Prev, []int{5, 10}) {
		t.Errorf("Prev=%v, want [5 10]", h.Prev)
	}
	if !intSliceEq(h.Curr, []int{8, 12}) {
		t.Errorf("Curr=%v, want [8 12]", h.Curr)
	}
	if !intSliceEq(h.Delta, []int{3, 2}) {
		t.Errorf("Delta=%v, want [3 2]", h.Delta)
	}
	if h.L1Distance != 5 {
		t.Errorf("L1Distance=%v, want 5", h.L1Distance)
	}
}

func intSliceEq(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- v1.7.0 C4 Markdown Histograms section -------------------------------

// TestDriftReportMarkdown_HistogramSection_RendersTable asserts the
// ### Histograms / #### <Name> / pipe-table rendering for one histogram.
func TestDriftReportMarkdown_HistogramSection_RendersTable(t *testing.T) {
	prev := emptyMetrics()
	prev.ReflectionRoundsMean = 1.0
	prev.AdoptedRoundCounts = []int{5, 10}
	curr := emptyMetrics()
	curr.ReflectionRoundsMean = 1.0
	curr.AdoptedRoundCounts = []int{8, 12}
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	md := r.Markdown()
	if !strings.Contains(md, "### Histograms") {
		t.Errorf("Markdown missing ### Histograms section header:\n%s", md)
	}
	if !strings.Contains(md, "#### AdoptedRoundCounts") {
		t.Errorf("Markdown missing #### AdoptedRoundCounts heading:\n%s", md)
	}
	if !strings.Contains(md, "| Bucket | Prev | Curr | Δ |") {
		t.Errorf("Markdown missing histogram pipe-table header:\n%s", md)
	}
}

// TestDriftReportMarkdown_HistogramSection_OmittedWhenEmpty asserts the
// histograms section is fully omitted when Histograms is empty.
func TestDriftReportMarkdown_HistogramSection_OmittedWhenEmpty(t *testing.T) {
	prev := emptyMetrics()
	curr := emptyMetrics()
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	md := r.Markdown()
	if strings.Contains(md, "### Histograms") {
		t.Errorf("Markdown should omit ### Histograms section when empty:\n%s", md)
	}
}

// TestDriftReportMarkdown_HistogramSection_L1Line asserts the L1=<value>
// summary line is present after the per-histogram table.
func TestDriftReportMarkdown_HistogramSection_L1Line(t *testing.T) {
	prev := emptyMetrics()
	prev.ReflectionRoundsMean = 1.0
	prev.AdoptedRoundCounts = []int{1, 2}
	curr := emptyMetrics()
	curr.ReflectionRoundsMean = 1.0
	curr.AdoptedRoundCounts = []int{4, 8}
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	md := r.Markdown()
	if !strings.Contains(md, "L1=") {
		t.Errorf("Markdown missing L1=<value> line:\n%s", md)
	}
}

// TestDriftReportMarkdown_EmptyExamplesNoFooter asserts the footer
// sections are omitted entirely when New/Dropped are empty.
func TestDriftReportMarkdown_EmptyExamplesNoFooter(t *testing.T) {
	prev := emptyMetrics()
	curr := emptyMetrics()
	r := eval.CompareBenchmarks(benchmarkOf("d", prev), benchmarkOf("d", curr))
	md := r.Markdown()
	if strings.Contains(md, "New examples") {
		t.Errorf("Markdown should omit New examples section when empty:\n%s", md)
	}
	if strings.Contains(md, "Dropped examples") {
		t.Errorf("Markdown should omit Dropped examples section when empty:\n%s", md)
	}
}
