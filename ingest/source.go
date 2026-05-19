package ingest

import (
	"context"
	"errors"
	"io"
)

// Source supplies a batch of documents to ingest. It is the document-input
// seam for finite, eagerly-loaded sources.
type Source interface {
	// Documents returns all documents to be ingested.
	Documents(ctx context.Context) ([]Document, error)
}

// SourceFunc adapts a plain function to the Source interface.
type SourceFunc func(ctx context.Context) ([]Document, error)

// Documents calls f, satisfying the Source interface.
func (f SourceFunc) Documents(ctx context.Context) ([]Document, error) {
	return f(ctx)
}

// StaticSource returns a Source that always yields the given documents.
func StaticSource(docs ...Document) Source {
	cp := append([]Document(nil), docs...)
	return SourceFunc(func(context.Context) ([]Document, error) {
		return cp, nil
	})
}

// StreamingSource supplies documents one at a time. It is the document-input
// seam for large or open-ended sources; Next returns io.EOF when exhausted.
type StreamingSource interface {
	// Next returns the next document, or io.EOF when the source is drained.
	Next(ctx context.Context) (Document, error)
}

// Collect drains a StreamingSource into a slice of documents.
func Collect(ctx context.Context, src StreamingSource) ([]Document, error) {
	var docs []Document
	for {
		doc, err := src.Next(ctx)
		if err != nil {
			if err == io.EOF {
				return docs, nil
			}
			return nil, err
		}
		docs = append(docs, doc)
	}
}

// ErrNilSource is returned when an import is attempted with no Source set.
var ErrNilSource = errors.New("ingest: source is required")

// ErrNilSplitter is returned when an import is attempted with no Splitter set.
var ErrNilSplitter = errors.New("ingest: splitter is required")
