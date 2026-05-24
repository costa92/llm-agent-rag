package rag

import (
	"errors"
	"strings"
	"testing"
)

// TestBudgetExceededError_ErrorMessage_IncludesStageUsedBudget asserts the
// Error() string carries Stage, Used, and Budget for operator legibility.
func TestBudgetExceededError_ErrorMessage_IncludesStageUsedBudget(t *testing.T) {
	e := &BudgetExceededError{Stage: "reflection_decision", Used: 1234, Budget: 1000}
	msg := e.Error()
	if !strings.Contains(msg, "reflection_decision") {
		t.Errorf("Error() = %q, missing stage", msg)
	}
	if !strings.Contains(msg, "1234") {
		t.Errorf("Error() = %q, missing used count", msg)
	}
	if !strings.Contains(msg, "1000") {
		t.Errorf("Error() = %q, missing budget", msg)
	}
}

// TestBudgetExceededError_Unwrap_ToSentinel asserts errors.Is matches the
// sentinel ErrTokenBudgetExceeded.
func TestBudgetExceededError_Unwrap_ToSentinel(t *testing.T) {
	e := &BudgetExceededError{Stage: "ask", Used: 100, Budget: 50}
	if !errors.Is(e, ErrTokenBudgetExceeded) {
		t.Fatalf("errors.Is(BudgetExceededError, ErrTokenBudgetExceeded) = false; want true")
	}
}

// TestBudgetExceededError_AsExtractsPartialDiagnostics asserts errors.As
// can fish out the typed struct and expose PartialDiagnostics.
func TestBudgetExceededError_AsExtractsPartialDiagnostics(t *testing.T) {
	want := Diagnostics{HitCount: 7}
	var inner error = &BudgetExceededError{Stage: "ask", Used: 10, Budget: 5, PartialDiagnostics: want}
	var target *BudgetExceededError
	if !errors.As(inner, &target) {
		t.Fatalf("errors.As did not unbox BudgetExceededError")
	}
	if target.PartialDiagnostics.HitCount != 7 {
		t.Fatalf("PartialDiagnostics.HitCount = %d, want 7", target.PartialDiagnostics.HitCount)
	}
}
