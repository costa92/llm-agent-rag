package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// AnswerExample is one labeled query for the C-Eval answer-quality
// benchmark. It embeds eval.Example (Query/Namespace/GoldDocIDs/
// GoldChunkIDs/Notes promoted) and adds two generation-side oracles:
// GoldAnswers (any-match counts as ExactMatch, F1Token picks the best
// per gold) and RequiredPhrases (case-sensitive verbatim substrings
// the answer should contain).
//
// Compatibility note: this exported struct may grow additively over
// time, so keyed composite literals are recommended.
type AnswerExample struct {
	Example          // Embedded; promoted fields Query, Namespace, GoldDocIDs, GoldChunkIDs, Notes.
	GoldAnswers     []string `json:"gold_answers,omitempty"`     // GoldAnswers are reference answer strings; any-match counts as ExactMatch.
	RequiredPhrases []string `json:"required_phrases,omitempty"` // RequiredPhrases are verbatim substrings the answer should contain.
}

// AnswerDataset is a named collection of AnswerExamples with a fixed
// TopK — sibling of eval.Dataset for the answer-quality benchmark.
type AnswerDataset struct {
	Name     string          // Name identifies the dataset.
	TopK     int             // TopK is the retrieval cutoff every example is scored at.
	Examples []AnswerExample // Examples are the labeled queries.
}

// LoadAnswerJSONL reads a JSONL file of AnswerExample records. The
// returned AnswerDataset takes its Name from the file's basename
// (without extension) and inherits its TopK from the first line that
// carries a "top_k" field; subsequent "top_k" fields are ignored.
//
// Per-line schema (JSON tags on Example, AnswerExample, plus the
// optional "top_k" sentinel):
//
//	{
//	  "query": "...",
//	  "namespace": "...",
//	  "gold_doc_ids": ["..."],
//	  "gold_chunk_ids": ["..."],
//	  "notes": "...",
//	  "gold_answers": ["..."],
//	  "required_phrases": ["..."],
//	  "top_k": 5
//	}
//
// Lines beginning with "//" or "#" and blank lines are skipped (line
// numbering still advances for error context). Errors include file:line.
func LoadAnswerJSONL(path string) (AnswerDataset, error) {
	f, err := os.Open(path)
	if err != nil {
		return AnswerDataset{}, fmt.Errorf("eval: open %s: %w", path, err)
	}
	defer f.Close()

	dataset := AnswerDataset{
		Name: datasetNameFromPath(path),
		TopK: 5,
	}
	type wireAnswerExample struct {
		AnswerExample
		TopK int `json:"top_k,omitempty"`
	}
	scanner := bufio.NewScanner(f)
	// allow large lines for verbose gold-answer arrays
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	topKSet := false
	for scanner.Scan() {
		lineNo++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "#") {
			continue
		}
		var w wireAnswerExample
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			return AnswerDataset{}, fmt.Errorf("eval: %s:%d: %w", path, lineNo, err)
		}
		if w.TopK > 0 && !topKSet {
			dataset.TopK = w.TopK
			topKSet = true
		}
		dataset.Examples = append(dataset.Examples, w.AnswerExample)
	}
	if err := scanner.Err(); err != nil {
		return AnswerDataset{}, fmt.Errorf("eval: scan %s: %w", path, err)
	}
	return dataset, nil
}
