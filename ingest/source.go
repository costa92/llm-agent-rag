package ingest

import (
	"context"
	"errors"
	"io"
)

type Source interface {
	Documents(ctx context.Context) ([]Document, error)
}

type SourceFunc func(ctx context.Context) ([]Document, error)

func (f SourceFunc) Documents(ctx context.Context) ([]Document, error) {
	return f(ctx)
}

func StaticSource(docs ...Document) Source {
	cp := append([]Document(nil), docs...)
	return SourceFunc(func(context.Context) ([]Document, error) {
		return cp, nil
	})
}

type StreamingSource interface {
	Next(ctx context.Context) (Document, error)
}

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

var ErrNilSource = errors.New("ingest: source is required")

var ErrNilSplitter = errors.New("ingest: splitter is required")
