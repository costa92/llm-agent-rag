// Package ingest turns source documents into stored, splittable chunks.
// Source and StreamingSource are the document-input seams; Splitter is the
// chunking seam (CharSplitter and MarkdownSplitter are the built-ins).
// Importer and ImportFrom drive the ingest pipeline, producing Chunks ready
// for an embedder and a store.
package ingest

import (
	"context"
)

// ImportOptions configures a streaming import via ImportFrom.
type ImportOptions struct {
	Namespace     string   // Namespace is the partition imported chunks belong to.
	MaxChars      int      // MaxChars is the chunk size in characters; 0 uses the splitter default.
	Splitter      Splitter // Splitter is the chunking strategy to apply.
	ReplaceSource bool     // ReplaceSource, when true, replaces all existing chunks from the same source.
}

// Importer drives the ingest pipeline: it reads documents from a Source and
// splits them with a Splitter into Chunks.
type Importer struct {
	Source   Source   // Source supplies the documents to import.
	Splitter Splitter // Splitter divides documents into chunks.
	MaxChars int      // MaxChars is the chunk size in characters; 0 uses the splitter default.
}

// NewImporter returns an Importer wired to the given source and splitter.
func NewImporter(src Source, splitter Splitter) *Importer {
	return &Importer{Source: src, Splitter: splitter}
}

// Import reads every document from the Source and splits it into chunks,
// returning the chunks and a summary ImportResult.
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

// ImportFrom drains a StreamingSource and imports its documents under opts,
// returning the produced chunks and a summary ImportResult.
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
