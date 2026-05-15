// Package feedback closes the loop between production retrievals and the
// eval framework. A Recorder captures flagged Asks as JSONL Example
// lines that eval.LoadJSONL can read back as a Dataset, so production
// misses become regression cases on the next eval run.
//
// Capture is opt-in per call. The recommended pattern is to wire a
// Recorder into your rag.Observer.OnAsk callback and decide there
// which traces are worth recording:
//
//	rec, closer, err := feedback.OpenFile("misses.jsonl")
//	if err != nil { /* ... */ }
//	defer closer()
//
//	sys := rag.New(rag.Options{
//	    Model: yourLLM,
//	    Observer: rag.Observer{
//	        OnAsk: func(ctx context.Context, trace rag.Trace) {
//	            if userFlaggedAsBad(trace) {
//	                _ = rec.Capture(feedback.BuildExample(
//	                    trace,
//	                    yourGoldDocIDs(trace),
//	                    yourGoldChunkIDs(trace),
//	                    "user thumbs-down",
//	                ))
//	            }
//	        },
//	    },
//	})
package feedback

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/rag"
)

// Recorder appends eval.Example values as JSONL. Safe for concurrent use.
type Recorder struct {
	mu  sync.Mutex
	out io.Writer
}

// NewRecorder wraps out so Capture writes JSONL lines to it.
func NewRecorder(out io.Writer) *Recorder {
	return &Recorder{out: out}
}

// OpenFile opens (or creates) path for append-write and returns a
// Recorder plus a Close func. The caller is responsible for calling
// Close when done.
func OpenFile(path string) (*Recorder, func() error, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("feedback: open %s: %w", path, err)
	}
	return NewRecorder(f), f.Close, nil
}

// Capture writes ex as a single JSON line, terminated by '\n'. Safe for
// concurrent use; lines from concurrent callers do not interleave.
func (r *Recorder) Capture(ex eval.Example) error {
	if r == nil || r.out == nil {
		return fmt.Errorf("feedback: Recorder has no writer")
	}
	raw, err := json.Marshal(ex)
	if err != nil {
		return fmt.Errorf("feedback: marshal example: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	bw := bufio.NewWriter(r.out)
	if _, err := bw.Write(raw); err != nil {
		return fmt.Errorf("feedback: write: %w", err)
	}
	if err := bw.WriteByte('\n'); err != nil {
		return fmt.Errorf("feedback: write newline: %w", err)
	}
	return bw.Flush()
}

// BuildExample maps a rag.Trace plus caller-supplied gold info into an
// eval.Example. Query and Namespace come from the trace; gold inputs
// come from whatever miss-detection signal the caller has (human
// thumbs-down, dataset lookup, etc.).
func BuildExample(trace rag.Trace, goldDocIDs, goldChunkIDs []string, notes string) eval.Example {
	return eval.Example{
		Query:        trace.Question,
		Namespace:    trace.Namespace,
		GoldDocIDs:   append([]string(nil), goldDocIDs...),
		GoldChunkIDs: append([]string(nil), goldChunkIDs...),
		Notes:        notes,
	}
}
