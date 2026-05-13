package ingest

import (
	"fmt"
	"strings"
)

type Splitter interface {
	Split(doc Document, maxChars int) []Chunk
}

type CharSplitter struct {
	MaxChars int
	Overlap int
}

func NewCharSplitter(maxChars, overlap int) CharSplitter {
	return CharSplitter{MaxChars: maxChars, Overlap: overlap}
}

func (c CharSplitter) Split(doc Document, maxChars int) []Chunk {
	if maxChars <= 0 {
		maxChars = c.MaxChars
	}
	if maxChars <= 0 {
		maxChars = 500
	}
	text := strings.TrimSpace(doc.Content)
	if text == "" {
		return nil
	}
	if doc.ID == "" {
		doc.ID = "doc"
	}
	parts := splitText(text, maxChars, c.Overlap)
	out := make([]Chunk, 0, len(parts))
	for i, part := range parts {
		md := copyMeta(doc.Metadata)
		md["chunk_index"] = i
		md["chunk_total"] = len(parts)
		out = append(out, Chunk{
			ID:       fmt.Sprintf("%s:%d", doc.ID, i),
			DocID:    doc.ID,
			Index:    i,
			Total:    len(parts),
			Title:    doc.Title,
			Content:  part,
			Metadata: md,
		})
	}
	return out
}

func splitText(text string, maxChars, overlap int) []string {
	if len(text) <= maxChars {
		return []string{text}
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxChars {
		overlap = maxChars / 2
	}
	out := make([]string, 0, len(text)/maxChars+1)
	for start := 0; start < len(text); {
		hardEnd := start + maxChars + maxChars/5
		if hardEnd >= len(text) {
			out = append(out, strings.TrimSpace(text[start:]))
			break
		}
		breakAt := -1
		if idx := strings.LastIndex(text[start:hardEnd], "\n\n"); idx >= 0 {
			breakAt = start + idx
		}
		if breakAt < 0 {
			end := start + maxChars
			if ws := strings.LastIndexByte(text[start:end], ' '); ws > 0 {
				breakAt = start + ws
			} else {
				breakAt = end
			}
		}
		nextStart := breakAt
		if breakAt < len(text)-1 && text[breakAt] == '\n' && text[breakAt+1] == '\n' {
			nextStart = breakAt + 2
		}
		out = append(out, strings.TrimSpace(text[start:breakAt]))
		next := nextStart - overlap
		if next <= start {
			next = start + 1
		}
		start = next
	}
	cleaned := out[:0]
	for _, chunk := range out {
		if chunk != "" {
			cleaned = append(cleaned, chunk)
		}
	}
	return cleaned
}

func copyMeta(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}
