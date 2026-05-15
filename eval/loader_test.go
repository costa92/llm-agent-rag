package eval_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
)

func TestLoadJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "production_misses.jsonl")
	content := `// comment lines are ignored
{"top_k": 3, "query": "paris museums", "namespace": "geo", "gold_doc_ids": ["cities"], "notes": "first line sets top_k"}
{"query": "berlin history", "namespace": "geo", "gold_doc_ids": ["cities"], "gold_chunk_ids": ["cities:2"]}

{"query": "noise", "gold_doc_ids": []}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	dataset, err := eval.LoadJSONL(path)
	if err != nil {
		t.Fatalf("LoadJSONL: %v", err)
	}
	if dataset.Name != "production_misses" {
		t.Fatalf("Dataset.Name = %q, want production_misses", dataset.Name)
	}
	if dataset.TopK != 3 {
		t.Fatalf("Dataset.TopK = %d, want 3 (from first example)", dataset.TopK)
	}
	if len(dataset.Examples) != 3 {
		t.Fatalf("Examples len = %d, want 3 (blank + comment lines ignored)", len(dataset.Examples))
	}
	if dataset.Examples[0].Query != "paris museums" {
		t.Fatalf("first query = %q", dataset.Examples[0].Query)
	}
	if len(dataset.Examples[1].GoldChunkIDs) != 1 || dataset.Examples[1].GoldChunkIDs[0] != "cities:2" {
		t.Fatalf("second example GoldChunkIDs = %v", dataset.Examples[1].GoldChunkIDs)
	}
}

func TestLoadJSONLMissingFile(t *testing.T) {
	if _, err := eval.LoadJSONL(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Fatalf("expected error for missing file")
	}
}
