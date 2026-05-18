package guard

import (
	"regexp"
	"strings"
	"testing"
)

func TestPIIRedactorBuiltinRules(t *testing.T) {
	in := "Contact alice@example.com or 555-123-4567. " +
		"Card 4111 1111 1111 1111, SSN 123-45-6789, host 192.168.1.1."
	res := NewPIIRedactor().Redact(in)

	for _, ph := range []string{
		"[REDACTED:EMAIL]", "[REDACTED:PHONE]", "[REDACTED:CREDIT_CARD]",
		"[REDACTED:SSN]", "[REDACTED:IPV4]",
	} {
		if !strings.Contains(res.Text, ph) {
			t.Fatalf("redacted text missing %s: %q", ph, res.Text)
		}
	}
	for _, raw := range []string{"alice@example.com", "123-45-6789", "192.168.1.1"} {
		if strings.Contains(res.Text, raw) {
			t.Fatalf("raw PII %q still present: %q", raw, res.Text)
		}
	}
	counts := map[string]int{}
	for _, r := range res.Redactions {
		counts[r.Kind] = r.Count
	}
	for _, kind := range []string{"email", "phone", "credit_card", "ssn", "ipv4"} {
		if counts[kind] != 1 {
			t.Fatalf("Redaction count for %s = %d, want 1 (%+v)", kind, counts[kind], res.Redactions)
		}
	}
}

func TestPIIRedactorNoPII(t *testing.T) {
	in := "The quick brown fox jumps over the lazy dog."
	res := NewPIIRedactor().Redact(in)
	if res.Text != in {
		t.Fatalf("clean text changed: %q", res.Text)
	}
	if len(res.Redactions) != 0 {
		t.Fatalf("Redactions not empty for clean text: %+v", res.Redactions)
	}
}

func TestPIIRedactorCustomRule(t *testing.T) {
	r := PIIRedactor{Rules: []Rule{
		{Kind: "ticket", Pattern: regexp.MustCompile(`TICKET-\d+`), Placeholder: "[REDACTED:TICKET]"},
	}}
	res := r.Redact("see TICKET-4842 for details")
	if !strings.Contains(res.Text, "[REDACTED:TICKET]") || strings.Contains(res.Text, "4842") {
		t.Fatalf("custom rule not applied: %q", res.Text)
	}
	if len(res.Redactions) != 1 || res.Redactions[0].Kind != "ticket" || res.Redactions[0].Count != 1 {
		t.Fatalf("Redactions = %+v", res.Redactions)
	}
}
