// Package fanout is a stateless generic primitive for "N tasks -> N results
// with bounded concurrency" — the shape that was hand-rolled in
// rag.System.runFollowupsParallel and eval.AnswerBenchmark.runParallel before
// this consolidation. Both now route their bounded fan-out through Run, which
// additionally recovers a Task's panic into *ErrTaskPanic instead of letting it
// crash the process (the hand-rolled loops had no recover).
//
// # Provenance (KC-3 copy, not import)
//
// The source is lifted verbatim from the sister core repo's
// github.com/costa92/llm-agent/pkg/fanout. It is COPIED, not imported, for the
// same reason the guard regex tables are (KC-3 per-repo source-of-truth): the
// core dependency in this module is isolated to the build-tagged
// adapter/llmagent package, and importing pkg/fanout into the main rag/ and
// eval/ packages would spread that edge across the whole RAG surface and pin
// the helper to whatever pkg/fanout API the pinned core version shipped. An
// internal copy keeps the main packages stdlib-only and free to evolve.
//
// # Invariants
//
//   - len(results) == len(tasks), always (even when ctx is already cancelled).
//   - results[i].Index == i (no sort needed by callers).
//   - Run never panics due to a Task's panic.
//   - Top-level error is ctx.Err() or nil — never a Task's error; per-task
//     errors (and recovered panics) live in Result.Err.
//
// # Concurrency primitive
//
// Pure stdlib: sync.WaitGroup + chan struct{} semaphore. No external deps.
package fanout
