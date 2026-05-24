package eval

import "strings"

// normalize lowercases and collapses internal whitespace runs to single
// spaces (and trims leading/trailing whitespace). It is the
// canonicalization step ExactMatch applies before comparing strings.
// Punctuation is NOT stripped — that decision is deferred to the dataset
// author so the benchmark is reproducible across runs.
func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// tokenize splits s on whitespace after lowercasing. Used by f1 — it
// must agree with normalize on what counts as a token boundary.
func tokenize(s string) []string {
	return strings.Fields(strings.ToLower(s))
}

// exactMatch reports whether any gold answer normalizes to the same
// string as the answer. With zero golds it returns false — a vacuous
// match would silently inflate the ExactMatch metric across a dataset
// of examples that simply forgot to label their answers.
func exactMatch(answer string, golds []string) bool {
	a := normalize(answer)
	for _, g := range golds {
		if a == normalize(g) {
			return true
		}
	}
	return false
}

// f1 returns the maximum token-F1 score of the answer against any of
// the candidate gold answers. The intersection follows the HotpotQA
// multiset convention: each answer-token can consume at most one
// occurrence of the same token in a gold answer. Returns 0 for an
// empty answer, no golds, or no token overlap (never panics, never
// returns NaN).
func f1(answer string, golds []string) float64 {
	ans := tokenize(answer)
	if len(golds) == 0 || len(ans) == 0 {
		return 0
	}
	best := 0.0
	for _, g := range golds {
		gt := tokenize(g)
		if len(gt) == 0 {
			continue
		}
		gcount := make(map[string]int, len(gt))
		for _, t := range gt {
			gcount[t]++
		}
		shared := 0
		for _, t := range ans {
			if gcount[t] > 0 {
				gcount[t]--
				shared++
			}
		}
		if shared == 0 {
			continue
		}
		p := float64(shared) / float64(len(ans))
		r := float64(shared) / float64(len(gt))
		score := 2 * p * r / (p + r)
		if score > best {
			best = score
		}
	}
	return best
}
