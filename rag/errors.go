package rag

import "errors"

var ErrEmptyQuery = errors.New("rag: query is required")

var ErrModelRequired = errors.New("rag: generator required for this operation")

var ErrImporterRequired = errors.New("rag: importer required for this operation")

var ErrRetrieverRequired = errors.New("rag: retriever required for this operation")

var ErrSourceRequired = errors.New("rag: import source is required")

// ErrCommunitySummarizerRequired is returned by System.AskGlobal when a
// community report must be generated (a cache miss or a stale ContentHash)
// but no Options.CommunitySummarizer was configured.
var ErrCommunitySummarizerRequired = errors.New("rag: community summarizer required for global search")
