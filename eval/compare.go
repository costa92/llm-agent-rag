package eval

// This file implements the v1.6.0 C-Drift comparator. CompareBenchmarks
// reduces two BenchmarkResults to a DriftReport — a list of per-metric
// deltas plus the Query string sets that appeared/disappeared between
// runs.
//
// Polarity. Some metrics are higher-is-better (ExactMatch, F1Token);
// some are lower-is-better (FollowupQueriesUsedMean); others have no
// directional meaning (ReflectionRoundsMean is "how hard the pipeline
// worked", neither good nor bad on its own — the same goes for
// GraderAdoptionRate and ActiveRetrievalFireRate, which report which
// features fired, not whether firing was good). The metricPolarity table
// is pinned in this file and CHANGELOG-locked at v1.6.0; future minor
// versions may add metrics (defaulting to undefined polarity) without
// renaming existing entries.
//
// NaN-touching transitions. Any NaN on either side of a comparison
// yields DirectionUndefined and a NaN Delta — the v1.3.0 sentinel for
// "feature was off everywhere" can't be ordered against a finite score.
//
// Tolerance. The Unchanged band is a hardcoded absolute 1e-9; deltas
// strictly below that magnitude collapse to Unchanged for higher- and
// lower-better polarities.
//
// Drift example matching. NewExamples / DroppedExamples are computed on
// the Query string sets — sorted, deduplicated. Callers wanting stable
// matching across renamed queries must keep Query unique within a
// dataset (documented in CHANGELOG v1.6.0).

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Direction is the qualitative outcome of one metric's change between
// two benchmark runs.
type Direction string

const (
	// DirectionImproved means the metric moved in the better direction
	// per its polarity by more than the Unchanged tolerance.
	DirectionImproved Direction = "improved"
	// DirectionRegressed means the metric moved in the worse direction
	// per its polarity by more than the Unchanged tolerance.
	DirectionRegressed Direction = "regressed"
	// DirectionUnchanged means the absolute delta is below 1e-9 and the
	// metric has a defined polarity.
	DirectionUnchanged Direction = "unchanged"
	// DirectionUndefined means the change is unordered: either side was
	// NaN (the v1.3.0 "feature was off" sentinel) or the metric has no
	// directional meaning per the polarity table.
	DirectionUndefined Direction = "undefined"
)

// MetricDelta is one row in a DriftReport: the metric's name, the prev
// and curr scalar values (NaN-bearing when the underlying feature was
// off on either side), the arithmetic Delta (NaN when either side is
// NaN), and the qualitative Direction outcome.
type MetricDelta struct {
	Name      string
	Prev      float64
	Curr      float64
	Delta     float64
	Direction Direction
}

// DriftReport is the structured output of CompareBenchmarks: per-metric
// deltas plus the example-Query set diff.
type DriftReport struct {
	Dataset         string
	Deltas          []MetricDelta
	NewExamples     []string // queries in Curr but not Prev (sorted, deduplicated)
	DroppedExamples []string // queries in Prev but not Curr (sorted, deduplicated)
}

// polarity is the internal direction-of-betterness for one metric.
type polarity int

const (
	polarityUndefined polarity = iota
	polarityHigher             // higher is better
	polarityLower              // lower is better
)

// metricPolarity returns the polarity for one metric name. Unknown names
// (future metrics not yet in the table) default to polarityUndefined —
// the comparator still emits a Delta entry, just with DirectionUndefined.
// Pinned table, locked at v1.6.0:
//
//	ExactMatch                  higher
//	F1Token                     higher
//	RequiredPhraseRecall        higher
//	MeanGroundedness            higher
//	MeanAnswerRelevance         higher
//	FollowupQueriesUsedMean     lower
//	ReflectionRoundsMean        undefined
//	GraderAdoptionRate          undefined
//	ActiveRetrievalFireRate     undefined
//	Examples                    undefined
func metricPolarity(name string) polarity {
	switch name {
	case "ExactMatch", "F1Token", "RequiredPhraseRecall",
		"MeanGroundedness", "MeanAnswerRelevance":
		return polarityHigher
	case "FollowupQueriesUsedMean":
		return polarityLower
	default:
		// Examples / ReflectionRoundsMean / GraderAdoptionRate /
		// ActiveRetrievalFireRate / any future metric.
		return polarityUndefined
	}
}

// unchangedAbsTolerance is the absolute |delta| band below which a
// directional metric collapses to Unchanged. Hardcoded at v1.6.0.
const unchangedAbsTolerance = 1e-9

