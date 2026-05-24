package eval

import (
	"context"

	"github.com/costa92/llm-agent-rag/obs"
)

// GenerateUsageHook mirrors rag.Observer.OnGenerateUsage for the eval
// package. It fires once per successful generate.Model.Generate call made
// inside an eval-side wrapper (currently NewCostObservingJudge). It lives
// in package eval — not package rag — so cross-package wrapping does not
// import the unexported rag.countingModel and break the layering: the eval
// package depends on rag, never the reverse.
//
// Standard stage tag emitted by the v1.5.0 eval-side wrapper:
//   - "judge_eval": the LLMJudge's groundedness/relevance call.
//
// The hook fires ONLY on success (err == nil) and may fire concurrently
// under AnswerBenchmark.Parallelism>=2. Implementations must be
// thread-safe (e.g. guard shared state with sync.Mutex).
type GenerateUsageHook func(ctx context.Context, stage string, usage obs.TokenUsage)
