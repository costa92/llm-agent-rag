// Package pack assembles retrieved chunks into a token-budgeted prompt
// context. Packer is the central seam (GreedyTokenPacker is the built-in)
// and TokenCounter is the tokenizer seam — SimpleCounter is the default
// whitespace counter a caller may swap for a real tokenizer.
package pack

import (
	"context"
	"strings"
	"unicode"

	"github.com/costa92/llm-agent-rag/store"
)

// TokenCounter estimates the token cost of a string. It is the tokenizer
// seam: a caller plugs in a real tokenizer by implementing it.
type TokenCounter interface {
	// Count returns the estimated token count of text.
	Count(text string) int
}

// SimpleCounter is the default TokenCounter — a whitespace/CJK heuristic that
// needs no tokenizer. A caller may swap it for a model-accurate counter.
type SimpleCounter struct{}

// Count returns SimpleCounter's heuristic token estimate for text.
func (SimpleCounter) Count(text string) int {
	var words int
	var inWord bool
	var cjk int
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			cjk++
			inWord = false
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if !inWord {
				words++
				inWord = true
			}
			continue
		}
		inWord = false
	}
	tokens := cjk + int(float64(words)*1.3)
	if tokens <= 0 && strings.TrimSpace(text) != "" {
		return 1
	}
	return tokens
}

// Request is a context-packing request: a question and its candidate hits,
// bounded by a token budget.
type Request struct {
	Question  string      // Question is the query the context will answer.
	Hits      []store.Hit // Hits are the retrieved candidate chunks, best-first.
	MaxTokens int         // MaxTokens is the token budget for the packed context.
}

// Trace records what a Packer decided for one Request.
type Trace struct {
	BudgetTokens      int      // BudgetTokens is the token budget the packer worked against.
	UsedTokens        int      // UsedTokens is the tokens consumed by the packed context.
	SelectedChunkIDs  []string // SelectedChunkIDs are the chunks kept in the context.
	DroppedChunkIDs   []string // DroppedChunkIDs are the chunks excluded for lack of budget.
	TruncatedChunkIDs []string // TruncatedChunkIDs are the chunks kept but shortened to fit.
}

// Result is the output of a Packer: the kept hits and a packing Trace.
type Result struct {
	Hits  []store.Hit // Hits are the chunks that fit the budget.
	Trace Trace       // Trace records the packing decisions.
}

// Packer assembles retrieved chunks into a token-budgeted context. It is the
// context-packing seam.
type Packer interface {
	// Pack selects and trims hits to fit req's token budget.
	Pack(ctx context.Context, req Request) (Result, error)
}

// GreedyTokenPacker is the built-in Packer: it greedily keeps best-first hits
// until the budget is exhausted, truncating the final hit to fit.
type GreedyTokenPacker struct {
	Counter TokenCounter // Counter estimates token cost; nil defaults to SimpleCounter.
}

// Pack greedily fills req's token budget with hits, truncating the last to fit.
func (p GreedyTokenPacker) Pack(_ context.Context, req Request) (Result, error) {
	counter := p.Counter
	if counter == nil {
		counter = SimpleCounter{}
	}
	budget := req.MaxTokens
	if budget <= 0 {
		budget = 300
	}
	used := counter.Count(req.Question)
	out := make([]store.Hit, 0, len(req.Hits))
	selected := make([]string, 0, len(req.Hits))
	dropped := make([]string, 0, len(req.Hits))
	truncated := make([]string, 0, 1)

	for i, hit := range req.Hits {
		tokenCost := counter.Count(renderChunk(hit))
		if used+tokenCost <= budget {
			out = append(out, hit)
			selected = append(selected, hit.Chunk.ID)
			used += tokenCost
			continue
		}

		remaining := budget - used
		truncatedHit, ok := truncateHit(hit, remaining, counter)
		if ok {
			out = append(out, truncatedHit)
			selected = append(selected, truncatedHit.Chunk.ID)
			truncated = append(truncated, truncatedHit.Chunk.ID)
			used += counter.Count(renderChunk(truncatedHit))
		} else {
			dropped = append(dropped, hit.Chunk.ID)
		}
		for _, rest := range req.Hits[i+1:] {
			dropped = append(dropped, rest.Chunk.ID)
		}
		break
	}

	return Result{
		Hits: out,
		Trace: Trace{
			BudgetTokens:      budget,
			UsedTokens:        used,
			SelectedChunkIDs:  selected,
			DroppedChunkIDs:   dropped,
			TruncatedChunkIDs: truncated,
		},
	}, nil
}

func renderChunk(hit store.Hit) string {
	return "[" + hit.Chunk.ID + "] " + hit.Chunk.Title + " " + hit.Chunk.Content
}

func truncateHit(hit store.Hit, budget int, counter TokenCounter) (store.Hit, bool) {
	if budget <= 0 {
		return store.Hit{}, false
	}
	base := "[" + hit.Chunk.ID + "] "
	baseBudget := counter.Count(base)
	if budget <= baseBudget+2 {
		return store.Hit{}, false
	}
	truncatedContent, ok := truncateText(hit.Chunk.Content, budget-baseBudget, counter)
	if !ok {
		return store.Hit{}, false
	}
	out := hit
	out.Chunk.Content = truncatedContent
	return out, true
}

func truncateText(text string, budget int, counter TokenCounter) (string, bool) {
	const marker = " [truncated]"
	markerTokens := counter.Count(marker)
	if budget <= markerTokens {
		return "", false
	}
	fields := strings.Fields(text)
	if len(fields) > 1 {
		var b strings.Builder
		for i, field := range fields {
			next := field
			if i > 0 {
				next = " " + next
			}
			if counter.Count(b.String()+next+marker) > budget {
				break
			}
			b.WriteString(next)
		}
		if b.Len() == 0 {
			return "", false
		}
		return b.String() + marker, true
	}

	runes := []rune(text)
	if len(runes) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, r := range runes {
		next := b.String() + string(r) + marker
		if counter.Count(next) > budget {
			break
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "", false
	}
	return b.String() + marker, true
}
