package fanout

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
)

// Task is the unit Run schedules.
type Task[T any] func(ctx context.Context) (T, error)

// Result carries one Task's outcome. Value is meaningful only when Err == nil.
type Result[T any] struct {
	Index int
	Value T
	Err   error
}

// Option modifies Run's behavior.
type Option func(*config)

type config struct {
	failFast bool
}

// WithFailFast: when any Task returns a non-nil error or panics, the internal
// derived ctx is cancelled, prompting siblings blocked on ctx-aware operations
// to exit cooperatively. Already-running Tasks are NOT force-killed; they see
// runCtx.Done() and must check it themselves.
//
// Errors are still written to the corresponding Result.Err — fail-fast only
// propagates the cancel signal. Run's top-level error remains nil unless the
// outer ctx is cancelled.
func WithFailFast() Option {
	return func(c *config) { c.failFast = true }
}

// ErrTaskPanic wraps a panic recovered from a Task.
//
// Stack captures the goroutine stack at the moment of recovery for diagnostics.
// Error() deliberately omits the stack to keep log output bounded; callers that
// need the stack must read the Stack field directly.
type ErrTaskPanic struct {
	Recovered any
	Stack     []byte
}

func (e *ErrTaskPanic) Error() string {
	return fmt.Sprintf("fanout: task panic: %v", e.Recovered)
}

// Run executes tasks with at most maxConcurrency in parallel.
//
//   - maxConcurrency <= 0 means unlimited (one goroutine per task, no semaphore).
//   - Default collect-all: a single Task's failure / panic does not abort siblings;
//     each Task's err lands in its Result.Err.
//   - The top-level error is non-nil only when the outer ctx was cancelled during Run
//     (== ctx.Err()).
//   - tasks == nil or empty returns (nil, nil) without spawning goroutines.
//   - len(results) == len(tasks); results[i].Index == i (sorted by input index).
//
// Panic handling: a Task's panic is recovered and turned into *ErrTaskPanic
// (placed in Result.Err). Run itself never panics due to a Task panic.
func Run[T any](ctx context.Context, maxConcurrency int, tasks []Task[T], opts ...Option) ([]Result[T], error) {
	if len(tasks) == 0 {
		return nil, nil
	}

	cfg := config{}
	for _, opt := range opts {
		opt(&cfg)
	}

	results := make([]Result[T], len(tasks))

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var sem chan struct{}
	if maxConcurrency > 0 {
		sem = make(chan struct{}, maxConcurrency)
	}

	var wg sync.WaitGroup
	for i, t := range tasks {
		wg.Add(1)
		go runOne(runCtx, &wg, sem, results, i, t, &cfg, cancel)
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return results, err
	}
	return results, nil
}

// runOne is the per-task wrapper: acquire sem (if any), fast-path ctx check,
// recover panics, propagate fail-fast cancel.
//
// Caller (Run) guarantees a unique idx per goroutine, so concurrent writes to
// results[idx] target distinct slice elements and require no synchronization.
func runOne[T any](
	ctx context.Context,
	wg *sync.WaitGroup,
	sem chan struct{},
	results []Result[T],
	idx int,
	task Task[T],
	cfg *config,
	cancel context.CancelFunc,
) {
	defer wg.Done()

	// 1. acquire sem (ctx-aware)
	if sem != nil {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			results[idx] = Result[T]{Index: idx, Err: ctx.Err()}
			return
		}
	}

	// 2. fast-path ctx check (covers the unlimited path)
	if err := ctx.Err(); err != nil {
		results[idx] = Result[T]{Index: idx, Err: err}
		return
	}

	// 3. panic recover
	defer func() {
		if r := recover(); r != nil {
			results[idx] = Result[T]{
				Index: idx,
				Err:   &ErrTaskPanic{Recovered: r, Stack: debug.Stack()},
			}
			if cfg.failFast {
				cancel()
			}
		}
	}()

	// 4. run task
	val, err := task(ctx)
	results[idx] = Result[T]{Index: idx, Value: val, Err: err}
	if err != nil && cfg.failFast {
		cancel()
	}
}
