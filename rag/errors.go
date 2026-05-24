package rag

import (
	"errors"
	"fmt"
)

// ErrEmptyQuery is returned by the answer paths when the query is empty.
var ErrEmptyQuery = errors.New("rag: query is required")

// ErrModelRequired is returned when an operation needs a generation model
// but none was configured.
var ErrModelRequired = errors.New("rag: generator required for this operation")

// ErrImporterRequired is returned when an operation needs an importer but
// none was configured.
var ErrImporterRequired = errors.New("rag: importer required for this operation")

// ErrRetrieverRequired is returned when an operation needs a retriever but
// none was configured.
var ErrRetrieverRequired = errors.New("rag: retriever required for this operation")

// ErrSourceRequired is returned when an import is attempted with no source.
var ErrSourceRequired = errors.New("rag: import source is required")

// ErrCommunitySummarizerRequired is returned by System.AskGlobal when a
// community report must be generated (a cache miss or a stale ContentHash)
// but no Options.CommunitySummarizer was configured.
var ErrCommunitySummarizerRequired = errors.New("rag: community summarizer required for global search")

// ErrTokenBudgetExceeded is the v1.7.0 sentinel returned (via
// BudgetExceededError.Unwrap) when the cumulative StageTokenUsage on an
// Ask call exceeds AskOptions.MaxTotalTokens after any successful
// Generate. Use errors.Is to detect; use errors.As(err, &budgetErr) to
// recover the typed struct (with PartialDiagnostics).
var ErrTokenBudgetExceeded = errors.New("rag: token budget exceeded")

// BudgetExceededError is the typed error returned by Ask when the
// cumulative StageTokenUsage across all stages of one Ask call exceeds
// AskOptions.MaxTotalTokens. It carries:
//
//   - Stage: the named generation stage whose Generate triggered the
//     budget check (e.g. "ask", "reflection_decision", "grader").
//   - Used: the cumulative TotalTokens across every Append so far,
//     including the overage call (StageUsageAccumulator.TotalSoFar()).
//   - Budget: the AskOptions.MaxTotalTokens value in effect.
//   - PartialDiagnostics: the Diagnostics assembled up to the abort,
//     including Metrics, StageTokenUsage snapshot, and any partial
//     Reflection rounds completed before the budget tripped.
//
// Unwrap returns ErrTokenBudgetExceeded; errors.Is matches the sentinel.
// Use errors.As(err, &budgetErr) to access PartialDiagnostics.
//
// Compatibility note: this exported struct may grow additively over
// time, so keyed composite literals are recommended. v1.7.0.
type BudgetExceededError struct {
	Stage              string
	Used               int
	Budget             int
	PartialDiagnostics Diagnostics
}

// Error returns a human-readable message naming the offending stage and
// the used / budget counts.
func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("rag: token budget exceeded at stage %q (used %d, budget %d)", e.Stage, e.Used, e.Budget)
}

// Unwrap returns the sentinel so callers can use errors.Is.
func (e *BudgetExceededError) Unwrap() error { return ErrTokenBudgetExceeded }
