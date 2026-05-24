package eval

import (
	"context"
	"strings"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/pack"
)

// evalCountingModel is the eval-package mirror of rag.countingModel. It is
// duplicated intentionally — keeping it inside the eval package means we
// can wire NewCostObservingJudge without making eval depend on a rag
// unexported type. The rag package depends on eval for nothing; eval
// depends on rag for the Asker/Judge surface — adding an eval -> rag
// internal-type dependency would risk a future cycle.
//
// On every successful Generate, the wrapper:
//
//  1. Calls the supplied hook with (ctx, stage, usage).
//  2. Returns the underlying response.
//
// Failures suppress the hook — partial usage on error is unreliable.
type evalCountingModel struct {
	inner generate.Model
	stage string
	hook  GenerateUsageHook
}

func (m evalCountingModel) Generate(ctx context.Context, req generate.Request) (generate.Response, error) {
	resp, err := m.inner.Generate(ctx, req)
	if err != nil {
		return resp, err
	}
	if m.hook != nil {
		m.hook(ctx, m.stage, deriveEvalTokenUsage(req, resp))
	}
	return resp, nil
}

// deriveEvalTokenUsage mirrors rag.deriveTokenUsage so the eval-side hook
// reports the same usage shape the rag-side OnGenerateUsage emits. Kept
// local so eval does not depend on an unexported rag helper.
func deriveEvalTokenUsage(req generate.Request, resp generate.Response) obs.TokenUsage {
	u := resp.Usage
	if u.PromptTokens > 0 || u.CompletionTokens > 0 || u.TotalTokens > 0 {
		total := u.TotalTokens
		if total == 0 {
			total = u.PromptTokens + u.CompletionTokens
		}
		return obs.TokenUsage{
			PromptTokens:     u.PromptTokens,
			CompletionTokens: u.CompletionTokens,
			TotalTokens:      total,
			Estimated:        false,
		}
	}
	counter := pack.SimpleCounter{}
	pt := counter.Count(evalPromptText(req))
	ct := counter.Count(resp.Text)
	return obs.TokenUsage{
		PromptTokens:     pt,
		CompletionTokens: ct,
		TotalTokens:      pt + ct,
		Estimated:        true,
	}
}

// evalPromptText flattens a rendered generate.Request to a single string
// for token estimation — the system prompt followed by each message's
// content. Mirrors rag.promptText for the same reason as
// deriveEvalTokenUsage: avoid touching unexported rag helpers.
func evalPromptText(req generate.Request) string {
	var b strings.Builder
	b.WriteString(req.SystemPrompt)
	for _, m := range req.Messages {
		b.WriteByte('\n')
		b.WriteString(m.Content)
	}
	return b.String()
}
