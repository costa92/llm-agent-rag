package eval

// JSONL streaming codec — Write/Read counterpart of MarshalBenchmark/
// UnmarshalBenchmark from serialize.go. Lives in its own file so the
// streaming surface is easy to audit in isolation.
//
// Format:
//
//   line 1   : {"kind":"header","dataset":{name,top_k},"metrics":{...}}
//   line 2..N: {"kind":"example","result":{...}}
//
// Each line is '\n'-terminated, no leading whitespace. The reader rebuilds
// Dataset.Examples from the per-example stream in line order, so the
// header line never carries the full Examples slice.
//
// Strict-mode reader: NO comment-line skipping (unlike LoadAnswerJSONL,
// which is permissive). Blank lines are tolerated only at EOF — a blank
// line in the middle of the stream errors.
//
// All record decodes use json.Decoder.DisallowUnknownFields() so schema
// drift surfaces immediately. Buffer is bufio.Scanner with a 4 MiB max
// line, matching LoadAnswerJSONL.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrEmptyBenchmark is returned by ReadBenchmarkJSONL when the input
// contains no benchmark lines (neither header nor any example).
var ErrEmptyBenchmark = errors.New("eval: empty benchmark JSONL")

// jsonlHeader is line 1 of the JSONL stream.
type jsonlHeader struct {
	Kind    string                   `json:"kind"`
	Dataset answerDatasetSummaryWire `json:"dataset"`
	Metrics benchmarkMetricsWire     `json:"metrics"`
}

// jsonlExample is each subsequent line of the JSONL stream.
type jsonlExample struct {
	Kind   string                  `json:"kind"`
	Result answerExampleResultWire `json:"result"`
}

// jsonlKind is the minimal projection used to peek at the discriminator
// on a line before decoding the full record. The full decode applies
// DisallowUnknownFields; this peek does not (it ignores all but the
// "kind" key).
type jsonlKind struct {
	Kind string `json:"kind"`
}

// WriteBenchmarkJSONL writes r as a streaming JSONL artifact: one header
// line carrying Dataset.{Name,TopK}+Metrics, followed by one example
// line per PerExample entry (each carrying the embedded AnswerExample so
// the per-line record is self-contained).
//
// Each line is terminated by a single '\n'; no leading whitespace. The
// reader (ReadBenchmarkJSONL) reconstructs Dataset.Examples from the
// per-example stream in line order.
func WriteBenchmarkJSONL(w io.Writer, r BenchmarkResult) error {
	header := jsonlHeader{
		Kind: "header",
		Dataset: answerDatasetSummaryWire{
			Name: r.Dataset.Name,
			TopK: r.Dataset.TopK,
		},
		Metrics: metricsToWire(r.Metrics),
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("eval: marshal header: %w", err)
	}
	if _, err := w.Write(append(hb, '\n')); err != nil {
		return fmt.Errorf("eval: write header: %w", err)
	}
	for i, ex := range r.PerExample {
		line := jsonlExample{Kind: "example", Result: exampleResultToWire(ex)}
		lb, err := json.Marshal(line)
		if err != nil {
			return fmt.Errorf("eval: marshal example %d: %w", i, err)
		}
		if _, err := w.Write(append(lb, '\n')); err != nil {
			return fmt.Errorf("eval: write example %d: %w", i, err)
		}
	}
	return nil
}

// ReadBenchmarkJSONL parses a stream written by WriteBenchmarkJSONL.
// Strict: this reader does NOT skip "//" or "#" comment lines (unlike
// LoadAnswerJSONL); a blank line is tolerated only at EOF (i.e. any
// blank line not followed by another non-blank line). Returns
// ErrEmptyBenchmark when no lines are present.
//
// Dataset.Examples is reconstructed from the per-example stream in line
// order, so callers can rely on
// result.Dataset.Examples[i].Query == result.PerExample[i].Example.Query.
func ReadBenchmarkJSONL(r io.Reader) (BenchmarkResult, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var (
		haveHeader bool
		header     jsonlHeader
		results    []AnswerExampleResult
		lineNo     int
		sawBlank   bool
		blankLine  int
	)
	for scanner.Scan() {
		lineNo++
		raw := scanner.Bytes()
		if len(bytes.TrimSpace(raw)) == 0 {
			// Blank line. Tolerated only if no later non-blank line shows up.
			// Track and enforce post-loop.
			if !sawBlank {
				sawBlank = true
				blankLine = lineNo
			}
			continue
		}
		if sawBlank {
			return BenchmarkResult{}, fmt.Errorf("eval: line %d: non-blank line after blank line at line %d", lineNo, blankLine)
		}
		// Peek at the discriminator.
		var kind jsonlKind
		if err := json.Unmarshal(raw, &kind); err != nil {
			return BenchmarkResult{}, fmt.Errorf("eval: line %d: peek kind: %w", lineNo, err)
		}
		switch kind.Kind {
		case "header":
			if haveHeader {
				return BenchmarkResult{}, fmt.Errorf("eval: line %d: duplicate header line", lineNo)
			}
			var h jsonlHeader
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&h); err != nil {
				return BenchmarkResult{}, fmt.Errorf("eval: line %d: decode header: %w", lineNo, err)
			}
			header = h
			haveHeader = true
		case "example":
			if !haveHeader {
				return BenchmarkResult{}, fmt.Errorf("eval: line %d: example line before header", lineNo)
			}
			var e jsonlExample
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&e); err != nil {
				return BenchmarkResult{}, fmt.Errorf("eval: line %d: decode example: %w", lineNo, err)
			}
			results = append(results, exampleResultFromWire(e.Result))
		case "":
			return BenchmarkResult{}, fmt.Errorf("eval: line %d: missing kind field", lineNo)
		default:
			return BenchmarkResult{}, fmt.Errorf("eval: line %d: unknown kind %q", lineNo, kind.Kind)
		}
	}
	if err := scanner.Err(); err != nil {
		return BenchmarkResult{}, fmt.Errorf("eval: scan: %w", err)
	}
	if !haveHeader && len(results) == 0 {
		return BenchmarkResult{}, ErrEmptyBenchmark
	}
	if !haveHeader {
		return BenchmarkResult{}, errors.New("eval: missing header line")
	}

	// Rebuild Dataset.Examples in line order.
	examples := make([]AnswerExample, len(results))
	for i, r := range results {
		examples[i] = r.Example
	}
	return BenchmarkResult{
		Dataset: AnswerDataset{
			Name:     header.Dataset.Name,
			TopK:     header.Dataset.TopK,
			Examples: examples,
		},
		Metrics:    metricsFromWire(header.Metrics),
		PerExample: results,
	}, nil
}
