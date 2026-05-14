package pack

import (
	"context"
	"strings"
	"unicode"

	"github.com/costa92/llm-agent-rag/store"
)

type TokenCounter interface {
	Count(text string) int
}

type SimpleCounter struct{}

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

type Request struct {
	Question  string
	Hits      []store.Hit
	MaxTokens int
}

type Trace struct {
	BudgetTokens      int
	UsedTokens        int
	SelectedChunkIDs  []string
	DroppedChunkIDs   []string
	TruncatedChunkIDs []string
}

type Result struct {
	Hits  []store.Hit
	Trace Trace
}

type Packer interface {
	Pack(ctx context.Context, req Request) (Result, error)
}

type GreedyTokenPacker struct {
	Counter TokenCounter
}

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
