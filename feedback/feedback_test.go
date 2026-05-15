package feedback_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/feedback"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/rag"
)

type fakeModel struct{}

func (fakeModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	if len(req.Messages) > 0 {
		return generate.Response{Text: req.Messages[0].Content}, nil
	}
	return generate.Response{}, nil
}

func TestBuildExampleMapsTrace(t *testing.T) {
	tr := rag.Trace{
		Question:  "where is paris",
		Namespace: "geo",
	}
	got := feedback.BuildExample(tr, []string{"cities"}, []string{"cities:1"}, "user thumbs-down")
	if got.Query != "where is paris" {
		t.Fatalf("Query = %q, want question", got.Query)
	}
	if got.Namespace != "geo" {
		t.Fatalf("Namespace = %q, want geo", got.Namespace)
	}
	if len(got.GoldDocIDs) != 1 || got.GoldDocIDs[0] != "cities" {
		t.Fatalf("GoldDocIDs = %v", got.GoldDocIDs)
	}
	if len(got.GoldChunkIDs) != 1 || got.GoldChunkIDs[0] != "cities:1" {
		t.Fatalf("GoldChunkIDs = %v", got.GoldChunkIDs)
	}
	if got.Notes != "user thumbs-down" {
		t.Fatalf("Notes = %q", got.Notes)
	}
}

func TestRecorderCaptureWritesJSONL(t *testing.T) {
	var buf bytes.Buffer
	rec := feedback.NewRecorder(&buf)
	if err := rec.Capture(eval.Example{Query: "a", Namespace: "n"}); err != nil {
		t.Fatalf("Capture 1: %v", err)
	}
	if err := rec.Capture(eval.Example{Query: "b", Namespace: "n", GoldDocIDs: []string{"d"}}); err != nil {
		t.Fatalf("Capture 2: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var ex eval.Example
		if err := json.Unmarshal([]byte(line), &ex); err != nil {
			t.Fatalf("line %d not valid JSON: %v\n%s", i, err, line)
		}
	}
}

func TestRecorderCaptureConcurrent(t *testing.T) {
	var buf bytes.Buffer
	rec := feedback.NewRecorder(&buf)
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			err := rec.Capture(eval.Example{
				Query:     fmt.Sprintf("query-%d", i),
				Namespace: "concurrent",
			})
			if err != nil {
				t.Errorf("Capture %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("lines = %d, want %d", len(lines), n)
	}
	seen := make(map[string]struct{}, n)
	for i, line := range lines {
		var ex eval.Example
		if err := json.Unmarshal([]byte(line), &ex); err != nil {
			t.Fatalf("line %d not valid JSON: %v\n%s", i, err, line)
		}
		if _, dup := seen[ex.Query]; dup {
			t.Fatalf("duplicate query %q across goroutines", ex.Query)
		}
		seen[ex.Query] = struct{}{}
	}
	if len(seen) != n {
		t.Fatalf("unique queries = %d, want %d", len(seen), n)
	}
}

func TestRoundTripObserverToLoadJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "misses.jsonl")
	rec, closer, err := feedback.OpenFile(path)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}

	sys := rag.New(rag.Options{
		Model:    fakeModel{},
		Splitter: ingest.NewMarkdownSplitter(500, 50),
		Observer: rag.Observer{
			OnAsk: func(ctx context.Context, trace rag.Trace) {
				// opt-in filter: only capture queries flagged via the
				// magic word "[miss]"
				if !strings.Contains(trace.Question, "[miss]") {
					return
				}
				_ = rec.Capture(feedback.BuildExample(
					trace,
					[]string{"gold-doc"},
					[]string{"gold-chunk"},
					"flagged by test",
				))
			},
		},
	})

	_, err = sys.Import(context.Background(), []ingest.Document{
		{ID: "d1", Content: "# Title\nparis france capital"},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// 1st Ask: NOT flagged, should be ignored.
	if _, err := sys.Ask(context.Background(), "capital of france", rag.AskOptions{
		Search: rag.SearchOptions{Namespace: "geo", TopK: 1},
	}); err != nil {
		t.Fatalf("Ask 1: %v", err)
	}
	// 2nd Ask: flagged via magic word, should be captured.
	if _, err := sys.Ask(context.Background(), "capital of france [miss]", rag.AskOptions{
		Search: rag.SearchOptions{Namespace: "geo", TopK: 1},
	}); err != nil {
		t.Fatalf("Ask 2: %v", err)
	}

	if err := closer(); err != nil {
		t.Fatalf("close recorder: %v", err)
	}

	// Verify the file content directly first
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("file has %d lines, want 1 (only the flagged Ask): %q", len(lines), string(raw))
	}

	dataset, err := eval.LoadJSONL(path)
	if err != nil {
		t.Fatalf("LoadJSONL: %v", err)
	}
	if dataset.Name != "misses" {
		t.Fatalf("dataset name = %q, want misses", dataset.Name)
	}
	if len(dataset.Examples) != 1 {
		t.Fatalf("examples = %d, want 1", len(dataset.Examples))
	}
	ex := dataset.Examples[0]
	if ex.Query != "capital of france [miss]" {
		t.Fatalf("captured Query = %q", ex.Query)
	}
	if ex.Namespace != "geo" {
		t.Fatalf("captured Namespace = %q", ex.Namespace)
	}
	if len(ex.GoldDocIDs) != 1 || ex.GoldDocIDs[0] != "gold-doc" {
		t.Fatalf("captured GoldDocIDs = %v", ex.GoldDocIDs)
	}
	if len(ex.GoldChunkIDs) != 1 || ex.GoldChunkIDs[0] != "gold-chunk" {
		t.Fatalf("captured GoldChunkIDs = %v", ex.GoldChunkIDs)
	}
}

func TestRecorderCaptureNilWriterFails(t *testing.T) {
	if err := (&feedback.Recorder{}).Capture(eval.Example{Query: "q"}); err == nil {
		t.Fatalf("expected error for Recorder with nil writer")
	}
}

func TestOpenFileFailsOnUnwritablePath(t *testing.T) {
	// /proc is read-only on Linux
	_, _, err := feedback.OpenFile("/proc/nonexistent/misses.jsonl")
	if err == nil {
		t.Fatalf("expected error opening unwritable path")
	}
}
