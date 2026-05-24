package eval

import (
	"math"
	"testing"
)

// TestNormalizeCollapsesWhitespaceAndLowercases pins the C-Eval-style
// normalization the benchmark uses for ExactMatch: lowercase + collapse
// all runs of whitespace to single spaces + trim leading/trailing
// whitespace.
func TestNormalizeCollapsesWhitespaceAndLowercases(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"  Hello   World  ", "hello world"},
		{"Foo\tBar\nBaz", "foo bar baz"},
		{"Already normalized", "already normalized"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := normalize(c.in); got != c.want {
			t.Errorf("normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestExactMatchAnyGoldMatches asserts ExactMatch is the union over
// gold answers — any one gold normalizing equal to the answer counts.
func TestExactMatchAnyGoldMatches(t *testing.T) {
	if !exactMatch("foo bar", []string{"foo", "Foo BAR"}) {
		t.Errorf("exactMatch should match second gold after normalize")
	}
	if exactMatch("foo bar", []string{"baz", "qux"}) {
		t.Errorf("exactMatch should NOT match disjoint golds")
	}
}

// TestExactMatchNoGoldsReturnsFalse asserts the no-gold case is false
// (vacuous-truth would silently inflate scores).
func TestExactMatchNoGoldsReturnsFalse(t *testing.T) {
	if exactMatch("foo", nil) {
		t.Errorf("exactMatch with nil golds should be false")
	}
	if exactMatch("foo", []string{}) {
		t.Errorf("exactMatch with empty golds should be false")
	}
}

// TestF1TokenMultisetIntersection pins the HotpotQA multiset convention:
// each answer-token consumes at most one gold occurrence.
//
//	answer "the the cat", gold "the cat"
//	  → ans tokens: [the, the, cat]   gold tokens: [the, cat]
//	  → shared after multiset intersect = 2 (one "the", one "cat")
//	  → P = 2/3, R = 2/2, F1 = 2*P*R/(P+R) = (2 * 2/3 * 1) / (2/3 + 1)
//	                                       = (4/3) / (5/3) = 4/5 = 0.8
func TestF1TokenMultisetIntersection(t *testing.T) {
	got := f1("the the cat", []string{"the cat"})
	want := 0.8
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("f1 = %v, want %v", got, want)
	}
}

// TestF1TokenMaxAcrossGolds asserts the F1 score is the best across
// all candidate golds, not the first or last.
func TestF1TokenMaxAcrossGolds(t *testing.T) {
	got := f1("dario amodei", []string{"unrelated", "dario amodei"})
	if math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("f1 = %v, want 1.0 (perfect match with second gold)", got)
	}
}

// TestF1TokenEmptyAnswerReturnsZero asserts the edge cases: empty
// answer and empty golds both return 0 (not NaN, not panic).
func TestF1TokenEmptyAnswerReturnsZero(t *testing.T) {
	if got := f1("", []string{"foo"}); got != 0 {
		t.Errorf("f1(empty ans) = %v, want 0", got)
	}
	if got := f1("foo", nil); got != 0 {
		t.Errorf("f1(nil golds) = %v, want 0", got)
	}
	if got := f1("foo", []string{}); got != 0 {
		t.Errorf("f1(empty golds) = %v, want 0", got)
	}
	if got := f1("foo", []string{""}); got != 0 {
		t.Errorf("f1(blank gold token list) = %v, want 0", got)
	}
}

// TestF1TokenNoOverlapReturnsZero pins the no-intersection branch — it
// must short-circuit before dividing by zero.
func TestF1TokenNoOverlapReturnsZero(t *testing.T) {
	if got := f1("alpha beta", []string{"gamma delta"}); got != 0 {
		t.Errorf("f1(no overlap) = %v, want 0", got)
	}
}
