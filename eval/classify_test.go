package eval_test

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/costa92/llm-agent-rag/eval"
)

// fakeTimeoutErr satisfies net.Error with Timeout() == true. It is the
// minimal shape ClassifyTransientHTTP probes via errors.As.
type fakeTimeoutErr struct{}

func (fakeTimeoutErr) Error() string   { return "i/o timeout" }
func (fakeTimeoutErr) Timeout() bool   { return true }
func (fakeTimeoutErr) Temporary() bool { return true }

// fakeStatusErr exposes a StatusCode() int method so the classifier's
// internal status-coded interface assertion (errors.As against
// statusErrer) succeeds. The Error() string is intentionally bland so
// substring fallback paths don't accidentally match.
type fakeStatusErr struct {
	code int
}

func (e *fakeStatusErr) Error() string  { return fmt.Sprintf("bad gateway: status %d", e.code) }
func (e *fakeStatusErr) StatusCode() int { return e.code }

func TestClassifyTransientHTTPReturnsFalseForNil(t *testing.T) {
	if eval.ClassifyTransientHTTP(nil) {
		t.Fatalf("ClassifyTransientHTTP(nil) = true, want false")
	}
}

func TestClassifyTransientHTTPMatchesNetTimeout(t *testing.T) {
	// A net.OpError that wraps a timeout-bearing net.Error is the
	// canonical "connection timed out" shape; the classifier must say
	// true via errors.As(net.Error) + Timeout() == true.
	op := &net.OpError{Op: "read", Net: "tcp", Err: fakeTimeoutErr{}}
	if !eval.ClassifyTransientHTTP(op) {
		t.Fatalf("ClassifyTransientHTTP(net.OpError{timeout}) = false, want true")
	}
	// A url.Error wrapping the same is also transient.
	u := &url.Error{Op: "Get", URL: "http://x", Err: fakeTimeoutErr{}}
	if !eval.ClassifyTransientHTTP(u) {
		t.Fatalf("ClassifyTransientHTTP(url.Error{timeout}) = false, want true")
	}
}

func TestClassifyTransientHTTPMatchesHTTP429And5xx(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		// Transient.
		{408, true},
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{504, true},
		// Not transient.
		{200, false},
		{400, false},
		{401, false},
		{403, false},
		{404, false},
		{410, false},
		{501, false},
	}
	for _, c := range cases {
		err := &fakeStatusErr{code: c.code}
		got := eval.ClassifyTransientHTTP(err)
		if got != c.want {
			t.Fatalf("ClassifyTransientHTTP(status=%d) = %v, want %v", c.code, got, c.want)
		}
	}
}

func TestClassifyTransientHTTPSubstringFallback(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"rate limit exceeded", true},
		{"too many requests", true},
		{"context deadline exceeded: timeout reading body", true},
		{"connection reset by peer", true},
		// Negatives.
		{"invalid token", false},
		{"permission denied", false},
		{"", false},
	}
	for _, c := range cases {
		err := errors.New(c.msg)
		got := eval.ClassifyTransientHTTP(err)
		if got != c.want {
			t.Fatalf("ClassifyTransientHTTP(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

// TestClassifyTransientHTTPRealNetTimeout exercises a true net package
// timeout via a deadline-exceeded listener. It's a smoke check that the
// classifier handles the real net.Error implementations, not just the
// hand-rolled fakes.
func TestClassifyTransientHTTPRealNetTimeout(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listen: %v", err)
	}
	defer ln.Close()
	// Accept with an immediate deadline so it times out.
	if l, ok := ln.(*net.TCPListener); ok {
		_ = l.SetDeadline(time.Now().Add(time.Millisecond))
		_, acceptErr := l.Accept()
		if acceptErr == nil {
			t.Skip("Accept returned nil error — cannot exercise timeout path")
		}
		if !eval.ClassifyTransientHTTP(acceptErr) {
			t.Fatalf("ClassifyTransientHTTP(real net timeout %v) = false, want true", acceptErr)
		}
	}
}
