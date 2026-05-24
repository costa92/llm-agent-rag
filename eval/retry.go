package eval

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/costa92/llm-agent-rag/rag"
)

// statusErrer is the duck-typed interface ClassifyTransientHTTP /
// ClassifyRateLimited probe via errors.As. SDKs that expose their HTTP
// status code on the error type (the openai-go family, for instance,
// exposes APIError.StatusCode) satisfy it directly. Kept unexported so
// it does not bleed into the v1 API surface.
type statusErrer interface {
	StatusCode() int
}

// ClassifyTransientHTTP is the prebuilt RetryPolicy.Classify suitable for
// "retry this on network blips and transient server errors". It returns
// true for:
//
//   - Any error satisfying net.Error (or *url.Error) with Timeout() == true.
//   - Any error satisfying the SDK status-code interface (a StatusCode() int
//     method) returning 408, 429, 500, 502, 503, or 504.
//   - Any error whose Error() string (lowercased) contains one of "rate
//     limit", "too many requests", "timeout", or "connection reset" —
//     the substring fallback for SDKs that don't expose a typed status.
//
// It is best-effort by design: production users with a single known SDK
// shape should write their own classifier against that SDK's error type
// for tighter precision. The builtin's purpose is plug-and-play for the
// common case of "I don't know exactly what shape my errors take".
//
// A nil error returns false — there is nothing to retry.
func ClassifyTransientHTTP(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return true
	}
	var se statusErrer
	if errors.As(err, &se) {
		switch se.StatusCode() {
		case 408, 429, 500, 502, 503, 504:
			return true
		}
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "rate limit") ||
		strings.Contains(s, "too many requests") ||
		strings.Contains(s, "timeout") ||
		strings.Contains(s, "connection reset")
}

// JitterMode selects the sleep distribution between retry attempts. The
// default (zero value) is JitterNone — deterministic exponential backoff,
// the same on every replay. JitterEqual adds the AWS "equal jitter"
// distribution to break up correlated retries across a fleet.
type JitterMode string

const (
	// JitterNone is deterministic exponential backoff. The sleep before
	// attempt N is exactly the current delay (BaseDelay << N, capped at
	// MaxDelay).
	JitterNone JitterMode = ""
	// JitterEqual is the "equal jitter" distribution: the sleep is half
	// the current delay plus a uniform random value in
	// [0, current_delay/2]. Drawn from math/rand/v2 (goroutine-safe;
	// no mutex required).
	JitterEqual JitterMode = "equal"
)

// RetryPolicy configures the exponential-backoff retry wrappers
// NewRetryAsker and NewRetryJudge. Zero values mean "single attempt" —
// MaxAttempts<=1 collapses to one call, matching v1.4.0 baseline behavior.
//
// Defaults applied at retry time:
//   - MaxAttempts<=1 → one call total.
//   - BaseDelay == 0 → 100ms.
//   - MaxDelay  == 0 → 30s.
//   - Jitter   == "" → JitterNone.
//   - Classify == nil → retry every error (subject to ctx cancellation).
//
// Context cancellation short-circuits the loop:
//   - A ctx.Done() before the first attempt returns ctx.Err() immediately.
//   - A ctx.Done() during the sleep between attempts returns ctx.Err()
//     without sleeping the rest of the interval.
type RetryPolicy struct {
	MaxAttempts int              // MaxAttempts caps how many times fn runs. <=1 means a single call (no retries).
	BaseDelay   time.Duration    // BaseDelay is the initial sleep before the second attempt. 0 => 100ms default.
	MaxDelay    time.Duration    // MaxDelay clamps the exponential growth of the sleep. 0 => 30s default.
	Jitter      JitterMode       // Jitter selects the sleep distribution. Empty == JitterNone.
	Classify    func(error) bool // Classify decides whether an error is retryable. nil => retry all.
	// OnRetry (v1.5.1) fires between a failed attempt and the next sleep,
	// only when there WILL be another attempt. attempt is the 0-indexed
	// number of the just-failed attempt — OnRetry receives attempt=0 after
	// the first failure, attempt=1 after the second, etc.
	//
	// OnRetry does NOT fire when:
	//   - The just-failed attempt was the terminal attempt (MaxAttempts-1)
	//     — there is no next attempt, so alerting would be wrong.
	//   - Classify is non-nil and returns false on the error — the loop
	//     short-circuits before any retry happens.
	//
	// Nil-safe (a nil OnRetry simply emits nothing). The hook may fire
	// concurrently when the wrapped Asker/Judge is invoked from multiple
	// goroutines (e.g. AnswerBenchmark.Parallelism>=2); implementations
	// must be thread-safe.
	OnRetry func(ctx context.Context, attempt int, err error)
}

