package rag

import (
	"context"

	"github.com/costa92/llm-agent-rag/advanced"
	"github.com/costa92/llm-agent-rag/generate"
)

// QueryCondenser rewrites a follow-up question into a standalone retrieval
// query using conversation history. It is the multi-turn query-shaping seam
// AskConversation depends on. With empty history a condenser MUST return the
// question unchanged.
type QueryCondenser interface {
	Condense(ctx context.Context, history []generate.Message, question string) (string, error)
}

// LLMCondenser is the default QueryCondenser. It delegates to
// advanced.CondenseQuery: empty history returns the question unchanged with
// no model call; otherwise the model rewrites it into a standalone query.
type LLMCondenser struct {
	Model generate.Model // Model performs the rewrite.
}

// Condense rewrites question into a standalone query using history.
func (c LLMCondenser) Condense(ctx context.Context, history []generate.Message, question string) (string, error) {
	return advanced.CondenseQuery(ctx, c.Model, history, question)
}

// passthroughCondenser returns the question unchanged. It is the default when
// no model is configured, so AskConversation degrades to plain Ask.
type passthroughCondenser struct{}

// Condense returns question unchanged.
func (passthroughCondenser) Condense(_ context.Context, _ []generate.Message, question string) (string, error) {
	return question, nil
}

// effectiveCondenser returns the configured QueryCondenser, or a default: an
// LLMCondenser over the System's model when a model is set, otherwise a
// passthroughCondenser. Mirrors effectiveGrader / effectiveQueryPlanner.
func (s *System) effectiveCondenser() QueryCondenser {
	if s.condenser != nil {
		return s.condenser
	}
	if s.model == nil {
		return passthroughCondenser{}
	}
	return LLMCondenser{Model: s.model}
}
