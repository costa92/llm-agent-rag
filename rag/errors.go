package rag

import "errors"

// ErrEmptyQuery is returned by the answer paths when the query is empty.
var ErrEmptyQuery = errors.New("rag: query is required")

// ErrModelRequired is returned when an operation needs a generation model
// but none was configured.
var ErrModelRequired = errors.New("rag: generator required for this operation")

// ErrImporterRequired is returned when an operation needs an importer but
// none was configured.
var ErrImporterRequired = errors.New("rag: importer required for this operation")

// ErrRetrieverRequired is returned when an operation needs a retriever but
// none was configured.
var ErrRetrieverRequired = errors.New("rag: retriever required for this operation")

// ErrSourceRequired is returned when an import is attempted with no source.
var ErrSourceRequired = errors.New("rag: import source is required")

// ErrCommunitySummarizerRequired is returned by System.AskGlobal when a
// community report must be generated (a cache miss or a stale ContentHash)
// but no Options.CommunitySummarizer was configured.
var ErrCommunitySummarizerRequired = errors.New("rag: community summarizer required for global search")