// retryLoop runs fn up to attempts times, sleeping between attempts with
// exponential backoff (base 2) clamped at maxDelay. It is the shared core
// of NewRetryAsker and NewRetryJudge — see RetryPolicy godoc for details.
//
// Concurrency: each call holds its own state; the function is safe for
// concurrent calls from multiple goroutines.
func retryLoop(ctx context.Context, policy RetryPolicy, fn func() error) error {
	attempts := policy.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	base := policy.BaseDelay
	if base <= 0 {
		base = 100 * time.Millisecond
	}
	maxDelay := policy.MaxDelay
	if maxDelay <= 0 {
		maxDelay = 30 * time.Second
	}
	// Pre-loop: a pre-cancelled ctx never even tries.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	delay := base
	var lastErr error
	for i := 0; i < attempts; i++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		// On the terminal attempt there is no sleep — return the last
		// error directly. OnRetry does NOT fire here: there will be no
		// next attempt, so alerting about a retry would be misleading.
		if i == attempts-1 {
			break
		}
		// Non-retryable error short-circuits before the sleep. OnRetry
		// does NOT fire here either: the loop is breaking out without a
		// retry, same reasoning as the terminal-attempt branch above.
		if policy.Classify != nil && !policy.Classify(err) {
			break
		}
		// v1.5.1: between the failed attempt and the sleep before the
		// next attempt, fire OnRetry with the just-failed 0-indexed
		// attempt number. nil-safe.
		if policy.OnRetry != nil {
			policy.OnRetry(ctx, i, err)
		}
		sleep := jitterSleep(delay, policy.Jitter)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
		// Exponential backoff with clamp at maxDelay.
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
	return lastErr
}

// jitterSleep applies the policy's JitterMode to delay. JitterEqual uses
// math/rand/v2.Int64N which is goroutine-safe — no mutex required.
func jitterSleep(delay time.Duration, mode JitterMode) time.Duration {
	switch mode {
	case JitterEqual:
		half := delay / 2
		// Int64N(0) would panic, so guard tiny delays.
		if half <= 0 {
			return delay
		}
		// Equal-jitter formula: half + uniform(0, half).
		return half + time.Duration(rand.Int64N(int64(half)+1))
	default:
		return delay
	}
}

// retryAsker is the Asker wrapper produced by NewRetryAsker. Generated
// answer + error semantics on the final attempt match the v1.4.0 Asker
// contract byte-for-byte.
type retryAsker struct {
	inner  Asker
	policy RetryPolicy
}

// Ask wraps the inner Asker call with retryLoop. The returned Answer is
// the value captured on the last (successful) attempt; on full exhaustion
// the zero Answer is returned alongside the last error.
func (r retryAsker) Ask(ctx context.Context, question string, opts rag.AskOptions) (rag.Answer, error) {
	var ans rag.Answer
	err := retryLoop(ctx, r.policy, func() error {
		a, e := r.inner.Ask(ctx, question, opts)
		if e != nil {
			return e
		}
		ans = a
		return nil
	})
	if err != nil {
		return rag.Answer{}, err
	}
	return ans, nil
}

// NewRetryAsker returns an Asker that retries the inner Ask call under
// policy. A nil inner panics on the first Ask — there is no fallback
// inner.
func NewRetryAsker(inner Asker, policy RetryPolicy) Asker {
	return retryAsker{inner: inner, policy: policy}
}

// retryJudge is the Judge wrapper produced by NewRetryJudge.
type retryJudge struct {
	inner  Judge
	policy RetryPolicy
}

// Judge wraps the inner Judge call with retryLoop. The returned Judgement
// is the value captured on the last (successful) attempt; on full
// exhaustion the zero Judgement is returned alongside the last error.
func (r retryJudge) Judge(ctx context.Context, req JudgeRequest) (Judgement, error) {
	var j Judgement
	err := retryLoop(ctx, r.policy, func() error {
		v, e := r.inner.Judge(ctx, req)
		if e != nil {
			return e
		}
		j = v
		return nil
	})
	if err != nil {
		return Judgement{}, err
	}
	return j, nil
}

// NewRetryJudge returns a Judge that retries the inner Judge call under
// policy. A nil inner panics on the first Judge — there is no fallback
// inner.
func NewRetryJudge(inner Judge, policy RetryPolicy) Judge {
	return retryJudge{inner: inner, policy: policy}
}
