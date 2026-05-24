package eval_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
)

// TestAnswerExampleEmbedsExample asserts that AnswerExample embeds
// eval.Example so promoted fields (Query, Namespace, GoldDocIDs,
// GoldChunkIDs, Notes) remain assignable and readable through the
// embedded path. This guards the on-disk wire layout against an
// accidental rename or hoist of the embedded struct.
func TestAnswerExampleEmbedsExample(t *testing.T) {
	var ex eval.AnswerExample
	// Promoted assignments — these only compile if Example is embedded.
	ex.Query = "q"
	ex.Namespace = "ns"
	ex.GoldDocIDs = []string{"d1"}
	ex.GoldChunkIDs = []string{"c1"}
	ex.Notes = "n"
	ex.GoldAnswers = []string{"a", "b"}
	ex.RequiredPhrases = []string{"p"}

	if ex.Query != "q" || ex.Namespace != "ns" {
		t.Fatalf("promoted assignment failed: %+v", ex)
	}
	if len(ex.GoldAnswers) != 2 || ex.GoldAnswers[0] != "a" {
		t.Fatalf("GoldAnswers = %v", ex.GoldAnswers)
	}
	if len(ex.RequiredPhrases) != 1 || ex.RequiredPhrases[0] != "p" {
		t.Fatalf("RequiredPhrases = %v", ex.RequiredPhrases)
	}
}

// TestLoadAnswerJSONLRoundTrip loads the hermetic testdata fixture and
// asserts the basic shape: 3 examples, TopK=5 from the second-line
// "top_k" field (first JSON-bearing line had none — the first top_k
// encountered wins), gold answers and required phrases populated.
func TestLoadAnswerJSONLRoundTrip(t *testing.T) {
	ds, err := eval.LoadAnswerJSONL(filepath.Join("testdata", "answer_bench_minimal.jsonl"))
	if err != nil {
		t.Fatalf("LoadAnswerJSONL: %v", err)
	}
	if ds.Name != "answer_bench_minimal" {
		t.Fatalf("Dataset.Name = %q, want answer_bench_minimal", ds.Name)
	}
	if ds.TopK != 5 {
		t.Fatalf("Dataset.TopK = %d, want 5 (set by second line)", ds.TopK)
	}
	if len(ds.Examples) != 3 {
		t.Fatalf("Examples len = %d, want 3 (comments/blanks skipped)", len(ds.Examples))
	}

	first := ds.Examples[0]
	if first.Query != "What language is the Go runtime written in?" {
		t.Fatalf("first query = %q", first.Query)
	}
	if len(first.GoldAnswers) != 2 || first.GoldAnswers[0] != "Go" {
		t.Fatalf("first GoldAnswers = %v", first.GoldAnswers)
	}
	if len(first.RequiredPhrases) != 1 || first.RequiredPhrases[0] != "Go" {
		t.Fatalf("first RequiredPhrases = %v", first.RequiredPhrases)
	}
	if len(first.GoldChunkIDs) != 1 || first.GoldChunkIDs[0] != "c1" {
		t.Fatalf("first GoldChunkIDs = %v", first.GoldChunkIDs)
	}

	second := ds.Examples[1]
	if second.Query != "Who founded Anthropic?" {
		t.Fatalf("second query = %q", second.Query)
	}
	if len(second.GoldAnswers) != 2 {
		t.Fatalf("second GoldAnswers len = %d, want 2", len(second.GoldAnswers))
	}
	if len(second.RequiredPhrases) != 1 || second.RequiredPhrases[0] != "Amodei" {
		t.Fatalf("second RequiredPhrases = %v", second.RequiredPhrases)
	}
}

// TestLoadAnswerJSONLSkipsCommentsAndBlanks asserts the loader honors
// the LoadJSONL conventions: // and # comment prefixes plus empty lines
// are silently skipped, line numbering still advances for error
// context.
func TestLoadAnswerJSONLSkipsCommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "with_comments.jsonl")
	content := `// double-slash comment
# hash comment

{"query":"q1","gold_answers":["a1"]}

{"query":"q2","gold_answers":["a2"]}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ds, err := eval.LoadAnswerJSONL(path)
	if err != nil {
		t.Fatalf("LoadAnswerJSONL: %v", err)
	}
	if len(ds.Examples) != 2 {
		t.Fatalf("Examples len = %d, want 2 (comments+blanks skipped)", len(ds.Examples))
	}
	if ds.Examples[0].Query != "q1" || ds.Examples[1].Query != "q2" {
		t.Fatalf("queries = %q / %q", ds.Examples[0].Query, ds.Examples[1].Query)
	}
}

// TestLoadAnswerJSONLBadJSON asserts errors include file:line context so
// dataset authoring is debuggable.
func TestLoadAnswerJSONLBadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.jsonl")
	content := `{"query":"ok"}
{this is not json}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := eval.LoadAnswerJSONL(path)
	if err == nil {
		t.Fatalf("LoadAnswerJSONL: expected error for malformed JSON")
	}
	msg := err.Error()
	if !strings.Contains(msg, "broken.jsonl") {
		t.Fatalf("error %q does not contain file name", msg)
	}
	if !strings.Contains(msg, ":2") {
		t.Fatalf("error %q does not contain line number :2", msg)
	}
}

// TestLoadAnswerJSONLMissingFile asserts an open error surfaces with the
// path.
func TestLoadAnswerJSONLMissingFile(t *testing.T) {
	if _, err := eval.LoadAnswerJSONL(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Fatalf("expected error for missing file")
	}
}
