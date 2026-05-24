package eval

import (
	"math"
	"testing"
)

// TestFloatOrNullPreservesFinite asserts floatOrNull returns a pointer
// to the value for ordinary finite inputs (including 0.0).
func TestFloatOrNullPreservesFinite(t *testing.T) {
	v := 0.5
	got := floatOrNull(v)
	if got == nil {
		t.Fatalf("floatOrNull(0.5) = nil; want non-nil")
	}
	if *got != 0.5 {
		t.Fatalf("floatOrNull(0.5) deref = %v; want 0.5", *got)
	}
	zero := 0.0
	if p := floatOrNull(zero); p == nil || *p != 0.0 {
		t.Fatalf("floatOrNull(0.0) = %v; want non-nil pointing at 0", p)
	}
}

// TestFloatOrNullReturnsNilForNaN asserts NaN collapses to nil.
func TestFloatOrNullReturnsNilForNaN(t *testing.T) {
	if got := floatOrNull(math.NaN()); got != nil {
		t.Fatalf("floatOrNull(NaN) = %v; want nil", got)
	}
}

// TestFloatOrNullReturnsNilForInf asserts both ±Inf collapse to nil.
func TestFloatOrNullReturnsNilForInf(t *testing.T) {
	if got := floatOrNull(math.Inf(1)); got != nil {
		t.Fatalf("floatOrNull(+Inf) = %v; want nil", got)
	}
	if got := floatOrNull(math.Inf(-1)); got != nil {
		t.Fatalf("floatOrNull(-Inf) = %v; want nil", got)
	}
}

// TestFloatFromPtrNaNForNil asserts nil rehydrates as NaN.
func TestFloatFromPtrNaNForNil(t *testing.T) {
	got := floatFromPtr(nil)
	if !math.IsNaN(got) {
		t.Fatalf("floatFromPtr(nil) = %v; want NaN", got)
	}
}

// TestFloatFromPtrPassthrough asserts non-nil pointer values pass through.
func TestFloatFromPtrPassthrough(t *testing.T) {
	v := 0.5
	if got := floatFromPtr(&v); got != 0.5 {
		t.Fatalf("floatFromPtr(&0.5) = %v; want 0.5", got)
	}
	z := 0.0
	if got := floatFromPtr(&z); got != 0.0 {
		t.Fatalf("floatFromPtr(&0.0) = %v; want 0.0", got)
	}
}
