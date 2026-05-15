package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadJSONL reads a JSONL file where each line is an Example. The
// returned Dataset takes its Name from the file's basename (without
// extension) and inherits its TopK from the first line's "top_k" field
// if present, else 5.
//
// Per-line schema (JSON tags on Example):
//
//	{
//	  "query": "...",
//	  "namespace": "...",
//	  "gold_doc_ids": ["..."],
//	  "gold_chunk_ids": ["..."],
//	  "notes": "..."
//	}
//
// A line with a "top_k" field configures the Dataset.TopK; that field is
// honored only on the first line that defines it.
func LoadJSONL(path string) (Dataset, error) {
	f, err := os.Open(path)
	if err != nil {
		return Dataset{}, fmt.Errorf("eval: open %s: %w", path, err)
	}
	defer f.Close()

	dataset := Dataset{
		Name: datasetNameFromPath(path),
		TopK: 5,
	}
	type wireExample struct {
		Example
		TopK int `json:"top_k,omitempty"`
	}
	scanner := bufio.NewScanner(f)
	// allow large lines for verbose notes
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	topKSet := false
	for scanner.Scan() {
		lineNo++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "#") {
			continue
		}
		var w wireExample
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			return Dataset{}, fmt.Errorf("eval: %s:%d: %w", path, lineNo, err)
		}
		if w.TopK > 0 && !topKSet {
			dataset.TopK = w.TopK
			topKSet = true
		}
		dataset.Examples = append(dataset.Examples, w.Example)
	}
	if err := scanner.Err(); err != nil {
		return Dataset{}, fmt.Errorf("eval: scan %s: %w", path, err)
	}
	return dataset, nil
}

func datasetNameFromPath(path string) string {
	base := filepath.Base(path)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" {
		return "dataset"
	}
	return base
}
