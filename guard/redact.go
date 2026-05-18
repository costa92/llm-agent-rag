// Package guard is the content-safety layer for the RAG pipeline. It
// redacts PII from ingested content and screens retrieved content for
// prompt-injection attempts. It is a leaf package — it imports only the
// standard library — so ingest and rag can depend on it with no cycle.
package guard

import "regexp"

// Redaction is a count of one kind of PII removed from a piece of text.
type Redaction struct {
	Kind  string
	Count int
}

// RedactResult is redacted text plus a per-kind tally of what was removed.
type RedactResult struct {
	Text       string
	Redactions []Redaction
}

// Redactor removes sensitive content from text before it is chunked,
// embedded, and stored. Implementations must be deterministic.
type Redactor interface {
	Redact(text string) RedactResult
}

// Rule is one named PII pattern and the placeholder its matches collapse to.
type Rule struct {
	Kind        string
	Pattern     *regexp.Regexp
	Placeholder string
}

// PIIRedactor applies an ordered set of Rules. The exported Rules slice is
// the configuration surface — callers append or replace rules. The zero
// value redacts nothing; use NewPIIRedactor for the built-in rule set.
type PIIRedactor struct {
	Rules []Rule
}

// Redact applies each rule in order, replacing matches with the rule's
// placeholder and tallying how many matches each rule made. Rules with no
// matches are omitted from the result.
func (r PIIRedactor) Redact(text string) RedactResult {
	out := text
	var reds []Redaction
	for _, rule := range r.Rules {
		if rule.Pattern == nil {
			continue
		}
		matches := rule.Pattern.FindAllString(out, -1)
		if len(matches) == 0 {
			continue
		}
		out = rule.Pattern.ReplaceAllString(out, rule.Placeholder)
		reds = append(reds, Redaction{Kind: rule.Kind, Count: len(matches)})
	}
	return RedactResult{Text: out, Redactions: reds}
}

// NewPIIRedactor returns a PIIRedactor with built-in rules for US SSN,
// credit-card, phone, IPv4, and email. The more specific patterns run
// before the broader numeric ones so a broad rule cannot consume a
// specific match first.
func NewPIIRedactor() PIIRedactor {
	return PIIRedactor{Rules: []Rule{
		{
			Kind:        "ssn",
			Pattern:     regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
			Placeholder: "[REDACTED:SSN]",
		},
		{
			Kind:        "credit_card",
			Pattern:     regexp.MustCompile(`\b\d{4}[ -]?\d{4}[ -]?\d{4}[ -]?\d{1,4}\b`),
			Placeholder: "[REDACTED:CREDIT_CARD]",
		},
		{
			Kind:        "phone",
			Pattern:     regexp.MustCompile(`\+?\b\d[\d ()-]{7,}\d\b`),
			Placeholder: "[REDACTED:PHONE]",
		},
		{
			Kind:        "ipv4",
			Pattern:     regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`),
			Placeholder: "[REDACTED:IPV4]",
		},
		{
			Kind:        "email",
			Pattern:     regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`),
			Placeholder: "[REDACTED:EMAIL]",
		},
	}}
}
