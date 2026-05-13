package rag

import (
	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/prompt"
	"github.com/costa92/llm-agent-rag/store"
)

type SearchOptions struct {
	TopK      int
	Namespace string
	Filters   map[string]any
}

type AskOptions struct {
	Search   SearchOptions
	Template prompt.Template
	Metadata map[string]any
}

type Options struct {
	Splitter ingest.Splitter
	Embedder embed.Embedder
	Store    store.Store
	Model    generate.Model
	Template prompt.Template
	MaxChars int
}
