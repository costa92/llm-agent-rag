package eval_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
)

// TestWriteReadBenchmarkJSONL_RoundTrip exercises a populated
// BenchmarkResult through Write -> Read and asserts the result reads back
// with deep-equal dataset/metric/per-example shape (NaN-aware via the
// internal helper invoked indirectly through MarshalBenchmark round-trip).
func TestWriteReadBenchmarkJSONL_RoundTrip(t *testing.T) {
	in := sampleBenchmarkForJSONL()
	var buf bytes.Buffer
	if err := eval.WriteBenchmarkJSONL(&buf, in); err != nil {
		t.Fatalf("WriteBenchmarkJSONL: %v", err)
	}
	out, err := eval.ReadBenchmarkJSONL(&buf)
	if err != nil {
		t.Fatalf("ReadBenchmarkJSONL: %v", err)
	}
	if out.Dataset.Name != in.Dataset.Name {
		t.Fatalf("Dataset.Name = %q; want %q", out.Dataset.Name, in.Dataset.Name)
	}
	if out.Dataset.TopK != in.Dataset.TopK {
		t.Fatalf("Dataset.TopK = %d; want %d", out.Dataset.TopK, in.Dataset.TopK)
	}
	if len(out.PerExample) != len(in.PerExample) {
		t.Fatalf("PerExample len = %d; want %d", len(out.PerExample), len(in.PerExample))
	}
	for i := range in.PerExample {
		if out.PerExample[i].Example.Query != in.PerExample[i].Example.Query {
			t.Fatalf("PerExample[%d].Query = %q; want %q",
				i, out.PerExample[i].Example.Query, in.PerExample[i].Example.Query)
		}
		if out.PerExample[i].Answer != in.PerExample[i].Answer {
			t.Fatalf("PerExample[%d].Answer = %q; want %q",
				i, out.PerExample[i].Answer, in.PerExample[i].Answer)
		}
	}
	// Examples slice reconstructed from per-example stream in line order.
	if len(out.Dataset.Examples) != len(in.PerExample) {
		t.Fatalf("Dataset.Examples len = %d; want %d (rebuilt from stream)",
			len(out.Dataset.Examples), len(in.PerExample))
	}
}

// TestReadBenchmarkJSONL_StrictNoComments asserts the strict reader
// rejects '//' comment lines (LoadAnswerJSONL allows them; this codec
// does not).
func TestReadBenchmarkJSONL_StrictNoComments(t *testing.T) {
	header := `{"kind":"header","dataset":{"name":"x","top_k":5},"metrics":{"examples":0,"exact_match":null,"f1_token":null,"required_phrase_recall":null,"reflection_rounds_mean":null,"grader_adoption_rate":null,"followup_queries_used_mean":null,"active_retrieval_fire_rate":null,"mean_groundedness":null,"mean_answer_relevance":null}}`
	in := header + "\n// this is a comment\n"
	_, err := eval.ReadBenchmarkJSONL(strings.NewReader(in))
	if err == nil {
		t.Fatalf("comment line accepted; want error")
	}
}

// TestReadBenchmarkJSONL_UnknownKind asserts unknown kind discriminators
// error with "unknown kind".
func TestReadBenchmarkJSONL_UnknownKind(t *testing.T) {
	header := `{"kind":"header","dataset":{"name":"x","top_k":5},"metrics":{"examples":0,"exact_match":null,"f1_token":null,"required_phrase_recall":null,"reflection_rounds_mean":null,"grader_adoption_rate":null,"followup_queries_used_mean":null,"active_retrieval_fire_rate":null,"mean_groundedness":null,"mean_answer_relevance":null}}`
	in := header + "\n" + `{"kind":"future"}` + "\n"
	_, err := eval.ReadBenchmarkJSONL(strings.NewReader(in))
	if err == nil {
		t.Fatalf("unknown kind accepted; want error")
	}
	if !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("error does not mention 'unknown kind': %v", err)
	}
}

// TestReadBenchmarkJSONL_Empty asserts an empty reader yields
// ErrEmptyBenchmark.
func TestReadBenchmarkJSONL_Empty(t *testing.T) {
	_, err := eval.ReadBenchmarkJSONL(strings.NewReader(""))
	if !errors.Is(err, eval.ErrEmptyBenchmark) {
		t.Fatalf("empty reader err = %v; want ErrEmptyBenchmark", err)
	}
}

// TestReadBenchmarkJSONL_BlankLineAtEOF tolerates a trailing blank line
// after the last example.
func TestReadBenchmarkJSONL_BlankLineAtEOF(t *testing.T) {
	in := sampleBenchmarkForJSONL()
	var buf bytes.Buffer
	if err := eval.WriteBenchmarkJSONL(&buf, in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Append a trailing blank line.
	raw := buf.String() + "\n"
	out, err := eval.ReadBenchmarkJSONL(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("trailing blank line errored: %v", err)
	}
	if out.Dataset.Name != in.Dataset.Name {
		t.Fatalf("Dataset.Name = %q; want %q", out.Dataset.Name, in.Dataset.Name)
	}
}

// TestWriteBenchmarkJSONL_HeaderFirstThenExamples asserts the on-disk
// line ordering: line 1 has "kind":"header", subsequent lines have
// "kind":"example".
func TestWriteBenchmarkJSONL_HeaderFirstThenExamples(t *testing.T) {
	in := sampleBenchmarkForJSONL()
	var buf bytes.Buffer
	if err := eval.WriteBenchmarkJSONL(&buf, in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) < 1+len(in.PerExample) {
		t.Fatalf("got %d lines; want >= %d (header + examples)",
			len(lines), 1+len(in.PerExample))
	}
	if !strings.Contains(lines[0], `"kind":"header"`) {
		t.Fatalf("line 1 missing kind:header: %s", lines[0])
	}
	for i := 1; i <= len(in.PerExample); i++ {
		if !strings.Contains(lines[i], `"kind":"example"`) {
			t.Fatalf("line %d missing kind:example: %s", i+1, lines[i])
		}
	}
}

// sampleBenchmarkForJSONL is a small, NaN-free BenchmarkResult fixture
// for the streaming-format tests. The single-doc codec exercises NaN
// round-trip; the streaming tests focus on the line layout.
func sampleBenchmarkForJSONL() eval.BenchmarkResult {
	return eval.BenchmarkResult{
		Dataset: eval.AnswerDataset{Name: "jsonl-sample", TopK: 5},
		Metrics: eval.BenchmarkMetrics{
			Examples:                2,
			ExactMatch:              0.5,
			F1Token:                 0.62,
			RequiredPhraseRecall:    0.0,
			ReflectionRoundsMean:    0.0,
			GraderAdoptionRate:      0.0,
			FollowupQueriesUsedMean: 0.0,
			ActiveRetrievalFireRate: 0.0,
			MeanGroundedness:        0.0,
			MeanAnswerRelevance:     0.0,
		},
		PerExample: []eval.AnswerExampleResult{
			{
				Example: eval.AnswerExample{Example: eval.Example{Query: "q1", Namespace: "ns"}},
				Answer:  "a1",
				F1Token: 0.5,
			},
			{
				Example: eval.AnswerExample{Example: eval.Example{Query: "q2", Namespace: "ns"}},
				Answer:  "a2",
				F1Token: 0.7,
			},
		},
	}
}
