package ingest

import (
	"context"
)

type ImportOptions struct {
	Namespace string
	MaxChars  int
	Splitter  Splitter
}

type Importer struct {
	Source   Source
	Splitter Splitter
	MaxChars int
}

func NewImporter(src Source, splitter Splitter) *Importer {
	return &Importer{Source: src, Splitter: splitter}
}

func (i *Importer) Import(ctx context.Context) ([]Chunk, ImportResult, error) {
	if i.Source == nil {
		return nil, ImportResult{}, ErrNilSource
	}
	if i.Splitter == nil {
		return nil, ImportResult{}, ErrNilSplitter
	}
	docs, err := i.Source.Documents(ctx)
	if err != nil {
		return nil, ImportResult{}, err
	}
	var out []Chunk
	var res ImportResult
	for _, doc := range docs {
		chunks := i.Splitter.Split(doc, i.MaxChars)
		res.Documents++
		res.Chunks += len(chunks)
		for _, chunk := range chunks {
			out = append(out, chunk)
			res.ChunkIDs = append(res.ChunkIDs, chunk.ID)
		}
	}
	return out, res, nil
}

func ImportFrom(ctx context.Context, src StreamingSource, opts ImportOptions) ([]Chunk, ImportResult, error) {
	docs, err := Collect(ctx, src)
	if err != nil {
		return nil, ImportResult{}, err
	}
	importer := &Importer{
		Source:   StaticSource(docs...),
		Splitter: opts.Splitter,
		MaxChars: opts.MaxChars,
	}
	if importer.Splitter == nil {
		importer.Splitter = NewCharSplitter(500, 50)
	}
	return importer.Import(ctx)
}