// compareMetric is the per-metric core: given prev/curr scalars, returns
// the MetricDelta. Pure / side-effect-free.
func compareMetric(name string, prev, curr float64) MetricDelta {
	if math.IsNaN(prev) || math.IsNaN(curr) {
		return MetricDelta{
			Name: name, Prev: prev, Curr: curr,
			Delta: math.NaN(), Direction: DirectionUndefined,
		}
	}
	delta := curr - prev
	pol := metricPolarity(name)
	if pol == polarityUndefined {
		return MetricDelta{
			Name: name, Prev: prev, Curr: curr,
			Delta: delta, Direction: DirectionUndefined,
		}
	}
	if math.Abs(delta) < unchangedAbsTolerance {
		return MetricDelta{
			Name: name, Prev: prev, Curr: curr,
			Delta: delta, Direction: DirectionUnchanged,
		}
	}
	improved := delta > 0
	if pol == polarityLower {
		improved = !improved
	}
	if improved {
		return MetricDelta{
			Name: name, Prev: prev, Curr: curr,
			Delta: delta, Direction: DirectionImproved,
		}
	}
	return MetricDelta{
		Name: name, Prev: prev, Curr: curr,
		Delta: delta, Direction: DirectionRegressed,
	}
}

// CompareBenchmarks reduces two BenchmarkResults to a DriftReport.
//
// Per-metric: every named scalar in BenchmarkMetrics gets one MetricDelta
// entry, in a stable order (declaration order on BenchmarkMetrics).
// Examples and the AdoptedRoundCounts histogram are emitted as
// undefined-polarity entries (Delta still computed for Examples;
// histogram diffing is out of scope for v1.6.0 and is a v1.7.0 candidate).
//
// Per-example: NewExamples and DroppedExamples are computed on
// AnswerExample.Query string equality, deduplicated and sorted.
func CompareBenchmarks(prev, curr BenchmarkResult) DriftReport {
	pm, cm := prev.Metrics, curr.Metrics
	deltas := []MetricDelta{
		// Examples is a plain counter; emit as undefined-polarity.
		compareMetric("Examples", float64(pm.Examples), float64(cm.Examples)),
		compareMetric("ExactMatch", pm.ExactMatch, cm.ExactMatch),
		compareMetric("F1Token", pm.F1Token, cm.F1Token),
		compareMetric("RequiredPhraseRecall", pm.RequiredPhraseRecall, cm.RequiredPhraseRecall),
		compareMetric("ReflectionRoundsMean", pm.ReflectionRoundsMean, cm.ReflectionRoundsMean),
		compareMetric("GraderAdoptionRate", pm.GraderAdoptionRate, cm.GraderAdoptionRate),
		compareMetric("FollowupQueriesUsedMean", pm.FollowupQueriesUsedMean, cm.FollowupQueriesUsedMean),
		compareMetric("ActiveRetrievalFireRate", pm.ActiveRetrievalFireRate, cm.ActiveRetrievalFireRate),
		compareMetric("MeanGroundedness", pm.MeanGroundedness, cm.MeanGroundedness),
		compareMetric("MeanAnswerRelevance", pm.MeanAnswerRelevance, cm.MeanAnswerRelevance),
	}

	prevQueries := uniqueQueries(prev.Dataset.Examples)
	currQueries := uniqueQueries(curr.Dataset.Examples)
	newExamples := setDiff(currQueries, prevQueries)
	droppedExamples := setDiff(prevQueries, currQueries)

	// Dataset name: prefer curr's; fall back to prev's when curr empty.
	name := curr.Dataset.Name
	if name == "" {
		name = prev.Dataset.Name
	}
	return DriftReport{
		Dataset:         name,
		Deltas:          deltas,
		NewExamples:     newExamples,
		DroppedExamples: droppedExamples,
	}
}

// uniqueQueries returns the deduplicated Query set as a sorted []string.
func uniqueQueries(exs []AnswerExample) []string {
	set := make(map[string]struct{}, len(exs))
	for _, e := range exs {
		set[e.Query] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for q := range set {
		out = append(out, q)
	}
	sort.Strings(out)
	return out
}

// setDiff returns a - b, preserving sorted order. Both inputs are
// expected to be sorted (uniqueQueries returns sorted).
func setDiff(a, b []string) []string {
	bset := make(map[string]struct{}, len(b))
	for _, x := range b {
		bset[x] = struct{}{}
	}
	out := make([]string, 0)
	for _, x := range a {
		if _, ok := bset[x]; !ok {
			out = append(out, x)
		}
	}
	return out
}

// Summary returns a compact human-readable scoreboard for a DriftReport.
// Format pinned by TestDriftReportSummary_FormatsHumanReadable.
func (r DriftReport) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "DriftReport for dataset %q:\n", r.Dataset)
	for _, d := range r.Deltas {
		fmt.Fprintf(&b, "  %-24s %s -> %s  delta=%s  %s\n",
			d.Name+":",
			formatScalar(d.Prev),
			formatScalar(d.Curr),
			formatDelta(d.Delta),
			d.Direction,
		)
	}
	fmt.Fprintf(&b, "  New examples: %d\n", len(r.NewExamples))
	fmt.Fprintf(&b, "  Dropped:      %d\n", len(r.DroppedExamples))
	return b.String()
}

func formatScalar(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	return fmt.Sprintf("%7.3f", f)
}

func formatDelta(f float64) string {
	if math.IsNaN(f) {
		return "  NaN"
	}
	return fmt.Sprintf("%+.3f", f)
}
