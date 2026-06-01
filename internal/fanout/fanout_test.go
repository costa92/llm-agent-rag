package fanout

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// TestRunPreservesInputOrder asserts results[i] corresponds to tasks[i]
// regardless of completion order.
func TestRunPreservesInputOrder(t *testing.T) {
	tasks := make([]Task[int], 50)
	for i := range tasks {
		i := i
		tasks[i] = func(context.Context) (int, error) { return i * 2, nil }
	}
	res, err := Run(context.Background(), 8, tasks)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res) != len(tasks) {
		t.Fatalf("len(res) = %d, want %d", len(res), len(tasks))
	}
	for i, r := range res {
		if r.Index != i {
			t.Errorf("res[%d].Index = %d, want %d", i, r.Index, i)
		}
		if r.Err != nil {
			t.Errorf("res[%d].Err = %v, want nil", i, r.Err)
		}
		if r.Value != i*2 {
			t.Errorf("res[%d].Value = %d, want %d", i, r.Value, i*2)
		}
	}
}

// TestRunBoundsConcurrency asserts no more than maxConcurrency tasks run
// at once.
func TestRunBoundsConcurrency(t *testing.T) {
	const limit = 3
	var active, peak int32
	tasks := make([]Task[struct{}], 30)
	for i := range tasks {
		tasks[i] = func(context.Context) (struct{}, error) {
			cur := atomic.AddInt32(&active, 1)
			for {
				old := atomic.LoadInt32(&peak)
				if cur <= old || atomic.CompareAndSwapInt32(&peak, old, cur) {
					break
				}
			}
			// brief spin so overlap is observable
			for n := 0; n < 1000; n++ {
			}
			atomic.AddInt32(&active, -1)
			return struct{}{}, nil
		}
	}
	if _, err := Run(context.Background(), limit, tasks); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&peak); got > limit {
		t.Fatalf("peak concurrency = %d, want <= %d", got, limit)
	}
}

// TestRunRecoversPanic asserts a panicking task becomes an *ErrTaskPanic in
// its Result.Err and does NOT crash sibling tasks or Run itself.
func TestRunRecoversPanic(t *testing.T) {
	tasks := []Task[int]{
		func(context.Context) (int, error) { return 1, nil },
		func(context.Context) (int, error) { panic("boom") },
		func(context.Context) (int, error) { return 3, nil },
	}
	res, err := Run(context.Background(), 0, tasks)
	if err != nil {
		t.Fatalf("Run top-level err = %v, want nil (task panic must not propagate)", err)
	}
	if res[0].Err != nil || res[0].Value != 1 {
		t.Errorf("res[0] = %+v, want {Value:1}", res[0])
	}
	var panicErr *ErrTaskPanic
	if !errors.As(res[1].Err, &panicErr) {
		t.Fatalf("res[1].Err = %v, want *ErrTaskPanic", res[1].Err)
	}
	if panicErr.Recovered != "boom" {
		t.Errorf("recovered = %v, want %q", panicErr.Recovered, "boom")
	}
	if len(panicErr.Stack) == 0 {
		t.Error("ErrTaskPanic.Stack is empty, want goroutine stack")
	}
	if res[2].Err != nil || res[2].Value != 3 {
		t.Errorf("res[2] = %+v, want {Value:3}", res[2])
	}
}

// TestRunEmptyTasks asserts the empty input fast-path returns (nil, nil)
// without spawning goroutines.
func TestRunEmptyTasks(t *testing.T) {
	res, err := Run[int](context.Background(), 4, nil)
	if err != nil || res != nil {
		t.Fatalf("Run(nil) = (%v, %v), want (nil, nil)", res, err)
	}
}
