package rag

import "errors"

var ErrEmptyQuery = errors.New("rag: query is required")

var ErrModelRequired = errors.New("rag: generator required for this operation")

var ErrImporterRequired = errors.New("rag: importer required for this operation")

var ErrRetrieverRequired = errors.New("rag: retriever required for this operation")

var ErrSourceRequired = errors.New("rag: import source is required")
