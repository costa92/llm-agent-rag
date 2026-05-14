package rag

import (
	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/pack"
	"github.com/costa92/llm-agent-rag/prompt"
	"github.com/costa92/llm-agent-rag/rerank"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

type SearchOptions struct {
	TopK            int
	Namespace       string
	Filters         map[string]any
	SecurityFilters map[string]any
	EnableMQE       bool
	EnableHyDE      bool
	MQECount        int
	EnableRerank    bool
	EnableStructure bool
}

type AskOptions struct {
	Search    SearchOptions
	Template  prompt.Template
	Metadata  map[string]any
	MaxTokens int
}

type Options struct {
	Splitter     ingest.Splitter
	Embedder     embed.Embedder
	Store        store.Store
	Model        generate.Model
	Template     prompt.Template
	Preprocessor retrieve.QueryPreprocessor
	Retriever    retrieve.Retriever
	Reranker     rerank.Reranker
	Packer       pack.Packer
	MaxChars     int
}
