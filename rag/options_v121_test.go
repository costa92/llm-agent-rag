package rag

import "testing"

// TestReflectionOptions_ParallelFollowupsField pins the v1.2.1 additive
// surface: ReflectionOptions.ParallelFollowups is a bool with the
// expected name and the expected zero value. A keyed composite literal
// here makes the test fail at compile time if the field is renamed or
// removed.
func TestReflectionOptions_ParallelFollowupsField(t *testing.T) {
	opts := ReflectionOptions{
		ParallelFollowups: true,
	}
	if !opts.ParallelFollowups {
		t.Fatalf("ParallelFollowups = false, want true")
	}
	// Zero value must remain false (default = sequential dispatch).
	var zero ReflectionOptions
	if zero.ParallelFollowups {
		t.Fatalf("zero-value ParallelFollowups = true, want false (sequential default)")
	}
}

// TestReflectionOptions_MaxFollowupConcurrencyField pins the v1.2.1
// additive surface: ReflectionOptions.MaxFollowupConcurrency is an int
// with the expected name and zero default (which resolves to
// MaxFollowupQueries when ParallelFollowups=true).
func TestReflectionOptions_MaxFollowupConcurrencyField(t *testing.T) {
	opts := ReflectionOptions{
		MaxFollowupConcurrency: 3,
	}
	if opts.MaxFollowupConcurrency != 3 {
		t.Fatalf("MaxFollowupConcurrency = %d, want 3", opts.MaxFollowupConcurrency)
	}
	var zero ReflectionOptions
	if zero.MaxFollowupConcurrency != 0 {
		t.Fatalf("zero-value MaxFollowupConcurrency = %d, want 0 (default)", zero.MaxFollowupConcurrency)
	}
}
